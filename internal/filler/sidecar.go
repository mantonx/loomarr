package filler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/loomarr/loomarr/internal/mediatools"
)

// Info-JSON sidecars (§10). Both ingest paths write one next to every clip they
// download — yt-dlp via `--write-info-json`, and the Archive.org walker by hand in
// the same shape — precisely so AI tagging has real text to work with instead of a
// filename. Nothing read them until now: `Classify` was being handed `Clip.Source`,
// which is a PROVENANCE enum ("tunarr-local"), so every clip's prompt said
// "Source description: tunarr-local". That is worse than no description — it is a
// misleading signal fed to a classifier.
//
// Two different concepts had collapsed into one `string` field, which is why the
// compiler never caught it:
//   - Clip.Source     — provenance. Where the clip came from. An enum, for the catalog.
//   - sidecar text    — description. What the clip is ABOUT. Free text, for the LLM.

// sidecarInfo is the subset of the info-JSON we use. yt-dlp writes a large object;
// Archive.org's writer emits the same field names for the ones that matter. Unknown
// fields are ignored, so a yt-dlp version bump can't break parsing.
type sidecarInfo struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	// yt-dlp records the uploader/channel; Archive.org records the collection. Either
	// is a useful hint about what KIND of clip this is (a toy-ad channel vs a news reel).
	Uploader string `json:"uploader"`
	Channel  string `json:"channel"`
	// Upload date ("20240131" from yt-dlp) is a weak era hint — weak because it is when
	// the clip was UPLOADED, not when it aired. Deliberately not used for Era: a 1985
	// commercial uploaded in 2024 would be tagged 2024, which is exactly the kind of
	// confidently-wrong metadata §10's grounding pass exists to keep out.
	UploadDate string `json:"upload_date"`
	// License is the source's declared licence URL (V33). ⚠ NOT a text signal — it never
	// reaches the tagger's prompt, because "CC BY-NC-SA 4.0" says nothing about whether a
	// clip is a cereal advert. It is a catalog fact, read by SidecarLicense instead.
	License string `json:"license"`
}

// sidecarPathFor returns the info-JSON path for a media file: "clip.mp4" →
// "clip.info.json". Mirrors how both writers name them.
func sidecarPathFor(mediaPath string) string {
	return strings.TrimSuffix(mediaPath, filepath.Ext(mediaPath)) + ".info.json"
}

// Loomarr's own keys inside the info-JSON (§10 V38c). Namespaced under `loomarr` so they cannot
// collide with anything yt-dlp writes now or adds later.
const (
	loomarrKey = "loomarr"
	// fetchedByKey marks a clip Loomarr DOWNLOADED, as opposed to one an operator dropped in.
	//
	// ⚠ This replaces "the sidecar exists" as the held/filed signal (V38b → V38c). That worked
	// only while Loomarr never wrote sidecars; now that it writes tags for hand-dropped clips
	// too, existence says nothing. An explicit field is also the better signal — an operator who
	// copies a clip WITH its sidecar gets the honest answer, and one who tidies sidecars away no
	// longer flips a clip's lifecycle by accident.
	fetchedByKey = "fetchedBy"
	fetchedByUs  = "loomarr"
)

// SidecarFetchedMark is the `loomarr` block a DOWNLOADER writes into the info-JSON beside a clip
// it just fetched, so the sync holds it for review instead of filing it on sight (§10 V38c).
//
// ⚠ **Exported because the mark has to be written by whoever DOWNLOADED the file**, and that is
// `clipfetch`, which must not import this package's internals. Both sides reading one definition
// is the point: the sync's `wasFetchedByUs` looks for exactly this shape, and a second hand-rolled
// copy in the downloader is how the two silently stop agreeing.
//
// ⚠ **Nothing wrote it until V38c.8, so the approval gate never engaged for auto-fetched clips.**
// The `fetched=true` branch of `TakeIn` had no caller: both call sites pass false, correctly, since
// the sync cannot know who put a file in the watch folder. Only the downloader knows. Found by
// running auto-fetch against real archive.org collections and seeing every clip land `held=false`.
func SidecarFetchedMark() map[string]any {
	return SidecarFetchedMarkFor("")
}

