package fillersafetycorpus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareKnownScriptPublishesOpaquePositiveFamilies(t *testing.T) {
	t.Parallel()
	fixture := newKnownScriptFixture(t)

	result, err := prepareKnownScript(t.Context(), fixture.config, fixture.wrapper)
	if err != nil {
		t.Fatal(err)
	}
	cohort := readPreparedFixture[PreparedCohort](t, filepath.Join(fixture.output, "cohort.json"))
	owner := readPreparedFixture[KnownScriptOwnerMap](t, filepath.Join(fixture.output, "owner-map.json"))
	if result.Speakers != 59 || len(cohort.Cases) != 59 || len(owner.Entries) != 59 ||
		fixture.media.wraps.Calls() != 59 || fixture.media.identities.Calls() != 2 ||
		result.CohortSHA256 != owner.CohortSHA256 {
		t.Fatalf("result=%+v cases=%d owner=%d wrapper=%d/%d", result, len(cohort.Cases), len(owner.Entries), fixture.media.wraps.Calls(), fixture.media.identities.Calls())
	}
	families := map[string]struct{}{}
	for _, item := range cohort.Cases {
		families[item.SourceFamily] = struct{}{}
		if item.Claim != PreparedCohortKindPositiveCandidate || len(item.PositiveIntervals) == 0 ||
			!item.SourceAuthority.HasAudio || !item.SourceAuthority.HasVideo ||
			!validSHA256(item.TruthProvenanceSHA256) || !validSHA256(item.RightsSHA256) {
			t.Fatalf("invalid prepared case: %+v", item)
		}
	}
	if len(families) != 59 {
		t.Fatalf("families=%d", len(families))
	}
	cohortRaw, err := os.ReadFile(filepath.Join(fixture.output, "cohort.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"participant-", "private known script", "participants/", "session-", "take-"} {
		if strings.Contains(string(cohortRaw), private) {
			t.Fatalf("cohort leaked private input %q", private)
		}
	}
	actualBytes := int64(0)
	if err := filepath.Walk(fixture.output, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Mode().Perm() != 0o700 {
				return fmt.Errorf("directory has mode %o", info.Mode().Perm())
			}
			return nil
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || filepath.Base(path) == ".verified-audio" {
			return fmt.Errorf("file has type/mode %v", info.Mode())
		}
		actualBytes += info.Size()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if actualBytes != result.OutputBytes {
		t.Fatalf("actual output bytes=%d reported=%d", actualBytes, result.OutputBytes)
	}
}

func TestPrepareKnownScriptIsByteReproducible(t *testing.T) {
	t.Parallel()
	fixture := newKnownScriptFixture(t)
	first, err := prepareKnownScript(t.Context(), fixture.config, fixture.wrapper)
	if err != nil {
		t.Fatal(err)
	}
	secondConfig := fixture.config
	secondConfig.OutputDirectory = filepath.Join(fixture.parent, "prepared-second")
	second, err := prepareKnownScript(t.Context(), secondConfig, (&wrapperFixture{recipe: KnownScriptPackagingRecipe}).wrapperForFixture())
	if err != nil {
		t.Fatal(err)
	}
	firstCohort, firstErr := os.ReadFile(filepath.Join(fixture.output, "cohort.json"))
	secondCohort, secondErr := os.ReadFile(filepath.Join(secondConfig.OutputDirectory, "cohort.json"))
	firstMap, firstMapErr := os.ReadFile(filepath.Join(fixture.output, "owner-map.json"))
	secondMap, secondMapErr := os.ReadFile(filepath.Join(secondConfig.OutputDirectory, "owner-map.json"))
	if firstErr != nil || secondErr != nil || firstMapErr != nil || secondMapErr != nil ||
		first.CohortSHA256 != second.CohortSHA256 || first.OwnerMapSHA256 != second.OwnerMapSHA256 ||
		string(firstCohort) != string(secondCohort) || string(firstMap) != string(secondMap) {
		t.Fatalf("known-script output is not reproducible: first=%+v second=%+v", first, second)
	}
}
