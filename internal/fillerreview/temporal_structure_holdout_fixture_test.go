package fillerreview

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerreference"
	"github.com/loomarr/loomarr/internal/mediatools"
)

type temporalStructureHoldoutFixture struct {
	temporalHumanReviewFixture
	humanAssessment  string
	humanAttestation string
	quality          string
	suitability      string
	referenceAudit   string
	family           string
	transition       string
	inventory        string
	plannedAt        time.Time
}

func newTemporalStructureHoldoutFixture(t *testing.T) temporalStructureHoldoutFixture {
	t.Helper()
	base := newTemporalHumanReviewFixture(t)
	manifest := readStrictTestJSON[TemporalTruthEvidenceManifest](t, base.manifest)
	privateMap := readStrictTestJSON[TemporalTruthEvidencePrivateMap](t, base.privateMap)
	for index := range manifest.Cases {
		durationMS := int64(index+1) * 10_000
		raw := []byte("distinct bounded source " + manifest.Cases[index].Alias)
		name := "bounded-" + manifest.Cases[index].Alias + ".mp4"
		path := filepath.Join(filepath.Dir(base.manifest), name)
		if err := os.WriteFile(path, raw, 0o640); err != nil {
			t.Fatal(err)
		}
		manifest.Cases[index].DurationMS = durationMS
		manifest.Cases[index].Plan.SourceEndMS = durationMS
		manifest.Cases[index].Plan.EvidenceEndMS = durationMS
		manifest.Cases[index].Video.Path = name
		manifest.Cases[index].Video.SHA256 = hashBytes(raw)
		manifest.Cases[index].Video.Bytes = int64(len(raw))
		manifest.Cases[index].Video.DurationMS = durationMS
	}
	writeTemporalHumanJSON(t, filepath.Dir(base.manifest), filepath.Base(base.manifest), manifest)
	evidenceSHA, err := hashFile(base.manifest)
	if err != nil {
		t.Fatal(err)
	}
	roles := []fillereval.TemporalRole{
		fillereval.TemporalRoleBumper, fillereval.TemporalRoleBumper,
		fillereval.TemporalRoleCommercial, fillereval.TemporalRoleCommercial, fillereval.TemporalRoleCommercial,
		fillereval.TemporalRolePromo, fillereval.TemporalRolePromo,
		fillereval.TemporalRolePSA, fillereval.TemporalRolePSA,
		fillereval.TemporalRoleTrailer, fillereval.TemporalRoleTrailer, fillereval.TemporalRoleTrailer,
	}
	completedAt := base.preparedAt.Add(time.Hour)
	set := TemporalHumanAssessmentSet{
		SchemaVersion: TemporalHumanReviewSchemaVersion, ContractVersion: TemporalHumanReviewContractVersion,
		BatchID: "holdout-human", ReviewerID: "reviewer", EvidenceManifestSHA256: evidenceSHA,
		SelectionSHA256: manifest.SelectionSHA256, PackageSHA256: strings.Repeat("1", 64), SubmissionSHA256: strings.Repeat("2", 64),
		PreparedAt: base.preparedAt, CompletedAt: completedAt,
	}
	for index, item := range manifest.Cases {
		assessment := TemporalHumanReviewAssessment{EvidenceAlias: item.Alias, Unit: fillereval.UnitUnusable, DecisiveAtMS: 0}
		if index < len(roles) {
			role := roles[index]
			assessment.Unit, assessment.Role = fillereval.UnitStandalone, &role
		}
		set.Assessments = append(set.Assessments, assessment)
	}
	humanAssessment := writeTemporalHumanJSON(t, base.root, "human-assessment.json", set)
	humanSHA, err := hashFile(humanAssessment)
	if err != nil {
		t.Fatal(err)
	}
	attestation := TemporalHumanReviewAttestation{
		SchemaVersion: TemporalHumanReviewSchemaVersion, ContractVersion: TemporalHumanReviewContractVersion,
		BatchID: set.BatchID, ReviewerID: set.ReviewerID, LockedAt: completedAt.Add(time.Hour),
		PackageSHA256: set.PackageSHA256, MapSHA256: strings.Repeat("3", 64), SubmissionSHA256: set.SubmissionSHA256,
		AssessmentSetSHA256: humanSHA,
	}
	attestation.AttestationSHA256 = temporalHumanAttestationSHA256(attestation)
	humanAttestation := writeTemporalHumanJSON(t, base.root, "human-attestation.json", attestation)
	attestationFileSHA, err := hashFile(humanAttestation)
	if err != nil {
		t.Fatal(err)
	}
	tool := TemporalTruthToolIdentity{Path: "/tool", Version: "v1", BinarySHA256: strings.Repeat("a", 64)}
	quality := TemporalMediaQualityReport{
		SchemaVersion: TemporalMediaQualitySchemaVersion, ContractVersion: TemporalMediaQualityContractVersion,
		PolicyVersion: MediaIntegrityPolicyVersion, MeasuredAt: attestation.LockedAt.Add(time.Hour),
		HumanPackageSHA256: strings.Repeat("4", 64), HumanPrivateMapSHA256: strings.Repeat("5", 64),
		HumanAssessmentSetSHA256: humanSHA, HumanAttestationFileSHA256: attestationFileSHA,
		EvidenceManifestSHA256: evidenceSHA, SelectionSHA256: manifest.SelectionSHA256,
		MediaTools: TemporalTruthMediaIdentity{FFmpeg: tool, FFprobe: tool}, Cases: len(manifest.Cases),
		ProductionAdmissionAllowed: false,
	}
	for index, item := range manifest.Cases {
		measurement := TemporalMediaQualityCase{
			EvidenceAlias: item.Alias, SourceMediaSHA256: item.Video.SHA256, HumanUnit: set.Assessments[index].Unit,
			DurationMS: item.Video.DurationMS, HadAudio: true, PolicyVerdict: mediaQualityContinue,
		}
		quality.CaseMeasurements = append(quality.CaseMeasurements, measurement)
		accumulateTemporalMediaQuality(&quality, measurement)
	}
	qualityPath := writeTemporalHumanJSON(t, base.root, "quality.json", quality)
	suitabilityAliases := make([]string, 0, len(manifest.Cases))
	for _, item := range manifest.Cases {
		suitabilityAliases = append(suitabilityAliases, item.Alias)
	}
	sort.Strings(suitabilityAliases)
	suitability := TemporalSuitabilityComparisonReport{
		SchemaVersion: TemporalSuitabilityComparisonSchemaVersion, ContractVersion: TemporalSuitabilityComparisonContractVersion,
		ComparedAt: quality.MeasuredAt.Add(time.Hour), EvidenceManifestSHA256: evidenceSHA,
		SelectionSHA256: temporalTruthJSONSHA(suitabilityAliases), FirstResultSHA256: strings.Repeat("8", 64), SecondResultSHA256: strings.Repeat("9", 64),
		FirstAssessor: fillereval.TemporalAssessorIdentity{
			ID: "first", Provider: "openrouter", Model: "model-one", ModelFamily: "one",
			ModelDigest: "digest-one", PromptVersion: "prompt-v1",
		},
		SecondAssessor: fillereval.TemporalAssessorIdentity{
			ID: "second", Provider: "openrouter", Model: "model-two", ModelFamily: "two",
			ModelDigest: "digest-two", PromptVersion: "prompt-v1",
		},
		Cases: len(manifest.Cases), CoverageHoldCases: len(manifest.Cases), ProductionAdmissionAllowed: false,
	}
	for _, item := range manifest.Cases {
		suitability.CaseComparisons = append(suitability.CaseComparisons, TemporalSuitabilityCaseComparison{
			EvidenceAlias: item.Alias, FirstOutcome: string(SuitabilityOutcomeCoverageHold),
			SecondOutcome: string(SuitabilityOutcomeCoverageHold), Disposition: "coverage_hold",
		})
	}
	suitabilityPath := writeTemporalHumanJSON(t, base.root, "suitability.json", suitability)
	reference := fillerreference.Audit{
		SchemaVersion: fillerreference.AuditSchemaVersion, Contract: fillerreference.ContractVersion,
		GeneratedAt: suitability.ComparedAt.Add(30 * time.Minute),
		Inputs: fillerreference.InputIdentity{
			ManifestSHA256: strings.Repeat("1", 64), PacketsSHA256: strings.Repeat("2", 64),
			MappingSHA256: strings.Repeat("3", 64), DownloadLedgerSHA256: strings.Repeat("4", 64),
			ContentReviewSHA256: strings.Repeat("5", 64),
		},
		Summary: fillerreference.Summary{
			Cases: len(privateMap.Entries), Candidates: len(privateMap.Entries),
			Mapping: "fixture-mapping-v1", Contract: fillerreference.ContractVersion,
		},
	}
	for _, item := range privateMap.Entries {
		reference.Cases = append(reference.Cases, fillerreference.Case{
			CaseID: item.CaseID, ContentSHA256: item.ContentSHA256, SourceLocalFile: item.SourceLocalFile,
			Disposition: fillerreference.DispositionCandidate,
		})
	}
	for index := len(reference.Cases); index < 300; index++ {
		caseID := fmt.Sprintf("reference-only-%03d", index)
		reference.Cases = append(reference.Cases, fillerreference.Case{
			CaseID: caseID, ContentSHA256: hashBytes([]byte(caseID)), SourceLocalFile: caseID + ".mp4",
			Disposition: fillerreference.DispositionCandidate,
		})
	}
	reference.Summary.Cases = len(reference.Cases)
	reference.Summary.Candidates = len(reference.Cases)
	referencePath := writeTemporalHumanJSON(t, base.root, "reference-audit.json", reference)
	referenceRaw, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatal(err)
	}
	referenceSHA, err := hashFile(referencePath)
	if err != nil {
		t.Fatal(err)
	}
	fingerprints := make([]fillerreference.FamilyFingerprint, 0, len(reference.Cases))
	for _, item := range reference.Cases {
		fingerprints = append(fingerprints, fillerreference.FamilyFingerprint{
			CaseID: item.CaseID, ContentSHA256: item.ContentSHA256, LocalFile: item.SourceLocalFile,
			FrameHashes: []uint64{1}, AudioRMS: []uint32{1},
		})
	}
	family, err := fillerreference.BuildFamilyAudit(referenceRaw, fingerprints, suitability.ComparedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if family.SourceAudit != referenceSHA {
		t.Fatal("family fixture did not bind reference audit bytes")
	}
	familyPath := writeTemporalHumanJSON(t, base.root, "family.json", family)
	privateMapRaw, err := os.ReadFile(base.privateMap)
	if err != nil {
		t.Fatal(err)
	}
	transition := TemporalTransitionAuthority{
		SchemaVersion: TemporalTransitionAuthoritySchemaVersion, ContractVersion: TemporalTransitionAuthorityContractVersion,
		GeneratedAt: family.GeneratedAt.Add(30 * time.Minute), EvidenceManifestSHA256: evidenceSHA,
		EvidencePrivateMapSHA256: hashBytes(privateMapRaw), FFmpeg: tool, Policy: temporalTransitionPolicy(),
		TrainingAllowed: false, ProductionAdmissionAllowed: false,
	}
	caseIDByAlias := make(map[string]string, len(privateMap.Entries))
	for _, item := range privateMap.Entries {
		caseIDByAlias[item.Alias] = item.CaseID
	}
	for index, item := range manifest.Cases {
		head := TemporalTransitionEdge{StartMS: 0, EndMS: 1_000, RMSMilliDBFS: -10_000, PeakMilliDBFS: -3_000}
		tail := TemporalTransitionEdge{StartMS: item.Video.DurationMS - 1_000, EndMS: item.Video.DurationMS, RMSMilliDBFS: -10_000, PeakMilliDBFS: -3_000}
		switch index % 3 {
		case 0:
			head.Black = []mediatools.Interval{{StartMs: 0, EndMs: 100}}
		case 2:
			head.Silence = []mediatools.Interval{{StartMs: 0, EndMs: 100}}
		}
		transition.Cases = append(transition.Cases, TemporalTransitionAuthorityCase{
			EvidenceAlias: item.Alias, CaseID: caseIDByAlias[item.Alias], SourceSHA256: item.Video.SHA256,
			DurationMS: item.Video.DurationMS, Head: head, Tail: tail,
		})
	}
	sort.Slice(transition.Cases, func(i, j int) bool { return transition.Cases[i].EvidenceAlias < transition.Cases[j].EvidenceAlias })
	transitionPath := writeTemporalHumanJSON(t, base.root, "transition-authority.json", transition)
	programmeRoot := filepath.Join(base.root, "programmes")
	if err := os.MkdirAll(programmeRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	inventory := TemporalStructureHoldoutProgrammeInventory{
		SchemaVersion: TemporalStructureHoldoutSchemaVersion, ContractVersion: TemporalStructureHoldoutProgrammeInventoryContract,
		GeneratedAt: family.GeneratedAt.Add(time.Hour),
	}
	for index := 0; index < temporalStructureHoldoutParentSources; index++ {
		raw := []byte(strings.Repeat(string(rune('a'+index)), index+1))
		path := filepath.Join(programmeRoot, "parent-"+string(rune('a'+index))+".mp4")
		if err := os.WriteFile(path, raw, 0o640); err != nil {
			t.Fatal(err)
		}
		inventory.Sources = append(inventory.Sources, TemporalStructureChallengeSource{
			ID: "programme-" + string(rune('a'+index)), Path: filepath.ToSlash(path[len(base.root)+1:]),
			SHA256: hashBytes(raw), DurationMS: 180_000 + int64(index)*10_000,
			Provenance: TemporalStructureSourceProvenance{
				Kind: TemporalStructureSourceProgrammeParent, Authority: "test-programme-authority",
				Reference: "test-programme-" + string(rune('a'+index)), MetadataSHA256: hashBytes([]byte("metadata-" + string(rune('a'+index)))),
				RetrievedAt: inventory.GeneratedAt.Add(-time.Hour),
			},
		})
	}
	inventoryPath := writeTemporalHumanJSON(t, base.root, "programme-inventory.json", inventory)
	return temporalStructureHoldoutFixture{
		temporalHumanReviewFixture: base, humanAssessment: humanAssessment, humanAttestation: humanAttestation,
		quality: qualityPath, suitability: suitabilityPath, referenceAudit: referencePath, family: familyPath, transition: transitionPath, inventory: inventoryPath,
		plannedAt: inventory.GeneratedAt.Add(time.Hour),
	}
}

func (fixture temporalStructureHoldoutFixture) config(output string) TemporalStructureHoldoutConfig {
	return TemporalStructureHoldoutConfig{
		SelectionPath: fixture.selection, EvidenceManifestPath: fixture.manifest, EvidencePrivateMapPath: fixture.privateMap,
		HumanAssessmentPath: fixture.humanAssessment, HumanAttestationPath: fixture.humanAttestation,
		MediaQualityPath: fixture.quality, SuitabilityPath: fixture.suitability, FamilyAuditPath: fixture.family,
		ReferenceAuditPath:      fixture.referenceAudit,
		TransitionAuthorityPath: fixture.transition,
		ProgrammeInventoryPath:  fixture.inventory, SourceRoot: fixture.root, Seed: "holdout-seed",
		Genesis:   true,
		PlannedAt: fixture.plannedAt, OutputDir: output,
	}
}

func rebuildTemporalStructureHoldoutFamily(t *testing.T, referencePath string, audit temporalStructureHoldoutFamilyAudit) string {
	t.Helper()
	referenceRaw, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := fillerreference.BuildFamilyAudit(referenceRaw, audit.Fingerprints, audit.GeneratedAt)
	if err != nil {
		t.Fatal(err)
	}
	return writeTemporalHumanJSON(t, t.TempDir(), "family.json", rebuilt)
}
