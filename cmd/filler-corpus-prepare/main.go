// Command filler-corpus-prepare turns rights-approved media into a provenance-
// complete unlabeled draft and label-blind evidence packets.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/filleradmission"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerquarantine"
	"github.com/loomarr/loomarr/internal/mediatools"
)

const preparationSchemaVersion = 4

const maxPreparationSegmentDurationMS = int64(300_000)

const (
	profileDevelopment   = "development"
	profileCertification = "certification"
)

type preparationPlan struct {
	SchemaVersion   int                    `json:"schemaVersion"`
	Kind            fillereval.CorpusKind  `json:"kind"`
	CorpusVersion   string                 `json:"corpusVersion"`
	EvidenceVersion string                 `json:"evidenceVersion"`
	SliceGates      []fillereval.SliceGate `json:"sliceGates"`
	Cases           []plannedCase          `json:"cases"`
}

type plannedCase struct {
	CaseID            string           `json:"caseId"`
	Split             fillereval.Split `json:"split"`
	Cluster           string           `json:"cluster"`
	SegmentStartMS    int64            `json:"segmentStartMs"`
	SegmentDurationMS int64            `json:"segmentDurationMs"`
	VideoStartMS      int64            `json:"videoStartMs"`
	VideoDurationMS   int64            `json:"videoDurationMs"`
}

type mediaMeasurement struct {
	DurationMS int64
	Usable     bool
	Detail     string
}

type videoDerivative struct {
	Data          []byte
	SHA256        string
	DurationMS    int64
	Width, Height int
}

type audioDerivative struct {
	Data       []byte
	SHA256     string
	DurationMS int64
}

type perceptualFamily struct {
	caseID  string
	cluster string
	hashes  [4]uint64
}

type sourcePreflight struct {
	path      string
	sourceRef string
	hashes    mediaHashes
	size      int64
}

type mediaDeriver interface {
	Measure(context.Context, string, int64, int64) (mediaMeasurement, error)
	Frames(context.Context, string, int64, int64) ([][]byte, error)
	Audio(context.Context, string, int64, int64) (audioDerivative, error)
	Video(context.Context, string, int64, int64) (videoDerivative, error)
}

type realDeriver struct {
	ffmpeg string
	tools  *mediatools.FFmpegTools
}

type options struct {
	inventoryPath, approvalsPath, planPath, quarantineInspectionPath string
	localRoot, remoteRoot                                            string
	draftOut, packetsOut, derivativesRoot                            string
	kind                                                             fillereval.CorpusKind
	preparedAt                                                       time.Time
	minItems, maxItems                                               int
	maxInputBytes, maxOutputBytes                                    int64
	maxWallTime                                                      time.Duration
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-corpus-prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profile := flags.String("profile", "", "required preparation profile: development or certification")
	inventoryPath := flags.String("inventory", "", "strict mixed-authority inventory JSON")
	approvalsPath := flags.String("rights-approvals", "", "locked rights decisions JSONL")
	quarantineInspectionPath := flags.String("quarantine-inspection", "", "immutable quarantine-inspection report (required for non-local cases)")
	planPath := flags.String("plan", "", "authored split, cluster, and segment plan JSON")
	localRoot := flags.String("local-root", "", "direct-cohort media root")
	remoteRoot := flags.String("remote-root", "", "downloaded public media root")
	draftOut := flags.String("draft-out", "", "unlabeled development or certification draft JSON")
	packetsOut := flags.String("packets-out", "", "label-blind packet JSONL")
	derivativesRoot := flags.String("derivatives-root", "", "external bounded derivative root")
	preparedText := flags.String("prepared-at", "", "fixed RFC3339 preparation time")
	ffmpegPath := flags.String("ffmpeg", "ffmpeg", "ffmpeg executable")
	minItems := flags.Int("min-items", fillereval.CertificationMinDevelopment+fillereval.CertificationMinHoldout, "minimum complete corpus cases")
	maxItems := flags.Int("max-items", 1600, "maximum complete corpus cases")
	maxInputBytes := flags.Int64("max-input-bytes", 0, "aggregate source-media byte ceiling")
	maxOutputBytes := flags.Int64("max-output-bytes", 0, "aggregate derivative byte ceiling")
	maxWall := flags.Duration("max-wall-time", 0, "complete preparation wall-time ceiling")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	preparedAt, timeErr := time.Parse(time.RFC3339, *preparedText)
	kind, profileErr := corpusKindForProfile(*profile)
	minimum := fillereval.CertificationMinDevelopment
	if kind == fillereval.CorpusCertification {
		minimum += fillereval.CertificationMinHoldout
	}
	if timeErr != nil || profileErr != nil || *inventoryPath == "" || *approvalsPath == "" || *planPath == "" || *localRoot == "" || *remoteRoot == "" || *draftOut == "" || *packetsOut == "" || *derivativesRoot == "" || *minItems < minimum || *maxItems < *minItems || *maxInputBytes <= 0 || *maxOutputBytes <= 0 || *maxWall <= 0 {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-prepare: an explicit development or certification profile, all paths, valid preparation time, profile-sized item bounds, byte ceilings, and wall-time ceiling are required")
		return 2
	}
	opts := options{inventoryPath: *inventoryPath, approvalsPath: *approvalsPath, planPath: *planPath, quarantineInspectionPath: *quarantineInspectionPath, localRoot: *localRoot, remoteRoot: *remoteRoot, draftOut: *draftOut, packetsOut: *packetsOut, derivativesRoot: *derivativesRoot, kind: kind, preparedAt: preparedAt.UTC(), minItems: *minItems, maxItems: *maxItems, maxInputBytes: *maxInputBytes, maxOutputBytes: *maxOutputBytes, maxWallTime: *maxWall}
	deriver := &realDeriver{ffmpeg: *ffmpegPath, tools: mediatools.NewFFmpegTools(*ffmpegPath, filler.FFprobePathNextTo(*ffmpegPath), "", "", "")}
	draft, packets, err := prepare(context.Background(), opts, deriver)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-prepare:", err)
		return 1
	}
	if err := writeJSON(opts.draftOut, draft); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-prepare: write draft:", err)
		return 1
	}
	if err := writeJSONL(opts.packetsOut, packets); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-prepare: write packets:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-corpus-prepare: froze %d draft cases and evidence packets\n", len(draft.Cases))
	return 0
}

