package fillersafetycorpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillersafety"
)

type knownScriptFixture struct {
	parent, root, authorityPath, seedPath, output string
	authority                                     KnownScriptAuthority
	config                                        PrepareKnownScriptConfig
	wrapper                                       mediaWrapper
	media                                         *wrapperFixture
}

func newKnownScriptFixture(t *testing.T) *knownScriptFixture {
	return newKnownScriptFixtureForPolicy(t, fixtureSHA(900))
}

func newKnownScriptFixtureForPolicy(t *testing.T, policySHA256 string) *knownScriptFixture {
	t.Helper()
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "source")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	preparedAt := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	authority := KnownScriptAuthority{
		SchemaVersion: KnownScriptAuthoritySchemaVersion, ContractVersion: KnownScriptAuthorityContractVersion,
		Dataset: KnownScriptDatasetID, AuthoredAt: preparedAt.Add(-time.Hour), PolicySHA256: policySHA256,
		Implementation: "spoken-safety-evaluator-v1",
	}
	processorSchedule := writeAuthorityJSON(t, root, "shared/processors.json", KnownScriptProcessorSchedule{
		SchemaVersion: KnownScriptProcessorSchemaVersion, ContractVersion: KnownScriptProcessorContractVersion,
		Processors: []KnownScriptHostedProcessor{{
			Kind: KnownScriptProcessorOpenRouter, SourceBaseURL: "https://openrouter.ai/api/v1",
			RequestedModel: "vendor/reviewer", ResolvedModel: "vendor/reviewer-2026",
			UpstreamProvider: "Pinned Provider", UpstreamProviderSlug: "pinned-provider", ZDR: true,
		}},
	})
	withdrawal := writeAuthorityFile(t, root, "shared/withdrawal.txt", []byte("private withdrawal instructions"))
	slices := knownScriptPositiveSlices()
	for index := 0; index < 59; index++ {
		authority.Members = append(authority.Members, writeKnownScriptMember(
			t, root, preparedAt, processorSchedule, withdrawal, index, slices[index%len(slices)], authority.PolicySHA256,
		))
	}
	authorityPath := filepath.Join(parent, "known-script-authority.json")
	writePrivateJSONFixture(t, authorityPath, authority)
	seedPath := filepath.Join(parent, "alias-seed.bin")
	if err := os.WriteFile(seedPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := &knownScriptFixture{
		parent: parent, root: root, authorityPath: authorityPath, seedPath: seedPath,
		output: filepath.Join(parent, "prepared"), authority: authority,
		media: &wrapperFixture{recipe: KnownScriptPackagingRecipe},
	}
	fixture.wrapper = fixture.media.wrapperForFixture()
	fixture.config = PrepareKnownScriptConfig{
		AuthorityPath: authorityPath, SourceRoot: root, SeedPath: seedPath,
		FFmpegPath: "fixture-ffmpeg", FFprobePath: "fixture-ffprobe", PreparedAt: preparedAt,
		ExpectedSpeakers: 59, MaximumInputBytes: 32 << 20, MaximumOutputBytes: 32 << 20,
		MaximumWallTime: time.Minute, OutputDirectory: fixture.output,
	}
	return fixture
}

