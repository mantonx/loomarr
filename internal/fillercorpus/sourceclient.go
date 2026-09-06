package fillercorpus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// SourceClient is the bounded, serial, raw-caching HTTP edge shared by corpus
// discovery adapters. It records transport facts but interprets no rights.
type SourceClient struct {
	http             *http.Client
	cacheDir         string
	userAgent        string
	maxRequests      int
	maxResponseBytes int64
	delay            time.Duration
	allowedHosts     []string
	requests         int
	cacheHits        int
	responseBytes    int64
	lastRequest      time.Time
}

type SourceClientConfig struct {
	HTTP             *http.Client
	CacheDir         string
	UserAgent        string
	MaxRequests      int
	MaxResponseBytes int64
	Delay            time.Duration
	AllowedHosts     []string
}

// SourceHead is the representation metadata a discovery adapter may use for
// bounded planning. It is transport evidence, not download authorization.
type SourceHead struct {
	ContentLength int64  `json:"contentLength"`
	ContentType   string `json:"contentType"`
}

type sourceHTTPStatusError struct {
	method     string
	rawURL     string
	status     string
	statusCode int
}

func (e *sourceHTTPStatusError) Error() string {
	return fmt.Sprintf("%s %s: %s", e.method, e.rawURL, e.status)
}

// IsSourceHTTPStatus reports whether err came from a completed source request
// with the given HTTP status. Callers retain no access to the response body.
func IsSourceHTTPStatus(err error, statusCode int) bool {
	var statusErr *sourceHTTPStatusError
	return errors.As(err, &statusErr) && statusErr.statusCode == statusCode
}

func NewSourceClient(config SourceClientConfig) (*SourceClient, error) {
	if config.HTTP == nil || config.CacheDir == "" || config.UserAgent == "" || config.MaxRequests <= 0 || config.MaxResponseBytes <= 0 || config.Delay < 0 || len(config.AllowedHosts) == 0 {
		return nil, fmt.Errorf("source client requires HTTP transport, cache, identity, exact hosts, and positive ceilings")
	}
	for _, host := range config.AllowedHosts {
		if host == "" || strings.ContainsAny(host, ":/@") {
			return nil, fmt.Errorf("source client host allowlist contains invalid host %q", host)
		}
	}
	if err := os.MkdirAll(config.CacheDir, 0o750); err != nil {
		return nil, err
	}
	allowedHosts := slices.Clone(config.AllowedHosts)
	httpClient := *config.HTTP
	configuredRedirect := httpClient.CheckRedirect
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !slices.Contains(allowedHosts, req.URL.Hostname()) {
			return fmt.Errorf("source redirect host %q is not allowed", req.URL.Hostname())
		}
		if configuredRedirect != nil {
			return configuredRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	return &SourceClient{http: &httpClient, cacheDir: config.CacheDir, userAgent: config.UserAgent, maxRequests: config.MaxRequests, maxResponseBytes: config.MaxResponseBytes, delay: config.Delay, allowedHosts: allowedHosts}, nil
}

func (c *SourceClient) Get(ctx context.Context, rawURL string) ([]byte, time.Time, error) {
	if err := c.validateURL(rawURL); err != nil {
		return nil, time.Time{}, err
	}
	cachePath := filepath.Join(c.cacheDir, sourceCacheKey(rawURL)+".json")
	if raw, err := os.ReadFile(cachePath); err == nil {
		if int64(len(raw)) > c.maxResponseBytes-c.responseBytes {
			return nil, time.Time{}, fmt.Errorf("cached response-byte ceiling exceeded")
		}
		info, err := os.Stat(cachePath)
		if err != nil {
			return nil, time.Time{}, err
		}
		c.responseBytes += int64(len(raw))
		c.cacheHits++
		return raw, info.ModTime().UTC(), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, time.Time{}, fmt.Errorf("read source cache: %w", err)
	}
	resp, requestedAt, err := c.request(ctx, http.MethodGet, rawURL)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, &sourceHTTPStatusError{method: http.MethodGet, rawURL: rawURL, status: resp.Status, statusCode: resp.StatusCode}
	}
	remaining := c.maxResponseBytes - c.responseBytes
	raw, err := io.ReadAll(io.LimitReader(resp.Body, remaining+1))
	if err != nil {
		return nil, time.Time{}, err
	}
	if int64(len(raw)) > remaining {
		return nil, time.Time{}, fmt.Errorf("response-byte ceiling exceeded")
	}
	c.responseBytes += int64(len(raw))
	if err := writeSourceCache(cachePath, raw); err != nil {
		return nil, time.Time{}, err
	}
	return raw, requestedAt, nil
}