func corpusKindForProfile(profile string) (fillereval.CorpusKind, error) {
	switch strings.TrimSpace(profile) {
	case profileDevelopment:
		return fillereval.CorpusDevelopmentSeed, nil
	case profileCertification:
		return fillereval.CorpusCertification, nil
	default:
		return "", fmt.Errorf("profile must be development or certification")
	}
}

func prepare(ctx context.Context, opts options, deriver mediaDeriver) (fillereval.Manifest, []fillerbakeoff.Packet, error) {
	started := time.Now()
	inventoryRaw, err := os.ReadFile(opts.inventoryPath)
	if err != nil {
		return fillereval.Manifest{}, nil, err
	}
	inv, err := fillercorpus.DecodeInventoryBytes(inventoryRaw)
	if err != nil {
		return fillereval.Manifest{}, nil, err
	}
	inventoryDigest := fillercorpus.InventorySHA256(inventoryRaw)
	var inspectionRaw []byte
	if opts.quarantineInspectionPath != "" {
		inspectionRaw, err = os.ReadFile(opts.quarantineInspectionPath)
		if err != nil {
			return fillereval.Manifest{}, nil, fmt.Errorf("read quarantine inspection: %w", err)
		}
	}
	authority, err := fillerquarantine.OpenRightsEligibility(inventoryRaw, inspectionRaw)
	if err != nil {
		return fillereval.Manifest{}, nil, err
	}
	selection, err := authority.Selected(1, len(inv.Cases))
	if err != nil {
		return fillereval.Manifest{}, nil, err
	}
	approvals, err := readJSONL[fillercorpus.RightsDecision](opts.approvalsPath)
	if err != nil {
		return fillereval.Manifest{}, nil, err
	}
	var plan preparationPlan
	if err := readStrictJSON(opts.planPath, &plan); err != nil {
		return fillereval.Manifest{}, nil, err
	}
	if plan.SchemaVersion != preparationSchemaVersion || plan.Kind != opts.kind || strings.TrimSpace(plan.CorpusVersion) == "" || strings.TrimSpace(plan.EvidenceVersion) == "" || len(plan.SliceGates) == 0 || len(plan.Cases) < opts.minItems || len(plan.Cases) > opts.maxItems {
		return fillereval.Manifest{}, nil, fmt.Errorf("plan schema, kind, identity, gates, or item count is invalid")
	}
	if err := validateSliceGates(plan.Kind, plan.SliceGates, len(plan.Cases)); err != nil {
		return fillereval.Manifest{}, nil, err
	}
	byCase := make(map[string]fillercorpus.InventoryCase, len(inv.Cases))
	for _, item := range inv.Cases {
		byCase[item.CaseID] = item
	}
	eligibleCases := make(map[string]struct{}, len(selection.Cases))
	for _, candidate := range selection.Cases {
		eligibleCases[candidate.Inventory.CaseID] = struct{}{}
	}
	decisions := make(map[string]fillercorpus.RightsDecision, len(approvals))
	approvedCount := 0
	for _, decision := range approvals {
		if _, duplicate := decisions[decision.CaseID]; duplicate {
			return fillereval.Manifest{}, nil, fmt.Errorf("duplicate rights decision %q", decision.CaseID)
		}
		item, exists := byCase[decision.CaseID]
		if !exists {
			return fillereval.Manifest{}, nil, fmt.Errorf("rights decision %q has no inventory case", decision.CaseID)
		}
		if _, eligible := eligibleCases[decision.CaseID]; !eligible {
			return fillereval.Manifest{}, nil, fmt.Errorf("rights decision %q is outside quarantine eligibility", decision.CaseID)
		}
		if err := validateRightsDecision(decision, item, inventoryDigest, opts.preparedAt, plan.Kind); err != nil {
			return fillereval.Manifest{}, nil, err
		}
		decisions[decision.CaseID] = decision
		if decision.Decision == "approved" {
			approvedCount++
		}
	}
	if len(decisions) != len(eligibleCases) {
		return fillereval.Manifest{}, nil, fmt.Errorf("rights decisions cover %d/%d quarantine-eligible cases", len(decisions), len(eligibleCases))
	}
	expectedCases := len(byCase)
	if plan.Kind == fillereval.CorpusDevelopmentSeed {
		expectedCases = approvedCount
	} else if approvedCount != len(byCase) {
		return fillereval.Manifest{}, nil, fmt.Errorf("certification preparation requires every inventory case to be approved")
	}
	if len(plan.Cases) != expectedCases {
		return fillereval.Manifest{}, nil, fmt.Errorf("plan covers %d cases; profile requires exactly %d", len(plan.Cases), expectedCases)
	}

	// Complete every authority, decision, and source-byte check before a
	// derivative staging directory or provider-visible packet can exist.
	preflight := make(map[string]sourcePreflight, len(plan.Cases))
	preflightContent := make(map[string]string, len(plan.Cases))
	var preflightInputBytes int64
	for _, planned := range plan.Cases {
		item, exists := byCase[planned.CaseID]
		if !exists {
			return fillereval.Manifest{}, nil, fmt.Errorf("plan case %q is absent from inventory", planned.CaseID)
		}
		if _, duplicate := preflight[planned.CaseID]; duplicate {
			return fillereval.Manifest{}, nil, fmt.Errorf("duplicate plan case %q", planned.CaseID)
		}
		approval, approved := decisions[planned.CaseID]
		if !approved || approval.Decision != "approved" || !approval.Redistributable {
			return fillereval.Manifest{}, nil, fmt.Errorf("case %q lacks a complete redistribution approval bound to this inventory", planned.CaseID)
		}
		mediaPath, sourceRef, pathErr := mediaPathFor(opts, item)
		if pathErr != nil {
			return fillereval.Manifest{}, nil, fmt.Errorf("case %q: %w", planned.CaseID, pathErr)
		}
		hashes, size, hashErr := hashMedia(mediaPath)
		if hashErr != nil {
			return fillereval.Manifest{}, nil, fmt.Errorf("case %q: %w", planned.CaseID, hashErr)
		}
		if size != item.Representation.Bytes || !matches(item.Representation.SHA256, hashes.sha256) || !matches(item.Representation.SHA1, hashes.sha1) || !matches(item.Representation.MD5, hashes.md5) {
			return fillereval.Manifest{}, nil, fmt.Errorf("case %q media identity differs from inventory", planned.CaseID)
		}
		if err := authority.Require(approval, hashes.sha256); err != nil {
			return fillereval.Manifest{}, nil, err
		}
		if prior := preflightContent[hashes.sha256]; prior != "" {
			return fillereval.Manifest{}, nil, fmt.Errorf("case %q duplicates media bytes from %q", planned.CaseID, prior)
		}
		preflightContent[hashes.sha256] = planned.CaseID
		preflightInputBytes += size
		if preflightInputBytes > opts.maxInputBytes {
			return fillereval.Manifest{}, nil, fmt.Errorf("source media exceeds aggregate byte ceiling")
		}
		preflight[planned.CaseID] = sourcePreflight{path: mediaPath, sourceRef: sourceRef, hashes: hashes, size: size}
	}
	if _, err := os.Stat(opts.derivativesRoot); !os.IsNotExist(err) {
		return fillereval.Manifest{}, nil, fmt.Errorf("derivative output already exists")
	}
	if err := os.MkdirAll(filepath.Dir(opts.derivativesRoot), 0o750); err != nil {
		return fillereval.Manifest{}, nil, err
	}
	stageRoot, err := os.MkdirTemp(filepath.Dir(opts.derivativesRoot), ".filler-corpus-derivatives-*")
	if err != nil {
		return fillereval.Manifest{}, nil, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stageRoot)
		}
	}()

	seenCases := map[string]struct{}{}
	clusterSplits := map[string]fillereval.Split{}
	holdoutClusters := map[string]string{}
	campaignSplits := map[string]fillereval.Split{}
	holdoutCampaigns := map[string]string{}
	familySplits := map[string]fillereval.Split{}
	holdoutFamilies := map[string]string{}
	var perceptualFamilies []perceptualFamily
	splitCounts := map[fillereval.Split]int{}
	draft := fillereval.Manifest{SchemaVersion: fillereval.SchemaVersion, Kind: plan.Kind, CorpusVersion: plan.CorpusVersion, SliceGates: slices.Clone(plan.SliceGates)}
	packets := make([]fillerbakeoff.Packet, 0, len(plan.Cases))
	var outputBytes int64
	for _, planned := range plan.Cases {
		if time.Since(started) > opts.maxWallTime {
			return fillereval.Manifest{}, nil, fmt.Errorf("wall-time ceiling exceeded")
		}
		item, ok := byCase[planned.CaseID]
		if !ok {
			return fillereval.Manifest{}, nil, fmt.Errorf("plan case %q is absent from inventory", planned.CaseID)
		}
		if _, duplicate := seenCases[planned.CaseID]; duplicate {
			return fillereval.Manifest{}, nil, fmt.Errorf("duplicate plan case %q", planned.CaseID)
		}
		seenCases[planned.CaseID] = struct{}{}
		if planned.Split != fillereval.SplitDevelopment && planned.Split != fillereval.SplitHoldout || plan.Kind == fillereval.CorpusDevelopmentSeed && planned.Split != fillereval.SplitDevelopment || strings.TrimSpace(planned.Cluster) == "" || planned.SegmentStartMS < 0 || planned.SegmentDurationMS <= 0 || planned.SegmentDurationMS > maxPreparationSegmentDurationMS || planned.VideoStartMS < planned.SegmentStartMS || planned.VideoDurationMS <= 0 || planned.VideoDurationMS > mediatools.HostedVideoMaxDurationMS || planned.VideoStartMS+planned.VideoDurationMS > planned.SegmentStartMS+planned.SegmentDurationMS {
			return fillereval.Manifest{}, nil, fmt.Errorf("case %q has invalid split, cluster, or bounded spans", planned.CaseID)
		}
		if plan.Kind == fillereval.CorpusCertification && (len(item.Creator) == 0 || strings.TrimSpace(item.Campaign) == "" || strings.TrimSpace(item.SourceFamily) == "") {
			return fillereval.Manifest{}, nil, fmt.Errorf("case %q has incomplete acquisition provenance", planned.CaseID)
		}
		if prior, exists := clusterSplits[planned.Cluster]; exists && prior != planned.Split {
			return fillereval.Manifest{}, nil, fmt.Errorf("cluster %q crosses splits", planned.Cluster)
		}
		clusterSplits[planned.Cluster] = planned.Split
		if planned.Split == fillereval.SplitHoldout {
			if prior := holdoutClusters[planned.Cluster]; prior != "" {
				return fillereval.Manifest{}, nil, fmt.Errorf("holdout cluster %q repeats cases %q and %q", planned.Cluster, prior, planned.CaseID)
			}
			holdoutClusters[planned.Cluster] = planned.CaseID
		}
		if plan.Kind == fillereval.CorpusCertification {
			if prior, exists := campaignSplits[item.Campaign]; exists && prior != planned.Split {
				return fillereval.Manifest{}, nil, fmt.Errorf("campaign %q crosses splits", item.Campaign)
			}
			campaignSplits[item.Campaign] = planned.Split
			if planned.Split == fillereval.SplitHoldout {
				if prior := holdoutCampaigns[item.Campaign]; prior != "" {
					return fillereval.Manifest{}, nil, fmt.Errorf("holdout campaign %q repeats cases %q and %q", item.Campaign, prior, planned.CaseID)
				}
				holdoutCampaigns[item.Campaign] = planned.CaseID
			}
			if prior, exists := familySplits[item.SourceFamily]; exists && prior != planned.Split {
				return fillereval.Manifest{}, nil, fmt.Errorf("source family %q crosses splits", item.SourceFamily)
			}
			familySplits[item.SourceFamily] = planned.Split
			if planned.Split == fillereval.SplitHoldout {
				if prior := holdoutFamilies[item.SourceFamily]; prior != "" {
					return fillereval.Manifest{}, nil, fmt.Errorf("holdout source family %q repeats cases %q and %q", item.SourceFamily, prior, planned.CaseID)
				}
				holdoutFamilies[item.SourceFamily] = planned.CaseID
			}
		}
		splitCounts[planned.Split]++
		approval, ok := decisions[planned.CaseID]
		if !ok || approval.Decision != "approved" || !approval.Redistributable {
			return fillereval.Manifest{}, nil, fmt.Errorf("case %q lacks a complete redistribution approval bound to this inventory", planned.CaseID)
		}
		checked := preflight[planned.CaseID]
		mediaPath, sourceRef, hashes, size := checked.path, checked.sourceRef, checked.hashes, checked.size
		measurement, err := deriver.Measure(ctx, mediaPath, planned.SegmentStartMS, planned.SegmentStartMS+planned.SegmentDurationMS)
		if err != nil || measurement.DurationMS < planned.SegmentStartMS+planned.SegmentDurationMS {
			return fillereval.Manifest{}, nil, fmt.Errorf("case %q media measurement is incomplete: %w", planned.CaseID, err)
		}
		packet := fillerbakeoff.Packet{SchemaVersion: fillerbakeoff.PacketSchemaVersion, CaseID: planned.CaseID, EvidenceVersion: plan.EvidenceVersion, ContentSHA256: hashes.sha256, Facts: []filleradmission.Evidence{
			{ID: "media-usability", Claim: filleradmission.ClaimMediaUsability, Value: usability(measurement.Usable), Kind: filleradmission.KindDecoder, Source: "decoder:" + planned.CaseID, Location: measurement.Detail},
			{ID: "source-license", Claim: filleradmission.ClaimSourceLicense, Value: filleradmission.EligibilityEligible, Kind: filleradmission.KindSourcePolicy, Source: "rights:" + planned.CaseID},
		}, Signals: metadataSignals(item)}
		if measurement.Usable {
			caseDir := filepath.Join(stageRoot, fillercorpus.InventorySHA256([]byte(planned.CaseID)))
			frames, err := deriver.Frames(ctx, mediaPath, planned.SegmentStartMS, planned.SegmentStartMS+planned.SegmentDurationMS)
			if err != nil || len(frames) != 4 {
				return fillereval.Manifest{}, nil, fmt.Errorf("case %q requires exactly four bounded frames: %w", planned.CaseID, err)
			}
			var perceptualHashes [4]uint64
			for index, frame := range frames {
				cfg, _, err := image.DecodeConfig(bytes.NewReader(frame))
				if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 1920 {
					return fillereval.Manifest{}, nil, fmt.Errorf("case %q frame %d is invalid", planned.CaseID, index+1)
				}
				perceptualHashes[index], err = perceptualHash(frame)
				if err != nil {
					return fillereval.Manifest{}, nil, fmt.Errorf("case %q frame %d perceptual hash: %w", planned.CaseID, index+1, err)
				}
				rel := filepath.ToSlash(filepath.Join(filepath.Base(caseDir), fmt.Sprintf("frame-%d.jpg", index+1)))
				if err := writeArtifact(filepath.Join(stageRoot, filepath.FromSlash(rel)), frame); err != nil {
					return fillereval.Manifest{}, nil, err
				}
				digest := fillercorpus.InventorySHA256(frame)
				outputBytes += int64(len(frame))
				packet.Signals = append(packet.Signals, fillerbakeoff.Signal{ID: fmt.Sprintf("frame-%d", index+1), Kind: string(filleradmission.KindFrame), Path: rel, SHA256: digest, Bytes: int64(len(frame)), Width: cfg.Width, Height: cfg.Height, AtMS: semanticAt(planned.SegmentStartMS, planned.SegmentDurationMS, index), ContentTypes: []string{"image/jpeg"}})
			}
			for _, prior := range perceptualFamilies {
				if perceptuallyRelated(prior.hashes, perceptualHashes) && prior.cluster != planned.Cluster {
					return fillereval.Manifest{}, nil, fmt.Errorf("case %q is perceptually related to %q but uses a different cluster", planned.CaseID, prior.caseID)
				}
			}
			perceptualFamilies = append(perceptualFamilies, perceptualFamily{caseID: planned.CaseID, cluster: planned.Cluster, hashes: perceptualHashes})
			audio, err := deriver.Audio(ctx, mediaPath, planned.SegmentStartMS, planned.SegmentStartMS+planned.SegmentDurationMS)
			maxMeasuredAudioMS := planned.SegmentDurationMS + 1_000
			if err != nil || len(audio.Data) == 0 || audio.DurationMS <= 0 || audio.DurationMS > maxMeasuredAudioMS || audio.SHA256 != fillercorpus.InventorySHA256(audio.Data) {
				return fillereval.Manifest{}, nil, fmt.Errorf("case %q audio derivative is invalid: err=%v bytes=%d duration_ms=%d max_duration_ms=%d sha256=%q", planned.CaseID, err, len(audio.Data), audio.DurationMS, maxMeasuredAudioMS, audio.SHA256)
			}
			audioRel := filepath.ToSlash(filepath.Join(filepath.Base(caseDir), "audio.wav"))
			if err := writeArtifact(filepath.Join(stageRoot, filepath.FromSlash(audioRel)), audio.Data); err != nil {
				return fillereval.Manifest{}, nil, err
			}
			outputBytes += int64(len(audio.Data))
			packet.Signals = append(packet.Signals, fillerbakeoff.Signal{ID: "audio", Kind: string(filleradmission.KindAudio), Path: audioRel, SHA256: audio.SHA256, Bytes: int64(len(audio.Data)), DurationMS: audio.DurationMS, AtMS: planned.SegmentStartMS, ContentTypes: []string{"audio/wav"}})
			video, err := deriver.Video(ctx, mediaPath, planned.VideoStartMS, planned.VideoStartMS+planned.VideoDurationMS)
			// Old and variable-frame-rate containers can report a short mux tail beyond ffmpeg's
			// requested cut. Permit at most three seconds of that container-level delta while
			// retaining the provider contract's absolute sixty-second ceiling.
			maxMeasuredVideoMS := min(mediatools.HostedVideoMaxDurationMS, planned.VideoDurationMS+3_000)
			if err != nil || len(video.Data) == 0 || video.DurationMS <= 0 || video.DurationMS > maxMeasuredVideoMS || video.Width <= 0 || video.Height <= 0 || video.Width > 1280 || video.Height > 720 || video.SHA256 != fillercorpus.InventorySHA256(video.Data) {
				return fillereval.Manifest{}, nil, fmt.Errorf("case %q direct-video derivative is invalid: err=%v bytes=%d duration_ms=%d max_duration_ms=%d dimensions=%dx%d sha256=%q", planned.CaseID, err, len(video.Data), video.DurationMS, maxMeasuredVideoMS, video.Width, video.Height, video.SHA256)
			}
			videoRel := filepath.ToSlash(filepath.Join(filepath.Base(caseDir), "video.mp4"))
			if err := writeArtifact(filepath.Join(stageRoot, filepath.FromSlash(videoRel)), video.Data); err != nil {
				return fillereval.Manifest{}, nil, err
			}
			outputBytes += int64(len(video.Data))
			packet.Signals = append(packet.Signals, fillerbakeoff.Signal{ID: "video", Kind: string(filleradmission.KindVideo), Path: videoRel, SHA256: video.SHA256, Bytes: int64(len(video.Data)), DurationMS: video.DurationMS, Width: video.Width, Height: video.Height, AtMS: planned.VideoStartMS, ContentTypes: []string{"video/mp4"}})
		}
		if outputBytes > opts.maxOutputBytes {
			return fillereval.Manifest{}, nil, fmt.Errorf("derivatives exceed aggregate byte ceiling")
		}
		if time.Since(started) > opts.maxWallTime {
			return fillereval.Manifest{}, nil, fmt.Errorf("wall-time ceiling exceeded")
		}
		evidenceDigest := fillerbakeoff.PacketSHA256(packet)
		itemRef := item.ItemURL
		if itemRef == "" {
			itemRef = "inventory:" + inventoryDigest + "#" + item.CaseID
		}
		license := item.LicenseURL
		if license == "" {
			license = strings.Join(item.RightsAssertions, "; ")
		}
		preparedCase := fillereval.Case{ID: item.CaseID, Split: planned.Split, Cluster: planned.Cluster, ContentSHA256: hashes.sha256, EvidenceSHA256: evidenceDigest, Source: item.Authority, License: license, Provenance: fillereval.MediaProvenance{
			Authority: item.Authority, Collection: strings.Join(item.Collection, ", "), ItemID: item.ItemID, ItemRef: itemRef, MetadataRetrievedAt: item.MetadataRetrievedAt, MetadataSHA256: item.MetadataSHA256,
			EvidenceRef: "inventory:" + inventoryDigest + "#" + item.CaseID, LicenseURL: item.LicenseURL, RightsStatement: strings.Join(item.RightsAssertions, "; ") + "; review basis: " + approval.Basis, RightsDecision: approval.Decision, RightsReviewerID: approval.ReviewerID, RightsReviewedAt: approval.ReviewedAt, Redistributable: approval.Redistributable,
			Creator: strings.Join(item.Creator, ", "), Campaign: item.Campaign, SourceFamily: item.SourceFamily, RequiredCredit: approval.RequiredCredit, Restrictions: slices.Clone(approval.Restrictions), SourceFilename: item.Representation.Name, SourceRef: sourceRef, SourceBytes: size, SegmentStartMS: planned.SegmentStartMS, SegmentDurationMS: planned.SegmentDurationMS,
		}}
		if err := fillerbakeoff.ValidatePacketAgainstCase(preparedCase, packet, plan.EvidenceVersion, stageRoot); err != nil {
			return fillereval.Manifest{}, nil, err
		}
		draft.Cases = append(draft.Cases, preparedCase)
		packets = append(packets, packet)
	}
	for caseID, decision := range decisions {
		_, selected := seenCases[caseID]
		if decision.Decision == "approved" && !selected {
			return fillereval.Manifest{}, nil, fmt.Errorf("approved case %q is absent from the preparation plan", caseID)
		}
		if decision.Decision == "held" && selected {
			return fillereval.Manifest{}, nil, fmt.Errorf("held case %q entered the preparation plan", caseID)
		}
	}
	if plan.Kind == fillereval.CorpusCertification && (splitCounts[fillereval.SplitDevelopment] == 0 || splitCounts[fillereval.SplitHoldout] == 0) {
		return fillereval.Manifest{}, nil, fmt.Errorf("preparation plan requires non-empty development and holdout splits")
	}
	if plan.Kind == fillereval.CorpusCertification {
		if failures := fillereval.ValidateCertificationDraft(draft); len(failures) > 0 {
			return fillereval.Manifest{}, nil, fmt.Errorf("certification draft: %s", strings.Join(failures, "; "))
		}
	}
	sort.Slice(draft.Cases, func(i, j int) bool { return draft.Cases[i].ID < draft.Cases[j].ID })
	sort.Slice(packets, func(i, j int) bool { return packets[i].CaseID < packets[j].CaseID })
	if err := os.Rename(stageRoot, opts.derivativesRoot); err != nil {
		return fillereval.Manifest{}, nil, err
	}
	published = true
	return draft, packets, nil
}

