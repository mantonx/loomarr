package fillerreview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filleradmission"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/mediatools"
)

const (
	TemporalTruthEvidenceSchemaVersion   = 1
	TemporalTruthEvidenceContractVersion = "filler-temporal-truth-evidence-v1"
	TemporalTruthEvidenceVersion         = "filler-temporal-truth-evidence-2026-09-01-v1"
	TemporalTruthMaximumVideoBytes       = int64(64 << 20)
)

type TemporalTruthToolIdentity struct {
	Path         string `json:"path"`
	Version      string `json:"version,omitempty"`
	BinarySHA256 string `json:"binarySha256"`
	SourceSHA256 string `json:"sourceSha256,omitempty"`
}

type TemporalTruthEvidenceMedia interface {
	Identity() TemporalTruthMediaIdentity
	Analyze(context.Context, string, int64, int64, float64) ([]mediatools.Interval, []mediatools.Interval, []int64, error)
	WriteReviewVideo(context.Context, string, int64, int64, string) (TemporalTruthVideoInfo, error)
	Frames(context.Context, string, []int64) ([][]byte, error)
}

type TemporalTruthMediaIdentity struct {
	FFmpeg  TemporalTruthToolIdentity `json:"ffmpeg"`
	FFprobe TemporalTruthToolIdentity `json:"ffprobe"`
}

type TemporalTruthVideoInfo struct {
	DurationMS int64
	Width      int
	Height     int
	HasAudio   bool
	Profile    TemporalTruthVideoProfile
}

// TemporalTruthVideoProfile contains measured stream facts, not encoder requests.
type TemporalTruthVideoProfile struct {
	VideoCodec   string `json:"videoCodec"`
	PixelFormat  string `json:"pixelFormat"`
	FrameRate    string `json:"frameRate"`
	AudioCodec   string `json:"audioCodec"`
	SampleRate   int    `json:"sampleRate"`
	Channels     int    `json:"channels"`
	AudioStreams int    `json:"audioStreams"`
}

type TemporalTruthEvidenceOCR interface {
	Identity() TemporalTruthToolIdentity
	Recognize(context.Context, []TemporalTruthOCRInput) ([]TemporalTruthOCRResult, error)
}

type TemporalTruthOCRInput struct {
	Path   string
	SHA256 string
}

type TemporalTruthOCRResult struct {
	SHA256       string
	Observations []TemporalTruthOCRObservation
}

type TemporalTruthOCRObservation struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
}

type TemporalTruthEvidenceConfig struct {
	SelectionPath      string
	DraftPath          string
	DownloadLedgerPath string
	MediaRoot          string
	PacketsPath        string
	PacketRoot         string
	TranscriptsPath    string
	OutputDir          string
	GeneratedAt        time.Time
	SceneThreshold     float64
	PerCaseTimeout     time.Duration
	Media              TemporalTruthEvidenceMedia
	OCR                TemporalTruthEvidenceOCR
}

type TemporalTruthEvidenceManifest struct {
	SchemaVersion   int                           `json:"schemaVersion"`
	ContractVersion string                        `json:"contractVersion"`
	EvidenceVersion string                        `json:"evidenceVersion"`
	GeneratedAt     time.Time                     `json:"generatedAt"`
	SelectionSHA256 string                        `json:"selectionSha256"`
	MediaTools      TemporalTruthMediaIdentity    `json:"mediaTools"`
	OCR             TemporalTruthOCRStatus        `json:"ocr"`
	Config          TemporalTruthEvidenceSettings `json:"config"`
	Cases           []TemporalTruthEvidenceCase   `json:"cases"`
}

type TemporalTruthEvidenceSettings struct {
	SceneThreshold       float64 `json:"sceneThreshold"`
	MaximumFramesPerCase int     `json:"maximumFramesPerCase"`
	MaximumVideoBytes    int64   `json:"maximumVideoBytes"`
	PerCaseTimeoutMS     int64   `json:"perCaseTimeoutMs"`
}

type TemporalTruthOCRStatus struct {
	Status            string                     `json:"status"`
	Engine            *TemporalTruthToolIdentity `json:"engine,omitempty"`
	MinimumConfidence float64                    `json:"minimumConfidence,omitempty"`
}

