package fillersafetycorpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func TestCertificationEvidenceValidatorBindsVCTKMemberAndWrappedSource(t *testing.T) {
	t.Parallel()
	fixture := newVCTKFixture(t)
	if _, err := prepareVCTK(t.Context(), fixture.config, fixture.wrapper); err != nil {
		t.Fatal(err)
	}
	cohort := readPreparedFixture[PreparedCohort](t, filepath.Join(fixture.output, "cohort.json"))
	first, second := cohort.Cases[0], cohort.Cases[1]
	validator := NewCertificationEvidenceValidator()
	rightsRaw := readCertificationEvidence(t, fixture.output, first.RightsPath)
	firstProvenance := readCertificationEvidence(t, fixture.output, first.TruthProvenancePath)
	at := fixture.config.PreparedAt.Add(time.Hour)

	if err := validator.Validate(rightsRaw, firstProvenance, certificationDraftCase(first), at); err != nil {
		t.Fatalf("exact VCTK evidence: %v", err)
	}
	secondProvenance := readCertificationEvidence(t, fixture.output, second.TruthProvenancePath)
	if err := validator.Validate(rightsRaw, secondProvenance, certificationDraftCase(second), at); err != nil {
		t.Fatalf("second exact VCTK evidence: %v", err)
	}
	if len(validator.vctk) != 1 || len(validator.vctkCurrent) != 1 {
		t.Fatalf("decoded/current shared VCTK authorities=%d/%d", len(validator.vctk), len(validator.vctkCurrent))
	}
	if err := validator.Validate(rightsRaw, secondProvenance, certificationDraftCase(first), at); err == nil {
		t.Fatal("validator accepted another valid member's provenance for the case source")
	}
	wrongSource := certificationDraftCase(first)
	wrongSource.SourceAuthority.SourceSHA256 = fixtureSHA(98_001)
	if err := validator.Validate(rightsRaw, firstProvenance, wrongSource, at); err == nil {
		t.Fatal("validator accepted valid VCTK provenance for different source bytes")
	}
	authority, err := decodeKnownScriptJSON[VCTKReleaseAuthority](rightsRaw)
	if err != nil {
		t.Fatal(err)
	}
	authority.RightsContract.Term = fillercorpus.RightsTermExpires
	authority.RightsContract.ExpiresAt = &at
	expiredRaw, err := marshalPrivateJSON(authority)
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(expiredRaw, firstProvenance, certificationDraftCase(first), at); err == nil {
		t.Fatal("validator accepted expired VCTK evaluation rights")
	}
}

func TestCertificationEvidenceValidatorBindsKnownScriptParticipantAndWrappedSource(t *testing.T) {
	t.Parallel()
	fixture := newKnownScriptFixture(t)
	if _, err := prepareKnownScript(t.Context(), fixture.config, fixture.wrapper); err != nil {
		t.Fatal(err)
	}
	cohort := readPreparedFixture[PreparedCohort](t, filepath.Join(fixture.output, "cohort.json"))
	first, second := cohort.Cases[0], cohort.Cases[1]
	validator := NewCertificationEvidenceValidator()
	firstRights := readCertificationEvidence(t, fixture.output, first.RightsPath)
	firstProvenance := readCertificationEvidence(t, fixture.output, first.TruthProvenancePath)
	at := fixture.config.PreparedAt.Add(time.Hour)

	if err := validator.Validate(firstRights, firstProvenance, certificationDraftCase(first), at); err != nil {
		t.Fatalf("exact known-script evidence: %v", err)
	}
	secondRights := readCertificationEvidence(t, fixture.output, second.RightsPath)
	if err := validator.Validate(secondRights, firstProvenance, certificationDraftCase(first), at); err == nil {
		t.Fatal("validator accepted another participant's current rights for the case source")
	}
	secondProvenance := readCertificationEvidence(t, fixture.output, second.TruthProvenancePath)
	if err := validator.Validate(firstRights, secondProvenance, certificationDraftCase(first), at); err == nil {
		t.Fatal("validator accepted another participant's valid provenance for the case source")
	}
	rights, err := decodeKnownScriptJSON[knownScriptRights](firstRights)
	if err != nil {
		t.Fatal(err)
	}
	rights.Consent.WithdrawnAt = &at
	withdrawnRaw, err := marshalPrivateJSON(rights)
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(withdrawnRaw, firstProvenance, certificationDraftCase(first), at); err == nil {
		t.Fatal("validator accepted withdrawn participant consent")
	}
	rights.Consent.WithdrawnAt = nil
	rights.Consent.ExpiresAt = &at
	expiredRaw, err := marshalPrivateJSON(rights)
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(expiredRaw, firstProvenance, certificationDraftCase(first), at); err == nil {
		t.Fatal("validator accepted expired participant consent")
	}
	nonCanonical, err := json.Marshal(rights)
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(nonCanonical, firstProvenance, certificationDraftCase(first), at); err == nil {
		t.Fatal("validator accepted a noncanonical rights envelope")
	}
	if err := validator.Validate([]byte(`{"contractVersion":"unsupported-rights-v1"}`), firstProvenance, certificationDraftCase(first), at); err == nil {
		t.Fatal("validator accepted an unsupported rights contract")
	}
}

func certificationDraftCase(item PreparedCohortCase) fillersafetycert.AuthorityDraftCase {
	label := fillersafetycert.LabelClean
	if item.Claim == PreparedCohortKindPositiveCandidate {
		label = fillersafetycert.LabelPositive
	}
	result := fillersafetycert.AuthorityDraftCase{
		CaseID: item.CaseID, SourcePath: item.SourcePath, SourceAuthority: item.SourceAuthority,
		SourceFamily: item.SourceFamily, TruthProvenancePath: item.TruthProvenancePath,
		TruthProvenanceSHA256: item.TruthProvenanceSHA256, RightsPath: item.RightsPath,
		RightsSHA256: item.RightsSHA256, Label: label, Locale: item.Locale, Slices: item.Slices,
	}
	for _, interval := range item.PositiveIntervals {
		result.PositiveIntervals = append(result.PositiveIntervals, fillersafetycert.PositiveInterval{
			RuleID: interval.RuleID, StartMS: interval.StartMS, EndMS: interval.EndMS,
		})
	}
	return result
}

func readCertificationEvidence(t *testing.T, root, relative string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
