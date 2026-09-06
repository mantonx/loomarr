package fillerstructurewindowopenrouter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

type capturedLedger struct {
	state        fillerstructurewindow.CallReservationState
	reserveErr   error
	settleErr    error
	reservations []fillerstructurewindow.CallReservation
	settlements  []fillerstructurewindow.CallRecord
	order        []string
	settleCtxErr error
}

func (l *capturedLedger) Reserve(_ context.Context, reservation fillerstructurewindow.CallReservation) (fillerstructurewindow.CallReservationState, error) {
	l.order = append(l.order, "reserve")
	l.reservations = append(l.reservations, reservation)
	return l.state, l.reserveErr
}

func (l *capturedLedger) Settle(ctx context.Context, record fillerstructurewindow.CallRecord) error {
	l.order = append(l.order, "settle")
	l.settlements = append(l.settlements, record)
	l.settleCtxErr = ctx.Err()
	return l.settleErr
}

func TestAssessorReturnsAcceptedSourceRelativeWindowAfterDurableReservation(t *testing.T) {
	ledger := &capturedLedger{state: fillerstructurewindow.CallReservationAccepted}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		ledger.order = append(ledger.order, "call")
		_, _ = response.Write([]byte(successResponse(`{"segments":[{"endMs":5000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"},{"endMs":10000,"role":"promo","decisiveAtMs":[7000],"reason":"promotion"}]}`, "resolved-model")))
	}))
	defer server.Close()
	set, media := assessorMediaFixture(t)
	assessor := assessorFixture(t, server.URL, server.Client(), ledger)
	recorded, err := assessor.AssessWindow(t.Context(), set, media)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded.Assessment.Segments) != 2 || recorded.Assessment.Segments[0].StartMS != media.Window.MediaStartMS ||
		recorded.Assessment.Segments[1].EndMS != media.Window.MediaEndMS ||
		!slicesEqual(ledger.order, []string{"reserve", "call", "settle"}) || len(ledger.reservations) != 1 || len(ledger.settlements) != 1 ||
		recorded.Record.State != fillerstructure.AssessmentRecordAccepted || recorded.Record.RequestSHA256 != ledger.reservations[0].RequestSHA256 ||
		recorded.Record.SHA256 != ledger.settlements[0].SHA256 || ledger.reservations[0].MediaSet.SHA256 != set.SHA256 ||
		ledger.reservations[0].WindowOrdinal != media.Window.Ordinal || ledger.reservations[0].MaximumChargeNanoUSD != 1_500 {
		t.Fatalf("order=%v reservation=%+v recorded=%+v", ledger.order, ledger.reservations, recorded)
	}
}

func TestAssessorRecordsBudgetHoldWithoutCallingProvider(t *testing.T) {
	ledger := &capturedLedger{state: fillerstructurewindow.CallReservationHeldBudget}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	set, media := assessorMediaFixture(t)
	recorded, err := assessorFixture(t, server.URL, server.Client(), ledger).AssessWindow(t.Context(), set, media)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || recorded.Record.State != fillerstructure.AssessmentRecordHeldBudget ||
		recorded.Assessment.Failure != fillerstructure.AssessmentFailureBudget ||
		!slicesEqual(ledger.order, []string{"reserve", "settle"}) {
		t.Fatalf("calls=%d order=%v recorded=%+v", calls, ledger.order, recorded)
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
		{name: "invalid structured output", response: successResponse(`{"segments":[]}`, "resolved-model"), wantState: fillerstructure.AssessmentRecordFailed, wantFail: fillerstructure.AssessmentFailureInvalidResponse},
		{name: "route mismatch", response: successResponse(`{"segments":[{"endMs":10000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"}]}`, "different-model"), wantState: fillerstructure.AssessmentRecordFailed, wantFail: fillerstructure.AssessmentFailureRouteMismatch},
		{name: "unknown transport settlement", client: func(*httptest.Server) *http.Client {
			return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("network lost") })}
		}, wantState: fillerstructure.AssessmentRecordUnsettled, wantFail: fillerstructure.AssessmentFailureTransport},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ledger := &capturedLedger{state: fillerstructurewindow.CallReservationAccepted}
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte(test.response)) }))
			defer server.Close()
			client := server.Client()
			if test.client != nil {
				client = test.client(server)
			}
			set, media := assessorMediaFixture(t)
			recorded, err := assessorFixture(t, server.URL, client, ledger).AssessWindow(t.Context(), set, media)
			if err != nil || recorded.Record.State != test.wantState || recorded.Assessment.Failure != test.wantFail || len(recorded.Assessment.Segments) != 0 {
				t.Fatalf("recorded=%+v error=%v", recorded, err)
			}
		})
	}
}

func TestAssessorSettlesWithDetachedContextAfterCallerCancellation(t *testing.T) {
	ledger := &capturedLedger{state: fillerstructurewindow.CallReservationAccepted}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(successResponse(`{"segments":[{"endMs":10000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"}]}`, "resolved-model")))
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	nowCalls := 0
	config := assessorConfig(server.URL, server.Client(), ledger)
	config.Now = func() time.Time {
		nowCalls++
		if nowCalls == 2 {
			cancel()
		}
		return time.Date(2026, time.September, 12, 5, 0, nowCalls, 0, time.UTC)
	}
	assessor, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	set, media := assessorMediaFixture(t)
	if _, err := assessor.AssessWindow(ctx, set, media); err != nil {
		t.Fatal(err)
	}
	if ledger.settleCtxErr != nil || ctx.Err() == nil {
		t.Fatalf("settlement context=%v caller context=%v", ledger.settleCtxErr, ctx.Err())
	}
}

