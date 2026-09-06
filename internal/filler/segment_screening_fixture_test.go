package filler

import (
	"strings"
	"testing"
	"time"
)

func screeningProfileFixture(axis SegmentScreeningAxis, digit string) SegmentScreeningAxisProfile {
	return SegmentScreeningAxisProfile{
		Axis: axis, EvidenceContract: "axis-evidence-v1",
		PolicySHA256: strings.Repeat(digit, 64), CertificationSHA256: strings.Repeat("a", 64), ImplementationSHA256: strings.Repeat("b", 64),
	}
}

func screeningSubjectFixture(t *testing.T) SegmentScreeningSubject {
	t.Helper()
	manifest := screeningSubjectManifest(t)
	subject, err := NewSegmentScreeningSubject(manifest.Playback.Asset.ClipHash, SidecarTags{MediaAssets: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

func screeningChildSubjectFixture(t *testing.T) SegmentScreeningSubject {
	t.Helper()
	tags := screeningChildTagsFixture(t)
	subject, err := NewSegmentScreeningSubject(tags.MediaAssets.Playback.Asset.ClipHash, tags)
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

func screeningChildTagsFixture(t *testing.T) SidecarTags {
	t.Helper()
	manifest := screeningSubjectManifest(t)
	lineage := ConditioningLineage{
		ChildHash: manifest.SourceMaster.ClipHash, ParentHash: strings.Repeat("7", 64),
		ParentAssetRole: string(SplitSourceEvidence), ParentAssetSHA256: strings.Repeat("8", 64),
		StructureDecisionSHA256: strings.Repeat("a", 64), StructureRole: SegmentRoleCommercial,
		IntendedStartMs: 1_000, IntendedEndMs: 31_000,
	}
	measurement := completeConditioningMeasurement(-23)
	conditioning := ConditioningEvidence{
		BeforeRewriteHash: lineage.ChildHash, AfterRewriteHash: manifest.Playback.Asset.ClipHash,
		BeforeRewrite: measurement, AfterRewrite: measurement,
		DerivedParentEdgesAfterRewrite: measurement.Cuts[0],
	}
	return SidecarTags{
		SourceID: "archive:commercials", AcquisitionID: "acq-17", MediaAssets: &manifest,
		ConditioningLineage: &lineage, Conditioning: &conditioning,
	}
}

func passingAxisEvidence(t *testing.T, subject SegmentScreeningSubject) []RecordedSegmentScreeningAxisEvidence {
	t.Helper()
	records := make([]RecordedSegmentScreeningAxisEvidence, 0, len(segmentScreeningAxisOrder))
	for index, axis := range segmentScreeningAxisOrder {
		reason := "policy_clear"
		switch axis {
		case ScreenRights:
			reason = "rights_verified"
		case ScreenPlayback:
			reason = "playback_verified"
		}
		recorded, err := NewSegmentScreeningAxisEvidence(
			subject, screeningProfileFixture(axis, string(rune('1'+index))), ScreenPass, reason,
			[]byte("recorded-"+string(axis)), time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC),
		)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, recorded)
	}
	return records
}
