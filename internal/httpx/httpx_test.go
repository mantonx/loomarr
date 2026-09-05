package httpx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/testkit/httpfixture"
)

type fixedIPLookup struct{ addrs []net.IPAddr }

func (r fixedIPLookup) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.addrs, nil
}

type contextBody struct{ ctx context.Context }

func (b *contextBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (*contextBody) Close() error { return nil }

func TestNew_WholeRequestTimeoutAbortsSlowBody(t *testing.T) {
	transport := httpfixture.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: &contextBody{ctx: req.Context()}}, nil
	})
	c := newClient(20*time.Millisecond, transport, nil)
	resp, err := c.Get("http://service/stream")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("expected whole-request timeout to abort the response body")
	}
}

func TestNewStreaming_HasNoWholeRequestTimeout(t *testing.T) {
	transport := httpfixture.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("one\ntwo\nthree\n")),
		}, nil
	})
	c := newStreamingClient(transport)
	if c.Timeout != 0 {
		t.Fatalf("NewStreaming timeout = %v, want zero", c.Timeout)
	}
	resp, err := c.Get("http://service/stream")
	if err != nil {
		t.Fatalf("streaming GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if string(body) != "one\ntwo\nthree\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestNamedClientIsTransparent(t *testing.T) {
	transport := httpfixture.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil
	})
	c := newClient(5*time.Second, transport, nil)
	if c.Timeout != 5*time.Second {
		t.Fatalf("NewNamed timeout = %v, want 5s", c.Timeout)
	}
	resp, err := c.Get("http://service/")
	if err != nil {
		t.Fatalf("instrumented GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
}

func TestNewNamedObservedRecordsIntoTheSuppliedGeneration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	recorder := metrics.New(metrics.Options{})

	response, err := NewNamedObserved("library", time.Second, recorder).Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	scrape := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	want := `loomarr_outbound_requests_total{code="204",target="library"} 1`
	if !strings.Contains(scrape.Body.String(), want) {
		t.Errorf("generation scrape does not contain %q", want)
	}
}

func TestRedirectPolicy(t *testing.T) {
	newRedirectClient := func() (*http.Client, *int) {
		calls := 0
		transport := httpfixture.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{"Location": []string{"http://service/final"}},
					Body:       http.NoBody,
					Request:    req,
				}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
		})
		return newClient(time.Second, transport, nil), &calls
	}

	get, getCalls := newRedirectClient()
	resp, err := get.Get("http://service/start")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || *getCalls != 2 {
		t.Fatalf("GET redirect status=%d calls=%d, want 200 and 2", resp.StatusCode, *getCalls)
	}

	post, postCalls := newRedirectClient()
	req, _ := http.NewRequest(http.MethodPost, "http://service/start", nil)
	resp, err = post.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound || *postCalls != 1 {
		t.Fatalf("POST redirect status=%d calls=%d, want 302 and 1", resp.StatusCode, *postCalls)
	}
}

func TestPublicDialPinsResolvedPublicAddressAndRejectsAnyPrivateAnswer(t *testing.T) {
	called := false
	dial := publicDialContext(fixedIPLookup{addrs: []net.IPAddr{
		{IP: net.ParseIP("203.0.113.10")},
		{IP: net.ParseIP("192.168.1.20")},
	}}, func(context.Context, string, string) (net.Conn, error) {
		called = true
		return nil, errors.New("unexpected dial")
	})
	if _, err := dial(context.Background(), "tcp", "reference.example:443"); err == nil {
		t.Fatal("mixed public/private DNS result accepted")
	}
	if called {
		t.Fatal("network dial occurred after a private DNS answer")
	}

	var address string
	dial = publicDialContext(fixedIPLookup{addrs: []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}},
		func(_ context.Context, _, got string) (net.Conn, error) {
			address = got
			return nil, errors.New("dial stopped by test")
		})
	_, _ = dial(context.Background(), "tcp", "reference.example:443")
	if address != "203.0.113.10:443" {
		t.Fatalf("dial address = %q, want pinned public IP", address)
	}
}
