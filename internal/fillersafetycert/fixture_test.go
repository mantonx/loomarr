package fillersafetycert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

const fixtureRuleID = "rule-000000000000000000000001"

type certificationFixture struct {
	authorityPath, resultsPath, outputPath string
	authority                              Authority
	manifest                               ResultManifest
	scoredAt                               time.Time
}

func newCertificationFixture(t *testing.T) *certificationFixture {
	t.Helper()
	directory := t.TempDir()
	authoredAt := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	authority := Authority{
		SchemaVersion: SchemaVersion, ContractVersion: ContractVersion, AuthoredAt: authoredAt,
		ChallengeKind: ChallengeCertification, CorpusManifestSHA256: fixtureSHA(9000),
		PolicySHA256: fixtureSHA(9001), ProposerSHA256: fixtureSHA(9002), ProposerFamily: "sherpa-onnx-vad",
		Implementation: "spoken-safety-evaluator-v1",
		AudioRoute:     fixtureRoute([]string{"audio"}, "native-audio", 9100),
		VideoRoute:     fixtureRoute([]string{"audio", "video"}, "complete-video", 9200),
	}
	positiveSlices := requiredPositiveSlices()
	for index := range MinimumPositiveFamilies {
		authority.Cases = append(authority.Cases, fixtureAuthorityCase(index, LabelPositive, []string{positiveSlices[index%len(positiveSlices)]}))
	}
	for index, slice := range requiredCleanSlices() {
		authority.Cases = append(authority.Cases, fixtureAuthorityCase(MinimumPositiveFamilies+index, LabelClean, []string{slice}))
	}
	fixture := &certificationFixture{
		authorityPath: filepath.Join(directory, "authority.json"),
		resultsPath:   filepath.Join(directory, "results.json"),
		outputPath:    filepath.Join(directory, "report.json"),
		authority:     authority, scoredAt: authoredAt.Add(4 * time.Hour),
	}
	fixture.rewriteAuthority(t)
	return fixture
}

func fixtureRoute(modalities []string, rung string, seed int) RouteAuthority {
	return RouteAuthority{
		Role: "spoken-safety", Rung: rung, Modalities: modalities,
		RequestedProvider: "openrouter", RequestedModel: "vendor/model",
		ResolvedProvider: "openrouter", ResolvedModel: "vendor/model-2026",
		UpstreamProvider: "provider", ModelFamily: "vendor-family-" + rung,
		CapabilitySHA256: fixtureSHA(seed), PromptSHA256: fixtureSHA(seed + 1), SchemaSHA256: fixtureSHA(seed + 2),
	}
}

func fixtureAuthorityCase(index int, label string, slices []string) AuthorityCase {
	item := AuthorityCase{
		Alias: fixtureOpaque("sc-", index+1), SourceSHA256: fixtureSHA(index + 1),
		SourceAuthoritySHA256: fixtureSHA(1000 + index), SourceFamilyID: fixtureOpaque("family-", index+1),
		SourceBytes: 4096 + int64(index), DurationMS: 1000,
		TruthProvenanceSHA256: fixtureSHA(2000 + index), RightsSHA256: fixtureSHA(3000 + index),
		Label: label, Locale: "en-US", Slices: slices,
		Reviewers: []ReviewerAttestation{
			{ReviewerID: fixtureOpaque("reviewer-", index*2+1), Role: ReviewerPrimary, Method: ReviewerHuman, Decision: label, AttestationSHA256: fixtureSHA(4000 + index*2)},
			{ReviewerID: fixtureOpaque("reviewer-", index*2+2), Role: ReviewerPrimary, Method: ReviewerHuman, Decision: label, AttestationSHA256: fixtureSHA(4001 + index*2)},
		},
	}
	if label == LabelPositive {
		item.PositiveIntervals = []PositiveInterval{{RuleID: fixtureRuleID, StartMS: 150, EndMS: 250}}
	}
	return item
}

