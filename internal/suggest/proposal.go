// Package suggest is the Suggester (design §8): it turns a channel intent into a
// grounded proposal (a lineup from the library + an acquisition list of missing
// titles). It owns the grounding loop — the LLM proposes names or candidates,
// but only real ids returned by Catalog operations survive before display,
// and nothing is auto-executed (§8 human-in-the-loop). Generation runs as a
// persisted job (§8 execution model); this package is the pure suggestion logic,
// driven by a worker (Phase 11e) and exposed via the API (Phase 11f).
//
// Source ownership follows the generation pipeline: suggester.go owns the public
// facade and model loop, prompt.go owns prompt messages, tools.go owns catalog
// tool execution, proposal_grounding.go owns the trust boundary that constructs a
// Proposal, and intent_policy.go owns deterministic interpretation of user intent.
package suggest

import (
	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/reference"
	"github.com/loomarr/loomarr/internal/schedule"
)

// Intent is a channel request: an NL description plus optional constraints (§8).
type Intent struct {
	Description string   `json:"description"`                // "90s action movies"
	Era         string   `json:"era,omitempty"`              // e.g. "1990s"
	Tone        string   `json:"tone,omitempty"`             // e.g. "high-energy"
	RuntimeTgt  int      `json:"runtimeTargetMin,omitempty"` // total target minutes
	MustInclude []string `json:"mustInclude,omitempty"`      // titles/terms to include
	MustExclude []string `json:"mustExclude,omitempty"`      // titles/terms to exclude
	MaxAcquire  int      `json:"maxAcquisitions,omitempty"`  // cap on acquisitions (§8 quota)
	// Refine inputs (§7 refine): a free-text change ("add more Schwarzenegger, drop the
	// slow ones") plus the channel's CURRENT lineup as context. The prompt renders these
	// so the model reasons from what's already on the channel and returns a revised
	// lineup. Context only — new picks are still grounded through Catalog operations, so
	// refine can't invent titles. Empty on a fresh (non-refine) suggestion.
	RefineText    string          `json:"refineText,omitempty"`
	CurrentLineup []LineupContext `json:"currentLineup,omitempty"`
	// Adjacent are pre-seeded candidates from the recommendation graph walked over this
	// channel's own lineup (programming-design §8.3) — the deterministic second corpus,
	// merged with whatever the model finds through the Catalog.
	//
	// They are OFFERED, never placed: the model still chooses, and an offered title it
	// ignores is simply not picked. Grounding is unweakened because these are real
	// catalog candidates with real ids that went through the same presence backfill a
	// tool result does — Suggest seeds them into `surfaced` before generation, so
	// buildProposal's "every pick traces to a candidate the catalog returned" invariant
	// holds verbatim. Merging AFTER generation instead would append picks the chokepoint
	// never checked, which is the one thing grounding exists to prevent.
	//
	// Empty on a fresh suggestion (no lineup to walk from) and on any install without a
	// TMDB corpus wired.
	Adjacent []AdjacentContext `json:"adjacent,omitempty"`
	// ReferenceTitles are bounded, source-resolved title anchors for this one
	// Suggest execution. They never enter the API or persisted Intent JSON.
	ReferenceTitles     []string `json:"-"`
	ReferenceResolved   bool     `json:"-"`
	referenceEvidence   reference.Evidence
	referenceKeys       map[provision.Key]bool
	referenceCandidates []catalog.Candidate
	// DiscoveryScopeID is internal execution context for channel-specific explicit
	// feedback during re-curation. It never enters the API or persisted intent JSON.
	DiscoveryScopeID string `json:"-"`
}

// LineupContext is a lightweight "what's on this channel now" entry fed to the refiner —
// title/year for the model to reason about, plus the real key so a kept item can be
// matched back exactly. Display/context only; not an identity the grounding gate checks.
type LineupContext struct {
	Name string `json:"name"`
	Year int    `json:"year,omitempty"`
	Key  string `json:"key,omitempty"` // provisioning key, e.g. "movie:tmdb:603"
}

// AdjacentContext is one pre-seeded adjacency candidate (§8.3): a title the channel's own
// lineup points at through TMDB's recommendation graph.
//
// Votes is how many of the channel's titles independently recommended it — the consensus
// that makes this a signal rather than noise, and the reason the candidate can explain
// itself ("recommended by 5 of your films") where an LLM pick can only paraphrase. It is
// rendered in the prompt so the model can weigh a strong consensus differently from a
// marginal one, and it is what the approval card shows.
type AdjacentContext struct {
	Name  string `json:"name"`
	Year  int    `json:"year,omitempty"`
	Key   string `json:"key,omitempty"`
	Votes int    `json:"votes,omitempty"`
}

