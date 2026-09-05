// Package httpx is the shared outbound HTTP client factory (design §6, §21 phase 1).
//
// Every adapter that talks to an external service builds its client here so the
// resilience defaults live in exactly one place: hard per-service timeouts, and
// jittered-backoff retries for idempotent GETs only. Writes never client-retry —
// write recovery is owned by the idempotent reconcile loops and periodic sweeps
// (§6). A down dependency degrades one feature; it never wedges the process.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/loomarr/loomarr/internal/metrics"
)

// Per-service hard timeouts (§6). Verified reachable at these budgets in Phase 0.
const (
	TimeoutLibrary       = 10 * time.Second // Emby/Jellyfin
	TimeoutSeerr         = 10 * time.Second
	TimeoutTMDB          = 10 * time.Second
	TimeoutArr           = 10 * time.Second // Sonarr/Radarr
	TimeoutTunarr        = 20 * time.Second // lineup pushes are chunky
	TimeoutNotifications = 15 * time.Second
	TimeoutReference     = 10 * time.Second // operator-supplied public reference pages
	// TimeoutTunarrBulk covers the ONE bulk read the programmer makes: the
	// content-index build (GET /api/media-libraries/{id}/programs), which returns a
	// library's ENTIRE persisted program list with no server-side paging. On a large
	// library that is tens of MB and many seconds — a live homelab returned 52 MB /
	// ~17 s for 15,788 programs — so it needs far more headroom than a channel CRUD
	// call. Kept separate so the snappy 20 s ceiling still guards every other call.
	TimeoutTunarrBulk = 120 * time.Second
	TimeoutLLM        = 120 * time.Second
	TimeoutProbe      = 5 * time.Second // quick LLM-host probes (version/tags, §8.1)

	// dialBudget bounds connect + TLS for streaming clients that intentionally
	// carry no whole-request budget (NewStreaming).
	dialBudget = 10 * time.Second
)

// New returns an *http.Client with the given hard timeout. The timeout is a
// whole-request budget (connect + headers + body), which is what we want for a
// homelab service that may hang rather than refuse.
func New(timeout time.Duration) *http.Client {
	return newClient(timeout, newTransport(), nil)
}

// NewNamed retains the standalone/test constructor without process-global metrics.
// Application composition uses NewNamedObserved with its generation Recorder.
func NewNamed(_ string, timeout time.Duration) *http.Client {
	return newClient(timeout, newTransport(), nil)
}

// NewNamedObserved is NewNamed bound to one application generation's Recorder.
// Production composition uses this form; NewNamed remains for standalone tools
// and tests that do not own an application generation.
func NewNamedObserved(target string, timeout time.Duration, recorder *metrics.Recorder) *http.Client {
	if recorder == nil {
		return NewNamed(target, timeout)
	}
	return newNamedObservedClient(target, timeout, newTransport(), recorder)
}

// NewPublicNamedObserved builds the client for operator-supplied public URLs.
// Its dialer resolves and pins a public address for the actual connection, so a
// DNS change between validation and dial cannot turn a reference fetch into an
// internal-network request.
func NewPublicNamedObserved(target string, timeout time.Duration, recorder *metrics.Recorder) *http.Client {
	transport := newPublicTransport(net.DefaultResolver, (&net.Dialer{Timeout: dialBudget}).DialContext)
	client := newNamedObservedClient(target, timeout, transport, recorder)
	client.CheckRedirect = publicGetRedirects
	return client
}

// NewStreaming returns an *http.Client for long streaming reads — an Ollama model
// pull runs for minutes and a multi-GB body would blow any fixed whole-request
// budget (a Client.Timeout aborts mid-body, surfacing as "context deadline
// exceeded while reading body"). So it sets NO whole-request timeout: the caller's
// context governs the stream's lifetime. Connect + TLS + response-header stages
// are still bounded, so a dead host fails fast rather than hanging forever. Use
// New (not this) for request/response RPCs — this is for streams only (§8.1 pull).
func NewStreaming() *http.Client {
	return newStreamingClient(&http.Transport{
		DialContext:           (&net.Dialer{Timeout: dialBudget}).DialContext,
		TLSHandshakeTimeout:   dialBudget,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
	})
}

func newTransport() http.RoundTripper {
	return &http.Transport{
		// Conservative pooled defaults; a homelab has few upstreams.
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
}

type ipLookup interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

func newPublicTransport(resolver ipLookup, dial dialContextFunc) *http.Transport {
	return &http.Transport{
		DialContext:           publicDialContext(resolver, dial),
		TLSHandshakeTimeout:   dialBudget,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
	}
}

func publicDialContext(resolver ipLookup, dial dialContextFunc) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("public HTTP dial address: %w", err)
		}
		var addrs []net.IPAddr
		if ip := net.ParseIP(host); ip != nil {
			addrs = []net.IPAddr{{IP: ip}}
		} else {
			addrs, err = resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("public HTTP resolve %q: %w", host, err)
			}
			if len(addrs) == 0 {
				return nil, fmt.Errorf("public HTTP resolve %q returned no addresses", host)
			}
		}
		for _, addr := range addrs {
			if unsafePublicIP(addr.IP) {
				return nil, fmt.Errorf("public HTTP refuses private address for %q", host)
			}
		}
		return dial(ctx, network, net.JoinHostPort(addrs[0].IP.String(), port))
	}
}

func unsafePublicIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func newClient(
	timeout time.Duration,
	next http.RoundTripper,
	onRetry func(metrics.OutboundRetryReason),
) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     newRetryTransport(next, onRetry),
		CheckRedirect: getOnlyRedirects,
	}
}

func newNamedObservedClient(
	target string,
	timeout time.Duration,
	next http.RoundTripper,
	recorder *metrics.Recorder,
) *http.Client {
	c := newClient(timeout, next, func(reason metrics.OutboundRetryReason) {
		recorder.OutboundRetried(target, reason)
	})
	c.Transport = recorder.InstrumentTransport(target, c.Transport)
	return c
}

func newStreamingClient(next http.RoundTripper) *http.Client {
	return &http.Client{
		// No whole-request Timeout on purpose — ctx cancels the stream.
		Transport:     newRetryTransport(next, nil),
		CheckRedirect: getOnlyRedirects,
	}
}

func getOnlyRedirects(_ *http.Request, via []*http.Request) error {
	if len(via) > 0 && via[0].Method != http.MethodGet {
		return http.ErrUseLastResponse
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

func publicGetRedirects(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("stopped after 3 public redirects")
	}
	port := req.URL.Port()
	standardPort := port == "" || (req.URL.Scheme == "https" && port == "443") || (req.URL.Scheme == "http" && port == "80")
	if (req.URL.Scheme != "https" && req.URL.Scheme != "http") || req.URL.User != nil || !standardPort {
		return errors.New("public redirect must use credential-free HTTP(S) on a standard port")
	}
	return nil
}