func writeKnownScriptMember(
	t *testing.T,
	root string,
	preparedAt time.Time,
	processorSchedule FileAuthority,
	withdrawal FileAuthority,
	index int,
	slice string,
	policySHA256 string,
) KnownScriptMember {
	t.Helper()
	participant := fmt.Sprintf("participant-%03d", index+1)
	prefix := filepath.ToSlash(filepath.Join("participants", participant))
	scriptID := fmt.Sprintf("script-v1-%03d", index+1)
	script := writeAuthorityFile(t, root, prefix+"/script.txt", []byte(fmt.Sprintf("private known script %03d\n", index+1)))
	intervals := []PreparedPositiveInterval{{RuleID: "rule-000000000000000000000001", StartMS: 100, EndMS: 500}}
	mapping := KnownScriptPolicyMapping{
		SchemaVersion: KnownScriptMappingSchemaVersion, ContractVersion: KnownScriptMappingContractVersion,
		ScriptID: scriptID, ScriptSHA256: script.SHA256, PolicySHA256: policySHA256, Intervals: intervals,
	}
	mappingRaw, err := json.Marshal(mapping)
	if err != nil {
		t.Fatal(err)
	}
	policyMapping := writeAuthorityFile(t, root, prefix+"/mapping.json", mappingRaw)
	audio := writeAuthorityFile(t, root, prefix+"/audio.wav", []byte(fmt.Sprintf("real consented speech %03d", index+1)))
	consent := KnownScriptConsent{
		SchemaVersion: KnownScriptConsentSchemaVersion, ContractVersion: KnownScriptConsentContractVersion,
		ParticipantID:           participant,
		Document:                writeAuthorityFile(t, root, prefix+"/consent.bin", []byte(fmt.Sprintf("signed consent %03d", index+1))),
		SignerAuthorityEvidence: writeAuthorityFile(t, root, prefix+"/signer.json", []byte(fmt.Sprintf("signer evidence %03d", index+1))),
		ProcessorSchedule:       processorSchedule, WithdrawalInstructions: withdrawal,
		SignedAt: preparedAt.Add(-4 * time.Hour), RightsReviewedAt: preparedAt.Add(-3 * time.Hour),
		RightsReviewerID: "owner-rights-reviewer", RedistributionScope: KnownScriptRedistributionPrivate,
		RetentionPolicy: KnownScriptRetentionWithdrawal, WithdrawalSupported: true, NoEndorsement: true,
		Grants: KnownScriptConsentGrants{
			Collection: true, PrivateStorage: true, TechnicalModification: true,
			EvidenceExtraction: true, IndependentReview: true, HostedModelEvaluation: true,
		},
	}
	member := KnownScriptMember{
		ParticipantID: participant, SessionID: fmt.Sprintf("session-%03d", index+1),
		TakeID: fmt.Sprintf("take-%03d", index+1), Locale: "en-US", Accent: "fixture-accent",
		ScriptID: scriptID, Script: script, PolicyMapping: policyMapping, MasterAudio: audio, SelectedAudio: audio,
		Slices: []string{slice}, PositiveIntervals: intervals, Consent: consent,
		Transformation: KnownScriptTransformation{
			RecipeID: "dry-recording-v1", RecipeSHA256: fixtureSHA(2_000 + index),
			RenderedAt:   preparedAt.Add(-2 * time.Hour),
			Tool:         fillersafety.ToolIdentity{Version: "ffmpeg fixture transform", BinarySHA256: fixtureSHA(3_000 + index)},
			MasterSHA256: audio.SHA256, OutputSHA256: audio.SHA256,
		},
	}
	if slice == "music_overlap" {
		rights := fixtureRightsContract(preparedAt)
		rights.EmbeddedRights.Music = fillercorpus.RightsStatusCleared
		member.Transformation.Assets = []KnownScriptAsset{{
			Role:           KnownScriptAssetMusic,
			Media:          writeAuthorityFile(t, root, prefix+"/music.wav", []byte(fmt.Sprintf("licensed music %03d", index+1))),
			RightsEvidence: writeAuthorityFile(t, root, prefix+"/music-rights.json", []byte(fmt.Sprintf("music rights %03d", index+1))),
			RightsContract: rights,
		}}
	}
	return member
}

func writeAuthorityJSON(t *testing.T, root, relative string, value any) FileAuthority {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return writeAuthorityFile(t, root, relative, raw)
}

func rewriteKnownScriptProcessorSchedule(
	t *testing.T,
	fixture *knownScriptFixture,
	schedule KnownScriptProcessorSchedule,
) {
	t.Helper()
	authority := writeAuthorityJSON(t, fixture.root, "shared/processors.json", schedule)
	for index := range fixture.authority.Members {
		fixture.authority.Members[index].Consent.ProcessorSchedule = authority
	}
	writePrivateJSONFixture(t, fixture.authorityPath, fixture.authority)
}
