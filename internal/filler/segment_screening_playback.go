package filler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"
)

const (
	playbackIntegrityEvidenceSchemaVersion   = 1
	playbackIntegrityEvidenceContractVersion = "playback-integrity-evidence-v1"
	segmentScreeningSidecarMaxBytes          = 64 << 20
)

type playbackIntegrityRawEvidence struct {
	SchemaVersion      int                                 `json:"schemaVersion"`
	ContractVersion    string                              `json:"contractVersion"`
	SubjectSHA256      string                              `json:"subjectSha256"`
	Playback           segmentScreeningArtifactObservation `json:"playback"`
	ManifestSHA256     string                              `json:"manifestSha256"`
	ConditioningSHA256 string                              `json:"conditioningSha256,omitempty"`
	Outcome            SegmentScreeningOutcome             `json:"outcome"`
	ReasonCode         string                              `json:"reasonCode"`
}

// PlaybackIntegrityEvaluator verifies the exact derivative that may eventually air. It trusts
// neither the current path nor the sidecar alone: the live bytes, sparse catalog identity,
// manifest projection, media-quality evidence, and child conditioning must reproduce Subject.
type PlaybackIntegrityEvaluator struct {
	profile SegmentScreeningAxisProfile
	replay  SegmentScreeningAxisEvidenceReplay
	now     func() time.Time
}

func NewPlaybackIntegrityEvaluator(profile SegmentScreeningAxisProfile, replay SegmentScreeningAxisEvidenceReplay, now func() time.Time) (*PlaybackIntegrityEvaluator, error) {
	if ValidateSegmentScreeningAxisProfile(profile) != nil || profile.Axis != ScreenPlayback || profile.EvidenceContract != playbackIntegrityEvidenceContractVersion {
		return nil, fmt.Errorf("playback integrity evaluator profile is invalid")
	}
	if replay == nil || now == nil {
		return nil, fmt.Errorf("playback integrity evaluator requires replay and clock")
	}
	return &PlaybackIntegrityEvaluator{profile: profile, replay: replay, now: now}, nil
}

func (e *PlaybackIntegrityEvaluator) Axis() SegmentScreeningAxis { return ScreenPlayback }

func (e *PlaybackIntegrityEvaluator) Evaluate(ctx context.Context, media SegmentScreeningMedia) (RecordedSegmentScreeningAxisEvidence, error) {
	if e == nil || e.replay == nil || e.now == nil || ValidateSegmentScreeningAxisProfile(e.profile) != nil || e.profile.Axis != ScreenPlayback || e.profile.EvidenceContract != playbackIntegrityEvidenceContractVersion {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("playback integrity evaluator is unavailable")
	}
	if err := validateSegmentScreeningMedia(media); err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, err
	}
	observation, matches, err := inspectSegmentScreeningArtifact(
		ctx, media.PlaybackPath, media.Subject.PlaybackSHA256, media.Subject.PlaybackBytes, media.Subject.CatalogHash,
	)
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, err
	}
	outcome, reasonCode := ScreenHold, playbackArtifactHoldReason(observation)
	manifestSHA256 := ""
	conditioningSHA256 := ""
	if matches {
		tags, state, err := readSegmentScreeningSidecar(media.PlaybackPath)
		if err != nil {
			return RecordedSegmentScreeningAxisEvidence{}, err
		}
		switch state {
		case SidecarAbsent:
			reasonCode = "playback_sidecar_missing"
		case SidecarInvalid:
			reasonCode = "playback_sidecar_invalid"
		case SidecarValid:
			if err := VerifySegmentScreeningSubject(media.Subject, media.Subject.CatalogHash, tags); err != nil {
				reasonCode = "playback_subject_drift"
				break
			}
			confirmed, confirmedMatches, err := inspectSegmentScreeningArtifact(
				ctx, media.PlaybackPath, media.Subject.PlaybackSHA256, media.Subject.PlaybackBytes, media.Subject.CatalogHash,
			)
			if err != nil {
				return RecordedSegmentScreeningAxisEvidence{}, err
			}
			if !confirmedMatches || confirmed != observation {
				observation, reasonCode = confirmed, "playback_unsafe"
				break
			}
			manifestSHA256 = MediaAssetManifestAuthoritySHA256(*tags.MediaAssets)
			if tags.Conditioning != nil {
				conditioningSHA256 = ConditioningEvidenceSHA256(*tags.Conditioning)
			}
			switch verdict, _, _ := EvaluateMediaQuality(tags.MediaAssets.Playback.Quality); verdict {
			case VerdictReject:
				outcome, reasonCode = ScreenReject, "playback_quality_reject"
			case VerdictReview:
				outcome, reasonCode = ScreenHold, "playback_quality_hold"
			default:
				outcome, reasonCode = ScreenPass, "playback_verified"
			}
		}
	}
	raw, err := json.Marshal(playbackIntegrityRawEvidence{
		SchemaVersion: playbackIntegrityEvidenceSchemaVersion, ContractVersion: playbackIntegrityEvidenceContractVersion,
		SubjectSHA256: media.Subject.SHA256, Playback: observation, ManifestSHA256: manifestSHA256,
		ConditioningSHA256: conditioningSHA256, Outcome: outcome, ReasonCode: reasonCode,
	})
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("marshal playback integrity evidence: %w", err)
	}
	replayed, found, err := e.replay.FindSegmentScreeningAxisEvidence(ctx, media.Subject.SHA256, e.profile)
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("replay playback integrity evidence: %w", err)
	}
	if found {
		if replayed.Evidence.Outcome != outcome || replayed.Evidence.ReasonCode != reasonCode || !bytes.Equal(replayed.RawEvidence, raw) {
			return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("playback integrity operation conflicts with its settled result")
		}
		return replayed, nil
	}
	return NewSegmentScreeningAxisEvidence(media.Subject, e.profile, outcome, reasonCode, nil, raw, e.now())
}