func TestAssessorRejectsWindowMediaByteDriftBeforeReservationOrCall(t *testing.T) {
	ledger := &capturedLedger{state: fillerstructurewindow.CallReservationAccepted}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	set, media := assessorMediaFixture(t)
	if err := os.WriteFile(media.FullPath, []byte("different fixture bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := assessorFixture(t, server.URL, server.Client(), ledger).AssessWindow(t.Context(), set, media); err == nil {
		t.Fatal("expected window media byte drift to fail")
	}
	if calls != 0 || len(ledger.reservations) != 0 || len(ledger.settlements) != 0 {
		t.Fatalf("calls=%d reservations=%d settlements=%d", calls, len(ledger.reservations), len(ledger.settlements))
	}
}

func TestAssessorDoesNotRepeatProviderCallWhenDurableReservationConflicts(t *testing.T) {
	ledger := &capturedLedger{reserveErr: fillerstructurewindow.ErrCallLedgerConflict}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	set, media := assessorMediaFixture(t)
	if _, err := assessorFixture(t, server.URL, server.Client(), ledger).AssessWindow(t.Context(), set, media); err == nil {
		t.Fatal("existing durable reservation did not stop the repeated operation")
	}
	if calls != 0 || len(ledger.reservations) != 1 || len(ledger.settlements) != 0 {
		t.Fatalf("calls=%d reservations=%d settlements=%d", calls, len(ledger.reservations), len(ledger.settlements))
	}
}

func TestAssessorRetainsKnownOverReservationAsOperationalEvidence(t *testing.T) {
	ledger := &capturedLedger{state: fillerstructurewindow.CallReservationAccepted}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		raw := strings.Replace(successResponse(`{"segments":[{"endMs":10000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"}]}`, "resolved-model"), `"cost":0.000001`, `"cost":0.000003`, 1)
		_, _ = response.Write([]byte(raw))
	}))
	defer server.Close()
	set, media := assessorMediaFixture(t)
	recorded, err := assessorFixture(t, server.URL, server.Client(), ledger).AssessWindow(t.Context(), set, media)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Record.State != fillerstructure.AssessmentRecordOverReservation || recorded.Record.ChargedNanoUSD != 3_000 ||
		recorded.Record.AccountedNanoUSD != 3_000 || recorded.Assessment.Failure != fillerstructure.AssessmentFailureOverReservation {
		t.Fatalf("recorded=%+v", recorded)
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
			PromptVersion: fillerstructurewindow.DirectVideoPromptVersion, EvidenceContract: fillerstructurewindow.CallRecordContractVersion,
		},
		MetadataSnapshotSHA256: strings.Repeat("c", 64),
		APIKey:                 "test-key", BaseURL: baseURL, Model: "requested-model", ResolvedModel: "resolved-model",
		UpstreamProvider: "Provider", UpstreamProviderSlug: "provider", ReservationNanoUSD: 2_000,
		MaximumChargeNanoUSD: 1_500, MaxTokens: 1024, DisableReasoning: true,
		AllowInsecureTestURL: true, Client: client, Ledger: ledger,
		Now: func() time.Time { return time.Date(2026, time.September, 12, 5, 0, 0, 0, time.UTC) },
	}
}

func assessorMediaFixture(t *testing.T) (fillerstructurewindow.MediaSet, filler.StructureAssessmentWindowMedia) {
	t.Helper()
	raw := []byte("fixture window mp4 bytes")
	digest := sha256.Sum256(raw)
	path := filepath.Join(t.TempDir(), "window.mp4")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	source := fillerstructure.Source{SHA256: strings.Repeat("1", 64), Bytes: 2_048, DurationMS: 10_000}
	plan, err := fillerstructurewindow.NewPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	assessmentMedia := fillerstructure.AssessmentMedia{
		SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(raw)), DurationMS: 10_000,
		ProfileSHA256: plan.Profile.AssessmentMediaProfileSHA256, LineageSHA256: strings.Repeat("e", 64),
	}
	set, err := fillerstructurewindow.NewMediaSet(plan, []fillerstructure.AssessmentMedia{assessmentMedia})
	if err != nil {
		t.Fatal(err)
	}
	return set, filler.StructureAssessmentWindowMedia{Window: plan.Windows[0], Media: set.Windows[0], FullPath: path}
}

func successResponse(content, selectedModel string) string {
	return `{"id":"generation","model":"requested-model","choices":[{"message":{"content":` + strconv.Quote(content) + `}}],"usage":{"prompt_tokens":100,"completion_tokens":20,"cost":0.000001},"openrouter_metadata":{"attempt":1,"endpoints":{"available":[{"provider":"Provider","model":"` + selectedModel + `","selected":true}]},"attempts":[{"provider":"Provider","model":"` + selectedModel + `","status":200}]}}`
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }
