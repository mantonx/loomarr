// Package store is Loomarr's persistence abstraction (design §5): one Store
// interface, two first-class backends (SQLite via modernc.org/sqlite, Postgres
// via pgx's database/sql shim). Dialect differences stay inside this package:
// migrations, ClaimDue* methods, workflow locks, and Postgres commit invalidations.
// Domain persistence remains shared code, and one conformance suite runs against
// both backends (AGENTS.md: never fork the assertions).
package store

import (
	"context"
	"errors"
	"time"

	"github.com/loomarr/loomarr/internal/contact"
	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerdecision"
	"github.com/loomarr/loomarr/internal/inventory"
	"github.com/loomarr/loomarr/internal/invitation"
	"github.com/loomarr/loomarr/internal/notifications"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/quality"
	"github.com/loomarr/loomarr/internal/recovery"
	"github.com/loomarr/loomarr/internal/secretprotection"
	"github.com/loomarr/loomarr/internal/taxonomy"
)

// ErrNotFound reports that a requested durable identity does not exist. Besides Get* reads,
// commands whose integrity depends on an existing owner (such as channel-scoped feedback) return
// it before committing any dependent row.
var ErrNotFound = errors.New("store: not found")

// ErrConditioningPublicationMismatch reports a pending conditioned publication whose catalog
// rows are not one of the exact source-only, source-plus-held-reconstruction, or target-only
// states the owner-bound recovery protocol permits. Callers must hold it for review.
var ErrConditioningPublicationMismatch = filler.ErrConditioningOwnershipMismatch

// ErrTaxonConflict marks a taxonomy mutation that cannot safely apply: create over an existing
// slug, or delete of a taxon still directly asserted on clips. The caller should reload/retag,
// never retry the same write blindly.
var ErrTaxonConflict = errors.New("store: taxonomy conflict")

// ErrProposalNotSubmitted reports a terminal proposal decision that lost the
// submitted -> approved/denied compare-and-swap. It is distinct from ErrNotFound:
// the proposal exists, but another decision already won.
var ErrProposalNotSubmitted = errors.New("store: proposal is not submitted")

// ErrProposalSuperseded reports an approval candidate whose job already has an
// approved proposal created at the same time or later. Same-second ties fail closed:
// proposal timestamps are stored at second resolution, so guessing an order could
// roll a channel back to content the operator reviewed earlier.
var ErrProposalSuperseded = errors.New("store: proposal was superseded by a newer approval")

// ErrJobNotRunning reports a generation completion that lost the running -> done
// compare-and-swap. The proposal insert shares that transaction, so this error
// guarantees that no orphan proposal was persisted.
var ErrJobNotRunning = errors.New("store: suggestion job is not running")

// ErrJobNotTerminal reports a refine/recuration that raced another execution
// or targeted a job that is still queued/running. Active executions are never
// overwritten in place.
var ErrJobNotTerminal = errors.New("store: suggestion job is not terminal")

// ErrInferenceNotReserved reports a settlement that lost the reserved-state
// compare-and-swap. A completed/failed/held evaluation is immutable accounting.
var ErrInferenceNotReserved = errors.New("store: inference evaluation is not reserved")

// ErrInferenceBudgetExceeded reports a provider charge above its pre-call
// reservation. The charged fact is still persisted and the evaluation is held.
var ErrInferenceBudgetExceeded = errors.New("store: inference budget exceeded")

// ErrQualitySnapshotConflict reports reuse of an immutable evaluation-run id
// with different facts.
var ErrQualitySnapshotConflict = errors.New("store: quality snapshot conflict")

// ErrJobOwnershipMismatch reports a result whose proposal requester differs
// from the requester persisted on its job. It prevents a worker/store wiring
// mistake from moving generated content across user audit lifecycles.
var ErrJobOwnershipMismatch = errors.New("store: suggestion job ownership mismatch")

// ErrChannelConflict reports that a channel write lost a uniqueness race (normally
// number or non-empty intent binding). Approval transactions have rolled back
// completely when returning it, so their caller may safely reload and replan.
var ErrChannelConflict = errors.New("store: channel conflict")

// ErrChannelStale reports that a channel mutation was planned from an older
// revision than the row now holds. The row still exists; callers may reload and
// either reapply their domain merge or surface a conflict to the operator.
var ErrChannelStale = errors.New("store: stale channel revision")

// ErrContactAddressConflict means the normalized mailbox is already attached to another person
// or pending replacement. Public recovery must never resolve one address ambiguously.
var ErrContactAddressConflict = errors.New("store: contact address conflict")

// ErrInvitationIdentityConflict means an allowlist row or active Invitation already owns the
// reserved local username or exact Library account id.
var ErrInvitationIdentityConflict = errors.New("store: invitation identity conflict")

// TitleStore is the provisioning surface (§3–§4).
type TitleStore interface {
	GetTitle(ctx context.Context, key provision.Key) (provision.Record, error)
	UpsertTitle(ctx context.Context, rec provision.Record) error
	// UpdateTitleProgress writes only the poll-updated download fields (§18.1 arr-queue-poll),
	// leaving state-machine columns untouched so it never races the state Upsert.
	UpdateTitleProgress(ctx context.Context, key provision.Key, progress float64, eta, status string) error
	ListTitlesByState(ctx context.Context, state provision.State) ([]provision.Record, error)
	// ClaimDueTitles atomically claims up to limit non-terminal records
	// (wanted/requested/downloading) whose deadline is at/before now, for the
	// reconciler (§4: wanted→retry, in-flight→give-up; §5 concurrency).
	// Claiming *leases* each row by advancing its deadline to now+lease, so a
	// claimed row won't be handed out again (to a concurrent caller or the next
	// tick) until the reconciler either transitions it or the lease expires —
	// this is what prevents two replicas both firing a give-up/retry. SQLite: a
	// guarded UPDATE (single instance, §5). Postgres: FOR UPDATE SKIP LOCKED.
	ClaimDueTitles(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]provision.Record, error)
}

