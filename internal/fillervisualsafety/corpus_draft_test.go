package fillervisualsafety

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareVisualCorpusDraftPublishesCandidateBlindReviewBoard(t *testing.T) {
	t.Parallel()
	fixture := newVisualCorpusDraftFixture(t)
	result, err := PrepareVisualCorpusDraft(context.Background(), fixture.config(fixture.output("first")))
	if err != nil {
		t.Fatalf("PrepareVisualCorpusDraft() error = %v", err)
	}
	if result.CaseCount != MinimumVisualPositiveCandidateTarget+MinimumVisualCleanCandidateTarget {
		t.Fatalf("case count = %d", result.CaseCount)
	}
	manifest, owner, err := OpenVisualCorpusDraft(fixture.output("first"))
	if err != nil {
		t.Fatalf("OpenVisualCorpusDraft() error = %v", err)
	}
	if manifest.SHA256 != result.ManifestSHA256 || owner.SHA256 != result.OwnerMapSHA256 ||
		manifest.CandidateModelOutput || manifest.TruthAuthorityCreated || manifest.TrainingAllowed ||
		manifest.ProductionAdmissionAllowed || len(manifest.Cases) != result.CaseCount {
		t.Fatalf("published draft grants authority or does not reproduce: %#v %#v", manifest, result)
	}
	board, err := os.ReadFile(filepath.Join(fixture.output("first"), visualCorpusReviewDirectory, visualCorpusBoardFilename))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"P · Positive", "C · Clean", "U · Uncertain", "X · Exclude", "Download decisions",
		"localStorage.setItem", `const authority = "` + fixture.authority.SHA256 + `";`,
	} {
		if !bytes.Contains(board, []byte(required)) {
			t.Errorf("review board missing %q", required)
		}
	}
	if bytes.Contains(board, []byte(fixture.authority.Candidates[0].CandidateID)) ||
		bytes.Contains(board, []byte(VisualCorpusNominationPositive)) {
		t.Fatal("review board leaked owner identity or nomination")
	}
	for _, path := range []string{
		filepath.Join(fixture.output("first"), visualCorpusOwnerFilename),
		filepath.Join(fixture.output("first"), visualCorpusReviewDirectory, visualCorpusManifestFilename),
		filepath.Join(fixture.output("first"), visualCorpusReviewDirectory, visualCorpusBoardFilename),
	} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat private file %s: %v", path, statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("private file %s mode = %v", path, info.Mode().Perm())
		}
	}
}

func TestPrepareVisualCorpusDraftIsByteReproducible(t *testing.T) {
	t.Parallel()
	fixture := newVisualCorpusDraftFixture(t)
	first, err := PrepareVisualCorpusDraft(context.Background(), fixture.config(fixture.output("first")))
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrepareVisualCorpusDraft(context.Background(), fixture.config(fixture.output("second")))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("draft identities differ: %#v != %#v", first, second)
	}
}

func TestPrepareVisualCorpusDraftRejectsFamilyRightsAndNormalizedImageCollisions(t *testing.T) {
	t.Parallel()
	t.Run("family", func(t *testing.T) {
		fixture := newVisualCorpusDraftFixture(t)
		fixture.authority.Candidates[1].SourceFamilyID = fixture.authority.Candidates[0].SourceFamilyID
		if _, err := SealVisualCorpusDraftAuthority(fixture.authority); err == nil {
			t.Fatal("SealVisualCorpusDraftAuthority() accepted a family collision")
		}
	})
	t.Run("rights", func(t *testing.T) {
		fixture := newVisualCorpusDraftFixture(t)
		candidate := &fixture.authority.Candidates[0]
		raw := fixture.rightsEvidence(t, *candidate, true)
		writePrivateTestFile(t, filepath.Join(fixture.sourceRoot, candidate.RightsRelativePath), raw)
		candidate.RightsEvidence = VisualCorpusFileIdentity{SHA256: digestBytes(raw), Bytes: int64(len(raw))}
		fixture.reseal(t)
		output := fixture.output("rights-invalid")
		if _, err := PrepareVisualCorpusDraft(context.Background(), fixture.config(output)); err == nil {
			t.Fatal("PrepareVisualCorpusDraft() accepted training rights")
		}
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			t.Fatalf("failed draft output remains: %v", err)
		}
	})
	t.Run("normalized image", func(t *testing.T) {
		fixture := newVisualCorpusDraftFixture(t)
		candidate := &fixture.authority.Candidates[1]
		raw := visualCorpusTestPNG(t, 0, 18, 16)
		writePrivateTestFile(t, filepath.Join(fixture.sourceRoot, candidate.AssetRelativePath), raw)
		candidate.Asset = VisualCorpusFileIdentity{SHA256: digestBytes(raw), Bytes: int64(len(raw))}
		rightsRaw := fixture.rightsEvidence(t, *candidate, false)
		writePrivateTestFile(t, filepath.Join(fixture.sourceRoot, candidate.RightsRelativePath), rightsRaw)
		candidate.RightsEvidence = VisualCorpusFileIdentity{SHA256: digestBytes(rightsRaw), Bytes: int64(len(rightsRaw))}
		fixture.reseal(t)
		output := fixture.output("image-collision")
		if _, err := PrepareVisualCorpusDraft(context.Background(), fixture.config(output)); err == nil ||
			!strings.Contains(err.Error(), "normalized-image collision") {
			t.Fatalf("PrepareVisualCorpusDraft() normalized collision error = %v", err)
		}
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			t.Fatalf("failed draft output remains: %v", err)
		}
	})
}