// Head returns cached or live representation facts without fetching media.
func (c *SourceClient) Head(ctx context.Context, rawURL string) (SourceHead, time.Time, error) {
	if err := c.validateURL(rawURL); err != nil {
		return SourceHead{}, time.Time{}, err
	}
	cachePath := filepath.Join(c.cacheDir, sourceCacheKey(rawURL)+".head.json")
	if raw, err := os.ReadFile(cachePath); err == nil {
		head, err := decodeSourceHead(raw)
		if err != nil {
			return SourceHead{}, time.Time{}, fmt.Errorf("decode source HEAD cache: %w", err)
		}
		if int64(len(raw)) > c.maxResponseBytes-c.responseBytes {
			return SourceHead{}, time.Time{}, fmt.Errorf("cached response-byte ceiling exceeded")
		}
		info, err := os.Stat(cachePath)
		if err != nil {
			return SourceHead{}, time.Time{}, err
		}
		c.responseBytes += int64(len(raw))
		c.cacheHits++
		return head, info.ModTime().UTC(), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return SourceHead{}, time.Time{}, fmt.Errorf("read source HEAD cache: %w", err)
	}
	resp, requestedAt, err := c.request(ctx, http.MethodHead, rawURL)
	if err != nil {
		return SourceHead{}, time.Time{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return SourceHead{}, time.Time{}, &sourceHTTPStatusError{method: http.MethodHead, rawURL: rawURL, status: resp.Status, statusCode: resp.StatusCode}
	}
	head := SourceHead{ContentLength: resp.ContentLength, ContentType: resp.Header.Get("Content-Type")}
	if head.ContentLength <= 0 || head.ContentType == "" {
		return SourceHead{}, time.Time{}, fmt.Errorf("HEAD %s omitted positive length or content type", rawURL)
	}
	raw, err := json.Marshal(head)
	if err != nil {
		return SourceHead{}, time.Time{}, err
	}
	if int64(len(raw)) > c.maxResponseBytes-c.responseBytes {
		return SourceHead{}, time.Time{}, fmt.Errorf("response-byte ceiling exceeded")
	}
	c.responseBytes += int64(len(raw))
	if err := writeSourceCache(cachePath, raw); err != nil {
		return SourceHead{}, time.Time{}, err
	}
	return head, requestedAt, nil
}

func (c *SourceClient) request(ctx context.Context, method, rawURL string) (*http.Response, time.Time, error) {
	if err := c.validateURL(rawURL); err != nil {
		return nil, time.Time{}, err
	}
	if c.requests >= c.maxRequests {
		return nil, time.Time{}, fmt.Errorf("request ceiling exhausted")
	}
	if wait := c.delay - time.Since(c.lastRequest); !c.lastRequest.IsZero() && wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, time.Time{}, ctx.Err()
		case <-timer.C:
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	c.requests++
	c.lastRequest = time.Now()
	if err != nil && resp != nil {
		_ = resp.Body.Close()
	}
	return resp, c.lastRequest.UTC(), err
}

func (c *SourceClient) validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.User != nil || !slices.Contains(c.allowedHosts, u.Hostname()) {
		return fmt.Errorf("source URL requires HTTPS and an exact allowed host")
	}
	return nil
}
func decodeSourceHead(raw []byte) (SourceHead, error) {
	var head SourceHead
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&head); err != nil {
		return SourceHead{}, err
	}
	if head.ContentLength <= 0 || head.ContentType == "" {
		return SourceHead{}, fmt.Errorf("positive length and content type are required")
	}
	return head, nil
}

func (c *SourceClient) RequestsUsed() int    { return c.requests }
func (c *SourceClient) CacheHits() int       { return c.cacheHits }
func (c *SourceClient) ResponseBytes() int64 { return c.responseBytes }

func sourceCacheKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func writeSourceCache(path string, raw []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".filler-source-cache-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(raw); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}