func playbackArtifactHoldReason(observation segmentScreeningArtifactObservation) string {
	switch observation.State {
	case "missing":
		return "playback_missing"
	case "unsafe", "changed":
		return "playback_unsafe"
	default:
		return "playback_identity_drift"
	}
}

func readSegmentScreeningSidecar(mediaPath string) (SidecarTags, SidecarReadState, error) {
	path := sidecarPathFor(mediaPath)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return SidecarTags{}, SidecarAbsent, nil
	}
	if err != nil {
		return SidecarTags{}, SidecarInvalid, fmt.Errorf("inspect playback sidecar: %w", err)
	}
	if infoMode := info.Mode(); infoMode&os.ModeSymlink != 0 || !infoMode.IsRegular() {
		return SidecarTags{}, SidecarInvalid, nil
	}
	if info.Size() <= 0 || info.Size() > segmentScreeningSidecarMaxBytes {
		return SidecarTags{}, SidecarInvalid, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return SidecarTags{}, SidecarInvalid, fmt.Errorf("open playback sidecar: %w", err)
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil {
		return SidecarTags{}, SidecarInvalid, fmt.Errorf("stat playback sidecar: %w", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() != info.Size() {
		return SidecarTags{}, SidecarInvalid, nil
	}
	raw, err := io.ReadAll(io.LimitReader(file, segmentScreeningSidecarMaxBytes+1))
	if err != nil {
		return SidecarTags{}, SidecarInvalid, fmt.Errorf("read playback sidecar: %w", err)
	}
	after, err := file.Stat()
	if err != nil {
		return SidecarTags{}, SidecarInvalid, fmt.Errorf("restat playback sidecar: %w", err)
	}
	if int64(len(raw)) != opened.Size() || after.Size() != opened.Size() {
		return SidecarTags{}, SidecarInvalid, nil
	}
	tags, state, present := decodeSidecarTags(raw)
	if state != SidecarValid || !present {
		if state == SidecarValid {
			state = SidecarAbsent
		}
		return SidecarTags{}, state, nil
	}
	return tags, SidecarValid, nil
}

var _ SegmentScreeningEvaluator = (*PlaybackIntegrityEvaluator)(nil)