// ChannelStore is the scheduler/channel surface (§9), including channel icons.
type ChannelStore interface {
	GetChannel(ctx context.Context, id string) (Channel, error)
	GetChannelByNumber(ctx context.Context, number int) (Channel, error)
	// GetChannelByIntentRef finds the channel bound to a suggestion job (its intent_ref).
	// Indexed (00037); replaces two copy-pasted ListChannels-and-scan helpers.
	GetChannelByIntentRef(ctx context.Context, intentRef string) (Channel, error)
	// SaveChannel is the one full-row channel write. Revision 0 inserts a new row
	// at revision 1; a positive revision replaces the row only when it still
	// matches, then returns the saved channel with its incremented revision.
	SaveChannel(ctx context.Context, ch Channel) (Channel, error)
	// AttachTunarrChannel records the server-assigned id and the number actually used
	// without replacing a concurrently edited channel snapshot. Both old values are
	// compared; the targeted write advances and returns the row revision.
	AttachTunarrChannel(ctx context.Context, id, expectedTunarrID, newTunarrID string, expectedNumber, newNumber int) (int64, error)
	// SetChannelBroadcastCodec updates ONLY the derived broadcast_codec column (§9.1 V50) —
	// a targeted revision-checked write used after the lineup is bound.
	SetChannelBroadcastCodec(ctx context.Context, id string, expectedRevision int64, codec string) (int64, error)
	ListChannels(ctx context.Context) ([]Channel, error)
	// DeleteChannel hard-deletes the revision-matched Channel and only its channel-scoped
	// discovery feedback in one transaction. A detached Channel is retained through SaveChannel.
	DeleteChannel(ctx context.Context, id string, expectedRevision int64) error
	// ⚠ PutChannelIcon/GetChannelIcon were removed in V52 phase 8 with the `channel_icons` retired-ok
	// table. A channel's icon is an image-service image (§22) and its bytes are addressed by
	// content, not by channel id — see ImageStore.
	// ClaimDueChannels atomically claims up to limit channels whose
	// reconcile_deadline is at/before now, for the periodic reconcile sweep
	// (§9). Like ClaimDueTitles it *leases* each claimed channel (deadline →
	// now+lease) so two replicas never reconcile one Tunarr channel at once
	// (§18: single-leader / per-channel row claiming). SQLite: guarded UPDATE.
	// Postgres: FOR UPDATE SKIP LOCKED. Detached channels are never claimed.
	ClaimDueChannels(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Channel, error)
}

// SeriesEpisodeStore is the cached series episode lists (§5, §9 series expansion).
//
// A materialized answer, not a second source of truth: the media server still owns what
// episodes exist. It exists because enumerating a show is one library call, and doing it
// per series on every guide request was ~90% of that endpoint's latency.
type SeriesEpisodeStore interface {
	// GetSeriesEpisodes returns ErrNotFound for a show never enumerated — deliberately
	// distinct from a cached EMPTY list, which is a real answer ("no episodes present yet").
	GetSeriesEpisodes(ctx context.Context, libraryID string) (SeriesEpisodes, error)
	UpsertSeriesEpisodes(ctx context.Context, se SeriesEpisodes) error
	// ListStaleSeriesEpisodes returns shows fetched before `before`, oldest first, for the
	// channel-maintenance job (§18.1).
	ListStaleSeriesEpisodes(ctx context.Context, before time.Time, limit int) ([]SeriesEpisodes, error)
}

// JobStore is the suggester job queue (§8).
type JobStore interface {
	CreateJob(ctx context.Context, j Job) error
	GetJob(ctx context.Context, id string) (Job, error)
	// ListProposalJobIDs returns user-submitted generation jobs newest-first.
	// Re-curation jobs are operator maintenance and never appear in My requests.
	ListProposalJobIDs(ctx context.Context, limit int) ([]string, error)
	ListProposalJobIDsByCreator(ctx context.Context, createdBy string, limit int) ([]string, error)
	// GetProposalJob returns one consistent execution snapshot. An older
	// proposal is hidden while a reused refine job is queued/running/failed;
	// only a done job exposes its newest proposal in any decision state.
	GetProposalJob(ctx context.Context, id string) (ProposalJob, error)
	UpdateJob(ctx context.Context, j Job) error
	// ClaimDueJobs atomically claims up to limit queued jobs whose deadline is
	// at/before now, for the worker pool (§8). The claim also moves each job to
	// running and leases it (deadline → now+lease), so execution cannot race a
	// separate queued → running write. SQLite uses a guarded UPDATE; Postgres uses
	// FOR UPDATE SKIP LOCKED (§18).
	ClaimDueJobs(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Job, error)
	// ListProposalJobAttempts returns exact post-versioning execution history in
	// attempt order. Legacy version-0 jobs legitimately have no rows.
	ListProposalJobAttempts(ctx context.Context, jobID string) ([]ProposalJobAttempt, error)
	// FindJobByIntentHash returns the most recent successful job with the same
	// intent hash (§8 proposal cache), or ErrNotFound. `since` bounds the cache
	// TTL; incomplete and failed attempts do not shadow a reusable success.
	FindJobByIntentHash(ctx context.Context, hash string, since time.Time) (Job, error)
	// CommitSuggestionSuccess atomically inserts the generated proposal and moves
	// its existing job from running to done. A lost transition rolls both back.
	CommitSuggestionSuccess(ctx context.Context, jobID string, expectedAttempt int, p Proposal, updatedAt time.Time) error
	// CommitSuggestionFailure moves a running job to failed without rewriting
	// stale intent or ownership fields. A lost transition leaves the newer
	// lifecycle untouched.
	CommitSuggestionFailure(ctx context.Context, jobID string, expectedAttempt int, cause, failureCode, failureTraceJSON string, updatedAt time.Time) error
	// RequeueSuggestionJob replaces the intent only when the caller's observed
	// terminal execution is still current. Attempts are preserved; the next claim
	// increments them to create a new execution token.
	RequeueSuggestionJob(ctx context.Context, jobID string, expectedAttempt int, kind, intentJSON, intentHash string, deadline, updatedAt time.Time) error
	// CloneSuggestionSuccess materializes cached proposal CONTENT into a fresh,
	// caller-owned done job and submitted proposal. It never reuses the source
	// request's identity, requester, or decision state.
	CloneSuggestionSuccess(ctx context.Context, sourceJobID string, job Job, proposalID string) (Proposal, error)
	// PurgeFinishedJobs removes done/failed jobs older than `before` (§5 JOBS_RETENTION).
	// In-flight jobs (queued/running) are never removed by age.
	PurgeFinishedJobs(ctx context.Context, before time.Time) (int, error)
}

// ProposalApprovalReader is the transaction-bound read view available to an
// unattended approval guard. Reads and the eventual approval commit use the same
// transaction/connection, so losing a Postgres session loses both ordering and work.
type ProposalApprovalReader interface {
	GetUser(ctx context.Context, id string) (User, error)
	ListProposalsByCreator(ctx context.Context, userID string) ([]Proposal, error)
	GetTitle(ctx context.Context, key provision.Key) (provision.Record, error)
}

// ProposalApprovalGuard runs after requester ordering is acquired and before any
// approval mutation. Any error rolls the transaction back and leaves the proposal
// submitted; callers may use a private sentinel for a safe policy decline.
type ProposalApprovalGuard func(context.Context, ProposalApprovalReader) error