// SidecarFetchedMarkFor also carries the exact registered source policy responsible for the
// acquisition (§10 V57). The empty-id form preserves the historical manual-ingest marker.
func SidecarFetchedMarkFor(sourceID string) map[string]any {
	return SidecarFetchedMarkForAcquisition(sourceID, "")
}

// SidecarFetchedMarkForAcquisition also records the durable acquisition run responsible for the
// fetched bytes. It is intentionally separate from SourceID: the source owns admission policy,
// while the acquisition identifies one observable attempt that may be retried or inspected.
func SidecarFetchedMarkForAcquisition(sourceID, acquisitionID string) map[string]any {
	mark := map[string]any{fetchedByKey: fetchedByUs}
	if sourceID != "" {
		mark["sourceId"] = sourceID
	}
	if acquisitionID != "" {
		mark["acquisitionId"] = acquisitionID
	}
	return mark
}

// SidecarLoomarrKey is the namespaced key `SidecarFetchedMark` belongs under. Exported for the
// same reason: the downloader writes it, this package reads it.
func SidecarLoomarrKey() string { return loomarrKey }

// SidecarTags is what Loomarr records about a clip, written back beside the file so the metadata
// travels with it (§10 V38c). Reset the database, move the folder, or take migration 00033 and
// the tagging returns on the next scan instead of being retyped.
type SidecarTags struct {
	// SourceID attributes an acquired clip to the registered source whose admission policy applies.
	SourceID string `json:"sourceId,omitempty"`
	// AcquisitionID attributes the clip to the durable download attempt that produced it.
	AcquisitionID string `json:"acquisitionId,omitempty"`
	// OriginalName is the filename the clip arrived with, captured BEFORE intake renamed it to
	// its hash (§10 V38c).
	//
	// ⚠ Load-bearing, not sentimental. §10's grounding rule accepts an era only when the year
	// appears literally in the clip's TEXT SIGNALS, and the filename is one of them
	// (`Frosted Flakes 1993.mp4`). Once a clip is stored as `a3f9….mp4` the path carries no year,
	// so without this every clip whose era came from its filename would become ungrounded. The
	// tagger reads it from here instead of from the path.
	OriginalName string `json:"originalName,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Era          int    `json:"era,omitempty"`
	Audience     string `json:"audience,omitempty"`
	Category     string `json:"category,omitempty"`
	// Brand is the GROUNDED advertiser (§10 V44) — carried here so a catalog rebuild restores it
	// rather than re-running the tagger (or, for a vision-grounded brand, re-paying for the vision
	// call) over the whole folder. Only a grounded brand is ever written to a clip in the first
	// place, so what round-trips here is a fact, never a guess — the same "metadata travels with
	// the clip" rule `originalName`/`normalizedLufs` follow.
	Brand           string `json:"brand,omitempty"`
	GeographicScope string `json:"geographicScope,omitempty"`
	Country         string `json:"country,omitempty"`
	Market          string `json:"market,omitempty"`
	Network         string `json:"network,omitempty"`
	Station         string `json:"station,omitempty"`
	AirDate         string `json:"airDate,omitempty"`
	GeoEvidence     string `json:"geographyEvidence,omitempty"`
	// Transcript is the clip's spoken text (§10 V44), persisted beside the file for the same reason
	// as `normalizedLufs`: it must survive a catalog rebuild, because re-deriving it means re-running
	// Whisper (~341s per clip under QEMU) over the whole folder. omitempty keeps a wordless or
	// not-yet-transcribed clip's sidecar unchanged.
	Transcript string `json:"transcript,omitempty"`
	// Confidence is the grounding-capped score (§10 V38). Carried so a restored catalog does not
	// have to re-run the tagger to know what it already worked out.
	Confidence int `json:"confidence,omitempty"`
	// SuggestedEra is an UNGROUNDED guess awaiting confirmation — never a tag. Carried
	// separately for the same reason the column is separate: restoring it as `Era` would
	// launder a guess into a fact, which is the §8 failure the grounding rule exists to prevent.
	SuggestedEra int `json:"suggestedEra,omitempty"`
	// NormalizedLUFS records that this clip's AUDIO WAS REWRITTEN to that target (§10 V42),
	// set only by the opt-in on-file loudness pass.
	//
	// ⚠ This is an IDEMPOTENCY MARKER, not a measurement, and the pass is broken without it. A
	// normalised file looks like any other file — a re-scan cannot tell by inspection that the
	// work already happened — so every pass would normalise an already-normalised clip and walk
	// its loudness down run after run. Compared against the CURRENT target, so lowering
	// `filler.target_lufs` re-normalises once and then stops.
	//
	// ⚠ It lives in the sidecar rather than the database for the same reason `originalName`
	// does: it must survive a catalog rebuild. A restored install that lost this marker would
	// re-normalise the whole folder.
	NormalizedLUFS float64 `json:"normalizedLufs,omitempty"`
	// Mezzanine records that this clip was re-encoded to the ingest profile (§10 V51b), holding
	// the profile's ID — `h264-crf20-aac192` — rather than a bare `true`.
	//
	// ⚠ An IDEMPOTENCY MARKER like `normalizedLufs`, and here the stakes are higher: a transcode
	// is a GENERATION OF LOSS. V66 retains the exact source master, but a second unnecessary pass
	// would still waste time and could make the visible playback identity drift. The pipeline
	// ladder normally prevents that, while this portable marker survives a catalog rebuild.
	//
	// ⚠ The profile ID rather than a flag, so a future profile CHANGE is expressible: a clip
	// carrying an older id is re-encoded (from the operator's own file, once), while a bare `true`
	// would silently pin every existing clip to whatever profile shipped first.
	Mezzanine string `json:"mezzanine,omitempty"`
	// MediaQuality is the content inspection measured during the playback encode. A pointer is
	// deliberate: nil means "not inspected yet", while a non-nil report with empty intervals is
	// the meaningful answer "inspected and clean". Keeping it beside the bytes prevents a catalog
	// rebuild from paying for another full decode or forgetting a prior refusal.
	MediaQuality *MediaQuality `json:"mediaQuality,omitempty"`
	// ConditioningLineage binds a reviewed child to the exact composite bytes and intended cut
	// that produced it. It lives beside the child because split proposals are consumed after
	// confirmation and the clip table is a rebuildable cache. Nil identifies a top-level clip.
	ConditioningLineage *ConditioningLineage `json:"conditioningLineage,omitempty"`
	// Conditioning carries the immutable measurements of the reviewed child before transcode and
	// of the staged playback derivative after transcode. It is written before publication.
	Conditioning *ConditioningEvidence `json:"conditioning,omitempty"`
	// ConditioningPublication is the owner-bound quarantine record written before a conditioned
	// target becomes visible. Sync must hold the target until this exact owner clears it after re-key.
	ConditioningPublication *ConditioningPublication `json:"conditioningPublication,omitempty"`
	// SupersededByHash is a durable quarantine marker for pre-rewrite bytes whose catalog identity
	// was successfully re-keyed but whose source cleanup has not completed.
	SupersededByHash string `json:"supersededByHash,omitempty"`
	// SplitPublicationToken is the opaque durable fencing owner of a reviewed split artifact.
	// Rollback may remove a visible child only while this exact token remains beside it, so a
	// recovered confirmer cannot lose its bytes to a stale predecessor.
	SplitPublicationToken string `json:"splitPublicationToken,omitempty"`
	// MediaAssets binds the playable catalog rendition to the immutable source master and every
	// reproducible derivative. It is portable authority beside the bytes, not a cache-only path hint.
	MediaAssets *MediaAssetManifest `json:"mediaAssets,omitempty"`
	// SegmentScreening points to the exact immutable subject and five-axis aggregate recorded for
	// this rendered child. It is a portable locator, never release authority: terminal admission
	// reprojects the current sidecar and replays every referenced evidence record.
	SegmentScreening *SegmentScreeningReference `json:"segmentScreening,omitempty"`
}

const segmentScreeningReferenceSchemaVersion = 1

// SegmentScreeningReference is the small durable join between portable media lineage and the
// private content-addressed screening repository.
type SegmentScreeningReference struct {
	SchemaVersion  int    `json:"schemaVersion"`
	SubjectSHA256  string `json:"subjectSha256"`
	EvidenceSHA256 string `json:"evidenceSha256"`
}

func NewSegmentScreeningReference(subject SegmentScreeningSubject, evidence SegmentScreeningEvidence) (SegmentScreeningReference, error) {
	if ValidateSegmentScreeningSubject(subject) != nil || ValidateSegmentScreeningEvidence(evidence) != nil {
		return SegmentScreeningReference{}, fmt.Errorf("segment screening reference requires valid subject and aggregate evidence")
	}
	reference := SegmentScreeningReference{
		SchemaVersion: segmentScreeningReferenceSchemaVersion,
		SubjectSHA256: subject.SHA256, EvidenceSHA256: evidence.SHA256,
	}
	if err := reference.validate(); err != nil || evidence.SubjectSHA256 != subject.SHA256 {
		return SegmentScreeningReference{}, fmt.Errorf("segment screening reference does not bind the subject and aggregate")
	}
	return reference, nil
}

func (r SegmentScreeningReference) validate() error {
	if r.SchemaVersion != segmentScreeningReferenceSchemaVersion || !isContentHash(r.SubjectSHA256) || !isContentHash(r.EvidenceSHA256) {
		return fmt.Errorf("segment screening reference is invalid")
	}
	return nil
}

type ConditioningPublication struct {
	State      string `json:"state"`
	Owner      string `json:"owner"`
	SourceHash string `json:"sourceHash"`
	TargetHash string `json:"targetHash"`
}

// ConditioningLineage is immutable provenance for one reviewed split interval.
type ConditioningLineage struct {
	ChildHash               string `json:"childHash"`
	ParentHash              string `json:"parentHash"`
	ParentAssetRole         string `json:"parentAssetRole,omitempty"`
	ParentAssetSHA256       string `json:"parentAssetSha256,omitempty"`
	StructureDecisionSHA256 string `json:"structureDecisionSha256,omitempty"`
	// StructureRole is the exact semantic role from the confirmed complete-timeline decision.
	// It remains distinct from SidecarTags.Kind because promo is projected to the legacy
	// interstitial catalog kind while its more precise assessment meaning must survive rebuilds.
	StructureRole   StructureSegmentRole `json:"structureRole,omitempty"`
	IntendedStartMs int64                `json:"intendedStartMs"`
	IntendedEndMs   int64                `json:"intendedEndMs"`
}

// ConditioningEvidence keeps measurements separate from policy decisions and target markers.
type ConditioningEvidence struct {
	BeforeRewriteHash              string                                `json:"beforeRewriteHash"`
	AfterRewriteHash               string                                `json:"afterRewriteHash"`
	BeforeRewrite                  mediatools.ConditioningMeasurement    `json:"beforeRewrite"`
	AfterRewrite                   mediatools.ConditioningMeasurement    `json:"afterRewrite"`
	DerivedParentEdgesAfterRewrite mediatools.ConditioningCutMeasurement `json:"derivedParentEdgesAfterRewrite"`
}

// WriteSidecarTags records Loomarr's metadata into the clip's info-JSON, preserving everything
// already in the file.
//
// ⚠ **Round-trips through a map, NOT through `sidecarInfo`.** That struct is a PARTIAL view — it
// declares only the fields we read — so unmarshalling into it and re-marshalling would silently
// delete every key yt-dlp wrote that we do not name: formats, thumbnails, chapters, view counts.
// Writing into someone else's file means preserving what you do not understand.
//
// ⚠ **This writes only `*.info.json`** — never the media file beside it.
//
// ⚠ That used to read "the media files themselves stay byte-for-byte untouched", full stop, and
// V42 made the unqualified version false. The TRANSCODE rung now snapshots the exact source into
// the retained master tree, builds independently verified evidence and playback derivatives, and
// may replace only the visible playable name after durable publication. The retained master is
// not deleted by that replacement.
//
// The guarantee this function keeps is unchanged — it writes `*.info.json` and nothing else. The
// package-level mutation and retention contracts live with the derivative publisher.
func WriteSidecarTags(mediaPath string, tags SidecarTags, fetched bool) error {
	path := sidecarPathFor(mediaPath)

	doc := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		// A sidecar that exists but does not parse is left ALONE rather than overwritten. It is
		// the operator's file; replacing something we cannot read is the one move guaranteed to
		// destroy information.
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("sidecar %s is not readable JSON; leaving it untouched: %w", path, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read sidecar %s: %w", path, err)
	}

	ours := map[string]any{}
	if existing, ok := doc[loomarrKey].(map[string]any); ok {
		ours = existing
	}
	// ⚠ `fetchedBy` is written ONLY when this call is the download path saying so, and is never
	// cleared. A tagging pass must not be able to un-mark a clip as fetched — that would flip its
	// lifecycle on the next scan, which is precisely the fragility this field replaced.
	if fetched {
		ours[fetchedByKey] = fetchedByUs
	}
	blob, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("encode sidecar tags: %w", err)
	}
	var tagMap map[string]any
	if err := json.Unmarshal(blob, &tagMap); err != nil {
		return fmt.Errorf("encode sidecar tags: %w", err)
	}
	for k, v := range tagMap {
		ours[k] = v
	}
	doc[loomarrKey] = ours

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sidecar %s: %w", path, err)
	}
	// ⚠ Write to a temp file and rename: a crash mid-write would otherwise leave a truncated
	// sidecar, and the next scan would read a clip's tags as gone. Rename is atomic within a
	// directory on every platform Loomarr targets.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil { //nolint:gosec // metadata beside media the operator already owns
		return fmt.Errorf("write sidecar %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace sidecar %s: %w", path, err)
	}
	return nil
}

// SidecarReadState preserves the difference between ordinary absence and damaged durable state.
type SidecarReadState uint8

const (
	SidecarAbsent SidecarReadState = iota
	SidecarValid
	SidecarInvalid
)

// ReadSidecarTagsState reads the whole sidecar without collapsing corrupt JSON or an invalid
// Loomarr object into ordinary absence. A valid third-party sidecar with no Loomarr key is valid
// and returns zero tags; only present malformed state is invalid.
func ReadSidecarTagsState(mediaPath string) (SidecarTags, SidecarReadState) {
	raw, err := os.ReadFile(sidecarPathFor(mediaPath))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return SidecarTags{}, SidecarAbsent
		}
		return SidecarTags{}, SidecarInvalid
	}
	tags, state, _ := decodeSidecarTags(raw)
	return tags, state
}

func decodeSidecarTags(raw []byte) (SidecarTags, SidecarReadState, bool) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return SidecarTags{}, SidecarInvalid, false
	}
	ours, present := doc[loomarrKey]
	if !present {
		return SidecarTags{}, SidecarValid, false
	}
	var fields map[string]json.RawMessage
	if string(ours) == "null" || json.Unmarshal(ours, &fields) != nil || fields == nil {
		return SidecarTags{}, SidecarInvalid, true
	}
	lineageRaw, hasLineage := fields["conditioningLineage"]
	if _, hasConditioning := fields["conditioning"]; hasConditioning && !hasLineage {
		return SidecarTags{}, SidecarInvalid, true
	}
	if hasLineage {
		var lineageFields map[string]json.RawMessage
		if string(lineageRaw) == "null" || json.Unmarshal(lineageRaw, &lineageFields) != nil || lineageFields == nil {
			return SidecarTags{}, SidecarInvalid, true
		}
		if !rawJSONString(lineageFields, "childHash") || !rawJSONString(lineageFields, "parentHash") ||
			!rawJSONInt64(lineageFields, "intendedStartMs") || !rawJSONInt64(lineageFields, "intendedEndMs") {
			return SidecarTags{}, SidecarInvalid, true
		}
		_, hasParentAssetRole := lineageFields["parentAssetRole"]
		_, hasParentAssetSHA := lineageFields["parentAssetSha256"]
		if hasParentAssetRole != hasParentAssetSHA || hasParentAssetRole &&
			(!rawJSONString(lineageFields, "parentAssetRole") || !rawJSONString(lineageFields, "parentAssetSha256")) {
			return SidecarTags{}, SidecarInvalid, true
		}
		_, hasStructureDecision := lineageFields["structureDecisionSha256"]
		_, hasStructureRole := lineageFields["structureRole"]
		if hasStructureDecision != hasStructureRole || hasStructureDecision &&
			(!rawJSONString(lineageFields, "structureDecisionSha256") || !rawJSONString(lineageFields, "structureRole")) {
			return SidecarTags{}, SidecarInvalid, true
		}
		if _, present := lineageFields["segmentScreeningSha256"]; present && !rawJSONString(lineageFields, "segmentScreeningSha256") {
			return SidecarTags{}, SidecarInvalid, true
		}
	}
	if conditioningRaw, hasConditioning := fields["conditioning"]; hasConditioning {
		var conditioningFields map[string]json.RawMessage
		if string(conditioningRaw) == "null" || json.Unmarshal(conditioningRaw, &conditioningFields) != nil || conditioningFields == nil ||
			!rawJSONString(conditioningFields, "beforeRewriteHash") || !rawJSONString(conditioningFields, "afterRewriteHash") {
			return SidecarTags{}, SidecarInvalid, true
		}
	}
	if publicationRaw, ok := fields["conditioningPublication"]; ok {
		var publicationFields map[string]json.RawMessage
		if string(publicationRaw) == "null" || json.Unmarshal(publicationRaw, &publicationFields) != nil || publicationFields == nil ||
			!rawJSONString(publicationFields, "state") || !rawJSONString(publicationFields, "owner") ||
			!rawJSONString(publicationFields, "sourceHash") || !rawJSONString(publicationFields, "targetHash") {
			return SidecarTags{}, SidecarInvalid, true
		}
	}
	if screeningRaw, ok := fields["segmentScreening"]; ok {
		var screening SegmentScreeningReference
		if string(screeningRaw) == "null" || json.Unmarshal(screeningRaw, &screening) != nil || screening.validate() != nil {
			return SidecarTags{}, SidecarInvalid, true
		}
	}
	var tags SidecarTags
	if json.Unmarshal(ours, &tags) != nil {
		return SidecarTags{}, SidecarInvalid, true
	}
	if tags.Kind != "" && !validKind(Kind(tags.Kind)) {
		return SidecarTags{}, SidecarInvalid, true
	}
	if _, present := fields["mediaAssets"]; present && (tags.MediaAssets == nil || tags.MediaAssets.validate() != nil) {
		return SidecarTags{}, SidecarInvalid, true
	}
	return tags, SidecarValid, true
}

func rawJSONString(fields map[string]json.RawMessage, name string) bool {
	raw, ok := fields[name]
	if !ok || string(raw) == "null" {
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil
}

func rawJSONInt64(fields map[string]json.RawMessage, name string) bool {
	raw, ok := fields[name]
	if !ok || string(raw) == "null" {
		return false
	}
	var value int64
	return json.Unmarshal(raw, &value) == nil
}

// ReadSidecarTags retains the historical convenience interface for callers where absence and
// unreadability have the same non-authoritative meaning. Safety-sensitive rebuild uses the typed
// state above.
func ReadSidecarTags(mediaPath string) (SidecarTags, bool) {
	raw, err := os.ReadFile(sidecarPathFor(mediaPath))
	if err != nil {
		return SidecarTags{}, false
	}
	tags, state, present := decodeSidecarTags(raw)
	return tags, state == SidecarValid && present
}

// ReadSidecarTagsFS is ReadSidecarTags against an fs.FS rather than the OS filesystem.
//
// ⚠ Two readers exist because the two callers genuinely differ, not by oversight: intake works in
// real directories it is moving files between, while the tagger holds the clip folder as an fs.FS
// (which is what makes it testable with fstest.MapFS and confines it to that subtree). Both parse
// the same shape; only the read differs.
func ReadSidecarTagsFS(fsys fs.FS, mediaPath string) (SidecarTags, bool) {
	raw, err := fs.ReadFile(fsys, sidecarPathFor(mediaPath))
	if err != nil {
		return SidecarTags{}, false
	}
	tags, state, present := decodeSidecarTags(raw)
	return tags, state == SidecarValid && present
}

// SidecarFetchedByUs reports whether Loomarr downloaded this clip (§10 V38c) — the held/filed
// fork's signal.
//
// ⚠ Reads the FIELD, not the file's existence. See fetchedByKey.
func SidecarFetchedByUs(mediaPath string) bool {
	raw, err := os.ReadFile(sidecarPathFor(mediaPath))
	if err != nil {
		return false
	}
	var doc struct {
		Loomarr struct {
			FetchedBy string `json:"fetchedBy"`
		} `json:"loomarr"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false
	}
	return doc.Loomarr.FetchedBy == fetchedByUs
}

