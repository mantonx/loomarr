package filler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/loomarr/loomarr/internal/mediatools"
)

const (
	SegmentScreeningSubjectSchemaVersion   = 1
	SegmentScreeningSubjectContractVersion = "filler-segment-screening-subject-v1"
)

// SegmentScreeningSubject is the immutable, path-free identity shared by every child-screening
// axis. It binds the exact final artifacts before any evaluator runs. Lineage is nil only for a
// top-level clip; a materialized compilation child also binds its parent interval and conditioning.
type SegmentScreeningSubject struct {
	SchemaVersion       int                  `json:"schemaVersion"`
	ContractVersion     string               `json:"contractVersion"`
	CatalogHash         string               `json:"catalogHash"`
	SourceMasterHash    string               `json:"sourceMasterHash"`
	SourceMasterSHA256  string               `json:"sourceMasterSha256"`
	SourceMasterBytes   int64                `json:"sourceMasterBytes"`
	EvidenceHash        string               `json:"evidenceHash"`
	EvidenceSHA256      string               `json:"evidenceSha256"`
	EvidenceBytes       int64                `json:"evidenceBytes"`
	EvidenceDurationMs  int64                `json:"evidenceDurationMs"`
	PlaybackSHA256      string               `json:"playbackSha256"`
	PlaybackBytes       int64                `json:"playbackBytes"`
	PlaybackDurationMs  int64                `json:"playbackDurationMs"`
	MediaManifestSHA256 string               `json:"mediaManifestSha256"`
	SourceID            string               `json:"sourceId,omitempty"`
	AcquisitionID       string               `json:"acquisitionId,omitempty"`
	Lineage             *ConditioningLineage `json:"lineage,omitempty"`
	ConditioningSHA256  string               `json:"conditioningSha256,omitempty"`
	SHA256              string               `json:"sha256"`
}

// NewSegmentScreeningSubject projects the portable sidecar into the one path-free subject every
// evaluator must name. It accepts no filepath and therefore cannot accidentally bless a different
// object merely because it currently occupies the same catalog location.
func NewSegmentScreeningSubject(catalogHash string, tags SidecarTags) (SegmentScreeningSubject, error) {
	if tags.MediaAssets == nil || tags.MediaAssets.Evidence == nil || tags.MediaAssets.Playback == nil {
		return SegmentScreeningSubject{}, fmt.Errorf("segment screening subject requires complete media assets")
	}
	manifest := *tags.MediaAssets
	if err := manifest.validate(); err != nil {
		return SegmentScreeningSubject{}, fmt.Errorf("segment screening subject media: %w", err)
	}
	evidence, playback := manifest.Evidence, manifest.Playback
	if catalogHash != playback.Asset.ClipHash {
		return SegmentScreeningSubject{}, fmt.Errorf("segment screening subject catalog identity does not match playback")
	}
	if mediatools.ValidateMediaQualityEvidence(evidence.Quality) != nil || evidence.Quality.DurationMs != evidence.DurationMs ||
		mediatools.ValidateMediaQualityEvidence(playback.Quality) != nil || playback.Quality.DurationMs != playback.DurationMs {
		return SegmentScreeningSubject{}, fmt.Errorf("segment screening subject media-quality evidence is invalid")
	}
	subject := SegmentScreeningSubject{
		SchemaVersion: SegmentScreeningSubjectSchemaVersion, ContractVersion: SegmentScreeningSubjectContractVersion,
		CatalogHash:      catalogHash,
		SourceMasterHash: manifest.SourceMaster.ClipHash, SourceMasterSHA256: manifest.SourceMaster.SHA256, SourceMasterBytes: manifest.SourceMaster.Bytes,
		EvidenceHash: evidence.Asset.ClipHash, EvidenceSHA256: evidence.Asset.SHA256, EvidenceBytes: evidence.Asset.Bytes, EvidenceDurationMs: evidence.DurationMs,
		PlaybackSHA256: playback.Asset.SHA256, PlaybackBytes: playback.Asset.Bytes, PlaybackDurationMs: playback.DurationMs,
		MediaManifestSHA256: MediaAssetManifestAuthoritySHA256(manifest),
		SourceID:            strings.TrimSpace(tags.SourceID), AcquisitionID: strings.TrimSpace(tags.AcquisitionID),
	}
	if tags.ConditioningLineage != nil {
		lineage := *tags.ConditioningLineage
		if !validConditioningLineage(&lineage, lineage.ParentHash) || lineage.ParentAssetRole == "" || !isContentHash(lineage.ParentAssetSHA256) || lineage.ChildHash != manifest.SourceMaster.ClipHash {
			return SegmentScreeningSubject{}, fmt.Errorf("segment screening subject child lineage is invalid")
		}
		if tags.Conditioning == nil || validateConditioningPair(*tags.Conditioning, lineage.ChildHash, catalogHash) != nil || !conditioningEvidenceMatchesLineage(*tags.Conditioning, lineage) ||
			tags.Conditioning.AfterRewrite.ContainerDurationMs != playback.DurationMs || !reflect.DeepEqual(tags.Conditioning.AfterRewrite.Quality, playback.Quality) {
			return SegmentScreeningSubject{}, fmt.Errorf("segment screening subject conditioning evidence is invalid")
		}
		subject.Lineage = &lineage
		subject.ConditioningSHA256 = ConditioningEvidenceSHA256(*tags.Conditioning)
	} else if tags.Conditioning != nil {
		return SegmentScreeningSubject{}, fmt.Errorf("segment screening subject has conditioning without child lineage")
	}
	subject.SHA256 = SegmentScreeningSubjectSHA256(subject)
	if err := ValidateSegmentScreeningSubject(subject); err != nil {
		return SegmentScreeningSubject{}, err
	}
	return subject, nil
}