// ProposalItem is one entry in a lineup or acquisition list (§8 output contract).
// Identity is always a real external id (the grounding guarantee); Name/Year are
// for display only. It mirrors a catalog.Candidate plus the LLM's rationale.
type ProposalItem struct {
	MediaType provision.MediaType `json:"mediaType"`
	TMDBID    int                 `json:"tmdbId,omitempty"`
	TVDBID    int                 `json:"tvdbId,omitempty"`
	Name      string              `json:"name"`
	Year      int                 `json:"year,omitempty"`
	Seasons   []int               `json:"seasons,omitempty"` // series ACQUISITION (what to download)
	// SeasonMin/SeasonMax: optional AIRING season window for a series pick (which
	// seasons play on the channel), distinct from Seasons (what to acquire). Set by
	// the grounded suggester when the intent implies an era ("classic" → 1–10);
	// carried onto LineupEntry.SeasonMin/Max and enforced at series expansion (§9).
	// 0 = unbounded on that end. Validated + clamped by the suggester before it lands.
	SeasonMin        int                       `json:"seasonMin,omitempty"`
	SeasonMax        int                       `json:"seasonMax,omitempty"`
	EpisodeSelection schedule.EpisodeSelection `json:"episodeSelection,omitempty"`
	InLibrary        bool                      `json:"inLibrary"`
	LibraryItemID    string                    `json:"libraryItemId,omitempty"`
	Rationale        string                    `json:"rationale,omitempty"` // why-it-fits (LLM)
	Confidence       float64                   `json:"confidence,omitempty"`
	// Source records which corpus surfaced this pick, carried from the grounded Candidate
	// (§8.3). It is PROVENANCE, never identity — Key() ignores it, exactly as it ignores
	// Genres/Overview/OfficialRating.
	//
	// The re-curation quality bar reads it: an adjacency pick arrives with a consensus the
	// model didn't compute and can't see, so a bare 0 confidence from a model that declined
	// to score a title it was handed must not be read as "the model judged this poorly".
	Source string `json:"source,omitempty"`
	// AdjacentVotes is how many of the channel's own titles independently recommended this
	// pick (§8.3) — 0 for anything that did not come from the adjacency corpus.
	//
	// It rides to the approval surface because it is the REASON: "recommended by 5 of your
	// films" is a claim an operator can check, where an LLM rationale can only be read. The
	// consensus is also what the unscored-pick floor trusts, so showing it makes that
	// decision legible rather than magic.
	AdjacentVotes int `json:"adjacentVotes,omitempty"`
	// Genres + Overview carry from the grounded Candidate so deterministic theme
	// scoring measures real metadata, not the title string (§8). Display/scoring
	// only — never identity (Key ignores them).
	Genres           []string `json:"genres,omitempty"`
	Overview         string   `json:"overview,omitempty"`
	OriginalLanguage string   `json:"originalLanguage,omitempty"`
	OriginCountries  []string `json:"originCountries,omitempty"`
	RuntimeMinutes   int      `json:"runtimeMinutes,omitempty"`
	VoteAverage      float64  `json:"voteAverage,omitempty"`
	VoteCount        int      `json:"voteCount,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	Networks         []string `json:"networks,omitempty"`
	Cast             []string `json:"cast,omitempty"`
	Creators         []string `json:"creators,omitempty"`
	// OfficialRating carries from the grounded Candidate for ChannelPolicy audience
	// enforcement (programming-design §4): it's stamped onto the channel's lineup
	// entry at create time so enforcement filters without a library hit. Display/
	// enforcement only — never identity.
	OfficialRating string `json:"officialRating,omitempty"`
}

// Key derives the provisioning key (§3), enforcing the grounding guarantee: a
// validated ProposalItem always has a usable id.
func (p ProposalItem) Key() (provision.Key, error) {
	return provision.Title{
		MediaType: p.MediaType, TMDBID: p.TMDBID, TVDBID: p.TVDBID,
		Name: p.Name, Year: p.Year, Seasons: p.Seasons,
	}.Key()
}

// fromCandidate builds a ProposalItem from a grounded catalog Candidate, carrying
// the rationale/confidence the model attached.
func fromCandidate(c catalog.Candidate, rationale string, confidence float64) ProposalItem {
	return ProposalItem{
		MediaType: c.MediaType, TMDBID: c.TMDBID, TVDBID: c.TVDBID,
		Name: c.Name, Year: c.Year, InLibrary: c.InLibrary, LibraryItemID: c.LibraryItemID,
		Rationale: rationale, Confidence: confidence, Source: string(c.Source),
		Genres: c.Genres, Overview: c.Overview, OfficialRating: c.OfficialRating,
		OriginalLanguage: c.OriginalLanguage,
		OriginCountries:  append([]string(nil), c.OriginCountries...),
		RuntimeMinutes:   c.RuntimeMinutes,
		VoteAverage:      c.VoteAverage,
		VoteCount:        c.VoteCount,
		Keywords:         append([]string(nil), c.Keywords...),
		Networks:         append([]string(nil), c.Networks...),
		Cast:             append([]string(nil), c.Cast...),
		Creators:         append([]string(nil), c.Creators...),
	}
}

// Proposal is the suggester's output (§8): a lineup of library items, an
// acquisition list of missing titles, ranked alternates, deterministic scores,
// and a rationale. Approved acquisitions feed the provisioner; the approved
// lineup feeds the scheduler.
type Proposal struct {
	Intent Intent `json:"intent"`
	// ChannelName is the LLM's proposed channel name (§8) — a real title derived from the
	// intent + grounded picks (e.g. "Springfield Classics"), not the raw prompt. Empty ⇒
	// the API falls back to a truncated intent description.
	ChannelName  string         `json:"channelName,omitempty"`
	Lineup       []ProposalItem `json:"lineup"`       // in-library items, ordered
	Acquisitions []ProposalItem `json:"acquisitions"` // missing titles to acquire
	Alternates   []ProposalItem `json:"alternates"`   // ranked backups (§9 substitution)
	Scores       Scores         `json:"scores"`       // deterministic post-scoring
	Rationale    string         `json:"rationale,omitempty"`
	// Policy is the grounded ChannelPolicy the suggester extracted (programming-
	// design §8): scope/audience/ordering/seasonal, validated + clamped (off-ladder
	// ceilings dropped unless explicit child safety supplies its deterministic bound;
	// era bounded). It rides the proposal into channel-create,
	// where it lands on the channel row. Empty = the channel uses built-in defaults.
	Policy schedule.ChannelPolicy `json:"policy,omitempty"`
	// Retired are lineup keys the auto-curate turnstile decided to rotate OUT to make room
	// for this proposal's incoming titles (§8.2a). Written by `recurate`, applied by the
	// binder — never by the suggester, and never by a human approval.
	//
	// ⚠ It rides the PROPOSAL rather than being applied directly to the channel, and that is
	// the whole point of the field. `recurate` used to trim `ch.Lineup` and call UpsertChannel
	// itself, which made it a second lineup writer racing the binder's additive union — the
	// two were ordered against each other by a code comment, and an additive union that ran
	// first would put every retired title straight back. One writer (the binder), one
	// primitive (schedule.ApplyLineup), and the retirement is now an INPUT to it.
	Retired []provision.Key `json:"retired,omitempty"`
	// Refused are picks the model grounded that this proposal's OWN extracted policy cannot
	// air (§4). They are moved out of Lineup/Acquisitions and kept here with a reason, so the
	// approval card shows what will not play instead of offering it.
	//
	// ⚠ Kept rather than deleted, deliberately. The operator's fix is usually to raise the
	// ceiling, not to lose the title — and a pick that silently vanished between the model's
	// answer and the approval screen is indistinguishable from one the model never made.
	Refused []RefusedPick `json:"refused,omitempty"`
	// Trace is immutable evidence from the original grounded proposal run. It is
	// never updated by approval edits or later scheduler/channel decisions.
	Trace DecisionTrace `json:"trace"`
}

// RefusedPick is one grounded pick the extracted policy will not air, with the exclusion
// vocabulary §4 already uses (`over_ceiling`) so the approval card and the channel's exclusion
// report say the same words about the same event.
type RefusedPick struct {
	Item   ProposalItem `json:"item"`
	Reason string       `json:"reason"` // "over_ceiling"
}

// Scores is the deterministic post-scoring layered on the LLM output (§8) so
// ranking isn't pure vibes. Criteria are configurable; v1 ships three.
type Scores struct {
	ThemeFit          float64 `json:"themeFit"`          // how well items match the intent terms
	AvailabilityRatio float64 `json:"availabilityRatio"` // in-library / total (live-now readiness)
	EraBalance        float64 `json:"eraBalance"`        // spread across the target era/years
	Overall           float64 `json:"overall"`           // weighted composite
}
