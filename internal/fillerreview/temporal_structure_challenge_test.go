package fillerreview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
)

func TestBuildTemporalStructureChallengeSeparatesBlindedMediaFromConstructionAuthority(t *testing.T) {
	fixture := newTemporalStructureFixture(t)
	first, firstResult := fixture.build(t, "seed-one")
	if fixture.media.probeCalls != 3 {
		t.Fatalf("source authority probes=%d, want one per unique source", fixture.media.probeCalls)
	}
	repeat, repeatResult := fixture.build(t, "seed-one")
	second, secondResult := fixture.build(t, "seed-two")

	if firstResult.Cases != 3 || firstResult.PublicManifestSHA256 != repeatResult.PublicManifestSHA256 || firstResult.AuthoritySHA256 != repeatResult.AuthoritySHA256 {
		t.Fatalf("non-reproducible result: first=%+v repeat=%+v", firstResult, repeatResult)
	}
	if firstResult.PublicManifestSHA256 == secondResult.PublicManifestSHA256 || firstResult.AuthoritySHA256 == secondResult.AuthoritySHA256 {
		t.Fatal("independent blinding seeds produced the same authority identities")
	}
	if !bytes.Equal(readTree(t, first), readTree(t, repeat)) {
		t.Fatal("same construction inputs did not reproduce byte-identical output trees")
	}

	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, filepath.Join(first, "public", "manifest.json"))
	authority := readStrictTestJSON[TemporalStructureChallengeAuthority](t, filepath.Join(first, "private", "authority.json"))
	loadedManifest, loadedAuthority, loadedManifestSHA, loadedAuthoritySHA, err := LoadTemporalStructureChallenge(
		filepath.Join(first, "public", "manifest.json"), filepath.Join(first, "private", "authority.json"), 3,
	)
	if err != nil || loadedManifestSHA != firstResult.PublicManifestSHA256 || loadedAuthoritySHA != firstResult.AuthoritySHA256 || len(loadedManifest.Cases) != 3 || len(loadedAuthority.Cases) != 3 {
		t.Fatalf("loaded authority = %d/%d %s/%s, %v", len(loadedManifest.Cases), len(loadedAuthority.Cases), loadedManifestSHA, loadedAuthoritySHA, err)
	}
	if manifest.ProductionAdmissionAllowed || manifest.AssessmentMediaProfileSHA256 != fillerstructuremedia.CanonicalProfile().SHA256 || len(manifest.Cases) != 3 || authority.PublicManifestSHA256 != firstResult.PublicManifestSHA256 || authority.AssessmentMediaProfile.SHA256 != manifest.AssessmentMediaProfileSHA256 || len(authority.Cases) != 3 {
		t.Fatalf("manifest=%+v authority=%+v", manifest, authority)
	}
	units := make(map[fillereval.UnitKind]TemporalStructureChallengeAuthorityCase, len(authority.Cases))
	for _, item := range authority.Cases {
		units[item.Unit] = item
		if item.VideoSHA256 == "" || len(item.Segments) == 0 {
			t.Fatalf("incomplete case authority: %+v", item)
		}
	}
	if units[fillereval.UnitStandalone].Role != fillereval.TemporalRoleCommercial || len(units[fillereval.UnitCompilation].JoinTimesMS) != 1 || units[fillereval.UnitCompilation].JoinTimesMS[0] != 10_000 {
		t.Fatalf("factored standalone or join authority is wrong: %+v", units)
	}
	if units[fillereval.UnitStandalone].Segments[0].SourceRole != fillereval.TemporalRoleCommercial || units[fillereval.UnitProgrammeExcerpt].Segments[0].SourceRole != "" {
		t.Fatalf("source-role authority is not factored: %+v", units)
	}
	excerpt := units[fillereval.UnitProgrammeExcerpt].Segments[0]
	if excerpt.SourceStartMS != 10_000 || excerpt.RequestedMS != 20_000 || excerpt.OutputStartMS != 0 || excerpt.OutputEndMS != 20_000 {
		t.Fatalf("excerpt authority = %+v", excerpt)
	}

	publicRaw := readTree(t, filepath.Join(first, "public"))
	for _, secret := range fixture.secrets {
		if bytes.Contains(publicRaw, []byte(secret)) {
			t.Fatalf("public package leaks coordinator-private value %q", secret)
		}
	}
	firstAliases := temporalStructureAliases(manifest)
	secondManifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, filepath.Join(second, "public", "manifest.json"))
	if slices.Equal(firstAliases, temporalStructureAliases(secondManifest)) {
		t.Fatal("new seed did not change blinded aliases and ordering")
	}
}

