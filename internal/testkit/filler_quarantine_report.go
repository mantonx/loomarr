package testkit

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillerquarantine"
	"github.com/loomarr/loomarr/internal/fillerreference"
	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/mediatools"
)

// FillerQuarantineReport builds strict synthetic report bytes for command and
// module tests. dispositions may omit a non-local case to model incomplete
// download coverage; contentSHA256 overrides the inspected source identity.
func FillerQuarantineReport(t testing.TB, inventoryRaw []byte, dispositions, contentSHA256 map[string]string) []byte {
	t.Helper()
	inventory, err := fillercorpus.DecodeInventoryBytes(inventoryRaw)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	tool := fillerreview.TemporalTruthToolIdentity{Path: "/fixture/tool", Version: "fixture", BinarySHA256: digest}
	report := fillerquarantine.Report{
		SchemaVersion: fillerquarantine.SchemaVersion, ContractVersion: fillerquarantine.ContractVersion,
		GeneratedAt: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC),
		Inputs: fillerquarantine.InputIdentity{
			InventorySHA256: fillercorpus.InventorySHA256(inventoryRaw), DownloadLedgerSHA256: strings.Repeat("b", 64),
			PriorPublicManifestSHA256: strings.Repeat("c", 64), PriorAuthoritySHA256: strings.Repeat("d", 64),
		},
		MediaTools: fillerreview.TemporalTruthMediaIdentity{FFmpeg: tool, FFprobe: tool},
		Ceilings:   fillerquarantine.Ceilings{MaxMediaWallTimeMS: 1_000}, Algorithm: "fixture-v1",
		Authority: fillerquarantine.AuthorityDisposition{CopyAndStorage: true, LocalTechnicalInspection: true},
	}
	for _, item := range inventory.Cases {
		if item.Representation.Transport == fillercorpus.TransportLocal {
			continue
		}
		disposition, included := dispositions[item.CaseID]
		if !included {
			continue
		}
		content := contentSHA256[item.CaseID]
		if !fillercorpus.IsSHA256(content) {
			content = fillercorpus.InventorySHA256([]byte("inspected:" + item.CaseID))
		}
		caseReport := fillerquarantine.Case{
			CaseID: item.CaseID, LocalFile: item.CaseID + ".mp4", ContentSHA256: content, Bytes: max(item.Representation.Bytes, 1),
			ExpectedMedia: fillerquarantine.MediaExpectation{Bytes: max(item.Representation.Bytes, 1)},
			Media: fillerquarantine.MediaEvidence{DurationMS: 1_000, Width: 640, Height: 360, HasVideo: true, HasAudio: true, Quality: mediatools.MediaQuality{
				EvidenceVersion: mediatools.MediaQualityEvidenceV1, Provenance: mediatools.MediaQualityProvenanceFFmpegDetectors, DurationMs: 1_000,
			}},
			Fingerprint: fillerquarantine.FingerprintEvidence{FrameCount: 1, FrameSHA256: digest, AudioBinCount: 1, AudioRMSSHA256: digest, VisualComparable: true, AudioComparable: true},
			Disposition: disposition,
		}
		if disposition == fillerquarantine.DispositionHold {
			caseReport.HoldReasons = []string{"missing_audio"}
			caseReport.Media.HasAudio = false
		}
		report.Cases = append(report.Cases, caseReport)
	}
	slices.SortFunc(report.Cases, func(a, b fillerquarantine.Case) int {
		return strings.Compare(a.CaseID, b.CaseID)
	})
	for left := range report.Cases {
		for right := left + 1; right < len(report.Cases); right++ {
			report.Comparisons = append(report.Comparisons, fillerquarantine.Comparison{
				Scope: fillerquarantine.ComparisonCandidate, CaseA: report.Cases[left].CaseID, CaseB: report.Cases[right].CaseID,
				Visual: fillerreference.DuplicateComparison{}, Audio: fillerreference.AudioComparison{},
			})
		}
	}
	report.Summary.Cases = len(report.Cases)
	for _, item := range report.Cases {
		if item.Disposition == fillerquarantine.DispositionEligibleForRightsReview {
			report.Summary.EligibleForRightsReview++
		} else {
			report.Summary.Held++
		}
	}
	if err := fillerquarantine.Validate(report); err != nil {
		t.Fatalf("synthetic quarantine report: %v", err)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