func perceptualHash(raw []byte) (uint64, error) {
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	bounds := decoded.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return 0, fmt.Errorf("empty image")
	}
	var hash uint64
	bit := 0
	for y := 0; y < 8; y++ {
		py := bounds.Min.Y + y*(bounds.Dy()-1)/7
		for x := 0; x < 8; x++ {
			leftX := bounds.Min.X + x*(bounds.Dx()-1)/8
			rightX := bounds.Min.X + (x+1)*(bounds.Dx()-1)/8
			left := color.GrayModel.Convert(decoded.At(leftX, py)).(color.Gray).Y
			right := color.GrayModel.Convert(decoded.At(rightX, py)).(color.Gray).Y
			if left > right {
				hash |= 1 << bit
			}
			bit++
		}
	}
	return hash, nil
}

func perceptuallyRelated(first, second [4]uint64) bool {
	matches := 0
	for i := range first {
		if bits.OnesCount64(first[i]^second[i]) <= 8 {
			matches++
		}
	}
	return matches >= 3
}

func validateSliceGates(kind fillereval.CorpusKind, gates []fillereval.SliceGate, cases int) error {
	seen := map[string]struct{}{}
	for _, gate := range gates {
		if strings.TrimSpace(gate.Slice) == "" || gate.MinCases <= 0 || gate.MinCases > cases || gate.MinAccuracy <= 0 || gate.MinAccuracy > 1 || gate.MinAccuracyLower < 0 || gate.MinAccuracyLower > 1 || kind == fillereval.CorpusCertification && gate.MinAccuracyLower == 0 {
			return fmt.Errorf("slice gates require unique names, feasible case counts, valid accuracy bounds, and positive confidence bounds for certification")
		}
		if _, duplicate := seen[gate.Slice]; duplicate {
			return fmt.Errorf("duplicate slice gate %q", gate.Slice)
		}
		seen[gate.Slice] = struct{}{}
	}
	return nil
}