// ProposalStore is the suggester proposal surface (§8).
type ProposalStore interface {
	CreateProposal(ctx context.Context, p Proposal) error
	GetProposal(ctx context.Context, id string) (Proposal, error)
	// CommitProposalApproval atomically wins the submitted -> approved decision,
	// inserts title records that do not already exist, and creates or patches the
	// intent-bound channel. Existing title lifecycle state is never overwritten.
	// The returned count is newly inserted wanted titles.
	CommitProposalApproval(ctx context.Context, commit ProposalApproval) (int, error)
	// CommitProposalApprovalGuarded runs guard and the same durable commit under
	// requester ordering. SQLite uses its single-process keyed semaphore; Postgres
	// takes a transaction-scoped advisory lock and runs guard + commit on that same
	// transaction/connection. Manual CommitProposalApproval enters the same ordering
	// but has no guard, so deliberate admin spending is never quota-rejected.
	CommitProposalApprovalGuarded(ctx context.Context, commit ProposalApproval, guard ProposalApprovalGuard) (int, error)
	// CommitProposalDenial atomically wins the submitted -> denied decision.
	CommitProposalDenial(ctx context.Context, p Proposal) error
	ListProposalsByStatus(ctx context.Context, status string) ([]Proposal, error)
	// NewestProposalByStatusForJob is the binder's bind target: the most recent proposal for
	// one job in one status. Newest wins because a refine produces a newer approved proposal
	// for the same job and the channel must bind to THAT (§7). Indexed on job_id (00037).
	NewestProposalByStatusForJob(ctx context.Context, jobID, status string) (Proposal, error)
	ListProposalsByCreator(ctx context.Context, userID string) ([]Proposal, error)
	// PurgeDeniedProposals removes denied proposals older than `before` (§5
	// PROPOSALS_RETENTION). Approved proposals are the audit trail and are kept
	// indefinitely; submitted ones are still awaiting an answer.
	PurgeDeniedProposals(ctx context.Context, before time.Time) (int, error)
}

// DiscoveryQualityStore is the local privacy-safe quality ledger (§17).
type DiscoveryQualityStore interface {
	PutQualityRunSnapshot(ctx context.Context, snapshot quality.RunSnapshot) error
	RecordQualityObservation(ctx context.Context, observation quality.Observation) error
	MaintainQualityLedger(ctx context.Context, now time.Time) error
	ExportQualityLedger(ctx context.Context, now time.Time) (quality.Export, error)
}

// ScheduledJobStore is the background-job scheduler's state (§18.1).
type ScheduledJobStore interface {
	// UpsertScheduledJob writes a job's runtime state (last-run/result + next-run lease).
	// ⚠ Never writes `paused` — see SetScheduledJobPaused.
	UpsertScheduledJob(ctx context.Context, j ScheduledJob) error
	// SetScheduledJobPaused pauses or resumes a job (§18.1). A paused job is never claimed by
	// ClaimDueScheduledJobs, so it simply does not run until resumed. Creates the row if the
	// job has not run yet, so a task can be paused before its first execution.
	SetScheduledJobPaused(ctx context.Context, name string, paused bool) error
	// GetScheduledJob returns one job's state, or ErrNotFound.
	GetScheduledJob(ctx context.Context, name string) (ScheduledJob, error)
	// ListScheduledJobs returns all job state rows for the Tasks page.
	ListScheduledJobs(ctx context.Context) ([]ScheduledJob, error)
	// ClaimDueScheduledJobs leases every job whose next_run is due (advancing next_run to
	// now+lease) so only one replica runs it per tick — same SKIP LOCKED idiom as titles.
	ClaimDueScheduledJobs(ctx context.Context, now time.Time, lease time.Duration) ([]ScheduledJob, error)
}

// UserStore is the users & sessions surface (§11).
type UserStore interface {
	GetUser(ctx context.Context, id string) (User, error)
	// GetUserByName resolves a username to its allowlist row (§11 local login).
	GetUserByName(ctx context.Context, name string) (User, error)
	// CreateUserUnlessInvited creates a new allowlist row only when no active
	// invitation reserves its local username or exact Library account id.
	CreateUserUnlessInvited(ctx context.Context, u User, now time.Time) error
	UpsertUser(ctx context.Context, u User) error
	ListUsers(ctx context.Context) ([]User, error)
	CountAdmins(ctx context.Context) (int, error)
	CreateSession(ctx context.Context, sess Session) error
	GetSession(ctx context.Context, tokenHash string, now time.Time) (Session, error)
	ListSessionsForUser(ctx context.Context, userID string, now time.Time) ([]Session, error)
	TouchSession(ctx context.Context, tokenHash string, expiresAt time.Time) error
	RevokeSession(ctx context.Context, tokenHash string) error
	RevokeSessionsForUser(ctx context.Context, userID string) error
	PurgeExpiredSessions(ctx context.Context, now time.Time) (int, error)
	// Contact addresses are optional and independent from usernames/credential paths (§11).
	GetContactAddresses(ctx context.Context, userID string) (contact.Set, error)
	ListContactAddresses(ctx context.Context) ([]contact.Address, error)
	GetVerifiedContactAddressByNormalized(ctx context.Context, normalized string) (contact.Address, error)
	PutPendingContactAddress(ctx context.Context, address contact.Address) error
	VerifyPendingContactAddress(ctx context.Context, userID, normalized string, at time.Time) (contact.Address, error)
	DeletePendingContactAddress(ctx context.Context, userID string) error
	DeleteContactAddresses(ctx context.Context, userID string) error

	// Device pairing (§11, Shield P1) — the credential class a keyboard-less client uses. Kept in
	// this interface rather than a separate one because it is the same concern as sessions: who is
	// allowed to act, and how that permission is revoked.
	CreateDevicePairing(ctx context.Context, p DevicePairing) error
	GetDevicePairing(ctx context.Context, codeHash string, now time.Time) (DevicePairing, error)
	GetDevicePairingByUserCode(ctx context.Context, userCode string, now time.Time) (DevicePairing, error)
	ApproveDevicePairing(ctx context.Context, userCode, userID string, at time.Time) (bool, error)
	DeleteDevicePairing(ctx context.Context, codeHash string) error
	PurgeExpiredDevicePairings(ctx context.Context, now time.Time) error
	CreateDeviceToken(ctx context.Context, t DeviceToken) error
	GetDeviceToken(ctx context.Context, tokenHash string) (DeviceToken, error)
	TouchDeviceToken(ctx context.Context, tokenHash string, at time.Time) error
	ListDeviceTokensForUser(ctx context.Context, userID string) ([]DeviceToken, error)
	DeleteDeviceToken(ctx context.Context, tokenHash, userID string) (bool, error)
}

