package fillersafetycert

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

func TestExactRuleAttributionIsRequiredForAPositiveHit(t *testing.T) {
	t.Parallel()
	fixture := newCertificationFixture(t)
	run := &fixture.manifest.Runs[0]
	terminal := run.Events[len(run.Events)-1].Terminal
	terminal.Evidence.Audio[0].MatchedRuleIDs = []string{"rule-000000000000000000000002"}
	refreshTerminal(t, run)
	fixture.rewriteManifest(t)

	report, _, err := Publish(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	if report.CertificationStatus != StatusFailed || report.DetectedPositiveSources != MinimumPositiveFamilies-1 ||
		report.MissedPositiveSources != 1 || report.DetectedPositiveIntervals != report.PositiveIntervals-1 {
		t.Fatalf("report=%+v", report)
	}
}

func TestCleanAudioDetectionFailsEveryAffectedSlice(t *testing.T) {
	t.Parallel()
	fixture := newCertificationFixture(t)
	index := MinimumPositiveFamilies
	item := fixture.authority.Cases[index]
	item.Label = LabelPositive
	fixture.manifest.Runs[index] = fixtureResultRun(fixture.authority, fixture.manifest.AuthoritySHA256, item, index)
	fixture.rewriteManifest(t)

	report, _, err := Publish(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	if report.CertificationStatus != StatusFailed || report.CleanFalsePositiveSources != 1 {
		t.Fatalf("report=%+v", report)
	}
	failed := 0
	for _, metric := range report.CleanSlices {
		if !metric.Passed {
			failed++
		}
	}
	if failed == 0 {
		t.Fatalf("clean slice metrics=%+v", report.CleanSlices)
	}
}

func TestCompleteProviderFailureIsACoverageHold(t *testing.T) {
	t.Parallel()
	fixture := newCertificationFixture(t)
	run := &fixture.manifest.Runs[0]
	settlement := run.Events[3].Settle
	settlement.State, settlement.Failure, settlement.Outcome = fillersafety.SettlementFailed, fillersafety.FailureTransport, ""
	terminal := run.Events[len(run.Events)-1].Terminal
	terminal.Evidence.Audio[0].State = fillersafety.AudioFailed
	terminal.Evidence.Audio[0].MatchedRuleIDs = []string{}
	terminal.Result = fillersafety.Reduce(terminal.Evidence)
	refreshTerminal(t, run)
	fixture.rewriteManifest(t)

	report, _, err := Publish(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	if report.CertificationStatus != StatusFailed || report.CoverageHolds != 1 || report.MissedPositiveSources != 1 ||
		report.Cases[0].Outcome != OutcomeCoverageHold {
		t.Fatalf("report=%+v", report)
	}
}

func TestManifestRejectsMissingRunsAndTerminalDrift(t *testing.T) {
	t.Parallel()
	t.Run("missing run", func(t *testing.T) {
		fixture := newCertificationFixture(t)
		fixture.manifest.Runs = fixture.manifest.Runs[:len(fixture.manifest.Runs)-1]
		fixture.rewriteManifest(t)
		if _, _, err := Publish(fixture.config()); err == nil || !strings.Contains(err.Error(), "run count") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("terminal digest", func(t *testing.T) {
		fixture := newCertificationFixture(t)
		fixture.manifest.Runs[0].TerminalSHA256 = fixtureSHA(99999)
		fixture.rewriteManifest(t)
		if _, _, err := Publish(fixture.config()); err == nil || !strings.Contains(err.Error(), "terminal digest") {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestAuthorityRejectsNonIndependentModelReviewer(t *testing.T) {
	t.Parallel()
	fixture := newCertificationFixture(t)
	reviewer := &fixture.authority.Cases[0].Reviewers[0]
	reviewer.Method = ReviewerModel
	reviewer.ModelFamily = fixture.authority.AudioRoute.ModelFamily
	fixture.rewriteAuthority(t)
	if _, _, err := Publish(fixture.config()); err == nil || !strings.Contains(err.Error(), "family-independent") {
		t.Fatalf("err=%v", err)
	}
}

func TestAuthorityRequiresNoiseAndPlacementCoverage(t *testing.T) {
	t.Parallel()
	for _, required := range []string{SliceNoise, SlicePlacement} {
		t.Run(required, func(t *testing.T) {
			fixture := newCertificationFixture(t)
			for index := range fixture.authority.Cases {
				item := &fixture.authority.Cases[index]
				if item.Label != LabelPositive {
					continue
				}
				item.Slices = slices.DeleteFunc(item.Slices, func(slice string) bool { return slice == required })
				if len(item.Slices) == 0 {
					item.Slices = []string{SliceQuietSpeech}
				}
			}
			fixture.rewriteAuthority(t)
			if _, _, err := Publish(fixture.config()); err == nil || !strings.Contains(err.Error(), "coverage") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestManifestRejectsRouteDriftAndHistoricalUnattributedEvidence(t *testing.T) {
	t.Parallel()
	t.Run("route schema", func(t *testing.T) {
		fixture := newCertificationFixture(t)
		fixture.manifest.Runs[0].Events[2].Reserve.SchemaSHA256 = fixtureSHA(99998)
		fixture.rewriteManifest(t)
		if _, _, err := Publish(fixture.config()); err == nil || !strings.Contains(err.Error(), "route") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("historical attribution", func(t *testing.T) {
		fixture := newCertificationFixture(t)
		run := &fixture.manifest.Runs[0]
		run.Events[len(run.Events)-1].Terminal.Evidence.Audio[0].MatchedRuleIDs = nil
		refreshTerminal(t, run)
		fixture.rewriteManifest(t)
		if _, _, err := Publish(fixture.config()); err == nil || !strings.Contains(err.Error(), "rule attribution") {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestManifestBindsCandidateCoverageAndAudioDerivativeDuration(t *testing.T) {
	t.Parallel()
	t.Run("candidate outside source", func(t *testing.T) {
		fixture := newCertificationFixture(t)
		run := &fixture.manifest.Runs[0]
		run.Events[1].Proposal.Candidates[0].EndMS = fixture.authority.Cases[0].DurationMS + 1
		refreshRunTerminalEvidence(t, run)
		fixture.rewriteManifest(t)
		if _, _, err := Publish(fixture.config()); err == nil || !strings.Contains(err.Error(), "outside the source") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("wrong in-source audio duration", func(t *testing.T) {
		fixture := newCertificationFixture(t)
		fixture.authority.Cases[0].DurationMS = 5_000
		fixture.rewriteAuthority(t)
		run := &fixture.manifest.Runs[0]
		run.Events[1].Proposal.Candidates[0].StartMS = 200
		run.Events[1].Proposal.Candidates[0].EndMS = 400
		refreshRunTerminalEvidence(t, run)
		fixture.rewriteManifest(t)
		if _, _, err := Publish(fixture.config()); err == nil || !strings.Contains(err.Error(), "audio reservation") {
			t.Fatalf("err=%v", err)
		}
	})
	for _, test := range []struct {
		name             string
		startMS, endMS   int64
		derivativeLength int64
	}{
		{name: "clipped beginning", startMS: 0, endMS: 250, derivativeLength: 1_250},
		{name: "middle", startMS: 2_000, endMS: 2_500, derivativeLength: 2_500},
		{name: "clipped end", startMS: 4_750, endMS: 5_000, derivativeLength: 1_250},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCertificationFixture(t)
			fixture.authority.Cases[0].DurationMS = 5_000
			fixture.rewriteAuthority(t)
			run := &fixture.manifest.Runs[0]
			run.Events[1].Proposal.Candidates[0].StartMS = test.startMS
			run.Events[1].Proposal.Candidates[0].EndMS = test.endMS
			run.Events[2].Reserve.DerivativeDurationMS = test.derivativeLength
			refreshRunTerminalEvidence(t, run)
			fixture.rewriteManifest(t)
			if _, _, err := Publish(fixture.config()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrivateInputsRejectGroupReadableFiles(t *testing.T) {
	fixture := newCertificationFixture(t)
	if err := os.Chmod(fixture.resultsPath, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Publish(fixture.config()); err == nil || !strings.Contains(err.Error(), "mode 0600") {
		t.Fatalf("err=%v", err)
	}
}