func TestBuildTemporalStructureChallengeSupportsProgrammeWithInsertedSpot(t *testing.T) {
	fixture := newTemporalStructureFixture(t)
	fixture.authoring.Cases = append(fixture.authoring.Cases, TemporalStructureChallengeCase{
		ID: "programme-spots-case-secret", Unit: fillereval.UnitProgrammeSpots,
		Segments: []TemporalStructureChallengeSegment{
			{SourceID: "programme-parent-secret", StartMS: 10_000, DurationMS: 10_000},
			{SourceID: "bounded-commercial-secret", DurationMS: 10_000},
			{SourceID: "programme-parent-secret", StartMS: 20_000, DurationMS: 10_000},
		},
	})
	fixture.secrets = append(fixture.secrets, "programme-spots-case-secret", string(fillereval.UnitProgrammeSpots))
	fixture.writeAuthoring(t)
	output := filepath.Join(t.TempDir(), "challenge")
	result, err := buildFixtureTemporalStructureChallenge(context.Background(), fixture.config(output, "programme-spots"))
	if err != nil {
		t.Fatal(err)
	}
	_, authority, _, _, err := LoadTemporalStructureChallenge(
		filepath.Join(output, "public", "manifest.json"), filepath.Join(output, "private", "authority.json"), 4,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cases != 4 {
		t.Fatalf("challenge cases = %d, want 4", result.Cases)
	}
	var inserted TemporalStructureChallengeAuthorityCase
	for _, item := range authority.Cases {
		if item.Unit == fillereval.UnitProgrammeSpots {
			inserted = item
		}
	}
	if len(inserted.Segments) != 3 || len(inserted.JoinTimesMS) != 2 || inserted.JoinTimesMS[0] != 10_000 || inserted.JoinTimesMS[1] != 20_000 || inserted.Segments[0].Provenance.Kind != TemporalStructureSourceProgrammeParent || inserted.Segments[1].SourceRole != fillereval.TemporalRoleCommercial || inserted.Segments[2].Provenance.Kind != TemporalStructureSourceProgrammeParent {
		t.Fatalf("programme-with-spots authority = %+v", inserted)
	}
}

func TestLoadTemporalStructureChallengeFailsClosedOnPublicAndPrivateTamper(t *testing.T) {
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "load-tamper")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	authorityPath := filepath.Join(root, "private", "authority.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	videoPath := filepath.Join(root, "public", filepath.FromSlash(manifest.Cases[0].Video.Path))
	if err := os.WriteFile(videoPath, []byte("tampered"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := LoadTemporalStructureChallenge(manifestPath, authorityPath, 3); err == nil {
		t.Fatal("tampered public video was accepted")
	}

	root, _ = fixture.build(t, "authority-tamper")
	manifestPath = filepath.Join(root, "public", "manifest.json")
	authorityPath = filepath.Join(root, "private", "authority.json")
	authority := readStrictTestJSON[TemporalStructureChallengeAuthority](t, authorityPath)
	authority.Cases[0].Segments[0].OutputEndMS++
	raw, err := json.MarshalIndent(authority, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authorityPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := LoadTemporalStructureChallenge(manifestPath, authorityPath, 3); err == nil || !strings.Contains(err.Error(), "output authority") {
		t.Fatalf("tampered private authority error = %v", err)
	}

	root, _ = fixture.build(t, "profile-tamper")
	manifestPath = filepath.Join(root, "public", "manifest.json")
	authorityPath = filepath.Join(root, "private", "authority.json")
	authority = readStrictTestJSON[TemporalStructureChallengeAuthority](t, authorityPath)
	authority.AssessmentMediaProfile.Width++
	raw, err = json.MarshalIndent(authority, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authorityPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := LoadTemporalStructureChallenge(manifestPath, authorityPath, 3); err == nil || !strings.Contains(err.Error(), "media profile") {
		t.Fatalf("tampered media profile error = %v", err)
	}
}

func TestBuildTemporalStructureChallengeRejectsInvalidConstructionAndTamperAtomically(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*temporalStructureFixture)
		want   string
	}{
		{name: "standalone partial", mutate: func(f *temporalStructureFixture) { f.authoring.Cases[0].Segments[0].DurationMS-- }, want: "one whole bounded source"},
		{name: "standalone role drift", mutate: func(f *temporalStructureFixture) { f.authoring.Cases[0].Role = fillereval.TemporalRolePromo }, want: "authority-bound role"},
		{name: "compilation partial", mutate: func(f *temporalStructureFixture) { f.authoring.Cases[1].Segments[1].StartMS = 1 }, want: "invalid bounds"},
		{name: "unknown certification slice", mutate: func(f *temporalStructureFixture) { f.authoring.Cases[1].Slices = []string{"easy_cases"} }, want: "unknown or repeated slice"},
		{name: "unordered certification slices", mutate: func(f *temporalStructureFixture) {
			f.authoring.Cases[1].Slices = []string{TemporalStructureSliceTwoItemCompilation, TemporalStructureSliceMixedRoleJoins}
		}, want: "slices are not ordered"},
		{name: "excerpt edge", mutate: func(f *temporalStructureFixture) { f.authoring.Cases[2].Segments[0].StartMS = 0 }, want: "five-second parent margins"},
		{name: "unclear has no construction", mutate: func(f *temporalStructureFixture) { f.authoring.Cases[2].Unit = fillereval.UnitUnclear }, want: "no provenance-grounded construction"},
		{name: "source tamper", mutate: func(f *temporalStructureFixture) {
			if err := os.WriteFile(filepath.Join(f.root, "sources", "commercial.mp4"), []byte("tampered"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "content hash mismatch"},
		{name: "source without audio", mutate: func(f *temporalStructureFixture) {
			f.media.missingAudioPath = filepath.Join(f.root, "sources", "programme.mp4")
		}, want: "required-audio authority mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTemporalStructureFixture(t)
			test.mutate(&fixture)
			output := filepath.Join(fixture.root, "rejected")
			fixture.writeAuthoring(t)
			_, err := buildFixtureTemporalStructureChallenge(context.Background(), fixture.config(output, "seed"))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
				t.Fatalf("failed build published partial output: %v", statErr)
			}
		})
	}
}

func TestTemporalStructureChallengeRejectsUnknownAuthoringFields(t *testing.T) {
	fixture := newTemporalStructureFixture(t)
	raw, err := json.Marshal(fixture.authoring)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["labelsVisibleToAssessor"] = json.RawMessage(`true`)
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.authoringPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = buildFixtureTemporalStructureChallenge(context.Background(), fixture.config(filepath.Join(fixture.root, "unknown"), "seed"))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}
}

func TestBuildTemporalStructureChallengeReportsObservedOversizedMedia(t *testing.T) {
	fixture := newTemporalStructureFixture(t)
	fixture.media.renderBytes = TemporalTruthMaximumVideoBytes + 1
	output := filepath.Join(fixture.root, "oversized")
	_, err := buildFixtureTemporalStructureChallenge(context.Background(), fixture.config(output, "oversized"))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%d exceeds allowed range 1..%d bytes", fixture.media.renderBytes, TemporalTruthMaximumVideoBytes)) {
		t.Fatalf("oversized render error = %v", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("oversized build published partial output: %v", statErr)
	}
}

func TestAuditTemporalStructureChallengeLeakageIncludesReceiptOnlySecrets(t *testing.T) {
	publicRoot := t.TempDir()
	secret := "receipt-only-evidence-alias"
	if err := os.WriteFile(filepath.Join(publicRoot, "manifest.json"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	receipt := TemporalStructureHoldoutReceipt{SelectedAnchors: []TemporalStructureHoldoutAnchor{{EvidenceAlias: secret}}}
	if err := auditTemporalStructureChallengeLeakage(publicRoot, TemporalStructureChallengeAuthoring{}, &receipt); err == nil || !strings.Contains(err.Error(), secret) {
		t.Fatalf("receipt-only leak error = %v", err)
	}
}

func TestTemporalStructureConcatArgumentsPinDeterministicMetadataFreeCopy(t *testing.T) {
	arguments := strings.Join(fillerstructuremedia.ConcatArguments("concat.txt", "result.mp4"), " ")
	for _, required := range []string{"-safe 1", "-map_metadata -1", "-map_chapters -1", "-c copy", "-fflags +bitexact", "creation_time=", "encoder="} {
		if !strings.Contains(arguments, required) {
			t.Fatalf("concat arguments omit %q: %s", required, arguments)
		}
	}
}

func TestTemporalStructurePartArgumentsPinOneJoinCompatibleProfile(t *testing.T) {
	arguments := strings.Join(fillerstructuremedia.PartArguments("source.mp4", 1_000, 2_000, "part.mp4"), " ")
	for _, required := range []string{
		"-ss 1.000", "-t 2.000", "fps=30", "scale=w=960:h=720:force_original_aspect_ratio=decrease",
		"pad=960:720", "-pix_fmt yuv420p", "-ar 48000", "-ac 2", "-video_track_timescale 90000",
		"-threads 1", "-fflags +bitexact", "creation_time=", "encoder=",
	} {
		if !strings.Contains(arguments, required) {
			t.Fatalf("structure part arguments omit %q: %s", required, arguments)
		}
	}
}

type temporalStructureFixture struct {
	root          string
	authoringPath string
	authoring     TemporalStructureChallengeAuthoring
	media         *fakeTemporalStructureMedia
	generatedAt   time.Time
	secrets       []string
}

func newTemporalStructureFixture(t *testing.T) temporalStructureFixture {
	t.Helper()
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "sources")
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	type sourceSpec struct {
		id, name, kind string
		duration       int64
		role           fillereval.TemporalRole
	}
	specs := []sourceSpec{
		{id: "bounded-commercial-secret", name: "commercial.mp4", kind: TemporalStructureSourceBoundedItem, duration: 10_000, role: fillereval.TemporalRoleCommercial},
		{id: "bounded-promo-secret", name: "promo.mp4", kind: TemporalStructureSourceBoundedItem, duration: 12_000, role: fillereval.TemporalRolePromo},
		{id: "programme-parent-secret", name: "programme.mp4", kind: TemporalStructureSourceProgrammeParent, duration: 60_000},
	}
	media := &fakeTemporalStructureMedia{durationByPath: make(map[string]int64)}
	authoring := TemporalStructureChallengeAuthoring{SchemaVersion: TemporalStructureChallengeSchemaVersion, ContractVersion: TemporalStructureChallengeContractVersion}
	secrets := []string{string(fillereval.UnitStandalone), string(fillereval.UnitCompilation), string(fillereval.UnitProgrammeExcerpt)}
	for index, spec := range specs {
		content := []byte(fmt.Sprintf("opaque-source-%d", index))
		path := filepath.Join(sourceRoot, spec.name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		media.durationByPath[path] = spec.duration
		authority := "authority-secret-" + spec.id
		reference := "https://example.invalid/private/" + spec.id
		authoring.Sources = append(authoring.Sources, TemporalStructureChallengeSource{
			ID: spec.id, Path: spec.name, SHA256: hashBytes(content), DurationMS: spec.duration, StandaloneRole: spec.role,
			Provenance: TemporalStructureSourceProvenance{
				Kind: spec.kind, Authority: authority, Reference: reference, MetadataSHA256: strings.Repeat(fmt.Sprintf("%x", index+1), 64), RetrievedAt: time.Date(2026, 9, 2, 1, 0, index, 0, time.UTC),
			},
		})
		secrets = append(secrets, spec.id, spec.name, authority, reference)
	}
	authoring.Cases = []TemporalStructureChallengeCase{
		{ID: "standalone-case-secret", Unit: fillereval.UnitStandalone, Role: fillereval.TemporalRoleCommercial, Segments: []TemporalStructureChallengeSegment{{SourceID: specs[0].id, DurationMS: specs[0].duration}}},
		{ID: "compilation-case-secret", Unit: fillereval.UnitCompilation, Slices: []string{TemporalStructureSliceMixedRoleJoins, TemporalStructureSliceTwoItemCompilation}, Segments: []TemporalStructureChallengeSegment{{SourceID: specs[0].id, DurationMS: specs[0].duration}, {SourceID: specs[1].id, DurationMS: specs[1].duration}}},
		{ID: "excerpt-case-secret", Unit: fillereval.UnitProgrammeExcerpt, Segments: []TemporalStructureChallengeSegment{{SourceID: specs[2].id, StartMS: 10_000, DurationMS: 20_000}}},
	}
	secrets = append(secrets, "standalone-case-secret", "compilation-case-secret", "excerpt-case-secret", TemporalStructureSliceMixedRoleJoins, TemporalStructureSliceTwoItemCompilation)
	fixture := temporalStructureFixture{
		root: root, authoringPath: filepath.Join(root, "authoring.json"), authoring: authoring, media: media,
		generatedAt: time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC), secrets: secrets,
	}
	fixture.writeAuthoring(t)
	return fixture
}

func (fixture temporalStructureFixture) config(output, seed string) TemporalStructureChallengeConfig {
	return TemporalStructureChallengeConfig{
		AuthoringPath: fixture.authoringPath, PlanReceiptPath: filepath.Join(fixture.root, "fixture-plan-receipt.json"), SourceRoot: filepath.Join(fixture.root, "sources"), OutputDir: output,
		ChallengeID: "opaque-challenge-01", Seed: seed, GeneratedAt: fixture.generatedAt, Media: fixture.media,
	}
}

func (fixture temporalStructureFixture) build(t *testing.T, seed string) (string, TemporalStructureChallengeResult) {
	t.Helper()
	output := filepath.Join(t.TempDir(), "challenge")
	result, err := buildFixtureTemporalStructureChallenge(context.Background(), fixture.config(output, seed))
	if err != nil {
		t.Fatal(err)
	}
	return output, result
}

func buildFixtureTemporalStructureChallenge(ctx context.Context, config TemporalStructureChallengeConfig) (TemporalStructureChallengeResult, error) {
	if err := validateTemporalStructureChallengeConfig(config); err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	raw, authoring, err := loadTemporalStructureChallengeAuthoring(config.AuthoringPath)
	if err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	receipt := TemporalStructureHoldoutReceipt{
		ContractVersion: TemporalStructureHoldoutContractVersion,
		AuthoringSHA256: hashBytes(raw),
	}
	receiptRaw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	receiptRaw = append(receiptRaw, '\n')
	if err := os.WriteFile(config.PlanReceiptPath, receiptRaw, 0o600); err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	return buildTemporalStructureChallenge(ctx, config, raw, authoring, &receipt, hashBytes(receiptRaw), receipt.ContractVersion)
}

func (fixture temporalStructureFixture) writeAuthoring(t *testing.T) {
	t.Helper()
	raw, err := json.MarshalIndent(fixture.authoring, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.authoringPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

type fakeTemporalStructureMedia struct {
	durationByPath     map[string]int64
	missingAudioPath   string
	probeCalls         int
	renderVideoMutator func(*TemporalTruthVideoInfo)
	renderBytes        int64
}

func (media *fakeTemporalStructureMedia) Identity() TemporalTruthMediaIdentity {
	return TemporalTruthMediaIdentity{
		FFmpeg:  TemporalTruthToolIdentity{Path: "/opaque/ffmpeg", Version: "fixture", BinarySHA256: strings.Repeat("a", 64)},
		FFprobe: TemporalTruthToolIdentity{Path: "/opaque/ffprobe", Version: "fixture", BinarySHA256: strings.Repeat("b", 64)},
	}
}

func (media *fakeTemporalStructureMedia) Probe(_ context.Context, path string) (TemporalTruthVideoInfo, error) {
	media.probeCalls++
	duration, exists := media.durationByPath[path]
	if !exists {
		return TemporalTruthVideoInfo{}, fmt.Errorf("unknown fixture path")
	}
	return TemporalTruthVideoInfo{DurationMS: duration, Width: 640, Height: 360, HasAudio: path != media.missingAudioPath}, nil
}

func (media *fakeTemporalStructureMedia) Render(_ context.Context, segments []TemporalStructureRenderSegment, output string) (TemporalStructureRenderResult, error) {
	hasher := sha256.New()
	result := TemporalStructureRenderResult{Video: TemporalTruthVideoInfo{Width: 960, Height: 720, HasAudio: true, Profile: temporalStructureTestProfile()}}
	for _, segment := range segments {
		digest, err := hashFile(segment.SourcePath)
		if err != nil {
			return TemporalStructureRenderResult{}, err
		}
		_, _ = fmt.Fprintf(hasher, "%s:%d:%d;", digest, segment.StartMS, segment.DurationMS)
		result.Parts = append(result.Parts, TemporalStructureRenderedPart{DurationMS: segment.DurationMS})
		result.Video.DurationMS += segment.DurationMS
	}
	if err := os.WriteFile(output, []byte(hex.EncodeToString(hasher.Sum(nil))), 0o640); err != nil {
		return TemporalStructureRenderResult{}, err
	}
	if media.renderBytes > 0 {
		if err := os.Truncate(output, media.renderBytes); err != nil {
			return TemporalStructureRenderResult{}, err
		}
	}
	if media.renderVideoMutator != nil {
		media.renderVideoMutator(&result.Video)
	}
	return result, nil
}

func temporalStructureAliases(manifest TemporalStructureChallengeManifest) []string {
	aliases := make([]string, 0, len(manifest.Cases))
	for _, item := range manifest.Cases {
		aliases = append(aliases, item.Alias)
	}
	return aliases
}

func readStrictTestJSON[T any](t *testing.T, path string) T {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