func TestOpenVisualCorpusDraftRejectsTamperingAndUnexpectedEvidence(t *testing.T) {
	t.Parallel()
	fixture := newVisualCorpusDraftFixture(t)
	output := fixture.output("tampered")
	if _, err := PrepareVisualCorpusDraft(context.Background(), fixture.config(output)); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := OpenVisualCorpusDraft(output)
	if err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(output, visualCorpusReviewDirectory, filepath.FromSlash(manifest.Cases[0].Asset.RelativePath))
	if err := os.WriteFile(asset, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenVisualCorpusDraft(output); err == nil {
		t.Fatal("OpenVisualCorpusDraft() accepted a modified image")
	}
	if err := os.WriteFile(filepath.Join(output, "candidate-model-output.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenVisualCorpusDraft(output); err == nil {
		t.Fatal("OpenVisualCorpusDraft() accepted unexpected evidence")
	}
}

type visualCorpusDraftFixture struct {
	root       string
	sourceRoot string
	policyPath string
	seedPath   string
	authority  VisualCorpusDraftAuthority
	preparedAt time.Time
}

func newVisualCorpusDraftFixture(t *testing.T) *visualCorpusDraftFixture {
	t.Helper()
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "source")
	for _, directory := range []string{sourceRoot, filepath.Join(sourceRoot, "assets"), filepath.Join(sourceRoot, "rights")} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy := CandidateBlindReviewPolicy{
		SchemaVersion: 1, Kind: "loomarr-visual-sensitive-content-development-policy-v1",
		DevelopmentOnly: true, ProductionAdmissionAllowed: false,
		PolicyMatches: []CandidateBlindReviewPolicyMatch{{ID: "explicit_nudity_v1", Definition: "visible prohibited anatomy under the private development policy"}},
	}
	policyRaw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	policyRaw = append(policyRaw, '\n')
	policyPath := filepath.Join(root, "policy.json")
	writePrivateTestFile(t, policyPath, policyRaw)
	seed := bytes.Repeat([]byte{0x5a}, sha256Size)
	seedPath := filepath.Join(root, "alias-seed")
	writePrivateTestFile(t, seedPath, seed)
	authoredAt := time.Date(2026, 9, 4, 18, 0, 0, 0, time.UTC)
	fixture := &visualCorpusDraftFixture{
		root: root, sourceRoot: sourceRoot, policyPath: policyPath, seedPath: seedPath,
		preparedAt: authoredAt.Add(time.Minute),
		authority: VisualCorpusDraftAuthority{
			AuthoredAt: authoredAt, PolicySHA256: digestBytes(policyRaw), AliasSeedSHA256: digestBytes(seed),
			PositiveCandidateTarget: MinimumVisualPositiveCandidateTarget,
			CleanCandidateTarget:    MinimumVisualCleanCandidateTarget,
			TransportDecision:       VisualCorpusTransportDecisionUnresolved,
		},
	}
	count := MinimumVisualPositiveCandidateTarget + MinimumVisualCleanCandidateTarget
	fixture.authority.Candidates = make([]VisualCorpusDraftCandidate, 0, count)
	for index := 0; index < count; index++ {
		nomination := VisualCorpusNominationClean
		subject := VisualCorpusSubjectNoRiskFound
		slices := []string{DiagnosticSliceBeach}
		if index < MinimumVisualPositiveCandidateTarget {
			nomination = VisualCorpusNominationPositive
			subject = VisualCorpusSubjectHistoricalAdult
			slices = []string{DiagnosticSliceHistoricalGraphics}
		}
		candidate := VisualCorpusDraftCandidate{
			CandidateID: fmt.Sprintf("candidate-%03d", index), Nomination: nomination,
			InstitutionID: "example-museum", SourceWorkID: fmt.Sprintf("work-%03d", index),
			SourceFamilyID: fmt.Sprintf("family-%03d", index), IndependenceGroupID: fmt.Sprintf("independent-%03d", index),
			CreatorID: fmt.Sprintf("creator-%03d", index), ObjectURL: fmt.Sprintf("https://example.org/object/%d", index),
			RightsURL: fmt.Sprintf("https://example.org/rights/%d", index), RightsBasis: VisualCorpusRightsCC0,
			SubjectStatus: subject, GeneratedStatus: VisualCorpusGeneratedNo,
			AssetRelativePath: fmt.Sprintf("assets/%03d.png", index), RightsRelativePath: fmt.Sprintf("rights/%03d.json", index),
			Slices: slices,
		}
		asset := visualCorpusTestPNG(t, index, 9, 8)
		writePrivateTestFile(t, filepath.Join(sourceRoot, candidate.AssetRelativePath), asset)
		candidate.Asset = VisualCorpusFileIdentity{SHA256: digestBytes(asset), Bytes: int64(len(asset))}
		rights := fixture.rightsEvidence(t, candidate, false)
		writePrivateTestFile(t, filepath.Join(sourceRoot, candidate.RightsRelativePath), rights)
		candidate.RightsEvidence = VisualCorpusFileIdentity{SHA256: digestBytes(rights), Bytes: int64(len(rights))}
		fixture.authority.Candidates = append(fixture.authority.Candidates, candidate)
	}
	fixture.reseal(t)
	return fixture
}

func TestInspectVisualCorpusImageRejectsBytesAfterCompleteImage(t *testing.T) {
	raw := append(visualCorpusTestPNG(t, 7, 9, 8), []byte("trailing")...)
	if _, _, _, _, err := inspectVisualCorpusImage(raw); err == nil {
		t.Fatal("image with trailing bytes passed")
	}
}

func (fixture *visualCorpusDraftFixture) reseal(t *testing.T) {
	t.Helper()
	fixture.authority.SchemaVersion = 0
	fixture.authority.ContractVersion = ""
	fixture.authority.SHA256 = ""
	authority, err := SealVisualCorpusDraftAuthority(fixture.authority)
	if err != nil {
		t.Fatal(err)
	}
	fixture.authority = authority
}

func (fixture *visualCorpusDraftFixture) rightsEvidence(t *testing.T, candidate VisualCorpusDraftCandidate, training bool) []byte {
	t.Helper()
	evidence := VisualCorpusRightsEvidence{
		SchemaVersion: visualCorpusRightsEvidenceSchemaVersion, Kind: visualCorpusRightsEvidenceKind,
		InventorySHA256: strings.Repeat("a", 64), MaterializationSHA256: strings.Repeat("b", 64),
		RightsApprovalSHA256: strings.Repeat("c", 64), CaseID: candidate.CandidateID, ContentSHA256: candidate.Asset.SHA256,
		ReviewedAt: fixture.authority.AuthoredAt.Add(-time.Minute), ReviewedBy: "maintainer",
		InstitutionID: candidate.InstitutionID, SourceWorkID: candidate.SourceWorkID,
		ObjectURL: candidate.ObjectURL, RightsURL: candidate.RightsURL, RightsBasis: candidate.RightsBasis,
		SubjectStatus: candidate.SubjectStatus, GeneratedStatus: candidate.GeneratedStatus,
		PrivateRetentionAllowed: true, PrivateModelEvaluation: true, TrainingAllowed: training,
		ProductionBroadcastAllowed: false,
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func (fixture *visualCorpusDraftFixture) config(output string) VisualCorpusDraftConfig {
	return VisualCorpusDraftConfig{
		Authority: fixture.authority, SourceRoot: fixture.sourceRoot, PolicyPath: fixture.policyPath,
		AliasSeedPath: fixture.seedPath, OutputDir: output, PreparedAt: fixture.preparedAt,
	}
}

func (fixture *visualCorpusDraftFixture) output(name string) string {
	return filepath.Join(fixture.root, "output-"+name)
}

func visualCorpusTestPNG(t *testing.T, value, width, height int) []byte {
	t.Helper()
	var base [8][9]uint8
	for y := 0; y < 8; y++ {
		current := uint8(128)
		base[y][0] = current
		for x := 0; x < 8; x++ {
			if uint(value)&(uint(1)<<uint(y*8+x)) != 0 {
				current--
			} else {
				current++
			}
			base[y][x+1] = current
		}
	}
	imageValue := image.NewGray(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			imageValue.SetGray(x, y, color.Gray{Y: base[y*8/height][x*9/width]})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, imageValue); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func writePrivateTestFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

const sha256Size = 32