// ClipStore is the filler clip catalog (§10).
type ClipStore interface {
	UpsertClip(ctx context.Context, c Clip) error
	// ReplaceClipIdentity atomically moves every durable reference when an internal transform
	// changes a clip's content hash (§10). Metadata and operator overrides follow the bytes.
	ReplaceClipIdentity(ctx context.Context, oldHash string, c Clip) error
	// CommitConditioningPublication is the exact owner-bound variant used after a conditioned
	// target is visible. It atomically adopts a held Sync reconstruction, performs an ordinary
	// source-only re-key, or recognizes the exact target-only post-rekey state (§10 V65).
	CommitConditioningPublication(ctx context.Context, publication filler.ConditioningPublication, target Clip) error
	GetClip(ctx context.Context, libraryItemID string) (Clip, error)
	// GetClipByPath looks a clip up by its location under FILLER_DIR, NOT by its identity.
	//
	// ⚠ The two stopped being the same string in V38c: identity is the content hash, the path is
	// the sharded location. Routes whose URL carries a path (the byte-serving ones — `media`)
	// must use this; `GetClip` matches nothing for them and fails as an ordinary not-found.
	GetClipByPath(ctx context.Context, path string) (Clip, error)
	// ListClips returns clips matching the filter (any zero-value field is a
	// wildcard). Used by /v1/filler and by pod assembly's catalog load.
	//
	// ⚠ Clips the operator removed from the catalog (V35) are excluded unless the filter opts
	// in. That polarity is load-bearing: pod assembly loads the catalog through this call with
	// a ZERO filter, so an opt-out would keep a removed clip airing.
	//
	// ⚠ Sorted and pageable since V51d, and the same warning applies to the page size: `Limit == 0`
	// means NO limit, because pod assembly's zero filter must keep loading the whole catalog. The
	// operator-facing default of 100 lives in the API.
	ListClips(ctx context.Context, filter ClipFilter) ([]Clip, error)
	// CountClips is ListClips' question answered without the rows, for callers that only ever
	// took len() of the result. Same filter, same predicate (they share the WHERE builder).
	//
	// ⚠ It IGNORES Limit/Offset/Sort by construction — those live outside the WHERE builder — so
	// it answers "how many match?", which is what a pager's total means, not "how many are on this
	// page". Sharing the predicate is why a page's total can never disagree with its rows.
	CountClips(ctx context.Context, filter ClipFilter) (int, error)
	// CountClipsBySource returns the per-source clip count — a GROUP BY, not a catalog load
	// tallied in Go. Keyed by `Clip.Source`; sources with no clips are simply absent.
	CountClipsBySource(ctx context.Context, filter ClipFilter) (map[string]int, error)
	// SetClipsRemoved tombstones (or restores) clips by path — "Remove from catalog" (V35).
	//
	// ⚠ The ordinary tombstone writer; RetryClipPipeline is the only cross-table exception, so
	// restore+hold+requeue can commit atomically. UpsertClip deliberately omits the column, which stops the next scan
	// resurrecting a removed clip by finding its file still on disk. It never touches the file.
	SetClipsRemoved(ctx context.Context, paths []string, at time.Time) (int, error)
	// ReplaceSplitChildren makes one completed re-split generation airable and tombstones older
	// children of the same composite. It never deletes files and preserves channel-pinned clips.
	ReplaceSplitChildren(ctx context.Context, parentHash string, keepHashes []string, at time.Time) (int, error)
	// SetClipLanguage records the detected language (§10 V40).
	//
	// ⚠ The ONLY writer of that column, like the tombstone above: UpsertClip omits it, which is
	// what stops a folder scan blanking a detected language and making the job re-detect the whole
	// catalog every sync (~341s per clip under QEMU on the local backend).
	SetClipLanguage(ctx context.Context, path, language string, at time.Time) error
	// SetClipTranscript records the transcribe job's result (§10 V44). The ONLY writer of
	// `transcript`, like SetClipLanguage above: UpsertClip omits it so a re-sync cannot blank a
	// transcribed clip and re-trigger Whisper (~341s per clip under QEMU).
	SetClipTranscript(ctx context.Context, path, transcript string, at time.Time) error
	// SetClipConfidence records the tagger's grounding-capped score (§10 V38). The ONLY writer of
	// `confidence` — and until V51a there was none at all, so the column sat at 0 for every clip
	// ever catalogued while `TagSuggestion.Score` computed a value the tagger then discarded. The
	// value must be `Score`'s output, never the model's own self-assessment.
	SetClipConfidence(ctx context.Context, path string, confidence int, at time.Time) error
	// SetClipBrand records a GROUNDED advertiser found by the text tagger or confirmed by an operator
	// (§10 V44) — path-keyed,
	// writes `brand` and nothing else. It SHARES the `brand` column with ApplyClipVision (text
	// grounds a brand in the filename/sidecar/transcript, vision grounds one in the on-screen text);
	// UpsertClip omits `brand` from DO UPDATE so a re-sync cannot blank either. The caller has
	// already applied the grounding rule, so this writes what it is given.
	SetClipBrand(ctx context.Context, path, brand string, at time.Time) error
	// ApplyClipVision records a vision pass — the on-screen text it read, a grounded brand/era, and
	// its asserted taxonomy tags (§10 V44/V55). The ONLY writer of `visible_text`
	// and `vision_tagged`; `brand` it shares with SetClipBrand above. UpsertClip omits them so a
	// re-sync cannot undo a paid vision call. era/category are written only when grounded, leaving
	// text tags intact.
	ApplyClipVision(ctx context.Context, hash, path, brand, visibleText string, era, suggestedEra int, leaves []string, at time.Time) error
	// SetClipComposite marks a clip as a composite — a recorded break, not airable (§10 V45). The
	// ONLY writer of `is_composite`; UpsertClip omits it so a re-sync cannot flip a confirmed
	// composite back to an airable clip. Keyed by hash.
	SetClipComposite(ctx context.Context, hash string, composite bool, at time.Time) error
	// SetClipArtworkImages records the image-service identities of a clip's still and hover loop
	// (§22, V52 phase 6). Sole writer of those columns — they are omitted from UpsertClip's
	// DO UPDATE so a folder re-sync cannot blank them.
	SetClipArtworkImages(ctx context.Context, hash, thumbHash, hoverHash string, at time.Time) error
	// ListClipsPendingArtworkAdoption is the adoption job's work list: clips with rendered
	// artwork but no image-service identity for it yet. Paths are relative to the artwork cache.
	ListClipsPendingArtworkAdoption(ctx context.Context, limit int) ([]ClipArtworkPending, error)
	// --- Taxonomy (§10 V45a): the operator-editable tag vocabulary + a clip's denormalised tags. ---
	// ListTaxa returns the whole taxonomy graph (axis-then-slug order).
	ListTaxa(ctx context.Context) ([]taxonomy.Taxon, error)
	// ApplyTaxonomyEdit is the ONE operator-edit path. It validates the prospective graph and commits
	// the row edit, closure rebuild, and catalog rollup rebuild atomically. Callers never choreograph
	// those derived writes themselves.
	ApplyTaxonomyEdit(ctx context.Context, edit TaxonomyEdit, at time.Time) error
	// PreviewTaxonomyEdit validates the same prospective graph and reports the stored/playable
	// library facts whose derived taxonomy meaning may change, without mutating the graph.
	PreviewTaxonomyEdit(ctx context.Context, edit TaxonomyEdit) (TaxonomyEditImpact, error)
	// SeedTaxonomy writes the default forest only when `taxa` is empty — idempotent, run at boot.
	SeedTaxonomy(ctx context.Context, seed []taxonomy.Taxon, at time.Time) error
	// SetClipTags REPLACES one clip's asserted tags and derives its rollups and category shadow from
	// the store's current graph in the same transaction. Callers never supply a graph snapshot.
	SetClipTags(ctx context.Context, clipHash string, leaves []string) error
	GetClipTags(ctx context.Context, clipHash string, leavesOnly bool) ([]string, error)
	// TaxonomyUsage is the library-accounting read model: playable overall/per-axis coverage plus
	// direct and descendant counts for every taxon. It is computed over the whole catalog, never a UI page.
	TaxonomyUsage(ctx context.Context) (TaxonomyUsage, error)
	// SetClipsHeld files clips into the catalog or sends them back for review (§10 V38).
	//
	// ⚠ The ONLY writer of `held`/`auto_filed`, for the same reason as the tombstone above:
	// UpsertClip omits both, which is what stops the folder scan filing a held clip by finding
	// its file still on disk. `autoFiled` marks that no human looked before it became playable,
	// and is cleared whenever a person decides.
	SetClipsHeld(ctx context.Context, paths []string, held, autoFiled bool, at time.Time) (int, error)
	// UpdateClipClassification edits the non-taxonomy classifier facts (+ ai flag) — the tag
	// editor (§10) and AI job. Taxonomy writes exclusively own category. suggestedEra records an UNGROUNDED
	// AI-proposed era (§10 V34) for operator confirmation; writing an era clears
	// it in the same write, and a write with neither leaves it alone. Returns
	// ErrNotFound if absent.
	UpdateClipClassification(ctx context.Context, libraryItemID string, era int, audience string, suggestedEra int, aiTagged bool, updatedAt time.Time) error
	// UpdateClipKind corrects a clip's kind (§10). Separate from UpdateClipClassification because
	// the AI tagging job never sets kind — it classifies era/audience/category from text
	// signals, while kind is detected at sync and only a human corrects it (a trailer
	// scanned as a commercial being the likely case).
	UpdateClipKind(ctx context.Context, tunarrProgramID, kind string, updatedAt time.Time) error
	// UpdateClipGeography records grounded broadcast applicability and context. It is hash-keyed;
	// callers validate scope/country/date and provide the evidence attribution.
	UpdateClipGeography(ctx context.Context, hash, scope, country, market, network, station, airDate, evidence string, updatedAt time.Time) error
	// DeleteClipsNotIn removes clips whose id isn't in the given set — the sync's
	// prune step (a clip removed from the media server's filler library is gone).
	DeleteClipsNotIn(ctx context.Context, keepIDs []string) (int, error)
	// DeleteClip removes ONE clip by identity — the split-confirm path drops the
	// compilation row it just cut into segments (§10 V34). ErrNotFound if absent.
	DeleteClip(ctx context.Context, libraryItemID string) error
	// ListUntaggedCommercials returns commercials missing match tags — the AI
	// tagging job's work list (§10). Sugar over ListClips(UntaggedOnly).
	ListUntaggedCommercials(ctx context.Context) ([]Clip, error)
	// ListClipFingerprints/UpsertClipFingerprint own the persisted derived cache used by
	// compilation de-duplication (§10). Reads batch the catalog by exact algorithm; corrupt rows
	// are omitted with an error so valid siblings remain reusable and the bad row is recomputed.
	ListClipFingerprints(ctx context.Context, algorithm string) (map[string][]uint64, error)
	UpsertClipFingerprint(ctx context.Context, clipHash, algorithm string, frames []uint64) error
}

