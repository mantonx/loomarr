package fillersafetycorpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareKnownScriptRejectsConsentFailuresBeforeMedia(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*KnownScriptConsent, time.Time)
	}{
		{name: "withdrawn", mutate: func(consent *KnownScriptConsent, at time.Time) { consent.WithdrawnAt = &at }},
		{name: "expired", mutate: func(consent *KnownScriptConsent, at time.Time) { consent.ExpiresAt = &at }},
		{name: "hosted grant", mutate: func(consent *KnownScriptConsent, _ time.Time) { consent.Grants.HostedModelEvaluation = false }},
		{name: "endorsement", mutate: func(consent *KnownScriptConsent, _ time.Time) { consent.NoEndorsement = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newKnownScriptFixture(t)
			test.mutate(&fixture.authority.Members[0].Consent, fixture.config.PreparedAt)
			writePrivateJSONFixture(t, fixture.authorityPath, fixture.authority)
			if _, err := prepareKnownScript(t.Context(), fixture.config, fixture.wrapper); err == nil ||
				!strings.Contains(err.Error(), "consent") {
				t.Fatalf("err=%v", err)
			}
			if fixture.media.wraps.Calls() != 0 || fixture.media.identities.Calls() != 0 {
				t.Fatalf("media touched before consent rejection: %d/%d", fixture.media.wraps.Calls(), fixture.media.identities.Calls())
			}
		})
	}
}

func TestPrepareKnownScriptRejectsEvidenceAndMappingDriftBeforeMedia(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *knownScriptFixture)
	}{
		{name: "selected audio", mutate: func(t *testing.T, fixture *knownScriptFixture) {
			path := filepath.Join(fixture.root, filepath.FromSlash(fixture.authority.Members[0].SelectedAudio.Path))
			if err := os.WriteFile(path, []byte("changed private audio"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "policy mapping", mutate: func(t *testing.T, fixture *knownScriptFixture) {
			fixture.authority.Members[0].PositiveIntervals[0].EndMS++
			writePrivateJSONFixture(t, fixture.authorityPath, fixture.authority)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newKnownScriptFixture(t)
			test.mutate(t, fixture)
			if _, err := prepareKnownScript(t.Context(), fixture.config, fixture.wrapper); err == nil {
				t.Fatal("expected drift rejection")
			}
			if fixture.media.wraps.Calls() != 0 || fixture.media.identities.Calls() != 0 {
				t.Fatalf("media touched before evidence rejection: %d/%d", fixture.media.wraps.Calls(), fixture.media.identities.Calls())
			}
		})
	}
}

func TestPrepareKnownScriptRejectsUnauthorizedProcessorScheduleBeforeMedia(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*KnownScriptHostedProcessor)
	}{
		{name: "non HTTPS", mutate: func(processor *KnownScriptHostedProcessor) {
			processor.SourceBaseURL = "http://openrouter.invalid/api/v1"
		}},
		{name: "credentials", mutate: func(processor *KnownScriptHostedProcessor) {
			processor.SourceBaseURL = "https://secret@openrouter.invalid/api/v1"
		}},
		{name: "query", mutate: func(processor *KnownScriptHostedProcessor) {
			processor.SourceBaseURL = "https://openrouter.invalid/api/v1?route=other"
		}},
		{name: "ambiguous model", mutate: func(processor *KnownScriptHostedProcessor) {
			processor.RequestedModel = "vendor / reviewer"
		}},
		{name: "non ZDR", mutate: func(processor *KnownScriptHostedProcessor) {
			processor.ZDR = false
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newKnownScriptFixture(t)
			schedule := KnownScriptProcessorSchedule{
				SchemaVersion:   KnownScriptProcessorSchemaVersion,
				ContractVersion: KnownScriptProcessorContractVersion,
				Processors: []KnownScriptHostedProcessor{{
					Kind: KnownScriptProcessorOpenRouter, SourceBaseURL: "https://openrouter.invalid/api/v1",
					RequestedModel: "vendor/reviewer", ResolvedModel: "vendor/reviewer-2026",
					UpstreamProvider: "Pinned Provider", UpstreamProviderSlug: "pinned-provider", ZDR: true,
				}},
			}
			test.mutate(&schedule.Processors[0])
			rewriteKnownScriptProcessorSchedule(t, fixture, schedule)
			if _, err := prepareKnownScript(t.Context(), fixture.config, fixture.wrapper); err == nil ||
				!strings.Contains(err.Error(), "processor schedule") {
				t.Fatalf("err=%v", err)
			}
			if fixture.media.wraps.Calls() != 0 || fixture.media.identities.Calls() != 0 {
				t.Fatalf("media touched before processor rejection: %d/%d", fixture.media.wraps.Calls(), fixture.media.identities.Calls())
			}
		})
	}
}

func TestPrepareKnownScriptRejectsTransformationAndAssetRightsDriftBeforeMedia(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*KnownScriptMember)
	}{
		{name: "transformation output", mutate: func(member *KnownScriptMember) {
			member.Transformation.OutputSHA256 = fixtureSHA(9_999)
		}},
		{name: "asset provider transfer", mutate: func(member *KnownScriptMember) {
			member.Transformation.Assets[0].RightsContract.Grants.ProviderTransfer = false
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newKnownScriptFixture(t)
			memberIndex := 0
			if test.name == "asset provider transfer" {
				for index := range fixture.authority.Members {
					if len(fixture.authority.Members[index].Transformation.Assets) != 0 {
						memberIndex = index
						break
					}
				}
			}
			test.mutate(&fixture.authority.Members[memberIndex])
			writePrivateJSONFixture(t, fixture.authorityPath, fixture.authority)
			if _, err := prepareKnownScript(t.Context(), fixture.config, fixture.wrapper); err == nil ||
				!strings.Contains(err.Error(), "transformation") {
				t.Fatalf("err=%v", err)
			}
			if fixture.media.wraps.Calls() != 0 || fixture.media.identities.Calls() != 0 {
				t.Fatalf("media touched before transformation rejection: %d/%d", fixture.media.wraps.Calls(), fixture.media.identities.Calls())
			}
		})
	}
}

