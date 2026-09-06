package fillersafetyreview

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
	"github.com/loomarr/loomarr/internal/fillersafetycorpus"
)

func TestRunOpenRouterAcceptsExactParticipantProcessorSchedule(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		positive := strings.Contains(string(body), "positive_candidate")
		writeReviewResponse(t, writer, call, verifiedObservation(positive))
	}))
	fixture := newReviewFixture(t, endpoint.baseURL)
	installKnownScriptRights(t, &fixture, endpoint.baseURL, nil, func(processor *fillersafetycorpus.KnownScriptHostedProcessor) {})

	result, err := runOpenRouter(t.Context(), fixture.config, fixture.runtime(endpoint.client, endpoint.baseURL))
	if err != nil {
		t.Fatal(err)
	}
	if result.Cases != fillersafetycert.MinimumPositiveFamilies+fillersafetycert.MinimumCleanFamilies ||
		calls.Load() != int64(result.Cases) {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
}

func TestRunOpenRouterRejectsUnconsentedProcessorBeforeSideEffects(t *testing.T) {
	t.Parallel()
	var calls, identityCalls atomic.Int64
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	fixture := newReviewFixture(t, endpoint.baseURL)
	installKnownScriptRights(t, &fixture, endpoint.baseURL, nil, func(processor *fillersafetycorpus.KnownScriptHostedProcessor) {
		processor.RequestedModel = "vendor/unconsented-reviewer"
	})
	runtime := fixture.runtime(endpoint.client, endpoint.baseURL)
	runtime.identify = func(context.Context, string) (fillersafety.ToolIdentity, string, error) {
		identityCalls.Add(1)
		return fillersafety.ToolIdentity{}, "", nil
	}

	_, err := runOpenRouter(t.Context(), fixture.config, runtime)
	if err == nil || !strings.Contains(err.Error(), "processor authorization") {
		t.Fatalf("err=%v", err)
	}
	if calls.Load() != 0 || identityCalls.Load() != 0 {
		t.Fatalf("side effects before consent rejection: HTTP=%d identity=%d", calls.Load(), identityCalls.Load())
	}
	if strings.Contains(err.Error(), fixture.root) || strings.Contains(err.Error(), "participant-private") ||
		strings.Contains(err.Error(), "unconsented-reviewer") {
		t.Fatalf("consent rejection leaked private identity: %v", err)
	}
	for _, path := range []string{fixture.checkpointDir, fixture.outputPath} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("side-effect path exists after consent rejection: %v", statErr)
		}
	}
}

func TestRunOpenRouterRechecksParticipantConsentBeforeEveryRequest(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	var expired atomic.Bool
	endpoint := newReviewTestEndpoint(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		call := calls.Add(1)
		writeReviewResponse(t, writer, call, verifiedObservation(true))
		expired.Store(true)
	}))
	fixture := newReviewFixture(t, endpoint.baseURL)
	expiresAt := fixture.now.Add(30 * time.Minute)
	installKnownScriptRights(t, &fixture, endpoint.baseURL, &expiresAt, func(processor *fillersafetycorpus.KnownScriptHostedProcessor) {})
	runtime := fixture.runtime(endpoint.client, endpoint.baseURL)
	runtime.now = func() time.Time {
		if expired.Load() {
			return fixture.now.Add(time.Hour)
		}
		return fixture.now
	}

	_, err := runOpenRouter(t.Context(), fixture.config, runtime)
	if err == nil || !strings.Contains(err.Error(), "processor authorization changed") || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	if _, statErr := os.Lstat(fixture.outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("review published after consent expiry: %v", statErr)
	}
}