// SidecarTitle reads the source-declared `title` from the info-JSON beside a clip (yt-dlp and the
// Archive downloader both write it). Returns "" when there is no sidecar, it does not parse, or the
// source declared no title.
//
// ⚠ This is the CLEAN display name, distinct from `SidecarTags.OriginalName` (§10 V44). The Archive
// downloader files clips as `"<archive-id> - <title>.mp4"` so ids cannot collide (archive.go), and
// `originalName` preserves that mangled filename — which is why the guide showed
// "CampbellsSoupAdvert - Campbell's Soup Advert". The `title` field carries just "Campbell's Soup
// Advert", so the scan prefers it for the DISPLAY name. It is deliberately NOT used for era
// grounding: `originalName` stays the grounding text (the year lives in the filename), so the two
// concerns — what a human READS vs what the tagger GROUNDS against — do not collapse into one field.
func SidecarTitle(mediaPath string) string {
	raw, err := os.ReadFile(sidecarPathFor(mediaPath))
	if err != nil {
		return ""
	}
	var info sidecarInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return ""
	}
	return strings.TrimSpace(info.Title)
}

// SidecarText reads the info-JSON beside a clip and renders the text signals as a
// short description for the tagger. Returns "" when there is no sidecar, it does not
// parse, or it carries nothing useful — an empty string is the honest answer, and
// tagUserPrompt already omits the line entirely rather than printing a blank one.
//
// Deliberately lossy: the model gets a few lines of real prose, not a JSON dump. A
// dump would bury the signal and burn tokens on fields (formats, thumbnails, chapter
// markers) that say nothing about what the clip IS.
func SidecarText(fsys fs.FS, mediaPath string) string {
	raw, err := fs.ReadFile(fsys, sidecarPathFor(mediaPath))
	if err != nil {
		return "" // no sidecar is normal — drop-folder clips never had one
	}
	var info sidecarInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return "" // malformed sidecar degrades to filename-only, never fails the tag
	}
	return info.text()
}