func validateRightsDecision(decision fillercorpus.RightsDecision, item fillercorpus.InventoryCase, inventoryDigest string, preparedAt time.Time, kind fillereval.CorpusKind) error {
	if decision.InventorySHA256 != inventoryDigest || decision.MetadataSHA256 != item.MetadataSHA256 || !slices.Equal(decision.CaptureIDs, item.CaptureIDs) || decision.Authority != item.Authority || decision.ItemID != item.ItemID || strings.TrimSpace(decision.ReviewerID) == "" || strings.TrimSpace(decision.Basis) == "" || decision.ReviewedAt.Before(item.MetadataRetrievedAt) || decision.ReviewedAt.After(preparedAt) {
		return fmt.Errorf("case %q has a rights decision that is not bound to this inventory", decision.CaseID)
	}
	if decision.Decision != "approved" && decision.Decision != "held" {
		return fmt.Errorf("case %q has an invalid rights decision", decision.CaseID)
	}
	if kind == fillereval.CorpusCertification {
		if decision.WorksheetSchemaVersion != fillercorpus.HoldoutRightsWorksheetSchemaVersion {
			return fmt.Errorf("case %q has a legacy or mismatched certification rights decision", decision.CaseID)
		}
		contract := decision.HoldoutContract
		if decision.QuarantineContract != nil || contract == nil || (decision.Decision == "approved" && (len(contract.HoldReasons) != 0 || len(fillercorpus.HoldoutRightsHoldReasons(contract, preparedAt)) != 0)) {
			return fmt.Errorf("case %q lacks certification holdout authority", decision.CaseID)
		}
		wantRedistributable := contract.RedistributionScope == fillercorpus.RedistributionMasterAndDerivatives
		if decision.Decision == "approved" && decision.Redistributable != wantRedistributable {
			return fmt.Errorf("case %q has a conflicting redistribution scope", decision.CaseID)
		}
	} else if decision.WorksheetSchemaVersion != fillercorpus.RightsWorksheetSchemaVersion || decision.QuarantineContract != nil || decision.HoldoutContract != nil || decision.Redistributable != (decision.Decision == "approved") {
		return fmt.Errorf("case %q has an invalid development rights decision", decision.CaseID)
	}
	return nil
}