// SplitProposalStore is the persisted split-proposal surface (§10, V34) —
// detector-authored, reviewer-edited cut lists that are NOT clips until
// confirmed. One proposal per compilation clip (re-detection replaces).
type SplitProposalStore interface {
	UpsertSplitProposal(ctx context.Context, p filler.SplitProposal) error
	// GetSplitProposal reads one proposal by id (the review's reconnect truth).
	GetSplitProposal(ctx context.Context, id string) (filler.SplitProposal, error)
	AcquireSplitProposalClaim(ctx context.Context, id, token string, at, expiresAt time.Time) (filler.SplitProposal, error)
	RenewSplitProposalClaim(ctx context.Context, id, token string, expiresAt time.Time) error
	ReleaseSplitProposalClaim(ctx context.Context, id, token string) error
	// ListSplitProposals returns every pending proposal, oldest first — the Incoming tab's
	// "reels" (V35). One read behind that tab, so a restart cannot lose the queue.
	ListSplitProposals(ctx context.Context) ([]filler.SplitProposal, error)
	// DeleteSplitProposal removes a proposal after confirm or on reject.
	DeleteSplitProposal(ctx context.Context, id string) error
	// UpdateSplitProposal replaces an EXISTING proposal document; ErrNotFound if the row is gone.
	// Never inserts — see the implementation for why that matters (§10 V54).
	UpdateSplitProposal(ctx context.Context, p filler.SplitProposal) error
	CompletePartialSplitConfirmation(ctx context.Context, completion filler.SplitPartialCompletion) error
	// ListSweepableSplitProposals finds reels whose leftover cuts nobody reviewed inside the
	// window AND which have already produced clips — the only ones the sweep may retire (§10 V54).
	ListSweepableSplitProposals(ctx context.Context, before time.Time) ([]SweepableProposal, error)
	// MarkClipReaped records that a composite's recording was reclaimed. The row survives so
	// `parent_hash` keeps resolving; `DeleteClipsNotIn` skips it.
	MarkClipReaped(ctx context.Context, hash string, at time.Time) error
	// MarkPipelineFiled takes a clip off the belt, so a swept reel is not re-proposed forever.
	MarkPipelineFiled(ctx context.Context, hash string, at time.Time) error
	// CompleteSplitConfirmation atomically transitions a fully reviewed split proposal, retained
	// parent, replacement pipelines, and selected child generation (§10 V65).
	CompleteSplitConfirmation(ctx context.Context, completion filler.SplitCompletion) (int, error)

	// --- The per-clip ingest pipeline (§10 V51b, migration 00044) ---
	//
	// ⚠ A SIBLING of `clips`, never columns on it: `clips` is a synced cache that has been dropped
	// and recreated twice, and these rows record that Whisper seconds and a paid vision call have
	// ALREADY been spent. This pipeline persistence surface is the table's only writer, so unlike the clip
	// columns there is no DO UPDATE omission list to keep in step.

	// UpsertClipPipeline writes an ordinary runner transition.
	UpsertClipPipeline(ctx context.Context, p filler.ClipPipeline) error
	// RetryClipPipeline writes the recovery transition and, for an exhausted terminal failure,
	// restores the catalog tombstone while holding the clip in the same transaction.
	RetryClipPipeline(ctx context.Context, failed, retry filler.ClipPipeline, restore bool) error
	// GetClipPipeline reads one row. Absence is ordinary (an un-enrolled clip), not an error.
	GetClipPipeline(ctx context.Context, hash string) (filler.ClipPipeline, bool, error)
	// ListPipelineWork returns non-terminal rows due at or before `now`, oldest first, with a
	// total order so one clip cannot starve while another is worked repeatedly.
	ListPipelineWork(ctx context.Context, now time.Time, limit int) ([]filler.ClipPipeline, error)
	// PipelineOverview groups the durable state through filler.ClipPipeline.Lifecycle so API,
	// runner telemetry and persistence cannot acquire separate ownership predicates.
	PipelineOverview(ctx context.Context, at time.Time) (filler.PipelineOverview, error)
	// ListClipPipelines serves the Incoming read model — what is moving, and what was refused.
	ListClipPipelines(ctx context.Context, f filler.PipelineFilter) ([]filler.ClipPipeline, error)
	// ListClipsWithoutPipeline returns catalogued clips with no pipeline row yet, so enrolment is
	// lazy and self-healing rather than a data migration.
	ListClipsWithoutPipeline(ctx context.Context, limit int) ([]filler.StoreClip, error)
	// ClearClipVisionTags drops the vision stamp so the rung looks again (§10 V51b). ⚠ It does NOT
	// clear brand/era/category — those are shared with the text tagger and nothing records which
	// tier wrote them. See the implementation for why that asymmetry is the safe one.
	ClearClipVisionTags(ctx context.Context, path string, at time.Time) error
}

