package fillersafetycorpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func TestPrepareVCTKPublishesOneOpaqueCasePerSpeaker(t *testing.T) {
	t.Parallel()
	fixture := newVCTKFixture(t)

	result, err := prepareVCTK(t.Context(), fixture.config, fixture.wrapper)
	if err != nil {
		t.Fatal(err)
	}
	cohort := readPreparedFixture[PreparedCohort](t, filepath.Join(fixture.output, "cohort.json"))
	owner := readPreparedFixture[VCTKOwnerMap](t, filepath.Join(fixture.output, "owner-map.json"))
	if result.Speakers != 100 || len(cohort.Cases) != 100 || len(owner.Entries) != 100 ||
		result.CohortSHA256 != owner.CohortSHA256 || fixture.media.wraps.Calls() != 100 || fixture.media.identities.Calls() != 2 {
		t.Fatalf("result=%+v cases=%d owner=%d wrapper=%d/%d", result, len(cohort.Cases), len(owner.Entries), fixture.media.wraps.Calls(), fixture.media.identities.Calls())
	}
	families := map[string]struct{}{}
	for _, item := range cohort.Cases {
		families[item.SourceFamily] = struct{}{}
		if item.Claim != PreparedCohortKindCleanCandidate || item.SourceAuthority.HasAudio != true || item.SourceAuthority.HasVideo != true ||
			!validSHA256(item.TruthProvenanceSHA256) || !validSHA256(item.RightsSHA256) {
			t.Fatalf("invalid prepared case: %+v", item)
		}
	}
	if len(families) != 100 {
		t.Fatalf("families=%d", len(families))
	}
	for _, item := range cohort.Cases {
		draftCase := fillersafetycert.AuthorityDraftCase{
			CaseID: item.CaseID, SourcePath: item.SourcePath, SourceAuthority: item.SourceAuthority,
			SourceFamily: item.SourceFamily, TruthProvenancePath: item.TruthProvenancePath,
			TruthProvenanceSHA256: item.TruthProvenanceSHA256, RightsPath: item.RightsPath,
			RightsSHA256: item.RightsSHA256, Label: fillersafetycert.LabelClean,
			Locale: item.Locale, Slices: item.Slices,
		}
		if draftCase.CaseID == "" || draftCase.SourceAuthority.SourceID != draftCase.CaseID ||
			draftCase.Label != fillersafetycert.LabelClean {
			t.Fatalf("prepared case cannot map to authority draft: %+v", draftCase)
		}
	}
	raw, err := os.ReadFile(filepath.Join(fixture.output, "cohort.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"p200", "members/", "harmless transcript"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("cohort leaked private input %q", private)
		}
	}
	info, err := os.Stat(fixture.output)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("output info=%v err=%v", info, err)
	}
	actualBytes := int64(0)
	if err := filepath.Walk(fixture.output, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Mode().Perm() != 0o700 {
				return fmt.Errorf("directory %s has mode %o", path, info.Mode().Perm())
			}
			return nil
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || filepath.Base(path) == ".verified-audio" {
			return fmt.Errorf("file %s has type/mode %v", path, info.Mode())
		}
		actualBytes += info.Size()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if actualBytes != result.OutputBytes {
		t.Fatalf("actual output bytes=%d reported=%d", actualBytes, result.OutputBytes)
	}
	authorityRaw, err := os.ReadFile(fixture.authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	expectedInputBytes := int64(len(authorityRaw) + len("0123456789abcdef0123456789abcdef"))
	unique := map[string]FileAuthority{}
	for _, file := range []FileAuthority{fixture.authority.License, fixture.authority.Readme, fixture.authority.RightsReviewEvidence} {
		unique[file.Path] = file
	}
	for _, member := range fixture.authority.Members {
		for _, file := range []FileAuthority{member.Audio, member.Transcript, member.ScreeningEvidence} {
			unique[file.Path] = file
		}
	}
	for _, file := range unique {
		expectedInputBytes += file.Bytes
	}
	if result.InputBytes != expectedInputBytes {
		t.Fatalf("input bytes=%d want unique verified bytes=%d", result.InputBytes, expectedInputBytes)
	}
}

func TestPrepareVCTKIsByteReproducible(t *testing.T) {
	t.Parallel()
	fixture := newVCTKFixture(t)
	secondOutput := filepath.Join(fixture.root, "prepared-second")

	first, err := prepareVCTK(t.Context(), fixture.config, fixture.wrapper)
	if err != nil {
		t.Fatal(err)
	}
	secondConfig := fixture.config
	secondConfig.OutputDirectory = secondOutput
	secondWrapper := (&wrapperFixture{}).wrapperForFixture()
	second, err := prepareVCTK(t.Context(), secondConfig, secondWrapper)
	if err != nil {
		t.Fatal(err)
	}
	firstCohort, firstErr := os.ReadFile(filepath.Join(fixture.output, "cohort.json"))
	secondCohort, secondErr := os.ReadFile(filepath.Join(secondOutput, "cohort.json"))
	firstMap, firstMapErr := os.ReadFile(filepath.Join(fixture.output, "owner-map.json"))
	secondMap, secondMapErr := os.ReadFile(filepath.Join(secondOutput, "owner-map.json"))
	if firstErr != nil || secondErr != nil || firstMapErr != nil || secondMapErr != nil ||
		first.CohortSHA256 != second.CohortSHA256 || first.OwnerMapSHA256 != second.OwnerMapSHA256 ||
		string(firstCohort) != string(secondCohort) || string(firstMap) != string(secondMap) {
		t.Fatalf("results differ: first=%+v second=%+v errs=%v/%v/%v/%v", first, second, firstErr, secondErr, firstMapErr, secondMapErr)
	}
}

func TestPrepareVCTKRejectsSourceDriftWithoutOutput(t *testing.T) {
	t.Parallel()
	fixture := newVCTKFixture(t)
	path := filepath.Join(fixture.root, filepath.FromSlash(fixture.authority.Members[0].Audio.Path))
	if err := os.WriteFile(path, []byte("changed source"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := prepareVCTK(t.Context(), fixture.config, fixture.wrapper); err == nil || !strings.Contains(err.Error(), "audio bytes") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(fixture.output); !os.IsNotExist(err) {
		t.Fatalf("output exists after failure: %v", err)
	}
}

func TestPrepareVCTKRejectsIncompleteRightsBeforeWrapping(t *testing.T) {
	t.Parallel()
	fixture := newVCTKFixture(t)
	fixture.authority.RightsContract.Grants.ProviderTransfer = false
	writePrivateJSONFixture(t, fixture.authorityPath, fixture.authority)

	if _, err := prepareVCTK(t.Context(), fixture.config, fixture.wrapper); err == nil || !strings.Contains(err.Error(), "provider_transfer") {
		t.Fatalf("err=%v", err)
	}
	if fixture.media.wraps.Calls() != 0 {
		t.Fatalf("wrapper calls=%d", fixture.media.wraps.Calls())
	}
}

func TestPrepareVCTKRemovesPartialStageOnWrapperFailure(t *testing.T) {
	t.Parallel()
	fixture := newVCTKFixture(t)
	fixture.media.failAt = 3

	if _, err := prepareVCTK(t.Context(), fixture.config, fixture.wrapper); err == nil || !strings.Contains(err.Error(), "source 3") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(fixture.output); !os.IsNotExist(err) {
		t.Fatalf("output exists after failure: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(fixture.root, ".filler-safety-stage-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("partial stages=%v err=%v", matches, err)
	}
}

func TestPrepareVCTKRejectsToolIdentityDrift(t *testing.T) {
	t.Parallel()
	fixture := newVCTKFixture(t)
	fixture.media.driftIdentity = true

	if _, err := prepareVCTK(t.Context(), fixture.config, fixture.wrapper); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(fixture.output); !os.IsNotExist(err) {
		t.Fatalf("output exists after failure: %v", err)
	}
}

func TestPrepareVCTKRejectsExcludedOrInsufficientSpeakerPopulation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*vctkFixture)
	}{
		{name: "excluded p315", mutate: func(fixture *vctkFixture) {
			fixture.authority.Members[0].SpeakerID = "p315"
			fixture.authority.Members[0].UtteranceID = "p315_001"
		}},
		{name: "ninety nine families", mutate: func(fixture *vctkFixture) {
			fixture.authority.Members = fixture.authority.Members[1:]
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newVCTKFixture(t)
			test.mutate(fixture)
			writePrivateJSONFixture(t, fixture.authorityPath, fixture.authority)
			if _, err := prepareVCTK(t.Context(), fixture.config, fixture.wrapper); err == nil {
				t.Fatal("expected release rejection")
			}
			if fixture.media.wraps.Calls() != 0 {
				t.Fatalf("wrapper calls=%d", fixture.media.wraps.Calls())
			}
		})
	}
}

func TestPrepareVCTKRejectsConflictingSharedFileAuthority(t *testing.T) {
	t.Parallel()
	fixture := newVCTKFixture(t)
	fixture.authority.Members[len(fixture.authority.Members)-1].Transcript.SHA256 = fixtureSHA(9999)
	writePrivateJSONFixture(t, fixture.authorityPath, fixture.authority)

	if _, err := prepareVCTK(t.Context(), fixture.config, fixture.wrapper); err == nil || !strings.Contains(err.Error(), "conflicting authorities") {
		t.Fatalf("err=%v", err)
	}
	if fixture.media.wraps.Calls() != 0 {
		t.Fatalf("wrapper calls=%d", fixture.media.wraps.Calls())
	}
}

func TestPrepareVCTKEnforcesResourceCeilingsWithoutPublication(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*PrepareVCTKConfig)
	}{
		{name: "input bytes", mutate: func(config *PrepareVCTKConfig) { config.MaximumInputBytes = 1 }},
		{name: "output bytes", mutate: func(config *PrepareVCTKConfig) { config.MaximumOutputBytes = 1 }},
		{name: "wall time", mutate: func(config *PrepareVCTKConfig) { config.MaximumWallTime = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newVCTKFixture(t)
			test.mutate(&fixture.config)
			if _, err := prepareVCTK(t.Context(), fixture.config, fixture.wrapper); err == nil {
				t.Fatal("expected resource ceiling rejection")
			}
			if _, err := os.Stat(fixture.output); !os.IsNotExist(err) {
				t.Fatalf("output exists after failure: %v", err)
			}
		})
	}
}

func readPreparedFixture[T any](t *testing.T, path string) T {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
