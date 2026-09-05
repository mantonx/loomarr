package fillerreview

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestLoadTemporalStructureHoldoutPriorAcceptsPublishedAdjudication(t *testing.T) {
	fixture := newTemporalStructureAnchorAdjudicationFixture(t)
	path := filepath.Join(t.TempDir(), "authority.json")
	if _, err := PublishTemporalStructureAnchorAdjudication(fixture.config(path)); err != nil {
		t.Fatal(err)
	}
	prior, err := loadTemporalStructureHoldoutPrior(TemporalStructureHoldoutConfig{
		PriorAdjudicationPaths: []string{path}, PlannedAt: fixture.adjudicatedAt.Add(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if prior.planKind != TemporalStructureHoldoutPlanReplacement || len(prior.inputs) != 1 || len(prior.exposure.SourceSHA256) != 54 || len(prior.exposure.FamilyIDs) != 12 || len(prior.exposure.ProgrammeProvenance) != 6 {
		t.Fatalf("loaded published prior = %+v", prior)
	}
}

func TestBuildTemporalStructureReplacementHoldoutCarriesCumulativeExposure(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	prior := emptyTemporalStructureHoldoutExposure()
	prior.SourceSHA256 = []string{strings.Repeat("e", 64)}
	prior.FamilyIDs = []string{"prior-family"}
	prior.ProgrammeProvenance = []TemporalStructureHoldoutProgrammeProvenance{{Authority: "prior-authority", Reference: "prior-reference"}}
	priorPath := writeTemporalStructurePriorAdjudicationFixture(t, fixture, prior)
	config := fixture.config(filepath.Join(t.TempDir(), "replacement"))
	config.Genesis = false
	config.PriorAdjudicationPaths = []string{priorPath}
	if _, err := BuildTemporalStructureHoldoutPlan(config); err != nil {
		t.Fatal(err)
	}
	receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, filepath.Join(config.OutputDir, "receipt.json"))
	if receipt.PlanKind != TemporalStructureHoldoutPlanReplacement || !equalTemporalStructureHoldoutExposure(receipt.PriorExposure, prior) {
		t.Fatalf("replacement lineage = kind %q prior %+v", receipt.PlanKind, receipt.PriorExposure)
	}
	if len(receipt.FutureTrainingExclusion.SourceSHA256) != 19 || len(receipt.FutureTrainingExclusion.FamilyIDs) != 13 || len(receipt.FutureTrainingExclusion.ProgrammeProvenance) != 7 {
		t.Fatalf("cumulative exposure = %+v", receipt.FutureTrainingExclusion)
	}
	foundPriorInput := false
	for _, input := range receipt.Inputs {
		foundPriorInput = foundPriorInput || strings.HasPrefix(input.Name, "prior_adjudication:")
	}
	if !foundPriorInput {
		t.Fatal("replacement receipt omitted its prior adjudication digest")
	}
}

func TestBuildTemporalStructureReplacementHoldoutRejectsPriorRequestLeakage(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	genesisRoot := filepath.Join(t.TempDir(), "genesis")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(genesisRoot)); err != nil {
		t.Fatal(err)
	}
	receipt := readStrictTestJSON[TemporalStructureHoldoutReceipt](t, filepath.Join(genesisRoot, "receipt.json"))
	authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, filepath.Join(genesisRoot, "authoring.json"))
	sources := make(map[string]TemporalStructureChallengeSource, len(authoring.Sources))
	for _, source := range authoring.Sources {
		sources[source.ID] = source
	}
	anchor := receipt.SelectedAnchors[0]
	programme := receipt.FutureTrainingExclusion.ProgrammeProvenance[0]
	tests := []struct {
		name     string
		exposure TemporalStructureHoldoutTrainingExclusion
		want     string
	}{
		{
			name: "rendered or source bytes",
			exposure: TemporalStructureHoldoutTrainingExclusion{
				Split: "holdout", SourceSHA256: []string{sources[anchor.SourceID].SHA256}, FamilyIDs: []string{"prior-family"},
				ProgrammeProvenance: []TemporalStructureHoldoutProgrammeProvenance{},
			},
			want: "insufficient eligible",
		},
		{
			name: "duplicate family",
			exposure: TemporalStructureHoldoutTrainingExclusion{
				Split: "holdout", SourceSHA256: []string{strings.Repeat("e", 64)}, FamilyIDs: []string{anchor.FamilyID},
				ProgrammeProvenance: []TemporalStructureHoldoutProgrammeProvenance{},
			},
			want: "insufficient eligible",
		},
		{
			name: "programme provenance",
			exposure: TemporalStructureHoldoutTrainingExclusion{
				Split: "holdout", SourceSHA256: []string{strings.Repeat("e", 64)}, FamilyIDs: []string{"prior-family"},
				ProgrammeProvenance: []TemporalStructureHoldoutProgrammeProvenance{programme},
			},
			want: "needs six programme parents",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			priorPath := writeTemporalStructurePriorAdjudicationFixture(t, fixture, test.exposure)
			config := fixture.config(filepath.Join(t.TempDir(), "replacement"))
			config.Genesis = false
			config.PriorAdjudicationPaths = []string{priorPath}
			_, err := BuildTemporalStructureHoldoutPlan(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func writeTemporalStructurePriorAdjudicationFixture(t *testing.T, fixture temporalStructureHoldoutFixture, exposure TemporalStructureHoldoutTrainingExclusion) string {
	t.Helper()
	inputs := []TemporalStructureHoldoutInput{
		{Name: "comparison", SHA256: strings.Repeat("1", 64)},
		{Name: "plan_authoring", SHA256: strings.Repeat("2", 64)},
		{Name: "plan_receipt", SHA256: strings.Repeat("3", 64)},
		{Name: "private_authority", SHA256: strings.Repeat("4", 64)},
		{Name: "public_manifest", SHA256: strings.Repeat("5", 64)},
		{Name: "submission", SHA256: strings.Repeat("6", 64)},
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })
	authority := TemporalStructureAnchorAdjudicationAuthority{
		SchemaVersion:   TemporalStructureAnchorAdjudicationSchemaVersion,
		ContractVersion: TemporalStructureAnchorAdjudicationAuthorityContract,
		ChallengeID:     "prior-challenge", AdjudicatedAt: fixture.plannedAt.Add(-1), ReviewerID: "prior-reviewer",
		Inputs: inputs, EvidenceManifestSHA256: strings.Repeat("7", 64), HumanAssessmentSHA256: strings.Repeat("8", 64),
		PlanReceiptSHA256: strings.Repeat("9", 64), ComparisonSHA256: strings.Repeat("a", 64), PriorExposure: exposure,
		Cases: []TemporalStructureAnchorAdjudicationCase{{
			Alias: "case-prior", EvidenceAlias: "evidence-prior", CaseID: "source-case-prior", SourceID: "source-prior",
			SourceSHA256: exposure.SourceSHA256[0], FamilyID: exposure.FamilyIDs[0], DurationMS: 1_000,
			Coverage: TemporalStructureAnchorReviewComplete,
			Observations: TemporalStructureAnchorObservations{
				Opening: "The opening was reviewed.", InternalJoins: []TemporalStructureAnchorJoinObservation{}, Closing: "The closing was reviewed.",
			},
			Disposition:  TemporalStructureAnchorConfirmed,
			Original:     TemporalStructureTruthLabel{Unit: "standalone", Role: "commercial"},
			Adjudicated:  TemporalStructureTruthLabel{Unit: "standalone", Role: "commercial"},
			DecisiveAtMS: []int64{100}, Rationale: "Complete audiovisual fixture review confirms the original bounded item.",
		}},
		ChallengeDisposition: TemporalStructureBurnedDiagnosticOnly,
	}
	return writeTemporalHumanJSON(t, t.TempDir(), "prior-adjudication.json", authority)
}