func (fixture *certificationFixture) rewriteAuthority(t *testing.T) {
	t.Helper()
	writeFixtureJSON(t, fixture.authorityPath, fixture.authority)
	raw, err := os.ReadFile(fixture.authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	authoritySHA := hashBytes(raw)
	manifestedAt := fixture.authority.AuthoredAt.Add(3 * time.Hour)
	fixture.manifest = ResultManifest{
		SchemaVersion: SchemaVersion, ContractVersion: ContractVersion,
		ManifestedAt: manifestedAt, AuthoritySHA256: authoritySHA,
	}
	for index, item := range fixture.authority.Cases {
		fixture.manifest.Runs = append(fixture.manifest.Runs, fixtureResultRun(fixture.authority, authoritySHA, item, index))
	}
	writeFixtureJSON(t, fixture.resultsPath, fixture.manifest)
}

func fixtureResultRun(authority Authority, authoritySHA string, item AuthorityCase, index int) ResultRun {
	createdAt := authority.AuthoredAt.Add(time.Duration(index+1) * time.Minute)
	runID := fmt.Sprintf("spoken-cert-run-%03d", index+1)
	run := fillersafety.LedgerRun{
		ID: runID, ClipHash: item.SourceSHA256, AuthoritySHA256: item.SourceAuthoritySHA256,
		SourceSHA256: item.SourceSHA256, CertificationSHA256: authoritySHA,
		PolicySHA256: authority.PolicySHA256, ProposerSHA256: authority.ProposerSHA256,
		Implementation: authority.Implementation, SourceBytes: item.SourceBytes, DurationMS: item.DurationMS,
		CreatedAt: createdAt,
	}
	candidates := []fillersafety.Candidate{}
	evidence := fillersafety.Evidence{ProposalState: fillersafety.ProposalComplete, Candidates: candidates, Audio: []fillersafety.AudioAssessment{}, Video: fillersafety.VideoNoSignal}
	if item.Label == LabelPositive {
		candidates = []fillersafety.Candidate{{ID: "candidate-one", StartMS: 100, EndMS: 300}}
		evidence = fillersafety.Evidence{
			ProposalState: fillersafety.ProposalComplete, Candidates: candidates,
			Audio: []fillersafety.AudioAssessment{{CandidateID: "candidate-one", State: fillersafety.AudioDetected, MatchedRuleIDs: []string{fixtureRuleID}}},
			Video: fillersafety.VideoNotRun,
		}
	}
	events := []fillersafety.LedgerEvent{
		{ID: runID + "-source", RunID: runID, Ordinal: 0, Kind: fillersafety.LedgerSourcePlanned,
			Source: &fillersafety.SourcePlanned{Audio: fillersafety.Span{EndMS: item.DurationMS}, Video: fillersafety.Span{EndMS: item.DurationMS}}, CreatedAt: createdAt},
		{ID: runID + "-proposal", RunID: runID, Ordinal: 1, Kind: fillersafety.LedgerProposalCompleted,
			Proposal: &fillersafety.ProposalCompleted{State: fillersafety.ProposalComplete, ProposerSHA256: authority.ProposerSHA256, Candidates: candidates}, CreatedAt: createdAt.Add(time.Nanosecond)},
	}
	route, candidateID, outcome := authority.VideoRoute, "", string(fillersafety.VideoNoSignal)
	if item.Label == LabelPositive {
		route, candidateID, outcome = authority.AudioRoute, "candidate-one", string(fillersafety.AudioDetected)
	}
	reserveID := runID + "-reserve"
	events = append(events,
		fillersafety.LedgerEvent{ID: reserveID, RunID: runID, Ordinal: 2, Kind: fillersafety.LedgerInferenceReserved,
			Reserve: &fillersafety.InferenceReserved{
				EvaluationID: runID + "-evaluation", RequestSHA256: fixtureSHA(6000 + index),
				RequestedProvider: route.RequestedProvider, RequestedModel: route.RequestedModel,
				UpstreamProvider: route.UpstreamProvider, Role: route.Role, Rung: route.Rung,
				CapabilitySHA256: route.CapabilitySHA256,
				PromptSHA256:     route.PromptSHA256, SchemaSHA256: route.SchemaSHA256, CandidateID: candidateID,
				Modalities: route.Modalities, DerivativeBytes: 512, DerivativeDurationMS: 1000,
				RequestedNanoUSD: 100, ReservedNanoUSD: 100, State: fillersafety.ReservationAccepted,
			}, CreatedAt: createdAt.Add(2 * time.Nanosecond)},
		fillersafety.LedgerEvent{ID: runID + "-settle", RunID: runID, Ordinal: 3, Kind: fillersafety.LedgerInferenceSettled,
			Settle: &fillersafety.InferenceSettled{
				ReservationEventID: reserveID, EvaluationID: runID + "-evaluation", ResponseSHA256: fixtureSHA(7000 + index),
				ResolvedProvider: route.ResolvedProvider, ResolvedModel: route.ResolvedModel, UpstreamProvider: route.UpstreamProvider,
				GenerationID: runID + "-generation", State: fillersafety.SettlementCompleted, Outcome: outcome,
				ChargedAmountUSD: "0", ChargeKnown: true,
			}, CreatedAt: createdAt.Add(3 * time.Nanosecond)},
	)
	if item.Label == LabelClean {
		events[2].Reserve.DerivativeBytes = item.SourceBytes
	}
	priorIDs := []string{events[0].ID, events[1].ID, events[2].ID, events[3].ID}
	terminal := fillersafety.LedgerEvent{
		ID: runID + "-terminal", RunID: runID, Ordinal: 4, Kind: fillersafety.LedgerTerminal,
		Terminal:  &fillersafety.TerminalResult{Evidence: evidence, Result: fillersafety.Reduce(evidence), EventIDs: priorIDs},
		CreatedAt: createdAt.Add(4 * time.Nanosecond),
	}
	events = append(events, terminal)
	digest, err := fillersafety.LedgerEventSHA256(terminal)
	if err != nil {
		panic(err)
	}
	return ResultRun{Alias: item.Alias, Run: run, Events: events, TerminalEventID: terminal.ID, TerminalSHA256: digest}
}

func (fixture *certificationFixture) rewriteManifest(t *testing.T) {
	t.Helper()
	writeFixtureJSON(t, fixture.resultsPath, fixture.manifest)
}

func refreshTerminal(t *testing.T, run *ResultRun) {
	t.Helper()
	terminal := run.Events[len(run.Events)-1]
	digest, err := fillersafety.LedgerEventSHA256(terminal)
	if err != nil {
		t.Fatal(err)
	}
	run.TerminalEventID, run.TerminalSHA256 = terminal.ID, digest
}

func writeFixtureJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixtureSHA(seed int) string { return fmt.Sprintf("%064x", seed) }

func fixtureOpaque(prefix string, seed int) string { return prefix + fmt.Sprintf("%024x", seed) }

func (fixture *certificationFixture) config() Config {
	return Config{AuthorityPath: fixture.authorityPath, ResultsPath: fixture.resultsPath, ScoredAt: fixture.scoredAt, OutputPath: fixture.outputPath}
}

func assertNoSensitiveVocabulary(t *testing.T, value string) {
	t.Helper()
	for _, forbidden := range []string{"transcript", "phrase", "source path", "private-policy"} {
		if strings.Contains(strings.ToLower(value), forbidden) {
			t.Fatalf("artifact contains forbidden vocabulary %q", forbidden)
		}
	}
}
