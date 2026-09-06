package fillersafetycorpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssembleReviewDraftRejectsCrossCohortFamilyCollision(t *testing.T) {
	t.Parallel()
	fixture := newAssemblyFixture(t)
	fixture.cohorts[1].Cases[0].SourceFamily = fixture.cohorts[0].Cases[0].SourceFamily
	rewriteAssemblyCohort(t, fixture, 1)

	err := runAssemblyFixture(t, fixture)
	if err == nil || !strings.Contains(err.Error(), "repeats a source family") {
		t.Fatalf("err=%v", err)
	}
	assertAssemblyNotPublished(t, fixture)
}

func TestAssembleReviewDraftRejectsCrossCohortSourceCollision(t *testing.T) {
	t.Parallel()
	fixture := newAssemblyFixture(t)
	positive := fixture.cohorts[0].Cases[0]
	target := &fixture.cohorts[1].Cases[0]
	positivePath := filepath.Join(fixture.root, fixture.plan.Cohorts[0].SourceRoot, filepath.FromSlash(positive.SourcePath))
	targetPath := filepath.Join(fixture.root, fixture.plan.Cohorts[1].SourceRoot, filepath.FromSlash(target.SourcePath))
	raw, err := os.ReadFile(positivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	target.SourceAuthority.SourceSHA256 = positive.SourceAuthority.SourceSHA256
	target.SourceAuthority.SourceBytes = positive.SourceAuthority.SourceBytes
	rewriteAssemblyCohort(t, fixture, 1)

	err = runAssemblyFixture(t, fixture)
	if err == nil || !strings.Contains(err.Error(), "repeats source content") {
		t.Fatalf("err=%v", err)
	}
	assertAssemblyNotPublished(t, fixture)
}

func TestAssembleReviewDraftRejectsCurrentSourceDrift(t *testing.T) {
	t.Parallel()
	fixture := newAssemblyFixture(t)
	item := fixture.cohorts[0].Cases[0]
	path := filepath.Join(fixture.root, fixture.plan.Cohorts[0].SourceRoot, filepath.FromSlash(item.SourcePath))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	err = runAssemblyFixture(t, fixture)
	if err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("err=%v", err)
	}
	assertAssemblyNotPublished(t, fixture)
}

func TestAssembleReviewDraftRejectsCurrentProvenanceDrift(t *testing.T) {
	t.Parallel()
	fixture := newAssemblyFixture(t)
	item := fixture.cohorts[0].Cases[0]
	path := filepath.Join(fixture.root, fixture.plan.Cohorts[0].SourceRoot, filepath.FromSlash(item.TruthProvenancePath))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	err = runAssemblyFixture(t, fixture)
	if err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("err=%v", err)
	}
	assertAssemblyNotPublished(t, fixture)
}

func TestAssembleReviewDraftRejectsIncompleteSliceCoverage(t *testing.T) {
	t.Parallel()
	fixture := newAssemblyFixture(t)
	last := len(fixture.cohorts[2].Cases) - 1
	fixture.cohorts[2].Cases[last].Slices = []string{"music_only"}
	rewriteAssemblyCohort(t, fixture, 2)

	err := runAssemblyFixture(t, fixture)
	if err == nil || !strings.Contains(err.Error(), "complete declared slice coverage") {
		t.Fatalf("err=%v", err)
	}
	assertAssemblyNotPublished(t, fixture)
}

func TestAssembleReviewDraftRejectsPolicyDrift(t *testing.T) {
	t.Parallel()
	fixture := newAssemblyFixture(t)
	fixture.cohorts[0].Cases[0].SourceAuthority.PolicySHA256 = fixtureSHA(9999)
	rewriteAssemblyCohort(t, fixture, 0)

	err := runAssemblyFixture(t, fixture)
	if err == nil || !strings.Contains(err.Error(), "policy or implementation") {
		t.Fatalf("err=%v", err)
	}
	assertAssemblyNotPublished(t, fixture)
}

func TestAssembleReviewDraftRejectsResourceCeilingsWithoutPartialPublication(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*AssemblyPlan)
	}{
		{name: "input bytes", mutate: func(plan *AssemblyPlan) { plan.MaximumInputBytes = 1 }},
		{name: "output bytes", mutate: func(plan *AssemblyPlan) { plan.MaximumOutputBytes = 1 }},
		{name: "wall time", mutate: func(plan *AssemblyPlan) { plan.MaximumWallTimeMS = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newAssemblyFixture(t)
			test.mutate(&fixture.plan)
			rewriteAssemblyPlan(t, fixture)
			if err := runAssemblyFixture(t, fixture); err == nil {
				t.Fatal("expected resource ceiling rejection")
			}
			assertAssemblyNotPublished(t, fixture)
		})
	}
}

func runAssemblyFixture(t *testing.T, fixture *assemblyFixture) error {
	t.Helper()
	_, err := AssembleReviewDraft(t.Context(), ReviewDraftConfig{
		PlanPath: fixture.planPath, InputRoot: fixture.root, OutputDirectory: fixture.output,
	})
	return err
}

func assertAssemblyNotPublished(t *testing.T, fixture *assemblyFixture) {
	t.Helper()
	if _, err := os.Stat(fixture.output); !os.IsNotExist(err) {
		t.Fatalf("assembly output exists after rejection: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(fixture.root, ".filler-safety-stage-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("partial stages=%v err=%v", matches, err)
	}
}
