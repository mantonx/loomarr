// Package reference resolves bounded, read-only evidence from public web pages
// supplied in channel Intents. Every site follows the same retrieval contract;
// a URL is evidence, never an instruction or title-identity authority.
package reference

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	// MaxResponseBytes bounds a public page before any parsing.
	MaxResponseBytes = 256 << 10
	maxExcerptBytes  = 16 << 10
	maxTitleAnchors  = 128
	maxLookupText    = 2048
	maxRedirects     = 3
	referenceAgent   = "Loomarr/reference (https://github.com/loomarr/loomarr)"
)

// Lookup names one public-page reference.
type Lookup struct {
	URL string
}

// Evidence is the bounded, prompt-safe shape returned by a Resolver. Excerpt
// and TitleAnchors remain untrusted source data; callers decide how to use them.
type Evidence struct {
	URL          string   `json:"url,omitempty"`
	Title        string   `json:"title"`
	Excerpt      string   `json:"excerpt"`
	TitleAnchors []string `json:"titleAnchors"`
}

// Resolver is the Suggester's source-neutral reference boundary.
type Resolver interface {
	Lookup(context.Context, Lookup) (Evidence, error)
}

type ipResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// Web resolves any bounded, publicly routed HTTP(S) text or HTML page. The
// caller supplies Loomarr's shared timeout/retry client.
type Web struct {
	client   *http.Client
	resolver ipResolver
}

func NewWeb(client *http.Client) *Web {
	return &Web{client: client, resolver: net.DefaultResolver}
}

func (w *Web) Lookup(ctx context.Context, lookup Lookup) (Evidence, error) {
	rawURL := strings.TrimSpace(lookup.URL)
	if rawURL == "" {
		return Evidence{}, errors.New("reference: provide a URL")
	}
	if w == nil || w.client == nil || w.resolver == nil {
		return Evidence{}, errors.New("reference: HTTP client is unavailable")
	}
	if len(rawURL) > maxLookupText {
		return Evidence{}, errors.New("reference: URL is too long")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return Evidence{}, fmt.Errorf("reference: parse URL: %w", err)
	}
	if err := w.checkPublicURL(ctx, u); err != nil {
		return Evidence{}, err
	}

	client := *w.client
	priorRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("reference: too many redirects")
		}
		if err := w.checkPublicURL(req.Context(), req.URL); err != nil {
			return err
		}
		if priorRedirect != nil {
			return priorRedirect(req, via)
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Evidence{}, fmt.Errorf("reference: build request: %w", err)
	}
	req.Header.Set("User-Agent", referenceAgent)
	req.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return Evidence{}, fmt.Errorf("reference: fetch page: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Evidence{}, fmt.Errorf("reference: upstream returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return Evidence{}, fmt.Errorf("reference: read page: %w", err)
	}
	if len(body) > MaxResponseBytes {
		return Evidence{}, fmt.Errorf("reference: response exceeds %d bytes", MaxResponseBytes)
	}

	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType == "" {
		mediaType = http.DetectContentType(body)
		mediaType, _, _ = mime.ParseMediaType(mediaType)
	}
	var title, excerpt string
	var anchors []string
	switch mediaType {
	case "text/html", "application/xhtml+xml":
		title, excerpt, anchors = extractHTML(string(body), u)
	case "text/plain":
		title = fallbackTitle(u)
		excerpt = cleanVisibleText(string(body))
		anchors = lineAnchors(excerpt)
	default:
		return Evidence{}, fmt.Errorf("reference: unsupported content type %q", mediaType)
	}
	excerpt = truncateUTF8(excerpt, maxExcerptBytes)
	if excerpt == "" {
		return Evidence{}, errors.New("reference: page has no usable text")
	}
	return Evidence{URL: u.String(), Title: title, Excerpt: excerpt, TitleAnchors: anchors}, nil
}

func (w *Web) checkPublicURL(ctx context.Context, u *url.URL) error {
	if u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return errors.New("reference: URL must be public HTTP(S) without credentials")
	}
	if port := u.Port(); port != "" && (u.Scheme != "https" || port != "443") && (u.Scheme != "http" || port != "80") {
		return errors.New("reference: URL must use a standard web port")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if unsafeIP(ip) {
			return fmt.Errorf("reference: refusing private address %q", host)
		}
		return nil
	}
	addrs, err := w.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("reference: cannot resolve public host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("reference: public host %q has no addresses", host)
	}
	for _, addr := range addrs {
		if unsafeIP(addr.IP) {
			return fmt.Errorf("reference: refusing private address for host %q", host)
		}
	}
	return nil
}

func unsafeIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

var publicURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

// URL returns the first syntactically valid HTTP(S) URL embedded in text. Public
// routability is checked at fetch time because it requires DNS resolution.
func URL(text string) (string, bool) {
	for _, match := range publicURLPattern.FindAllString(text, -1) {
		raw := strings.TrimRight(match, ").,;:!?]}")
		u, err := url.Parse(raw)
		if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil {
			return raw, true
		}
	}
	return "", false
}

var (
	commentPattern  = regexp.MustCompile(`(?is)<!--.*?-->`)
	scriptPattern   = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	stylePattern    = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	noscriptPattern = regexp.MustCompile(`(?is)<noscript\b[^>]*>.*?</noscript\s*>`)
	titlePattern    = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title\s*>`)
	anchorPattern   = regexp.MustCompile(`(?is)<(?:a|i|em|strong|li|td|th|h[1-6])\b[^>]*>(.*?)</(?:a|i|em|strong|li|td|th|h[1-6])\s*>`)
	blockPattern    = regexp.MustCompile(`(?is)</?(?:article|aside|blockquote|br|dd|div|dl|dt|figcaption|figure|footer|h[1-6]|header|li|main|nav|ol|p|section|table|td|th|tr|ul)\b[^>]*>`)
	tagPattern      = regexp.MustCompile(`(?is)<[^>]+>`)
)

func extractHTML(body string, u *url.URL) (string, string, []string) {
	body = commentPattern.ReplaceAllString(body, " ")
	body = scriptPattern.ReplaceAllString(body, " ")
	body = stylePattern.ReplaceAllString(body, " ")
	body = noscriptPattern.ReplaceAllString(body, " ")
	title := ""
	if match := titlePattern.FindStringSubmatch(body); len(match) == 2 {
		title = cleanFragment(match[1])
	}
	if title == "" {
		title = fallbackTitle(u)
	}

	matches := anchorPattern.FindAllStringSubmatchIndex(body, -1)
	anchors := make([]positionedAnchor, 0, len(matches))
	for _, indexes := range matches {
		value := cleanFragment(body[indexes[2]:indexes[3]])
		if value != "" && len(value) <= 120 {
			anchors = append(anchors, positionedAnchor{position: indexes[0], value: value})
		}
	}
	slices.SortFunc(anchors, func(a, b positionedAnchor) int { return a.position - b.position })

	visible := blockPattern.ReplaceAllString(body, "\n")
	visible = tagPattern.ReplaceAllString(visible, " ")
	return title, cleanVisibleText(visible), dedupeAnchors(anchors)
}

type positionedAnchor struct {
	position int
	value    string
}

func cleanFragment(value string) string {
	value = tagPattern.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(html.UnescapeString(value)), " ")
}

func cleanVisibleText(value string) string {
	value = html.UnescapeString(value)
	lines := strings.Split(strings.ReplaceAll(value, "\r", "\n"), "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			clean = append(clean, line)
		}
	}
	return strings.Join(clean, "\n")
}

func lineAnchors(text string) []string {
	anchors := make([]positionedAnchor, 0)
	for index, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*#0123456789.) "))
		if line != "" && len(line) <= 120 {
			anchors = append(anchors, positionedAnchor{position: index, value: line})
		}
	}
	return dedupeAnchors(anchors)
}

func dedupeAnchors(matches []positionedAnchor) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, min(len(matches), maxTitleAnchors))
	for _, match := range matches {
		key := strings.ToLower(match.value)
		if match.value == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, match.value)
		if len(result) == maxTitleAnchors {
			break
		}
	}
	return result
}

func fallbackTitle(u *url.URL) string {
	name := strings.TrimSpace(strings.ReplaceAll(path.Base(u.Path), "-", " "))
	if name == "" || name == "." || name == "/" {
		return u.Hostname()
	}
	decoded, err := url.PathUnescape(name)
	if err == nil {
		name = decoded
	}
	return name
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
