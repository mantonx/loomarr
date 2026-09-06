package fillerstructureopenrouter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

type capturedLedger struct {
	state        fillerstructure.AssessmentReservationState
	reserveErr   error
	settleErr    error
	reservations []fillerstructure.AssessmentReservation
	settlements  []fillerstructure.AssessmentRecord
	order        []string
}

func (l *capturedLedger) Reserve(_ context.Context, reservation fillerstructure.AssessmentReservation) (fillerstructure.AssessmentReservationState, error) {
	l.order = append(l.order, "reserve")
	l.reservations = append(l.reservations, reservation)
	return l.state, l.reserveErr
}

func (l *capturedLedger) Settle(_ context.Context, record fillerstructure.AssessmentRecord) error {
	l.order = append(l.order, "settle")
	l.settlements = append(l.settlements, record)
	return l.settleErr
}

func TestAssessorReturnsAcceptedReplayableAssessmentAfterDurableReservation(t *testing.T) {
	ledger := &capturedLedger{state: fillerstructure.AssessmentReservationAccepted}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		ledger.order = append(ledger.order, "call")
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(successResponse(`{"segments":[{"endMs":5000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"},{"endMs":10000,"role":"promo","decisiveAtMs":[7000],"reason":"promotion"}]}`, "resolved-model")))
	}))
	defer server.Close()
	media := assessorMediaFixture(t)
	assessor := assessorFixture(t, server.URL, server.Client(), ledger)
	recorded, err := assessor.AssessCompleteTimeline(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := recorded.Record.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ledger.order, []string{"reserve", "call", "settle"}) || len(ledger.reservations) != 1 || len(ledger.settlements) != 1 ||
		recorded.Record.State != fillerstructure.AssessmentRecordAccepted || candidate.Unit != fillerstructure.UnitCompilation || len(candidate.Segments) != 2 ||
		recorded.Record.RequestSHA256 != ledger.reservations[0].RequestSHA256 || recorded.Record.SHA256 != ledger.settlements[0].SHA256 ||
		ledger.reservations[0].Source.SHA256 != media.Source.SHA256 || ledger.reservations[0].Media != media.Assessment ||
		ledger.reservations[0].MaximumChargeNanoUSD != 1_500 || ledger.reservations[0].ExpectedResolvedModel != "resolved-model" ||
		ledger.reservations[0].PromptSHA256 != recorded.Record.PromptSHA256 || ledger.reservations[0].SchemaSHA256 != recorded.Record.SchemaSHA256 {
		t.Fatalf("order=%v record=%+v candidate=%+v", ledger.order, recorded.Record, candidate)
	}
}

func TestAssessorRecordsBudgetHoldWithoutCallingProvider(t *testing.T) {
	ledger := &capturedLedger{state: fillerstructure.AssessmentReservationHeldBudget}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	assessor := assessorFixture(t, server.URL, server.Client(), ledger)
	recorded, err := assessor.AssessCompleteTimeline(t.Context(), assessorMediaFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := recorded.Record.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || recorded.Record.State != fillerstructure.AssessmentRecordHeldBudget || recorded.Record.ReservedNanoUSD != 0 ||
		candidate.Failure != fillerstructure.AssessmentFailureBudget || !slices.Equal(ledger.order, []string{"reserve", "settle"}) {
		t.Fatalf("calls=%d order=%v record=%+v candidate=%+v", calls, ledger.order, recorded.Record, candidate)
	}
}

func TestAssessorFailsClosedAcrossProviderOutcomes(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		client    func(*httptest.Server) *http.Client
		wantState fillerstructure.AssessmentRecordState
		wantFail  string
	}{
		{
			name: "invalid structured output", response: successResponse(`{"segments":[]}`, "resolved-model"),
			wantState: fillerstructure.AssessmentRecordFailed, wantFail: fillerstructure.AssessmentFailureInvalidResponse,
		},
		{
			name: "route mismatch", response: successResponse(`{"segments":[{"endMs":10000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"}]}`, "different-model"),
			wantState: fillerstructure.AssessmentRecordFailed, wantFail: fillerstructure.AssessmentFailureRouteMismatch,
		},
		{
			name: "unknown transport settlement", client: func(*httptest.Server) *http.Client {
				return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("network lost") })}
			},
			wantState: fillerstructure.AssessmentRecordUnsettled, wantFail: fillerstructure.AssessmentFailureTransport,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ledger := &capturedLedger{state: fillerstructure.AssessmentReservationAccepted}
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte(test.response)) }))
			defer server.Close()
			client := server.Client()
			if test.client != nil {
				client = test.client(server)
			}
			assessor := assessorFixture(t, server.URL, client, ledger)
			recorded, err := assessor.AssessCompleteTimeline(t.Context(), assessorMediaFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			candidate, candidateErr := recorded.Record.Candidate()
			if candidateErr != nil || recorded.Record.State != test.wantState || candidate.Failure != test.wantFail || candidate.Unit != "" || len(candidate.Segments) != 0 {
				t.Fatalf("record=%+v candidate=%+v error=%v", recorded.Record, candidate, candidateErr)
			}
		})
	}
}