type TemporalTruthEvidenceCase struct {
	Alias              string                            `json:"alias"`
	DurationMS         int64                             `json:"durationMs"`
	Plan               TemporalEvidencePlan              `json:"plan"`
	Video              TemporalTruthEvidenceFile         `json:"video"`
	Frames             []TemporalTruthEvidenceFrame      `json:"frames"`
	TranscriptSegments []fillerbakeoff.TranscriptSegment `json:"transcriptSegments,omitempty"`
}

type TemporalTruthEvidenceFile struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
	DurationMS int64  `json:"durationMs,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

type TemporalTruthEvidenceFrame struct {
	ID     string                        `json:"id"`
	Path   string                        `json:"path"`
	SHA256 string                        `json:"sha256"`
	Bytes  int64                         `json:"bytes"`
	Width  int                           `json:"width"`
	Height int                           `json:"height"`
	AtMS   int64                         `json:"atMs"`
	OCR    []TemporalTruthOCRObservation `json:"ocr,omitempty"`
}

type TemporalTruthEvidencePrivateMap struct {
	SchemaVersion        int                                 `json:"schemaVersion"`
	ContractVersion      string                              `json:"contractVersion"`
	SelectionSHA256      string                              `json:"selectionSha256"`
	DraftSHA256          string                              `json:"draftSha256"`
	DownloadLedgerSHA256 string                              `json:"downloadLedgerSha256"`
	PacketsSHA256        string                              `json:"packetsSha256"`
	TranscriptSetSHA256  string                              `json:"transcriptSetSha256"`
	Entries              []TemporalTruthEvidencePrivateEntry `json:"entries"`
}

type TemporalTruthEvidencePrivateEntry struct {
	Alias            string `json:"alias"`
	CaseID           string `json:"caseId"`
	ContentSHA256    string `json:"contentSha256"`
	SourceLocalFile  string `json:"sourceLocalFile"`
	SourceSHA256     string `json:"sourceSha256"`
	PacketSHA256     string `json:"packetSha256"`
	TranscriptSHA256 string `json:"transcriptSha256,omitempty"`
}

type temporalTruthDownloadLedger struct {
	SchemaVersion   int                           `json:"schemaVersion"`
	InventorySHA256 string                        `json:"inventorySha256"`
	GeneratedAt     time.Time                     `json:"generatedAt"`
	MaxRequests     int                           `json:"maxRequests"`
	RequestsUsed    int                           `json:"requestsUsed"`
	MaxItems        int                           `json:"maxItems"`
	MaxBytes        int64                         `json:"maxBytes"`
	Bytes           int64                         `json:"bytes"`
	Cases           []temporalTruthDownloadedCase `json:"cases"`
}

type temporalTruthDownloadedCase struct {
	CaseID              string                               `json:"caseId"`
	Authority           string                               `json:"authority"`
	ItemID              string                               `json:"itemId"`
	LicenseURL          string                               `json:"licenseUrl"`
	ItemURL             string                               `json:"itemUrl"`
	MetadataURL         string                               `json:"metadataUrl"`
	MetadataRetrievedAt time.Time                            `json:"metadataRetrievedAt"`
	MetadataSHA256      string                               `json:"metadataSha256"`
	Representation      fillercorpus.InventoryRepresentation `json:"representation"`
	LocalFile           string                               `json:"localFile"`
	ContentSHA256       string                               `json:"contentSha256"`
	Approval            fillercorpus.RightsDecision          `json:"approval"`
	VerifiedAt          time.Time                            `json:"verifiedAt"`
}

// BuildTemporalTruthEvidence atomically publishes separate public and private
// roots. The public manifest has no corpus IDs, filenames, labels, or selection
// reasons; the private map is the only unblinding bridge.
func BuildTemporalTruthEvidence(ctx context.Context, config TemporalTruthEvidenceConfig) (TemporalTruthEvidenceManifest, error) {
	if err := validateTemporalTruthEvidenceConfig(config); err != nil {
		return TemporalTruthEvidenceManifest{}, err
	}
	selectionRaw, err := os.ReadFile(config.SelectionPath)
	if err != nil {
		return TemporalTruthEvidenceManifest{}, fmt.Errorf("read selection: %w", err)
	}
	selection, err := fillereval.DecodeTemporalTruthSelection(selectionRaw)
	if err != nil {
		return TemporalTruthEvidenceManifest{}, err
	}
	draft, err := readStrictJSON[fillereval.Manifest](config.DraftPath)
	if err != nil {
		return TemporalTruthEvidenceManifest{}, fmt.Errorf("read draft: %w", err)
	}
	if failures := fillereval.ValidateReviewDraft(draft); len(failures) > 0 || len(draft.Cases) != 300 {
		return TemporalTruthEvidenceManifest{}, fmt.Errorf("evidence draft is not the exact 300-case seed: %s", strings.Join(failures, "; "))
	}
	ledger, err := readStrictJSON[temporalTruthDownloadLedger](config.DownloadLedgerPath)
	if err != nil {
		return TemporalTruthEvidenceManifest{}, fmt.Errorf("read download ledger: %w", err)
	}
	packets, err := readPackets(config.PacketsPath)
	if err != nil {
		return TemporalTruthEvidenceManifest{}, fmt.Errorf("read packets: %w", err)
	}
	transcripts, err := readStrictHistoryJSONL[fillerbakeoff.TranscriptArtifact](config.TranscriptsPath)
	if err != nil {
		return TemporalTruthEvidenceManifest{}, fmt.Errorf("read transcripts: %w", err)
	}
	if ledger.SchemaVersion != 1 || len(ledger.Cases) != 300 || len(packets) != 300 || len(transcripts) != 298 {
		return TemporalTruthEvidenceManifest{}, fmt.Errorf("evidence inputs require the exact 300-case ledger/packet set and 298-case transcript set")
	}

	selectionSHA := hashBytes(selectionRaw)
	draftSHA, ledgerSHA, packetsSHA, transcriptsFileSHA := historyDigests(config.DraftPath, config.DownloadLedgerPath, config.PacketsPath, config.TranscriptsPath)
	if draftSHA == "" || ledgerSHA == "" || packetsSHA == "" || transcriptsFileSHA == "" || !temporalTruthSelectionHasInput(selection, "draft", draftSHA) {
		return TemporalTruthEvidenceManifest{}, fmt.Errorf("selection and exact evidence inputs do not bind the same draft")
	}
	draftByID := make(map[string]fillereval.Case, len(draft.Cases))
	for _, item := range draft.Cases {
		draftByID[item.ID] = item
	}
	ledgerByID := make(map[string]temporalTruthDownloadedCase, len(ledger.Cases))
	for _, item := range ledger.Cases {
		if _, duplicate := ledgerByID[item.CaseID]; duplicate {
			return TemporalTruthEvidenceManifest{}, fmt.Errorf("download ledger repeats case %q", item.CaseID)
		}
		ledgerByID[item.CaseID] = item
	}
	transcriptByID := make(map[string]fillerbakeoff.TranscriptArtifact, len(transcripts))
	for _, item := range transcripts {
		if _, duplicate := transcriptByID[item.CaseID]; duplicate {
			return TemporalTruthEvidenceManifest{}, fmt.Errorf("transcript set repeats case %q", item.CaseID)
		}
		transcriptByID[item.CaseID] = item
	}

	stage, err := beginTemporalTruthEvidenceStage(config.OutputDir)
	if err != nil {
		return TemporalTruthEvidenceManifest{}, err
	}
	defer stage.Cleanup()
	publicRoot, privateRoot := filepath.Join(stage.path, "public"), filepath.Join(stage.path, "private")
	if err := os.MkdirAll(publicRoot, 0o750); err != nil {
		return TemporalTruthEvidenceManifest{}, err
	}
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		return TemporalTruthEvidenceManifest{}, err
	}
	if err := os.Chmod(privateRoot, 0o700); err != nil {
		return TemporalTruthEvidenceManifest{}, err
	}

	manifest := TemporalTruthEvidenceManifest{
		SchemaVersion: TemporalTruthEvidenceSchemaVersion, ContractVersion: TemporalTruthEvidenceContractVersion,
		EvidenceVersion: TemporalTruthEvidenceVersion, GeneratedAt: config.GeneratedAt.UTC(), SelectionSHA256: selectionSHA,
		MediaTools: config.Media.Identity(), Config: TemporalTruthEvidenceSettings{SceneThreshold: config.SceneThreshold, MaximumFramesPerCase: TemporalEvidenceMaxFrames, MaximumVideoBytes: TemporalTruthMaximumVideoBytes, PerCaseTimeoutMS: config.PerCaseTimeout.Milliseconds()},
		OCR: TemporalTruthOCRStatus{Status: "unavailable"},
	}
	if config.OCR != nil {
		identity := config.OCR.Identity()
		manifest.OCR = TemporalTruthOCRStatus{Status: "available", Engine: &identity, MinimumConfidence: 0.5}
	}
	privateMap := TemporalTruthEvidencePrivateMap{
		SchemaVersion: TemporalTruthEvidenceSchemaVersion, ContractVersion: TemporalTruthEvidenceContractVersion,
		SelectionSHA256: selectionSHA, DraftSHA256: draftSHA, DownloadLedgerSHA256: ledgerSHA,
		PacketsSHA256: packetsSHA, TranscriptSetSHA256: fillerbakeoff.TranscriptSetSHA256(transcripts),
	}
	ordered := slices.Clone(selection.Cases)
	sort.Slice(ordered, func(i, j int) bool {
		left, right := draftByID[ordered[i].CaseID].Provenance.SegmentDurationMS, draftByID[ordered[j].CaseID].Provenance.SegmentDurationMS
		if left != right {
			return left > right
		}
		return ordered[i].ContentSHA256 < ordered[j].ContentSHA256
	})
	for _, selected := range ordered {
		draftCase, ok := draftByID[selected.CaseID]
		if !ok || draftCase.ContentSHA256 != selected.ContentSHA256 {
			return TemporalTruthEvidenceManifest{}, fmt.Errorf("selected case %q does not bind the draft content", selected.CaseID)
		}
		download, ok := ledgerByID[selected.CaseID]
		if !ok || download.ContentSHA256 != selected.ContentSHA256 || download.LocalFile == "" || download.Representation.Bytes <= 0 || download.Authority != draftCase.Provenance.Authority || download.ItemID != draftCase.Provenance.ItemID || download.MetadataSHA256 != draftCase.Provenance.MetadataSHA256 || download.Representation.Bytes != draftCase.Provenance.SourceBytes || download.Approval.Decision != "approved" || download.Approval.CaseID != selected.CaseID {
			return TemporalTruthEvidenceManifest{}, fmt.Errorf("selected case %q does not bind the download ledger", selected.CaseID)
		}
		packet, ok := packets[selected.CaseID]
		if !ok || packet.ContentSHA256 != selected.ContentSHA256 || fillerbakeoff.PacketSHA256(packet) != draftCase.EvidenceSHA256 {
			return TemporalTruthEvidenceManifest{}, fmt.Errorf("selected case %q does not bind its raw packet", selected.CaseID)
		}
		if err := fillerbakeoff.ValidatePacketAgainstCase(draftCase, packet, packet.EvidenceVersion, config.PacketRoot); err != nil {
			return TemporalTruthEvidenceManifest{}, fmt.Errorf("selected case %q packet: %w", selected.CaseID, err)
		}
		sourcePath, err := resolveWithin(config.MediaRoot, download.LocalFile)
		if err != nil {
			return TemporalTruthEvidenceManifest{}, fmt.Errorf("selected case %q media: %w", selected.CaseID, err)
		}
		sourceInfo, err := os.Stat(sourcePath)
		if err != nil || !sourceInfo.Mode().IsRegular() || sourceInfo.Size() != download.Representation.Bytes {
			return TemporalTruthEvidenceManifest{}, fmt.Errorf("selected case %q media byte binding failed", selected.CaseID)
		}
		sourceSHA, err := hashFile(sourcePath)
		if err != nil || sourceSHA != selected.ContentSHA256 {
			return TemporalTruthEvidenceManifest{}, fmt.Errorf("selected case %q media content binding failed", selected.CaseID)
		}
		alias := temporalTruthEvidenceAlias(selection.Seed, selected.ContentSHA256)
		caseContext, cancel := context.WithTimeout(ctx, config.PerCaseTimeout)
		publicCase, privateEntry, err := buildTemporalTruthEvidenceCase(caseContext, config, publicRoot, alias, draftCase, download, packet, transcriptByID[selected.CaseID], sourcePath, sourceSHA)
		cancel()
		if err != nil {
			return TemporalTruthEvidenceManifest{}, fmt.Errorf("selected case %q: %w", selected.CaseID, err)
		}
		manifest.Cases = append(manifest.Cases, publicCase)
		privateMap.Entries = append(privateMap.Entries, privateEntry)
	}
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return TemporalTruthEvidenceManifest{}, err
	}
	if err := writeTemporalTruthNew(filepath.Join(publicRoot, "manifest.json"), append(manifestRaw, '\n'), 0o640); err != nil {
		return TemporalTruthEvidenceManifest{}, err
	}
	privateRaw, err := json.MarshalIndent(privateMap, "", "  ")
	if err != nil {
		return TemporalTruthEvidenceManifest{}, err
	}
	if err := writeTemporalTruthNew(filepath.Join(privateRoot, "map.json"), append(privateRaw, '\n'), 0o600); err != nil {
		return TemporalTruthEvidenceManifest{}, err
	}
	if err := stage.Publish(); err != nil {
		return TemporalTruthEvidenceManifest{}, err
	}
	return manifest, nil
}

func buildTemporalTruthEvidenceCase(ctx context.Context, config TemporalTruthEvidenceConfig, publicRoot, alias string, draftCase fillereval.Case, download temporalTruthDownloadedCase, packet fillerbakeoff.Packet, transcript fillerbakeoff.TranscriptArtifact, sourcePath, sourceSHA string) (TemporalTruthEvidenceCase, TemporalTruthEvidencePrivateEntry, error) {
	duration := draftCase.Provenance.SegmentDurationMS
	start := draftCase.Provenance.SegmentStartMS
	black, silence, cuts, err := config.Media.Analyze(ctx, sourcePath, start, duration, config.SceneThreshold)
	if err != nil {
		return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, err
	}
	plan, err := BuildTemporalEvidencePlan(duration, black, silence, cuts)
	if err != nil {
		return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, err
	}
	caseRoot := filepath.Join(publicRoot, "cases", alias)
	if err := os.MkdirAll(caseRoot, 0o750); err != nil {
		return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, err
	}
	videoPath := filepath.Join(caseRoot, "review.mp4")
	videoInfo, err := config.Media.WriteReviewVideo(ctx, sourcePath, start, duration, videoPath)
	if err != nil {
		return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, err
	}
	video, err := temporalTruthEvidenceFile(publicRoot, videoPath)
	if err != nil || video.Bytes > TemporalTruthMaximumVideoBytes || videoInfo.DurationMS <= 0 || videoInfo.DurationMS > duration+1_000 || videoInfo.Width <= 0 || videoInfo.Height <= 0 {
		return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, fmt.Errorf("review video violates bounds: bytes=%d/%d durationMs=%d/%d dimensions=%dx%d: %w", video.Bytes, TemporalTruthMaximumVideoBytes, videoInfo.DurationMS, duration, videoInfo.Width, videoInfo.Height, err)
	}
	video.DurationMS, video.Width, video.Height = videoInfo.DurationMS, videoInfo.Width, videoInfo.Height
	absoluteTimes := make([]int64, len(plan.FrameTimesMS))
	for index, at := range plan.FrameTimesMS {
		absoluteTimes[index] = start + at
	}
	frameBytes, err := config.Media.Frames(ctx, sourcePath, absoluteTimes)
	if err != nil || len(frameBytes) != len(plan.FrameTimesMS) {
		return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, fmt.Errorf("frame extractor returned %d/%d frames: %w", len(frameBytes), len(plan.FrameTimesMS), err)
	}
	result := TemporalTruthEvidenceCase{Alias: alias, DurationMS: duration, Plan: plan, Video: video}
	ocrInputs := make([]TemporalTruthOCRInput, 0, len(frameBytes))
	for index, raw := range frameBytes {
		imageConfig, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil || imageConfig.Width <= 0 || imageConfig.Height <= 0 || imageConfig.Width > mediatools.SemanticFrameMaxWidth {
			return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, fmt.Errorf("frame %d is not a bounded JPEG", index+1)
		}
		path := filepath.Join(caseRoot, fmt.Sprintf("frame-%02d.jpg", index+1))
		if err := writeTemporalTruthNew(path, raw, 0o640); err != nil {
			return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, err
		}
		digest := hashBytes(raw)
		result.Frames = append(result.Frames, TemporalTruthEvidenceFrame{
			ID: fmt.Sprintf("frame-%02d", index+1), Path: temporalTruthRelative(publicRoot, path), SHA256: digest,
			Bytes: int64(len(raw)), Width: imageConfig.Width, Height: imageConfig.Height, AtMS: plan.FrameTimesMS[index],
		})
		ocrInputs = append(ocrInputs, TemporalTruthOCRInput{Path: path, SHA256: digest})
	}
	if config.OCR != nil {
		ocrResults, err := config.OCR.Recognize(ctx, ocrInputs)
		if err != nil || len(ocrResults) != len(ocrInputs) {
			return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, fmt.Errorf("OCR returned %d/%d frame results: %w", len(ocrResults), len(ocrInputs), err)
		}
		for index, ocr := range ocrResults {
			if ocr.SHA256 != ocrInputs[index].SHA256 {
				return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, fmt.Errorf("OCR result %d does not bind its ordered frame hash", index)
			}
			for _, observation := range ocr.Observations {
				if err := validateTemporalTruthOCRObservation(observation); err != nil {
					return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, err
				}
			}
			result.Frames[index].OCR = ocr.Observations
		}
	}

	transcriptSHA := ""
	audio, hasAudio := temporalTruthPacketAudio(packet)
	if hasAudio {
		if transcript.CaseID != draftCase.ID || transcript.PacketSHA256 != fillerbakeoff.PacketSHA256(packet) || transcript.EvidenceVersion != packet.EvidenceVersion || transcript.AudioSignalID != audio.ID || transcript.AudioSHA256 != audio.SHA256 || transcript.AudioBytes != audio.Bytes || transcript.AudioDurationMS != audio.DurationMS || hashBytes([]byte(transcript.Text)) != transcript.TextSHA256 {
			return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, fmt.Errorf("transcript does not bind the selected packet audio")
		}
		transcriptSHA = temporalTruthJSONSHA(transcript)
		result.TranscriptSegments, err = temporalTruthTranscriptSegments(transcript.Segments, duration)
		if err != nil {
			return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, err
		}
	} else if transcript.CaseID != "" {
		return TemporalTruthEvidenceCase{}, TemporalTruthEvidencePrivateEntry{}, fmt.Errorf("transcript exists for a packet without audio")
	}
	return result, TemporalTruthEvidencePrivateEntry{
		Alias: alias, CaseID: draftCase.ID, ContentSHA256: draftCase.ContentSHA256, SourceLocalFile: download.LocalFile,
		SourceSHA256: sourceSHA, PacketSHA256: fillerbakeoff.PacketSHA256(packet), TranscriptSHA256: transcriptSHA,
	}, nil
}

func validateTemporalTruthEvidenceConfig(config TemporalTruthEvidenceConfig) error {
	if strings.TrimSpace(config.SelectionPath) == "" || strings.TrimSpace(config.DraftPath) == "" || strings.TrimSpace(config.DownloadLedgerPath) == "" || strings.TrimSpace(config.MediaRoot) == "" || strings.TrimSpace(config.PacketsPath) == "" || strings.TrimSpace(config.PacketRoot) == "" || strings.TrimSpace(config.TranscriptsPath) == "" || strings.TrimSpace(config.OutputDir) == "" || config.GeneratedAt.IsZero() || config.SceneThreshold <= 0 || config.SceneThreshold >= 1 || config.PerCaseTimeout <= 0 || config.Media == nil {
		return fmt.Errorf("temporal truth evidence requires every bound path, fixed generation time, positive per-case timeout, media tool, and scene threshold between zero and one")
	}
	mediaIdentity := config.Media.Identity()
	for name, identity := range map[string]TemporalTruthToolIdentity{"ffmpeg": mediaIdentity.FFmpeg, "ffprobe": mediaIdentity.FFprobe} {
		if strings.TrimSpace(identity.Path) == "" || !reviewSHA256(identity.BinarySHA256) {
			return fmt.Errorf("%s tool identity is invalid", name)
		}
	}
	if config.OCR != nil {
		identity := config.OCR.Identity()
		if strings.TrimSpace(identity.Path) == "" || !reviewSHA256(identity.BinarySHA256) || !reviewSHA256(identity.SourceSHA256) {
			return fmt.Errorf("OCR tool identity is invalid")
		}
	}
	return nil
}

func temporalTruthSelectionHasInput(selection fillereval.TemporalTruthSelection, name, digest string) bool {
	return slices.ContainsFunc(selection.Inputs, func(input fillereval.TemporalTruthInputDigest) bool {
		return input.Name == name && input.SHA256 == digest
	})
}

func historyDigests(paths ...string) (string, string, string, string) {
	values := make([]string, len(paths))
	for index, path := range paths {
		values[index] = mustHistoryFileSHA256(path)
	}
	return values[0], values[1], values[2], values[3]
}

func temporalTruthEvidenceAlias(seed, contentSHA string) string {
	return "evidence-" + temporalTruthHash([]byte(seed + "\x00public-alias\x00" + contentSHA))[:24]
}

func temporalTruthEvidenceFile(root, path string) (TemporalTruthEvidenceFile, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return TemporalTruthEvidenceFile{}, fmt.Errorf("evidence file is not a non-empty regular file")
	}
	digest, err := hashFile(path)
	if err != nil {
		return TemporalTruthEvidenceFile{}, err
	}
	return TemporalTruthEvidenceFile{Path: temporalTruthRelative(root, path), SHA256: digest, Bytes: info.Size()}, nil
}

func temporalTruthRelative(root, path string) string {
	relative, _ := filepath.Rel(root, path)
	return filepath.ToSlash(relative)
}

func temporalTruthPacketAudio(packet fillerbakeoff.Packet) (fillerbakeoff.Signal, bool) {
	for _, signal := range packet.Signals {
		if signal.Kind == string(filleradmission.KindAudio) {
			return signal, true
		}
	}
	return fillerbakeoff.Signal{}, false
}

func temporalTruthTranscriptSegments(segments []fillerbakeoff.TranscriptSegment, duration int64) ([]fillerbakeoff.TranscriptSegment, error) {
	result := slices.Clone(segments)
	previousEnd := int64(0)
	for index, segment := range result {
		if strings.TrimSpace(segment.Text) == "" || segment.StartMS < previousEnd || segment.StartMS < 0 || segment.EndMS <= segment.StartMS || segment.StartMS >= duration {
			return nil, fmt.Errorf("transcript segment %d is invalid or outside the selected span", index)
		}
		result[index].EndMS = min(segment.EndMS, duration)
		previousEnd = result[index].EndMS
	}
	return result, nil
}

func validateTemporalTruthOCRObservation(observation TemporalTruthOCRObservation) error {
	if strings.TrimSpace(observation.Text) == "" || observation.Confidence < 0.5 || observation.Confidence > 1 || observation.X < -0.05 || observation.X > 1.05 || observation.Y < -0.05 || observation.Y > 1.05 || observation.Width <= 0 || observation.Width > 1.05 || observation.Height <= 0 || observation.Height > 1.05 || observation.X+observation.Width > 1.05 || observation.Y+observation.Height > 1.05 {
		return fmt.Errorf("OCR observation is empty, low-confidence, or outside normalized bounds")
	}
	return nil
}

func writeTemporalTruthNew(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func temporalTruthHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func temporalTruthJSONSHA(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return temporalTruthHash(raw)
}