// FillerPullStore is the filler approval gate (§10 V35).
//
// Separate from FillerSourceStore on purpose: a pull is an APPROVAL object that happens to
// reference sources, and folding it in would make "the thing that lists where clips come from"
// also the thing that records what a human agreed to download.
//
// ⚠ There is no Delete. A decided pull is KEPT — the queue's History answers "what did we agree
// to download, and when, and who said so", which a delete erases. Same reason §7 keeps deny
// reasons on title proposals.
type FillerPullStore interface {
	GetPull(ctx context.Context, id string) (filler.Pull, error)
	// ListPulls returns pulls with the given status, newest first; an empty status means all.
	ListPulls(ctx context.Context, status filler.PullStatus) ([]filler.Pull, error)
	UpsertPull(ctx context.Context, p filler.Pull) error
}

// FillerAcquisitionStore is the reconnect truth for filler downloads and their resulting clip
// lifecycle. It is separate from sources and pulls because one run is an execution record, not a
// source definition or approval decision.
type FillerAcquisitionStore interface {
	UpsertAcquisitionRun(ctx context.Context, run filler.AcquisitionRun) error
	// RecoverInterruptedAcquisitionRuns marks work orphaned by the previous process as failed.
	// The beta is single-replica; startup is therefore the exact ownership boundary.
	RecoverInterruptedAcquisitionRuns(ctx context.Context, at time.Time) (int, error)
	GetAcquisitionRun(ctx context.Context, id string, at time.Time) (filler.AcquisitionRun, error)
	ListAcquisitionRuns(ctx context.Context, limit int, at time.Time) ([]filler.AcquisitionRun, error)
}

// InteractiveOperationStore is the reconnect truth for request-launched asynchronous work. It
// stores snapshots only; recurring/distributed scheduling remains owned by ScheduledJobStore.
type InteractiveOperationStore interface {
	UpsertInteractiveOperation(ctx context.Context, operation InteractiveOperation) error
	GetInteractiveOperation(ctx context.Context, id string) (InteractiveOperation, error)
	RecoverInterruptedInteractiveOperations(ctx context.Context, at time.Time) (int, error)
}

// FillerInferenceStore owns append-only call attribution and the atomic budget
// reservation that must succeed before hosted inference starts (§10 V62).
type FillerInferenceStore interface {
	ReserveInferenceEvaluation(ctx context.Context, evaluation InferenceEvaluation, budget InferenceBudget) (InferenceEvaluation, error)
	SettleInferenceEvaluation(ctx context.Context, id string, settlement InferenceSettlement) (InferenceEvaluation, error)
	GetInferenceEvaluation(ctx context.Context, id string) (InferenceEvaluation, error)
	ListInferenceEvaluations(ctx context.Context, filter InferenceEvaluationFilter) ([]InferenceEvaluation, error)
}

// FillerDecisionStore owns immutable V63 admission results and append-only
// operator actions. Projection rules remain in fillerdecision.Service.
type FillerDecisionStore interface {
	fillerdecision.Repository
}

// FillerSourceStore is the persisted REMOTE filler-source registry (§10, V33).
//
// ⚠ Remote sources only. The drop-folder and the media-server library stay DERIVED from config
// (see `GET /v1/filler/sources`, V28) — they answer "you could set one up but have not", which
// rows cannot express. These describe the specific archive.org collections an operator added, and
// they nest under that read-model's `remote` row rather than replacing any of it.
type FillerSourceStore interface {
	// ListFillerSources returns every registered remote source, oldest first, so the UI order
	// is stable across reloads.
	ListFillerSources(ctx context.Context) ([]FillerSource, error)
	// UpsertFillerSource adds or updates one source by id.
	UpsertFillerSource(ctx context.Context, src FillerSource) error
	// DeleteFillerSource removes a source. Clips it already brought in are NOT deleted:
	// they are files in the drop-folder, and forgetting where something came from is not a
	// reason to throw it away.
	DeleteFillerSource(ctx context.Context, id string) error
	// MarkFillerSourceFetched stamps a successful fetch, for the Sources tab's "last fetched".
	MarkFillerSourceFetched(ctx context.Context, id string, at time.Time) error
	// SetFillerSourceFetchPolicy writes one source's per-source fetch overrides (§10 V38c).
	//
	// ⚠ The ONLY writer of those columns, like SetFillerSourceEnabled owns `enabled` — the upsert
	// omits them so a re-register cannot blank an operator's tuning. nil clears an override back
	// to "inherit the global", which is a real action and must be expressible.
	SetFillerSourceFetchPolicy(ctx context.Context, id string, everySeconds, maxPerRun *int) error
	SetFillerSourceGeography(ctx context.Context, id string, geography filler.Geography) error
	// SetFillerSourceEnabled switches a source on or off (V35). ⚠ Disabling is NOT deleting:
	// the row keeps its licence and fetch history, and clips it already brought in stay in the
	// catalog. It only withdraws the source from future searching and downloading.
	SetFillerSourceEnabled(ctx context.Context, id string, enabled bool) error
	// SetFillerSourceAutoAdmit changes only catalog admission (§10 V57). It does not authorize
	// acquisition and cannot bypass grounding, matching, or per-channel exclusions.
	SetFillerSourceAutoAdmit(ctx context.Context, id string, autoAdmit bool) error
}