func (d *realDeriver) Measure(ctx context.Context, path string, start, end int64) (mediaMeasurement, error) {
	p, err := filler.FFprobeNextTo(d.ffmpeg)(ctx, path)
	if err != nil {
		return mediaMeasurement{}, err
	}
	if end > p.DurationMs {
		return mediaMeasurement{}, fmt.Errorf("segment exceeds measured duration")
	}
	quality, err := mediatools.InspectQualityIn(ctx, d.ffmpeg, path, start, end, !p.Silent)
	if err != nil {
		return mediaMeasurement{}, err
	}
	black, silence := coverage(quality.Black, quality.DurationMs), coverage(quality.Silence, quality.DurationMs)
	usable := !p.NoVideo && !p.Silent && black < 90 && silence < 90
	return mediaMeasurement{DurationMS: p.DurationMs, Usable: usable, Detail: fmt.Sprintf("source_duration_ms=%d;segment_start_ms=%d;segment_duration_ms=%d;no_video=%t;no_audio=%t;black_percent=%d;silence_percent=%d", p.DurationMs, start, end-start, p.NoVideo, p.Silent, black, silence)}, nil
}

func (d *realDeriver) Frames(ctx context.Context, path string, start, end int64) ([][]byte, error) {
	return d.tools.KeyframesIn(ctx, path, start, end, 4)
}
func (d *realDeriver) Audio(ctx context.Context, path string, start, end int64) (audioDerivative, error) {
	dir, err := os.MkdirTemp("", "loomarr-corpus-audio-*")
	if err != nil {
		return audioDerivative{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	wav := mediatools.SpanWAVPath(dir)
	if err := mediatools.ExtractSpanWAV(ctx, d.ffmpeg, path, start, end, wav); err != nil {
		return audioDerivative{}, err
	}
	data, err := os.ReadFile(wav)
	if err != nil {
		return audioDerivative{}, err
	}
	probe, err := filler.FFprobeNextTo(d.ffmpeg)(ctx, wav)
	if err != nil {
		return audioDerivative{}, err
	}
	return audioDerivative{Data: data, SHA256: fillercorpus.InventorySHA256(data), DurationMS: probe.DurationMs}, nil
}
func (d *realDeriver) Video(ctx context.Context, path string, start, end int64) (videoDerivative, error) {
	got, err := d.tools.HostedVideoIn(ctx, path, start, end)
	if err != nil {
		return videoDerivative{}, err
	}
	temp, err := os.CreateTemp("", "loomarr-corpus-video-*.mp4")
	if err != nil {
		return videoDerivative{}, err
	}
	name := temp.Name()
	defer func() { _ = temp.Close(); _ = os.Remove(name) }()
	if _, err = temp.Write(got.MP4); err != nil {
		return videoDerivative{}, err
	}
	if err = temp.Close(); err != nil {
		return videoDerivative{}, err
	}
	p, err := filler.FFprobeNextTo(d.ffmpeg)(ctx, name)
	if err != nil {
		return videoDerivative{}, err
	}
	return videoDerivative{Data: got.MP4, SHA256: got.SHA256, DurationMS: p.DurationMs, Width: p.Width, Height: p.Height}, nil
}

func coverage(spans []mediatools.Interval, duration int64) int64 {
	var total int64
	for _, s := range spans {
		total += s.EndMs - s.StartMs
	}
	if duration == 0 {
		return 0
	}
	return total * 100 / duration
}
func usability(ok bool) string {
	if ok {
		return filleradmission.UsabilityUsable
	}
	return filleradmission.UsabilityUnusable
}
func semanticAt(start, duration int64, index int) int64 {
	fractions := []float64{.05, 1.0 / 3, 2.0 / 3, .9}
	return start + int64(float64(duration)*fractions[index])
}

func metadataSignals(item fillercorpus.InventoryCase) []fillerbakeoff.Signal {
	values := []fillerbakeoff.Signal{{ID: "filename", Kind: string(filleradmission.KindFilename), Text: item.Representation.Name}}
	metadata, _ := json.Marshal(struct {
		Title   string   `json:"title"`
		Creator []string `json:"creator,omitempty"`
		Date    string   `json:"date,omitempty"`
	}{item.Title, item.Creator, item.Date})
	values = append(values, fillerbakeoff.Signal{ID: "source-metadata", Kind: string(filleradmission.KindUploaderMetadata), Text: string(metadata)})
	return values
}

func mediaPathFor(opts options, item fillercorpus.InventoryCase) (string, string, error) {
	if item.Representation.Transport == fillercorpus.TransportLocal {
		p, err := inside(opts.localRoot, item.Representation.Path)
		return p, "local:" + item.Representation.Path, err
	}
	name := fillercorpus.InventorySHA256([]byte(item.CaseID))[:16] + filepath.Ext(item.Representation.Name)
	p, err := inside(opts.remoteRoot, name)
	return p, item.Representation.URL, err
}
func inside(root, relative string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", err
	}
	candidate, err := filepath.EvalSymlinks(filepath.Join(rootReal, relative))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootReal, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("media path escapes its declared root")
	}
	return candidate, nil
}