// text renders the parsed sidecar into the prompt fragment. Kept separate from the
// file read so the rendering rules are testable without a filesystem.
func (s sidecarInfo) text() string {
	var parts []string
	if t := strings.TrimSpace(s.Title); t != "" {
		parts = append(parts, t)
	}
	// Uploader/channel, whichever the writer filled in. Both being present is normal
	// for yt-dlp (they're often equal), so don't print it twice.
	by := strings.TrimSpace(s.Uploader)
	if by == "" {
		by = strings.TrimSpace(s.Channel)
	}
	if by != "" && !strings.EqualFold(by, strings.TrimSpace(s.Title)) {
		parts = append(parts, "from "+by)
	}
	// Descriptions can run to hundreds of lines of boilerplate (subscribe links,
	// timestamps). Take the leading prose — the part that actually describes the clip —
	// and cap it so one verbose uploader can't dominate the prompt.
	if d := firstProse(s.Description); d != "" {
		parts = append(parts, d)
	}
	return strings.Join(parts, " · ")
}

// maxDescriptionRunes bounds the description contribution. Generous enough for a real
// two-sentence summary, small enough that a wall of boilerplate can't crowd out the
// filename and the system prompt.
const maxDescriptionRunes = 280

// firstProse takes the leading non-empty lines of a description, stopping at the
// first line that looks like boilerplate rather than prose, and truncates on a rune
// boundary so multi-byte text is never cut mid-character.
func firstProse(desc string) string {
	var b strings.Builder
	for _, line := range strings.Split(desc, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if b.Len() > 0 {
				break // blank line after real content ends the lead paragraph
			}
			continue
		}
		if isBoilerplate(line) {
			break
		}
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(line)
		if len([]rune(b.String())) >= maxDescriptionRunes {
			break
		}
	}
	out := []rune(strings.TrimSpace(b.String()))
	if len(out) > maxDescriptionRunes {
		return strings.TrimSpace(string(out[:maxDescriptionRunes])) + "…"
	}
	return string(out)
}