// AiringStore records what actually went to air — written from playout only.
type AiringStore interface {
	// RecordClipPlay counts a filler clip having AIRED globally and on one channel (V58).
	// `at` is its scheduled start: repeating that start is an idempotent no-op, while a later
	// start counts as another airing. Written from playout only; a missing catalog clip is not
	// an error because the durable channel exposure intentionally survives catalog pruning and
	// re-admission.
	RecordClipPlay(ctx context.Context, channelID, clipHash string, at time.Time) (recorded bool, err error)
	// FillerExposuresByChannel returns the aggregate history strictly before `before`.
	// A zero cutoff returns all history. The strict boundary makes a break's exposure snapshot
	// immutable while that break is going to air, so a reconcile cannot reshuffle its tail.
	FillerExposuresByChannel(ctx context.Context, channelID string, before time.Time) (map[string]filler.Exposure, error)
	// RecordAiring stamps that a PROGRAMME aired on a channel (§5, programming-design §3.1) —
	// the programme analogue of RecordClipPlay. Written from playout only, when a programme is
	// actually resolved for streaming; upserts one row per (channel, key) holding the LAST
	// airing, because the only question asked of it is "when did this last air here?".
	RecordAiring(ctx context.Context, channelID string, key provision.Key, libraryItemID string, at time.Time) error
	// LastAiredByChannel returns the most recent airing per key on one channel, for
	// recency-aware placement (programming-design §3.1). A key that has never aired is simply
	// absent — callers treat absence as "least recently aired", which sorts it first.
	LastAiredByChannel(ctx context.Context, channelID string) (map[provision.Key]time.Time, error)
}

// ActivityStore is the Dashboard feed (§5, §12, V32).
type ActivityStore interface {
	// RecordActivity appends one Dashboard feed row. Best-effort by contract: callers log
	// the error and carry on — recording that something happened must never be able to
	// stop it happening.
	RecordActivity(ctx context.Context, a Activity) error
	// ListActivity returns the newest feed rows first, capped at limit.
	ListActivity(ctx context.Context, limit int) ([]Activity, error)
	// PurgeActivity deletes feed rows older than `before` (§18.1 housekeeping). The feed
	// is the one append-only table here, so it is the one that needs a purge.
	PurgeActivity(ctx context.Context, before time.Time) (int, error)
}

// NotificationStore owns provider-neutral intents and bounded delivery work (§11).
type NotificationStore interface {
	SaveNotificationDestinationRecord(context.Context, notifications.DestinationRecord) error
	GetNotificationDestinationRecord(context.Context, string) (notifications.DestinationRecord, error)
	ListNotificationDestinationRecords(context.Context) ([]notifications.DestinationRecord, error)
	ListNotificationDestinationHealth(context.Context) (map[string]notifications.DestinationHealth, error)
	DeleteNotificationDestination(context.Context, string) error
	ListNotificationReferenceRecipients(context.Context, notifications.ReferenceKind, string) ([]string, error)
	CreateNotificationIntent(context.Context, notifications.Intent, []notifications.Attempt) (notifications.Intent, bool, error)
	GetNotificationIntent(context.Context, string) (notifications.Intent, error)
	ListNotificationIntentsByReference(context.Context, notifications.ReferenceKind, string) ([]notifications.Intent, error)
	ListNotificationAttempts(context.Context, string) ([]notifications.Attempt, error)
	ClaimDueNotificationAttempt(context.Context, string, time.Time, time.Duration) (notifications.Attempt, error)
	CompleteNotificationAttempt(context.Context, notifications.Completion) error
	PurgeTerminalNotifications(context.Context, time.Time) (int, error)
}

// InvitationStore owns administrator admission decisions and hashed grants (§11).
type InvitationStore interface {
	CreateInvitation(ctx context.Context, value invitation.Invitation, address *contact.Address) error
	GetInvitation(ctx context.Context, id string, now time.Time) (invitation.Invitation, error)
	GetInvitationByGrant(ctx context.Context, tokenHash string, now time.Time) (invitation.Invitation, error)
	ListInvitations(ctx context.Context, now time.Time) ([]invitation.Invitation, error)
	GetInvitationContactAddress(ctx context.Context, invitationID string) (contact.Address, error)
	ReplaceInvitationGrant(ctx context.Context, invitationID string, grant invitation.Grant, at time.Time) error
	AddInvitationGrant(ctx context.Context, invitationID string, grant invitation.Grant, at time.Time) error
	RevokeInvitationGrant(ctx context.Context, tokenHash string, at time.Time) error
	ListInvitationGrants(ctx context.Context, invitationID string) ([]invitation.Grant, error)
	RevokeInvitation(ctx context.Context, invitationID string, at time.Time) error
	PurgeTerminalInvitations(ctx context.Context, before time.Time) (int, error)
	RedeemInvitation(
		ctx context.Context,
		grantHash string,
		user User,
		session Session,
		at time.Time,
	) (invitation.Invitation, error)
}

// PasswordRecoveryStore owns local-password recovery lifecycles and hashed grants (§11).
type PasswordRecoveryStore interface {
	CreatePasswordRecovery(context.Context, recovery.Record) error
	GetPasswordRecovery(context.Context, string, time.Time) (recovery.Record, error)
	GetPasswordRecoveryByGrant(context.Context, string, time.Time) (recovery.Record, error)
	AddPasswordRecoveryGrant(context.Context, string, recovery.Grant, time.Time) error
	RevokePasswordRecoveryGrant(context.Context, string, time.Time) error
	ListPasswordRecoveryGrants(context.Context, string) ([]recovery.Grant, error)
	RedeemPasswordRecovery(context.Context, string, string, time.Time) (recovery.Record, error)
	PurgeTerminalPasswordRecoveries(context.Context, time.Time) (int, error)
}

// DiagnosticStore is the retained technical evidence surface (§5, §17). Activity is deliberately
// separate: it is a curated product feed, while these records are pageable/filterable diagnostics.
type DiagnosticStore interface {
	AppendDiagnosticEvents(ctx context.Context, records []diagnostics.Record) error
	ListDiagnosticEvents(ctx context.Context, limit int) ([]diagnostics.Record, error)
	QueryDiagnosticEvents(ctx context.Context, query diagnostics.EventStoreQuery) ([]diagnostics.Record, error)
	UpsertDiagnosticProcessRun(ctx context.Context, run diagnostics.ProcessRun) error
	GetDiagnosticProcessRun(ctx context.Context, id string) (diagnostics.ProcessRun, error)
	FindDiagnosticProcessRun(ctx context.Context, id string) (diagnostics.ProcessRun, bool, error)
	QueryDiagnosticProcessRuns(ctx context.Context, query diagnostics.ProcessStoreQuery) ([]diagnostics.ProcessRun, error)
	ListDiagnosticRetentionCandidates(ctx context.Context, before time.Time, limit int) ([]diagnostics.RetentionCandidate, error)
	DeleteDiagnosticEvent(ctx context.Context, id string) (bool, error)
	DeleteDiagnosticProcessRun(ctx context.Context, id string) (bool, error)
	DiagnosticRetainedBytes(ctx context.Context) (int64, error)
	PurgeDiagnostics(ctx context.Context, before time.Time, maxBytes int64) (diagnostics.PurgeResult, error)
}