type mediaHashes struct{ sha256, sha1, md5 string }

func hashMedia(path string) (mediaHashes, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return mediaHashes{}, 0, err
	}
	defer func() { _ = f.Close() }()
	h256, h1, hm := sha256.New(), sha1.New(), md5.New()
	n, err := io.Copy(io.MultiWriter(h256, h1, hm), f)
	return mediaHashes{hex.EncodeToString(h256.Sum(nil)), hex.EncodeToString(h1.Sum(nil)), hex.EncodeToString(hm.Sum(nil))}, n, err
}
func matches(want, got string) bool { return want == "" || strings.EqualFold(want, got) }
func readStrictJSON(path string, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}
func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []T
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64<<10), 4<<20)
	for s.Scan() {
		if len(bytes.TrimSpace(s.Bytes())) == 0 {
			continue
		}
		var v T
		decoder := json.NewDecoder(bytes.NewReader(s.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&v); err != nil {
			return nil, err
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return nil, fmt.Errorf("trailing JSON value in JSONL record")
		}
		out = append(out, v)
	}
	return out, s.Err()
}
func writeArtifact(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return atomicWrite(path, data)
}
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'))
}
func writeJSONL[T any](path string, values []T) error {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	for _, v := range values {
		if err := e.Encode(v); err != nil {
			return err
		}
	}
	return atomicWrite(path, b.Bytes())
}
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".filler-corpus-prepare-*")
	if err != nil {
		return err
	}
	name := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}