func ValidateSegmentScreeningSubject(subject SegmentScreeningSubject) error {
	if subject.SchemaVersion != SegmentScreeningSubjectSchemaVersion || subject.ContractVersion != SegmentScreeningSubjectContractVersion ||
		!isContentHash(subject.CatalogHash) || !isContentHash(subject.SourceMasterHash) || !isContentHash(subject.SourceMasterSHA256) || subject.SourceMasterBytes <= 0 || subject.SourceMasterBytes > mediatools.ConditioningMaxSnapshotBytes ||
		!isContentHash(subject.EvidenceHash) || !isContentHash(subject.EvidenceSHA256) || subject.EvidenceBytes <= 0 || subject.EvidenceBytes > mediatools.ConditioningMaxSnapshotBytes || subject.EvidenceDurationMs <= 0 ||
		!isContentHash(subject.PlaybackSHA256) || subject.PlaybackBytes <= 0 || subject.PlaybackBytes > mediatools.ConditioningMaxSnapshotBytes || subject.PlaybackDurationMs <= 0 || !isContentHash(subject.MediaManifestSHA256) ||
		!validScreeningSubjectID(subject.SourceID) || !validScreeningSubjectID(subject.AcquisitionID) {
		return fmt.Errorf("segment screening subject identity is invalid")
	}
	if subject.Lineage == nil {
		if subject.ConditioningSHA256 != "" {
			return fmt.Errorf("segment screening subject has unbound conditioning")
		}
	} else if !validConditioningLineage(subject.Lineage, subject.Lineage.ParentHash) || subject.Lineage.ParentAssetRole == "" ||
		!isContentHash(subject.Lineage.ParentAssetSHA256) || subject.Lineage.ChildHash != subject.SourceMasterHash || !isContentHash(subject.ConditioningSHA256) {
		return fmt.Errorf("segment screening subject lineage is invalid")
	}
	if subject.SHA256 == "" || subject.SHA256 != SegmentScreeningSubjectSHA256(subject) {
		return fmt.Errorf("segment screening subject digest is invalid")
	}
	return nil
}

// VerifySegmentScreeningSubject reprojects the supplied portable sidecar and requires exact
// agreement with the persisted subject. The caller must separately reopen and verify the three
// artifacts; structural validation intentionally cannot prove that external bytes still match.
func VerifySegmentScreeningSubject(subject SegmentScreeningSubject, catalogHash string, tags SidecarTags) error {
	if err := ValidateSegmentScreeningSubject(subject); err != nil {
		return err
	}
	expected, err := NewSegmentScreeningSubject(catalogHash, tags)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(subject, expected) {
		return fmt.Errorf("segment screening subject does not match current child artifacts")
	}
	return nil
}

func SegmentScreeningSubjectSHA256(subject SegmentScreeningSubject) string {
	subject.SHA256 = ""
	raw, err := json.Marshal(subject)
	if err != nil {
		return ""
	}
	return screeningBytesSHA256(raw)
}

// MediaAssetManifestAuthoritySHA256 binds recipes, tools, probes, QC, and byte identities while
// deliberately excluding relocatable filesystem paths.
func MediaAssetManifestAuthoritySHA256(manifest MediaAssetManifest) string {
	if err := manifest.validate(); err != nil {
		return ""
	}
	projected := manifest
	projected.SourceMaster.Path = ""
	if projected.Evidence != nil {
		evidence := *projected.Evidence
		evidence.Asset.Path = ""
		projected.Evidence = &evidence
	}
	if projected.Playback != nil {
		playback := *projected.Playback
		playback.Asset.Path = ""
		projected.Playback = &playback
	}
	raw, err := json.Marshal(projected)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func ConditioningEvidenceSHA256(evidence ConditioningEvidence) string {
	raw, err := json.Marshal(evidence)
	if err != nil {
		return ""
	}
	return screeningBytesSHA256(raw)
}

func validScreeningSubjectID(value string) bool {
	return value == "" || value == strings.TrimSpace(value) && len(value) <= 256
}