// SettingStore is the settings KV (§5): instance id, per-app webhook last-received, etc.
type SettingStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	// WithSettingLock serializes one system-owned settings workflow. SQLite is a
	// single-process backend, so its lock is local; Postgres holds a session-level
	// advisory lock so replicas cannot overlap the protected external effects.
	// The callback must be idempotent because a process can still stop after an
	// external effect and before its durable checkpoint is written.
	WithSettingLock(ctx context.Context, key string, fn func(context.Context) error) error
	// ListSettings returns every persisted override with its audit metadata
	// (config-design §3). The settings service loads this into its snapshot; the
	// API surfaces updatedBy/updatedAt per field.
	ListSettings(ctx context.Context) ([]SettingRow, error)
	// ApplySettingBatch commits one settings PATCH's valid upserts and deletes in
	// one transaction. Readers therefore observe either the complete old settings
	// generation or the complete new one (config-design §8).
	ApplySettingBatch(ctx context.Context, batch SettingBatch) error
	// RewriteSettingValues atomically replaces values without changing their
	// audit metadata or authority. Startup uses it to encrypt legacy plaintext.
	RewriteSettingValues(ctx context.Context, rows []SettingMutation) error
	// UpsertSetting writes an override, stamping updated_at (epoch) and updated_by
	// (the admin who changed it; empty ⇒ NULL for env/migration/system writes).
	// This is the audited write path; SetSetting stays the un-audited system path
	// (instance id, webhook timestamps, the §8.1 model selection).
	UpsertSetting(ctx context.Context, row SettingRow) error
	// DeleteSetting removes an override so the key reverts to env/default
	// (config-design §9: an empty PATCH on an optional key clears it).
	DeleteSetting(ctx context.Context, key string) error
	// SetSettingEnvOverride claims a key for the app or hands it back to the
	// environment (config-design §3.1). Distinct from UpsertSetting because it
	// writes AUTHORITY, not a value: a plain save must never disturb the flag.
	// `seed` is the value to store when the row does not exist yet (the env value
	// being taken over, so unlocking does not blank the setting; empty for secrets,
	// which never seed). Existing rows keep their stored value.
	SetSettingEnvOverride(ctx context.Context, key string, on bool, seed, by string) error
}

// SecretProtectionStore owns wrapped data-encryption key persistence. Raw key
// material never crosses this seam.
type SecretProtectionStore interface {
	EnsureInstallationKeyFingerprint(context.Context, string) error
	EnsureSecretDataKey(context.Context, secretprotection.WrappedDataKey) (secretprotection.WrappedDataKey, error)
	RotateSecretDataKey(context.Context, secretprotection.WrappedDataKey) error
	ListSecretDataKeys(context.Context) ([]secretprotection.WrappedDataKey, error)
	ReplaceWrappedDataKeys(context.Context, string, string, []secretprotection.WrappedDataKey) error
}

// CountStore is the §17 /metrics state gauges. Read on scrape by the metrics
// collector, never on the write path.
type CountStore interface {
	// CountTitlesByState returns the record count per provisioning state; a
	// state with no rows is omitted (the collector zero-fills the known set).
	CountTitlesByState(ctx context.Context) (map[provision.State]int, error)
	// CountJobsByStatus returns the suggester-job count per status
	// (queued/running/done/failed) — the queue-depth gauge.
	CountJobsByStatus(ctx context.Context) (map[string]int, error)
	// OldestProposalJobsByStatus returns the oldest created_at retained for each
	// nonterminal caller-owned Proposal Job status. Empty statuses are absent.
	OldestProposalJobsByStatus(ctx context.Context) (map[string]time.Time, error)
	// CountProposalJobAttemptsByStatus counts retained terminal Attempts for
	// caller-owned Proposal Jobs. The metrics adapter bounds the label set.
	CountProposalJobAttemptsByStatus(ctx context.Context) (map[string]int, error)
	// CountFailedProposalJobsByCode counts retained failed caller-owned Proposal
	// Jobs by their bounded failure code. Unknown persisted values are returned so
	// the metrics adapter can collapse them to `other`.
	CountFailedProposalJobsByCode(ctx context.Context) (map[string]int, error)
	// CountActiveSessions returns the number of unexpired sessions as of now.
	CountActiveSessions(ctx context.Context, now time.Time) (int, error)
}

// InventoryStore persists provider-neutral Media Inventory aggregates (§5 V66). The methods are
// aggregate-shaped so no consumer can partially update the normalized six-table representation.
type InventoryStore interface {
	ApplyInventorySnapshot(ctx context.Context, snapshot inventory.Snapshot) (inventory.ItemID, error)
	InventoryItem(ctx context.Context, ref inventory.ItemRef) (inventory.Item, bool, error)
	RecordInventoryMeasurement(ctx context.Context, measurement inventory.Measurement) error
	MarkInventoryUnseen(ctx context.Context, authority inventory.AuthorityID, at time.Time, seen []inventory.OriginKey) error
}

// Store is the full persistence surface (§5) — the union of the per-domain
// interfaces above, which is what the composition root and the conformance suite
// hold. Callers that need one domain should depend on that domain's interface
// instead: the narrow role interfaces domain packages already declare for
// themselves (binder.Store, filler.Store, scheduler.ScheduleStore, …) are the
// pattern, and these groups exist so a consumer has something to name without
// re-declaring one.
//
// ⚠ The grouping is by DOMAIN, not by caller. A new method belongs in the group
// that owns its table; a group existing to serve one consumer would drift back
// into the 68-method union this replaced the moment a second consumer appeared.
type Store interface {
	TitleStore
	ChannelStore
	SeriesEpisodeStore
	JobStore
	ProposalStore
	ScheduledJobStore
	UserStore
	ClipStore
	FillerSourceStore
	FillerPullStore
	FillerAcquisitionStore
	InteractiveOperationStore
	FillerInferenceStore
	FillerDecisionStore
	SplitProposalStore
	AiringStore
	ActivityStore
	InvitationStore
	PasswordRecoveryStore
	NotificationStore
	DiagnosticStore
	SettingStore
	SecretProtectionStore
	CountStore
	ImageStore
	DiscoveryFeedbackStore
	DiscoveryQualityStore
	InventoryStore

	// Close releases the underlying database handle.
	Close() error
}
