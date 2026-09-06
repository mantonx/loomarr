package fillercorpus

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCaptureMetInventoryFreezesOnlyObjectValidatedPublicDomainCandidate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /public/collection/v1/search":
			if request.URL.Query().Get("hasImages") != "true" || request.URL.Query().Get("q") != "venus" {
				t.Fatalf("search query = %q", request.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"total":1,"objectIDs":[195733]}`))
		case "GET /public/collection/v1/objects/195733":
			if request.Header.Get("X-Test-Original-Host") != metAPIHost {
				t.Fatalf("object host = %q", request.Header.Get("X-Test-Original-Host"))
			}
			_, _ = w.Write([]byte(`{"objectID":195733,"isPublicDomain":true,"primaryImage":"https://images.metmuseum.org/CRDImages/es/original/DP-919-001.jpg","title":"Venus","artistDisplayName":"Massimiliano Soldani","objectDate":"18th century","objectURL":"https://www.metmuseum.org/art/collection/search/195733","repository":"Metropolitan Museum of Art, New York, NY","creditLine":"Purchase, 1916","tags":[{"term":"Female Nudes"},{"term":"Sculpture"}]}`))
		case "HEAD /CRDImages/es/original/DP-919-001.jpg":
			if request.Header.Get("X-Test-Original-Host") != metImageHost {
				t.Fatalf("image host = %q", request.Header.Get("X-Test-Original-Host"))
			}
			if request.URL.Query().Has("download") || request.URL.Query().Get("loomarr") == "" || len(request.URL.Query()) != 1 {
				t.Fatalf("image query = %q", request.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", "1156190")
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()

	config := metTestConfig(t, server)
	inventory, err := CaptureMetInventory(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if failures := ValidateInventory(inventory); len(failures) != 0 {
		t.Fatalf("inventory failures = %v", failures)
	}
	if len(inventory.Captures) != 1 || inventory.Captures[0].RequestsUsed != 3 || inventory.Captures[0].PredictedMediaBytes != 1156190 || inventory.Captures[0].SearchSHA256 == "" {
		t.Fatalf("capture = %+v", inventory.Captures)
	}
	if !strings.HasPrefix(inventory.Captures[0].Collection, "selection-sha256:") {
		t.Fatalf("selection identity = %q", inventory.Captures[0].Collection)
	}
	item := inventory.Cases[0]
	if !inventory.Captures[0].SnapshotAt.Equal(inventory.SnapshotAt) ||
		!inventory.SnapshotAt.Before(config.SnapshotAt) || inventory.SnapshotAt.Before(item.MetadataRetrievedAt) {
		t.Fatalf("snapshot=%s ceiling=%s metadata=%s", inventory.SnapshotAt, config.SnapshotAt, item.MetadataRetrievedAt)
	}
	if item.CaseID != "metmuseum.org/collection/195733" || item.SourceFamily != "met-object:195733" ||
		len(item.Creator) != 1 || item.Creator[0] != "Massimiliano Soldani" || item.MetadataCache == "" ||
		len(item.Collection) != 2 || item.Collection[1] != "search-term:venus" ||
		len(item.SubjectTerms) != 2 || item.SubjectTerms[0] != "Female Nudes" || item.SubjectTerms[1] != "Sculpture" ||
		item.Representation.MIMEType != "image/jpeg" || item.Representation.Bytes != 1156190 ||
		item.Representation.URL != "https://images.metmuseum.org/CRDImages/es/original/DP-919-001.jpg?loomarr="+item.MetadataSHA256 {
		t.Fatalf("case = %+v", item)
	}
}

func TestCaptureMetInventoryRequiresSourceAuthoredSubjectBeforeImageProbe(t *testing.T) {
	headRequests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /public/collection/v1/search":
			_, _ = w.Write([]byte(`{"total":1,"objectIDs":[195733]}`))
		case "GET /public/collection/v1/objects/195733":
			_, _ = w.Write([]byte(`{"objectID":195733,"isPublicDomain":true,"primaryImage":"https://images.metmuseum.org/object.jpg","title":"Decorative vase","artistDisplayName":"Artist","objectURL":"https://www.metmuseum.org/art/collection/search/195733","tags":[{"term":"Flowers"}]}`))
		case "HEAD /object.jpg":
			headRequests++
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	_, err := CaptureMetInventory(context.Background(), metTestConfig(t, server))
	if err == nil || !strings.Contains(err.Error(), "admitted 0 of 1") {
		t.Fatalf("err = %v", err)
	}
	if headRequests != 0 {
		t.Fatalf("untagged candidate triggered %d image requests", headRequests)
	}
}

func TestCaptureMetInventoryRejectsExcludedMinorSubjectBeforeImageProbe(t *testing.T) {
	headRequests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /public/collection/v1/search":
			_, _ = w.Write([]byte(`{"total":1,"objectIDs":[195733]}`))
		case "GET /public/collection/v1/objects/195733":
			_, _ = w.Write([]byte(`{"objectID":195733,"isPublicDomain":true,"primaryImage":"https://images.metmuseum.org/object.jpg","title":"Venus with child","artistDisplayName":"Artist","objectURL":"https://www.metmuseum.org/art/collection/search/195733","tags":[{"term":"Female Nudes"},{"term":"Infants"}]}`))
		case "HEAD /object.jpg":
			headRequests++
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	_, err := CaptureMetInventory(context.Background(), metTestConfig(t, server))
	if err == nil || !strings.Contains(err.Error(), "admitted 0 of 1") {
		t.Fatalf("err = %v", err)
	}
	if headRequests != 0 {
		t.Fatalf("minor-tagged candidate triggered %d image requests", headRequests)
	}
}

func TestCaptureMetInventorySkipsRepeatedCreatorBeforeImageProbe(t *testing.T) {
	selectionDigest := metSelectionDigest([]string{"venus"}, []string{"Female Nudes", "Male Nudes"}, []string{"Children", "Infants"})
	ids := []int64{195733, 392067, 402849}
	slices.SortFunc(ids, func(left, right int64) int {
		return strings.Compare(metObjectRank(selectionDigest, left), metObjectRank(selectionDigest, right))
	})
	duplicateCreator := map[int64]bool{ids[0]: true, ids[1]: true}
	headRequests := 0
	objectRequests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/public/collection/v1/search":
			_, _ = fmt.Fprintf(w, `{"total":3,"objectIDs":[%d,%d,%d]}`, ids[0], ids[1], ids[2])
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/public/collection/v1/objects/"):
			objectRequests++
			idText := path.Base(request.URL.Path)
			id, err := strconv.ParseInt(idText, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			creator := "Distinct Creator"
			if duplicateCreator[id] {
				creator = "Repeated Creator"
			}
			_, _ = fmt.Fprintf(w, `{"objectID":%d,"isPublicDomain":true,"primaryImage":"https://images.metmuseum.org/%d.jpg","title":"Work %d","artistDisplayName":%q,"objectURL":"https://www.metmuseum.org/art/collection/search/%d","tags":[{"term":"Female Nudes"}]}`, id, id, id, creator, id)
		case request.Method == http.MethodHead && strings.HasSuffix(request.URL.Path, ".jpg"):
			headRequests++
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", "100")
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	config := metTestConfig(t, server)
	config.MaxItems = 2
	config.MaxObjectLookups = 3
	config.MaxRequests = 6
	config.MaxItemBytes = 100
	config.MaxTotalBytes = 200
	inventory, err := CaptureMetInventory(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if objectRequests != 3 || headRequests != 2 || len(inventory.Cases) != 2 || inventory.Cases[0].Creator[0] == inventory.Cases[1].Creator[0] {
		t.Fatalf("objects=%d heads=%d cases=%+v", objectRequests, headRequests, inventory.Cases)
	}
}

func TestCaptureMetInventorySkipsStaleSearchObject(t *testing.T) {
	selectionDigest := metSelectionDigest([]string{"venus"}, []string{"Female Nudes", "Male Nudes"}, []string{"Children", "Infants"})
	ids := []int64{195733, 431922}
	slices.SortFunc(ids, func(left, right int64) int {
		return strings.Compare(metObjectRank(selectionDigest, left), metObjectRank(selectionDigest, right))
	})
	staleID, validID := ids[0], ids[1]
	headRequests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/public/collection/v1/search":
			_, _ = fmt.Fprintf(w, `{"total":2,"objectIDs":[%d,%d]}`, staleID, validID)
		case request.Method == http.MethodGet && request.URL.Path == "/public/collection/v1/objects/"+strconv.FormatInt(staleID, 10):
			http.NotFound(w, request)
		case request.Method == http.MethodGet && request.URL.Path == "/public/collection/v1/objects/"+strconv.FormatInt(validID, 10):
			_, _ = fmt.Fprintf(w, `{"objectID":%d,"isPublicDomain":true,"primaryImage":"https://images.metmuseum.org/valid.jpg","title":"Valid work","artistDisplayName":"Valid Creator","objectURL":"https://www.metmuseum.org/art/collection/search/%d","tags":[{"term":"Female Nudes"}]}`, validID, validID)
		case request.Method == http.MethodHead && request.URL.Path == "/valid.jpg":
			headRequests++
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", "100")
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	config := metTestConfig(t, server)
	config.MaxObjectLookups = 2
	config.MaxRequests = 4
	inventory, err := CaptureMetInventory(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if headRequests != 1 || len(inventory.Cases) != 1 || inventory.Cases[0].SourceFamily != "met-object:"+strconv.FormatInt(validID, 10) {
		t.Fatalf("heads=%d cases=%+v", headRequests, inventory.Cases)
	}
}

func TestCaptureMetInventoryDoesNotSkipSourceFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /public/collection/v1/search":
			_, _ = w.Write([]byte(`{"total":1,"objectIDs":[195733]}`))
		case "GET /public/collection/v1/objects/195733":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	config := metTestConfig(t, server)
	config.MaxRequests = 4
	_, err := CaptureMetInventory(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "503 Service Unavailable") {
		t.Fatalf("err = %v", err)
	}
}

func TestCaptureMetInventoryRetriesTransientSourceFailure(t *testing.T) {
	objectRequests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /public/collection/v1/search":
			_, _ = w.Write([]byte(`{"total":1,"objectIDs":[195733]}`))
		case "GET /public/collection/v1/objects/195733":
			objectRequests++
			if objectRequests == 1 {
				http.Error(w, "retry", http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`{"objectID":195733,"isPublicDomain":true,"primaryImage":"https://images.metmuseum.org/valid.jpg","title":"Valid work","artistDisplayName":"Valid Creator","objectURL":"https://www.metmuseum.org/art/collection/search/195733","tags":[{"term":"Female Nudes"}]}`))
		case "HEAD /valid.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", "100")
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	config := metTestConfig(t, server)
	config.MaxRequests = 4
	inventory, err := CaptureMetInventory(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if objectRequests != 2 || len(inventory.Cases) != 1 {
		t.Fatalf("object requests=%d cases=%+v", objectRequests, inventory.Cases)
	}
}

func TestCaptureMetInventorySearchHitCannotGrantPublicDomainStatus(t *testing.T) {
	headRequests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /public/collection/v1/search":
			_, _ = w.Write([]byte(`{"total":1,"objectIDs":[195733]}`))
		case "GET /public/collection/v1/objects/195733":
			_, _ = w.Write([]byte(`{"objectID":195733,"isPublicDomain":false,"primaryImage":"https://images.metmuseum.org/object.jpg","title":"Venus","artistDisplayName":"Artist","objectURL":"https://www.metmuseum.org/art/collection/search/195733"}`))
		case "HEAD /object.jpg":
			headRequests++
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()

	_, err := CaptureMetInventory(context.Background(), metTestConfig(t, server))
	if err == nil || !strings.Contains(err.Error(), "admitted 0 of 1") {
		t.Fatalf("err = %v", err)
	}
	if headRequests != 0 {
		t.Fatalf("search-only candidate triggered %d image requests", headRequests)
	}
}

func TestCaptureMetInventoryRejectsNonCanonicalTermsBeforeTransport(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unexpected request")
	}))
	defer server.Close()
	config := metTestConfig(t, server)
	config.Terms = []string{"venus", "adam and eve"}
	config.MaxRequests = len(config.Terms) + config.MaxObjectLookups + config.MaxItems
	if _, err := CaptureMetInventory(context.Background(), config); err == nil || !strings.Contains(err.Error(), "canonical terms") {
		t.Fatalf("err = %v", err)
	}
}

func TestMetSelectionIdentityChangesWithSubjectAdmissionRule(t *testing.T) {
	terms := []string{"venus"}
	first := metSelectionDigest(terms, []string{"Female Nudes"}, []string{"Infants"})
	second := metSelectionDigest(terms, []string{"Male Nudes"}, []string{"Infants"})
	if first == second || first == metSelectionDigest([]string{"nude"}, []string{"Female Nudes"}, []string{"Infants"}) ||
		first == metSelectionDigest(terms, []string{"Female Nudes"}, []string{"Children"}) {
		t.Fatal("selection identity omitted a search or subject-admission input")
	}
}

func TestCaptureMetInventoryRejectsObservationBeyondSnapshotCeiling(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /public/collection/v1/search":
			_, _ = w.Write([]byte(`{"total":1,"objectIDs":[195733]}`))
		case "GET /public/collection/v1/objects/195733":
			_, _ = w.Write([]byte(`{"objectID":195733,"isPublicDomain":true,"primaryImage":"https://images.metmuseum.org/object.jpg","title":"Valid work","artistDisplayName":"Valid Creator","objectURL":"https://www.metmuseum.org/art/collection/search/195733","tags":[{"term":"Female Nudes"}]}`))
		case "HEAD /object.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", "100")
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	config := metTestConfig(t, server)
	config.SnapshotAt = time.Now().Add(-time.Minute).UTC()
	_, err := CaptureMetInventory(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "source observation exceeded snapshot ceiling") {
		t.Fatalf("err = %v", err)
	}
}

func TestCaptureMetInventoryReportsCacheHitsSeparately(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /public/collection/v1/search":
			_, _ = w.Write([]byte(`{"total":1,"objectIDs":[195733]}`))
		case "GET /public/collection/v1/objects/195733":
			_, _ = w.Write([]byte(`{"objectID":195733,"isPublicDomain":true,"primaryImage":"https://images.metmuseum.org/object.jpg","title":"Valid work","artistDisplayName":"Valid Creator","objectURL":"https://www.metmuseum.org/art/collection/search/195733","tags":[{"term":"Female Nudes"}]}`))
		case "HEAD /object.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", "100")
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	config := metTestConfig(t, server)
	if _, err := CaptureMetInventory(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	inventory, err := CaptureMetInventory(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Captures[0].RequestsUsed != 0 || inventory.Captures[0].CacheHits != 3 {
		t.Fatalf("capture = %+v", inventory.Captures[0])
	}
}

func metTestConfig(t *testing.T, server *httptest.Server) MetCaptureConfig {
	t.Helper()
	return MetCaptureConfig{
		HTTP: metTestHTTPClient(t, server), CacheDir: t.TempDir(), UserAgent: "Loomarr test",
		Terms: []string{"venus"}, RoleHint: "policy-positive-nomination",
		RequiredSubjectTerms: []string{"Female Nudes", "Male Nudes"},
		ExcludedSubjectTerms: []string{"Children", "Infants"},
		SnapshotAt:           time.Now().Add(time.Hour).UTC(), MaxRequests: 3, MaxObjectLookups: 1, MaxItems: 1,
		MaxResponseBytes: 1 << 20, MaxItemBytes: 2 << 20, MaxTotalBytes: 2 << 20,
		Delay: 100 * time.Millisecond, MaxWallTime: 5 * time.Second,
	}
}

func metTestHTTPClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := server.Client().Transport
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clone.URL = cloneURL(request.URL)
		clone.Header = request.Header.Clone()
		clone.Header.Set("X-Test-Original-Host", request.URL.Hostname())
		clone.URL.Scheme = serverURL.Scheme
		clone.URL.Host = serverURL.Host
		return transport.RoundTrip(clone)
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func cloneURL(source *url.URL) *url.URL {
	value := *source
	return &value
}