func TestPrepareKnownScriptRejectsUnsafeAndSymlinkedSourcesBeforeMedia(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *knownScriptFixture)
	}{
		{name: "unsafe path", mutate: func(t *testing.T, fixture *knownScriptFixture) {
			fixture.authority.Members[0].SelectedAudio.Path = "../outside.wav"
			writePrivateJSONFixture(t, fixture.authorityPath, fixture.authority)
		}},
		{name: "symlink", mutate: func(t *testing.T, fixture *knownScriptFixture) {
			authority := fixture.authority.Members[0].SelectedAudio
			path := filepath.Join(fixture.root, filepath.FromSlash(authority.Path))
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(fixture.parent, "outside.wav")
			if err := os.WriteFile(target, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newKnownScriptFixture(t)
			test.mutate(t, fixture)
			if _, err := prepareKnownScript(t.Context(), fixture.config, fixture.wrapper); err == nil {
				t.Fatal("expected unsafe source rejection")
			}
			if fixture.media.wraps.Calls() != 0 || fixture.media.identities.Calls() != 0 {
				t.Fatalf("media touched before source rejection: %d/%d", fixture.media.wraps.Calls(), fixture.media.identities.Calls())
			}
		})
	}
}

func TestPrepareKnownScriptCleansPartialStageAndRejectsIdentityDrift(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*wrapperFixture)
	}{
		{name: "wrap failure", mutate: func(wrapper *wrapperFixture) { wrapper.failAt = 3 }},
		{name: "tool identity", mutate: func(wrapper *wrapperFixture) { wrapper.driftIdentity = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newKnownScriptFixture(t)
			test.mutate(fixture.media)
			if _, err := prepareKnownScript(t.Context(), fixture.config, fixture.wrapper); err == nil {
				t.Fatal("expected media failure")
			}
			if _, err := os.Stat(fixture.output); !os.IsNotExist(err) {
				t.Fatalf("output exists after failure: %v", err)
			}
			matches, err := filepath.Glob(filepath.Join(fixture.parent, ".filler-safety-stage-*"))
			if err != nil || len(matches) != 0 {
				t.Fatalf("partial stages=%v err=%v", matches, err)
			}
		})
	}
}

func TestPrepareKnownScriptRejectsEvidenceChangedDuringWrapping(t *testing.T) {
	t.Parallel()
	fixture := newKnownScriptFixture(t)
	document := fixture.authority.Members[0].Consent.Document
	fixture.media.onWrap = func(call int) {
		if call == 1 {
			if err := os.WriteFile(filepath.Join(fixture.root, filepath.FromSlash(document.Path)), []byte("changed consent"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := prepareKnownScript(t.Context(), fixture.config, fixture.wrapper); err == nil ||
		!strings.Contains(err.Error(), "evidence changed") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(fixture.output); !os.IsNotExist(err) {
		t.Fatalf("output exists after concurrent evidence drift: %v", err)
	}
}

func TestPrepareKnownScriptRejectsCoverageAndResourceFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *knownScriptFixture)
	}{
		{name: "unknown slice", mutate: func(t *testing.T, fixture *knownScriptFixture) {
			fixture.authority.Members[0].Slices = []string{"unknown"}
			writePrivateJSONFixture(t, fixture.authorityPath, fixture.authority)
		}},
		{name: "duplicate participant", mutate: func(t *testing.T, fixture *knownScriptFixture) {
			fixture.authority.Members[1].ParticipantID = fixture.authority.Members[0].ParticipantID
			writePrivateJSONFixture(t, fixture.authorityPath, fixture.authority)
		}},
		{name: "input bytes", mutate: func(_ *testing.T, fixture *knownScriptFixture) { fixture.config.MaximumInputBytes = 1 }},
		{name: "output bytes", mutate: func(_ *testing.T, fixture *knownScriptFixture) { fixture.config.MaximumOutputBytes = 1 }},
		{name: "wall time", mutate: func(_ *testing.T, fixture *knownScriptFixture) { fixture.config.MaximumWallTime = 1 }},
		{name: "output inside source", mutate: func(_ *testing.T, fixture *knownScriptFixture) {
			fixture.config.OutputDirectory = filepath.Join(fixture.root, "prepared")
		}},
		{name: "output inside resolved source", mutate: func(t *testing.T, fixture *knownScriptFixture) {
			alias := filepath.Join(fixture.parent, "source-parent-alias")
			if err := os.Symlink(fixture.parent, alias); err != nil {
				t.Fatal(err)
			}
			fixture.config.SourceRoot = filepath.Join(alias, "source")
			fixture.config.OutputDirectory = filepath.Join(fixture.root, "prepared")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newKnownScriptFixture(t)
			test.mutate(t, fixture)
			if _, err := prepareKnownScript(t.Context(), fixture.config, fixture.wrapper); err == nil {
				t.Fatal("expected preparation rejection")
			}
			if _, err := os.Stat(fixture.config.OutputDirectory); !os.IsNotExist(err) {
				t.Fatalf("output exists after rejection: %v", err)
			}
		})
	}
}