// isBoilerplate spots the lines that pad uploader descriptions and carry no signal
// about the clip's content. Narrow on purpose: a false positive only truncates the
// description early, but being too eager would drop real prose.
func isBoilerplate(line string) bool {
	l := strings.ToLower(line)
	switch {
	case strings.HasPrefix(l, "http://"), strings.HasPrefix(l, "https://"):
		return true
	case strings.HasPrefix(l, "subscribe"), strings.HasPrefix(l, "follow "):
		return true
	case strings.Contains(l, "patreon.com"), strings.Contains(l, "ko-fi.com"):
		return true
	}
	return false
}

// SidecarLicense reads the licence URL a source declared for a clip (V33). Returns "" when
// there is no sidecar, it does not parse, or the source declared none.
//
// ⚠ **Empty means UNKNOWN, never "public domain".** About 92% of Archive items carry no
// licence at all (667 of 8362 in `classic_tv_commercials` — measured during the 2026-07-31
// fixture capture), so absence is the common case and says nothing about permission. Callers
// render "unknown", never a reassuring default.
//
// ⚠ Separate from SidecarText, deliberately. That function builds PROSE for the tagger, and a
// licence is a catalog fact — "CC BY-NC-SA 4.0" tells a model nothing about whether a clip is a
// cereal advert, and it would burn prompt tokens. Worse, it would not survive the trip:
// `isBoilerplate` drops any line starting with http(s)://, which is every licence URL.
func SidecarLicense(fsys fs.FS, mediaPath string) string {
	raw, err := fs.ReadFile(fsys, sidecarPathFor(mediaPath))
	if err != nil {
		return "" // no sidecar is normal — drop-folder clips never had one
	}
	var info sidecarInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return ""
	}
	return strings.TrimSpace(info.License)
}