func installKnownScriptRights(
	t *testing.T,
	fixture *reviewFixture,
	baseURL string,
	expiresAt *time.Time,
	mutate func(*fillersafetycorpus.KnownScriptHostedProcessor),
) {
	t.Helper()
	plan, _, err := readPrivateJSON[Plan](fixture.planPath, maximumPlanBytes)
	if err != nil {
		t.Fatal(err)
	}
	processor := fillersafetycorpus.KnownScriptHostedProcessor{
		Kind:           fillersafetycorpus.KnownScriptProcessorOpenRouter,
		SourceBaseURL:  baseURL,
		RequestedModel: plan.Model, ResolvedModel: plan.ResolvedModel,
		UpstreamProvider: plan.UpstreamProvider, UpstreamProviderSlug: plan.UpstreamProviderSlug,
		ZDR: true,
	}
	mutate(&processor)
	preparedAt := fixture.now.Add(-3 * time.Hour)
	authority := func(path string, seed int) fillersafetycorpus.FileAuthority {
		return fillersafetycorpus.FileAuthority{Path: path, SHA256: testFixtureSHA(seed), Bytes: int64(seed)}
	}
	consent := fillersafetycorpus.KnownScriptConsent{
		SchemaVersion:           fillersafetycorpus.KnownScriptConsentSchemaVersion,
		ContractVersion:         fillersafetycorpus.KnownScriptConsentContractVersion,
		ParticipantID:           "participant-private-001",
		Document:                authority("consent/document.bin", 101),
		SignerAuthorityEvidence: authority("consent/signer.json", 102),
		ProcessorSchedule:       authority("consent/processors.json", 103),
		WithdrawalInstructions:  authority("consent/withdrawal.txt", 104),
		SignedAt:                preparedAt.Add(-3 * time.Hour), RightsReviewedAt: preparedAt.Add(-2 * time.Hour),
		RightsReviewerID:    "owner-rights-reviewer",
		ExpiresAt:           expiresAt,
		RedistributionScope: fillersafetycorpus.KnownScriptRedistributionPrivate,
		RetentionPolicy:     fillersafetycorpus.KnownScriptRetentionWithdrawal,
		WithdrawalSupported: true, NoEndorsement: true,
		Grants: fillersafetycorpus.KnownScriptConsentGrants{
			Collection: true, PrivateStorage: true, TechnicalModification: true,
			EvidenceExtraction: true, IndependentReview: true, HostedModelEvaluation: true,
		},
	}
	rights := struct {
		SchemaVersion     int                                             `json:"schemaVersion"`
		ContractVersion   string                                          `json:"contractVersion"`
		PreparedAt        time.Time                                       `json:"preparedAt"`
		AuthoritySHA256   string                                          `json:"authoritySha256"`
		ParticipantID     string                                          `json:"participantId"`
		Consent           fillersafetycorpus.KnownScriptConsent           `json:"consent"`
		ProcessorSchedule fillersafetycorpus.KnownScriptProcessorSchedule `json:"processorSchedule"`
		Assets            []fillersafetycorpus.KnownScriptAsset           `json:"assets"`
	}{
		SchemaVersion:   fillersafetycorpus.KnownScriptRightsSchemaVersion,
		ContractVersion: fillersafetycorpus.KnownScriptRightsContractVersion,
		PreparedAt:      preparedAt, AuthoritySHA256: testFixtureSHA(105), ParticipantID: consent.ParticipantID,
		Consent: consent,
		ProcessorSchedule: fillersafetycorpus.KnownScriptProcessorSchedule{
			SchemaVersion:   fillersafetycorpus.KnownScriptProcessorSchemaVersion,
			ContractVersion: fillersafetycorpus.KnownScriptProcessorContractVersion,
			Processors:      []fillersafetycorpus.KnownScriptHostedProcessor{processor},
		},
		Assets: []fillersafetycorpus.KnownScriptAsset{},
	}
	rightsPath := filepath.Join(fixture.root, "evidence", "rights.json")
	rightsRaw := marshalPrivateJSON(t, rightsPath, rights)

	draftPath := filepath.Join(fixture.root, "draft.json")
	draft, _, err := readPrivateJSON[fillersafetycert.AuthorityDraft](draftPath, maximumDocumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	worklistPath := filepath.Join(fixture.root, "primary-review-one.json")
	worklist, _, err := readPrivateJSON[fillersafetycorpus.ReviewWorklist](worklistPath, maximumDocumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	for index := range draft.Cases {
		draft.Cases[index].RightsSHA256 = testHash(rightsRaw)
		worklist.Cases[index].RightsSHA256 = testHash(rightsRaw)
		worklist.Cases[index].RightsBytes = int64(len(rightsRaw))
	}
	draftRaw, draftSHA, err := fillersafetycert.MarshalCertificationDraft(draft)
	if err != nil {
		t.Fatal(err)
	}
	writePrivateTestFile(t, draftPath, draftRaw)
	worklist.DraftSHA256 = draftSHA
	worklistRaw := marshalPrivateJSON(t, worklistPath, worklist)
	plan.Draft = testAuthority("draft.json", draftRaw)
	plan.Worklist = testAuthority("primary-review-one.json", worklistRaw)
	marshalPrivateJSON(t, fixture.planPath, plan)
}
