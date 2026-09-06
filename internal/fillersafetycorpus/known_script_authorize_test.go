package fillersafetycorpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

func TestAuthorizeKnownScriptProcessorRequiresExactCurrentConsent(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*knownScriptRights, *KnownScriptHostedProcessor, time.Time)
	}{
		{name: "exact", mutate: func(*knownScriptRights, *KnownScriptHostedProcessor, time.Time) {}},
		{name: "route mismatch", mutate: func(_ *knownScriptRights, processor *KnownScriptHostedProcessor, _ time.Time) {
			processor.RequestedModel = "vendor/different-reviewer"
		}},
		{name: "expired consent", mutate: func(rights *knownScriptRights, _ *KnownScriptHostedProcessor, at time.Time) {
			rights.Consent.ExpiresAt = &at
		}},
		{name: "withdrawn consent", mutate: func(rights *knownScriptRights, _ *KnownScriptHostedProcessor, at time.Time) {
			rights.Consent.WithdrawnAt = &at
		}},
		{name: "expired asset", mutate: func(rights *knownScriptRights, _ *KnownScriptHostedProcessor, at time.Time) {
			rights.Assets[0].RightsContract.Term = fillercorpus.RightsTermExpires
			rights.Assets[0].RightsContract.ExpiresAt = &at
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rights, processor := knownScriptRightsFixture(t, test.name == "expired asset")
			at := rights.PreparedAt.Add(time.Hour)
			test.mutate(&rights, &processor, at)
			raw, err := marshalPrivateJSON(rights)
			if err != nil {
				t.Fatal(err)
			}
			applies, err := AuthorizeKnownScriptProcessor(raw, at, processor)
			if test.name == "exact" {
				if !applies || err != nil {
					t.Fatalf("applies=%v err=%v", applies, err)
				}
				return
			}
			if !applies || err == nil {
				t.Fatalf("applies=%v err=%v", applies, err)
			}
			if strings.Contains(err.Error(), rights.ParticipantID) || strings.Contains(err.Error(), processor.RequestedModel) {
				t.Fatalf("authorization error leaked private or route identity: %v", err)
			}
		})
	}
}

func TestAuthorizeKnownScriptProcessorRecognizesOnlyCanonicalClaimedContract(t *testing.T) {
	t.Parallel()
	rights, processor := knownScriptRightsFixture(t, false)
	nonCanonical, err := json.Marshal(rights)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		raw     []byte
		applies bool
	}{
		{name: "unrelated", raw: []byte("private legacy rights"), applies: false},
		{name: "malformed claimed", raw: []byte(`{"contractVersion":"filler-spoken-known-script-rights-v1",`), applies: true},
		{name: "unsupported claimed", raw: []byte(`{"contractVersion":"filler-spoken-known-script-rights-v2"}`), applies: true},
		{name: "non canonical claimed", raw: nonCanonical, applies: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			applies, err := AuthorizeKnownScriptProcessor(test.raw, rights.PreparedAt.Add(time.Hour), processor)
			if applies != test.applies || (test.applies && err == nil) || (!test.applies && err != nil) {
				t.Fatalf("applies=%v err=%v", applies, err)
			}
		})
	}
}

func knownScriptRightsFixture(t *testing.T, withAsset bool) (knownScriptRights, KnownScriptHostedProcessor) {
	t.Helper()
	fixture := newKnownScriptFixture(t)
	member := fixture.authority.Members[0]
	if withAsset {
		for _, candidate := range fixture.authority.Members {
			if len(candidate.Transformation.Assets) != 0 {
				member = candidate
				break
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(fixture.root, filepath.FromSlash(member.Consent.ProcessorSchedule.Path)))
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := decodeKnownScriptJSON[KnownScriptProcessorSchedule](raw)
	if err != nil {
		t.Fatal(err)
	}
	return knownScriptRights{
		SchemaVersion: KnownScriptRightsSchemaVersion, ContractVersion: KnownScriptRightsContractVersion,
		PreparedAt: fixture.config.PreparedAt, AuthoritySHA256: fixtureSHA(9_500),
		ParticipantID: member.ParticipantID, Consent: member.Consent,
		ProcessorSchedule: schedule, Assets: member.Transformation.Assets,
	}, schedule.Processors[0]
}
