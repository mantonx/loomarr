package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/api"
	"gopkg.in/yaml.v3"
)

func TestFillerRightsCurrentRequiresAllScopeFields(t *testing.T) {
	specBytes, err := api.ExportOpenAPI(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name     string `yaml:"name"`
				Required bool   `yaml:"required"`
			} `yaml:"parameters"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(specBytes, &spec); err != nil {
		t.Fatal(err)
	}
	params := spec.Paths["/v1/filler/rights/current"]["get"].Parameters
	required := map[string]bool{}
	for _, parameter := range params {
		required[parameter.Name] = parameter.Required
	}
	for _, name := range []string{"sourceId", "acquisitionId", "sourceMasterSha256", "policySha256"} {
		if !required[name] {
			t.Errorf("%s is not required in OpenAPI", name)
		}
	}

	srv, _ := newServer(t)
	for _, name := range []string{"sourceId", "acquisitionId", "sourceMasterSha256", "policySha256"} {
		query := url.Values{
			"sourceId": {"source"}, "acquisitionId": {"acquisition"},
			"sourceMasterSha256": {strings.Repeat("a", 64)}, "policySha256": {strings.Repeat("b", 64)},
		}
		query.Del(name)
		res := do(t, srv, http.MethodGet, "/v1/filler/rights/current?"+query.Encode(), adminToken, "")
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("missing %s = %d, want 422", name, res.StatusCode)
		}
		_ = res.Body.Close()
	}
}

type fillerRightsGrantWire struct {
	SHA256             string     `json:"sha256"`
	SourceID           string     `json:"sourceId"`
	AcquisitionID      string     `json:"acquisitionId"`
	SourceMasterSHA256 string     `json:"sourceMasterSha256"`
	PolicySHA256       string     `json:"policySha256"`
	Use                string     `json:"use"`
	Status             string     `json:"status"`
	Withdrawal         string     `json:"withdrawal"`
	EvidenceSHA256     string     `json:"evidenceSha256"`
	ActorID            string     `json:"actorId"`
	EffectiveAt        time.Time  `json:"effectiveAt"`
	ValidUntil         *time.Time `json:"validUntil"`
	WithdrawnAt        *time.Time `json:"withdrawnAt"`
	SupersedesSHA256   string     `json:"supersedesSha256"`
	RecordedAt         time.Time  `json:"recordedAt"`
}

func TestFillerRightsAuthorityRequiresAdminAndRecordsExactCurrentGrant(t *testing.T) {
	srv, _ := newServer(t)
	scope := fillerRightsAPIScope{
		sourceID: "archive:commercials", acquisitionID: "acquisition-17",
		sourceMasterSHA256: strings.Repeat("1", 64), policySHA256: strings.Repeat("2", 64),
	}
	firstBody := fillerRightsGrantJSON(t, scope, "authorized", "clear", strings.Repeat("3", 64), "")

	for _, request := range []struct {
		method, path, body string
	}{
		{method: http.MethodPost, path: "/v1/filler/rights/grants", body: firstBody},
		{method: http.MethodGet, path: fillerRightsCurrentPath(scope)},
	} {
		res := do(t, srv, request.method, request.path, memberToken, request.body)
		if res.StatusCode != http.StatusForbidden {
			raw, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			t.Fatalf("member %s %s = %d, want 403: %s", request.method, request.path, res.StatusCode, raw)
		}
		_ = res.Body.Close()
	}

	missing := do(t, srv, http.MethodGet, fillerRightsCurrentPath(scope), adminToken, "")
	if missing.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(missing.Body)
		_ = missing.Body.Close()
		t.Fatalf("missing current grant = %d, want 404: %s", missing.StatusCode, raw)
	}
	_ = missing.Body.Close()

	first := decodeFillerRightsGrant(t, do(t, srv, http.MethodPost, "/v1/filler/rights/grants", adminToken, firstBody))
	if first.SHA256 == "" || first.SourceID != scope.sourceID || first.AcquisitionID != scope.acquisitionID ||
		first.SourceMasterSHA256 != scope.sourceMasterSHA256 || first.PolicySHA256 != scope.policySHA256 ||
		first.Use != "filler_broadcast" || first.Status != "authorized" || first.Withdrawal != "clear" ||
		first.EvidenceSHA256 != strings.Repeat("3", 64) || first.ActorID != "api-token" || first.RecordedAt.IsZero() {
		t.Fatalf("recorded grant = %+v", first)
	}

	current := decodeFillerRightsGrant(t, do(t, srv, http.MethodGet, fillerRightsCurrentPath(scope), adminToken, ""))
	if current.SHA256 != first.SHA256 {
		t.Fatalf("current digest = %q, want %q", current.SHA256, first.SHA256)
	}
}

func TestFillerRightsAuthorityRequiresLinearSupersession(t *testing.T) {
	srv, _ := newServer(t)
	scope := fillerRightsAPIScope{
		sourceID: "source-1", acquisitionID: "acquisition-1",
		sourceMasterSHA256: strings.Repeat("4", 64), policySHA256: strings.Repeat("5", 64),
	}
	first := decodeFillerRightsGrant(t, do(t, srv, http.MethodPost, "/v1/filler/rights/grants", adminToken,
		fillerRightsGrantJSON(t, scope, "unknown", "unknown", strings.Repeat("6", 64), "")))
	second := decodeFillerRightsGrant(t, do(t, srv, http.MethodPost, "/v1/filler/rights/grants", adminToken,
		fillerRightsGrantJSON(t, scope, "authorized", "clear", strings.Repeat("7", 64), first.SHA256)))
	if second.SupersedesSHA256 != first.SHA256 || second.SHA256 == first.SHA256 {
		t.Fatalf("second grant = %+v", second)
	}

	stale := do(t, srv, http.MethodPost, "/v1/filler/rights/grants", adminToken,
		fillerRightsGrantJSON(t, scope, "prohibited", "clear", strings.Repeat("8", 64), first.SHA256))
	if stale.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(stale.Body)
		_ = stale.Body.Close()
		t.Fatalf("stale supersession = %d, want 409: %s", stale.StatusCode, raw)
	}
	_ = stale.Body.Close()

	current := decodeFillerRightsGrant(t, do(t, srv, http.MethodGet, fillerRightsCurrentPath(scope), adminToken, ""))
	if current.SHA256 != second.SHA256 {
		t.Fatalf("stale write replaced current authority: got %q want %q", current.SHA256, second.SHA256)
	}
}

func TestFillerRightsAuthorityRejectsInvalidGrantBeforePersistence(t *testing.T) {
	srv, _ := newServer(t)
	scope := fillerRightsAPIScope{
		sourceID: "source-2", acquisitionID: "acquisition-2",
		sourceMasterSHA256: strings.Repeat("9", 64), policySHA256: strings.Repeat("a", 64),
	}
	body := fillerRightsGrantJSON(t, scope, "authorized", "unknown", strings.Repeat("b", 64), "")
	res := do(t, srv, http.MethodPost, "/v1/filler/rights/grants", adminToken, body)
	if res.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		t.Fatalf("invalid grant = %d, want 422: %s", res.StatusCode, raw)
	}
	_ = res.Body.Close()

	missing := do(t, srv, http.MethodGet, fillerRightsCurrentPath(scope), adminToken, "")
	if missing.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(missing.Body)
		_ = missing.Body.Close()
		t.Fatalf("invalid grant persisted: current = %d: %s", missing.StatusCode, raw)
	}
	_ = missing.Body.Close()
}

type fillerRightsAPIScope struct {
	sourceID, acquisitionID, sourceMasterSHA256, policySHA256 string
}

func fillerRightsGrantJSON(t *testing.T, scope fillerRightsAPIScope, status, withdrawal, evidenceSHA256, supersedesSHA256 string) string {
	t.Helper()
	body := map[string]any{
		"sourceId": scope.sourceID, "acquisitionId": scope.acquisitionID,
		"sourceMasterSha256": scope.sourceMasterSHA256, "policySha256": scope.policySHA256,
		"status": status, "withdrawal": withdrawal, "evidenceSha256": evidenceSHA256,
		"effectiveAt": time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC),
	}
	if supersedesSHA256 != "" {
		body["supersedesSha256"] = supersedesSHA256
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func fillerRightsCurrentPath(scope fillerRightsAPIScope) string {
	values := url.Values{}
	values.Set("sourceId", scope.sourceID)
	values.Set("acquisitionId", scope.acquisitionID)
	values.Set("sourceMasterSha256", scope.sourceMasterSHA256)
	values.Set("policySha256", scope.policySHA256)
	return "/v1/filler/rights/current?" + values.Encode()
}

func decodeFillerRightsGrant(t *testing.T, res *http.Response) fillerRightsGrantWire {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("filler rights response = %d: %s", res.StatusCode, raw)
	}
	var body fillerRightsGrantWire
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode filler rights response: %v: %s", err, raw)
	}
	return body
}