func TestAssessorDoesNotReturnEvidenceWhenSettlementFails(t *testing.T) {
	want := errors.New("ledger unavailable")
	ledger := &capturedLedger{state: fillerstructure.AssessmentReservationAccepted, settleErr: want}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(successResponse(`{"segments":[{"endMs":10000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"}]}`, "resolved-model")))
	}))
	defer server.Close()
	assessor := assessorFixture(t, server.URL, server.Client(), ledger)
	if _, err := assessor.AssessCompleteTimeline(t.Context(), assessorMediaFixture(t)); !errors.Is(err, want) {
		t.Fatalf("error=%v, want settlement failure", err)
	}
}

func TestAssessorRejectsAssessmentMediaByteDriftBeforeReservationOrCall(t *testing.T) {
	ledger := &capturedLedger{state: fillerstructure.AssessmentReservationAccepted}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	media := assessorMediaFixture(t)
	if err := os.WriteFile(media.FullPath, []byte("different fixture bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	assessor := assessorFixture(t, server.URL, server.Client(), ledger)
	if _, err := assessor.AssessCompleteTimeline(t.Context(), media); err == nil {
		t.Fatal("expected assessment-media byte drift to fail")
	}
	if calls != 0 || len(ledger.reservations) != 0 || len(ledger.settlements) != 0 {
		t.Fatalf("calls=%d reservations=%d settlements=%d", calls, len(ledger.reservations), len(ledger.settlements))
	}
}

func TestAssessorRetainsKnownOverReservationAsUnusableEvidence(t *testing.T) {
	ledger := &capturedLedger{state: fillerstructure.AssessmentReservationAccepted}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		raw := strings.Replace(successResponse(`{"segments":[{"endMs":10000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"}]}`, "resolved-model"), `"cost":0.000001`, `"cost":0.000003`, 1)
		_, _ = response.Write([]byte(raw))
	}))
	defer server.Close()
	assessor := assessorFixture(t, server.URL, server.Client(), ledger)
	recorded, err := assessor.AssessCompleteTimeline(t.Context(), assessorMediaFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := recorded.Record.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Record.State != fillerstructure.AssessmentRecordOverReservation ||
		recorded.Record.ChargedNanoUSD != 3_000 || recorded.Record.AccountedNanoUSD != 3_000 ||
		candidate.Failure != fillerstructure.AssessmentFailureOverReservation || len(candidate.Segments) != 0 {
		t.Fatalf("record=%+v candidate=%+v", recorded.Record, candidate)
	}
}

func TestNewRejectsWorstCaseChargeOutsideReservation(t *testing.T) {
	ledger := &capturedLedger{state: fillerstructure.AssessmentReservationAccepted}
	config := assessorConfig("http://example.test", &http.Client{}, ledger)
	config.MaximumChargeNanoUSD = config.ReservationNanoUSD + 1
	if _, err := New(config); err == nil {
		t.Fatal("expected invalid charge ceiling to fail")
	}
}

func assessorFixture(t *testing.T, baseURL string, client *http.Client, ledger Ledger) *Assessor {
	t.Helper()
	assessor, err := New(assessorConfig(baseURL, client, ledger))
	if err != nil {
		t.Fatal(err)
	}
	return assessor
}

func assessorConfig(baseURL string, client *http.Client, ledger Ledger) Config {
	return Config{
		Profile: fillerstructure.AssessorProfile{
			ID: "openrouter-a", ModelFamily: "family-a", Provider: "openrouter", Model: "requested-model",
			ModelDigest: strings.Repeat("a", 64), CapabilitySHA256: strings.Repeat("b", 64),
			PromptVersion: fillerstructure.DirectVideoPromptVersion, EvidenceContract: fillerstructure.AssessmentRecordContractVersion,
		},
		MetadataSnapshotSHA256: strings.Repeat("c", 64),
		APIKey:                 "test-key", BaseURL: baseURL, Model: "requested-model", ResolvedModel: "resolved-model",
		UpstreamProvider: "Provider", UpstreamProviderSlug: "provider", ReservationNanoUSD: 2_000,
		MaximumChargeNanoUSD: 1_500, MaxTokens: 1024, DisableReasoning: true,
		AllowInsecureTestURL: true, Client: client, Ledger: ledger,
		Now: func() time.Time { return time.Date(2026, time.September, 10, 5, 0, 0, 0, time.UTC) },
	}
}

func assessorMediaFixture(t *testing.T) filler.StructureAssessmentMedia {
	t.Helper()
	raw := []byte("fixture mp4 bytes")
	digest := sha256.Sum256(raw)
	path := filepath.Join(t.TempDir(), "conditioned.mp4")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return filler.StructureAssessmentMedia{
		Source: filler.SplitSourceAsset{
			Role: filler.SplitSourceLegacyPlayback, SHA256: strings.Repeat("a", 64), Bytes: 2_048,
			ClipHash: strings.Repeat("c", 64), Path: "conditioned.mp4", DurationMs: 10_000,
		},
		Assessment: fillerstructure.AssessmentMedia{
			SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(raw)), DurationMS: 10_000,
			ProfileSHA256: strings.Repeat("d", 64), LineageSHA256: strings.Repeat("e", 64),
		},
		FullPath: path,
	}
}

func successResponse(content, selectedModel string) string {
	return `{"id":"generation","model":"requested-model","choices":[{"message":{"content":` + strconv.Quote(content) + `}}],"usage":{"prompt_tokens":100,"completion_tokens":20,"cost":0.000001},"openrouter_metadata":{"attempt":1,"endpoints":{"available":[{"provider":"Provider","model":"` + selectedModel + `","selected":true}]},"attempts":[{"provider":"Provider","model":"` + selectedModel + `","status":200}]}}`
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }
