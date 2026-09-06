package fillercorpus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	MetAuthority = "metmuseum.org/collection"

	metAPIBase               = "https://collectionapi.metmuseum.org/public/collection/v1"
	metAPIHost               = "collectionapi.metmuseum.org"
	metImageHost             = "images.metmuseum.org"
	metWebHost               = "www.metmuseum.org"
	metSourceRequestAttempts = 3
)

// MetCaptureConfig freezes every input and resource ceiling needed to turn
// Met search nominations into a source-neutral, non-authorizing inventory.
type MetCaptureConfig struct {
	HTTP                 *http.Client
	CacheDir             string
	UserAgent            string
	Terms                []string
	RequiredSubjectTerms []string
	ExcludedSubjectTerms []string
	RoleHint             string
	SnapshotAt           time.Time
	MaxRequests          int
	MaxObjectLookups     int
	MaxItems             int
	MaxResponseBytes     int64
	MaxItemBytes         int64
	MaxTotalBytes        int64
	Delay                time.Duration
	MaxWallTime          time.Duration
}

// CaptureMetInventory discovers candidates and freezes their item-level source
// evidence. It downloads no media and grants no rights, truth, or model authority.
func CaptureMetInventory(parent context.Context, config MetCaptureConfig) (Inventory, error) {
	if err := validateMetCaptureConfig(parent, config); err != nil {
		return Inventory{}, err
	}
	ctx, cancel := context.WithTimeout(parent, config.MaxWallTime)
	defer cancel()
	started := time.Now()
	client, err := NewSourceClient(SourceClientConfig{
		HTTP: config.HTTP, CacheDir: config.CacheDir, UserAgent: config.UserAgent,
		MaxRequests: config.MaxRequests, MaxResponseBytes: config.MaxResponseBytes,
		Delay: config.Delay, AllowedHosts: []string{metAPIHost, metImageHost},
	})
	if err != nil {
		return Inventory{}, fmt.Errorf("capture Met inventory: %w", err)
	}

	selectionDigest := metSelectionDigest(config.Terms, config.RequiredSubjectTerms, config.ExcludedSubjectTerms)
	discovered, searchDigest, searchRetrievedAt, err := discoverMetObjectIDs(ctx, client, config.Terms, selectionDigest)
	if err != nil {
		return Inventory{}, err
	}
	captureID := NewCaptureID(MetAuthority, "selection-sha256:"+selectionDigest, config.RoleHint)
	result := Inventory{SchemaVersion: InventorySchemaVersion}
	latestObservedAt := searchRetrievedAt
	var predictedBytes int64
	lookups := 0
	seenCreators := make(map[string]struct{}, config.MaxItems)
	for _, discovery := range discovered {
		if len(result.Cases) == config.MaxItems || lookups == config.MaxObjectLookups {
			break
		}
		if err := ctx.Err(); err != nil {
			return Inventory{}, fmt.Errorf("capture Met inventory: %w", err)
		}
		lookups++
		objectID := discovery.objectID
		metadataURL := metAPIBase + "/objects/" + strconv.FormatInt(objectID, 10)
		raw, retrievedAt, err := getMetSource(ctx, client, metadataURL)
		if err != nil {
			if IsSourceHTTPStatus(err, http.StatusNotFound) {
				continue
			}
			return Inventory{}, fmt.Errorf("capture Met object %d: %w", objectID, err)
		}
		if retrievedAt.After(latestObservedAt) {
			latestObservedAt = retrievedAt
		}
		object, ok := decodeMetObject(raw, objectID, config.RequiredSubjectTerms, config.ExcludedSubjectTerms)
		if !ok {
			continue
		}
		creatorKey := strings.ToLower(strings.TrimSpace(object.ArtistDisplayName))
		if _, duplicate := seenCreators[creatorKey]; duplicate {
			continue
		}
		downloadURL := metOpenAccessDownloadURL(object.PrimaryImage, raw)
		head, headRetrievedAt, err := client.Head(ctx, downloadURL)
		if err != nil {
			return Inventory{}, fmt.Errorf("capture Met object %d image: %w", objectID, err)
		}
		if headRetrievedAt.After(latestObservedAt) {
			latestObservedAt = headRetrievedAt
		}
		mediaType, _, _ := mime.ParseMediaType(head.ContentType)
		if !supportedMetImage(mediaType) || head.ContentLength > config.MaxItemBytes || head.ContentLength > config.MaxTotalBytes-predictedBytes {
			continue
		}
		item := metInventoryCase(object, discovery.terms, raw, metadataURL, retrievedAt, captureID, config.RoleHint, mediaType, head.ContentLength)
		result.Cases = append(result.Cases, item)
		seenCreators[creatorKey] = struct{}{}
		predictedBytes += head.ContentLength
	}
	if len(result.Cases) != config.MaxItems {
		return Inventory{}, fmt.Errorf("capture Met inventory: admitted %d of %d candidates after %d of %d object lookups", len(result.Cases), config.MaxItems, lookups, config.MaxObjectLookups)
	}
	if latestObservedAt.IsZero() || latestObservedAt.After(config.SnapshotAt) {
		return Inventory{}, fmt.Errorf("capture Met inventory: source observation exceeded snapshot ceiling")
	}
	result.SnapshotAt = latestObservedAt.UTC()
	result.Captures = []Capture{{
		CaptureID: captureID, Transport: TransportHTTPS, Authority: MetAuthority,
		Collection: "selection-sha256:" + selectionDigest, RoleHint: config.RoleHint, SnapshotAt: result.SnapshotAt,
		MaxRequests: config.MaxRequests, RequestsUsed: client.RequestsUsed(),
		MaxResponseBytes: config.MaxResponseBytes, ResponseBytes: client.ResponseBytes(),
		MaxPredictedMediaBytes: config.MaxTotalBytes, PredictedMediaBytes: predictedBytes,
		MaxWallTimeMS: config.MaxWallTime.Milliseconds(), WallTimeMS: time.Since(started).Milliseconds(),
		SearchSHA256: searchDigest, SearchRetrievedAt: searchRetrievedAt, CacheHits: client.CacheHits(),
	}}
	if failures := ValidateInventory(result); len(failures) != 0 {
		return Inventory{}, fmt.Errorf("capture Met inventory: %s", strings.Join(failures, "; "))
	}
	return result, nil
}

func getMetSource(ctx context.Context, client *SourceClient, sourceURL string) ([]byte, time.Time, error) {
	var lastErr error
	for range metSourceRequestAttempts {
		raw, retrievedAt, err := client.Get(ctx, sourceURL)
		if err == nil {
			return raw, retrievedAt, nil
		}
		lastErr = err
		if !retryableMetSourceError(err) {
			break
		}
	}
	return nil, time.Time{}, lastErr
}

func retryableMetSourceError(err error) bool {
	for _, statusCode := range []int{
		http.StatusForbidden,
		http.StatusRequestTimeout,
		http.StatusTooEarly,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		if IsSourceHTTPStatus(err, statusCode) {
			return true
		}
	}
	return false
}

func validateMetCaptureConfig(ctx context.Context, config MetCaptureConfig) error {
	requestFloor := len(config.Terms) + config.MaxObjectLookups + config.MaxItems
	if ctx == nil || ctx.Err() != nil || config.HTTP == nil || strings.TrimSpace(config.CacheDir) == "" ||
		strings.TrimSpace(config.UserAgent) == "" || !canonicalMetTerms(config.Terms) || !canonicalMetSubjectTerms(config.RequiredSubjectTerms) || !canonicalMetSubjectTerms(config.ExcludedSubjectTerms) ||
		slices.ContainsFunc(config.RequiredSubjectTerms, func(term string) bool { return slices.Contains(config.ExcludedSubjectTerms, term) }) ||
		strings.TrimSpace(config.RoleHint) == "" || config.SnapshotAt.IsZero() || config.SnapshotAt.Location() != time.UTC ||
		config.MaxItems <= 0 || config.MaxItems > 500 || config.MaxObjectLookups < config.MaxItems || config.MaxObjectLookups > 2000 ||
		config.MaxRequests < requestFloor || config.MaxRequests > 3000 || config.MaxResponseBytes <= 0 ||
		config.MaxItemBytes <= 0 || config.MaxTotalBytes < config.MaxItemBytes || config.Delay < 100*time.Millisecond || config.MaxWallTime <= 0 {
		return fmt.Errorf("capture Met inventory requires a live context, transport, cache, identity, canonical terms, UTC snapshot, positive bounded items/requests/bytes, >=100ms delay, and wall ceiling")
	}
	return nil
}

func canonicalMetSubjectTerms(terms []string) bool {
	if len(terms) > 32 || !slices.IsSorted(terms) {
		return false
	}
	previous := ""
	for _, term := range terms {
		if term == "" || term != strings.TrimSpace(term) || len(term) > 100 || term == previous || strings.ContainsAny(term, "\r\n\x00") {
			return false
		}
		previous = term
	}
	return true
}

func canonicalMetTerms(terms []string) bool {
	if len(terms) == 0 || len(terms) > 32 || !slices.IsSorted(terms) {
		return false
	}
	previous := ""
	for _, term := range terms {
		if term == "" || term != strings.TrimSpace(term) || len(term) > 100 || term == previous || strings.ContainsAny(term, "\r\n\x00") {
			return false
		}
		previous = term
	}
	return true
}

func metSelectionDigest(terms, requiredSubjectTerms, excludedSubjectTerms []string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("terms:"))
	for _, term := range terms {
		_, _ = hash.Write([]byte(strconv.Itoa(len(term))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(term))
	}
	_, _ = hash.Write([]byte("subjects:"))
	for _, term := range requiredSubjectTerms {
		_, _ = hash.Write([]byte(strconv.Itoa(len(term))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(term))
	}
	_, _ = hash.Write([]byte("excluded-subjects:"))
	for _, term := range excludedSubjectTerms {
		_, _ = hash.Write([]byte(strconv.Itoa(len(term))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(term))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func metSearchURL(term string) string {
	query := url.Values{}
	query.Set("hasImages", "true")
	query.Set("q", term)
	return metAPIBase + "/search?" + query.Encode()
}

func metOpenAccessDownloadURL(primaryImage string, metadata []byte) string {
	parsed, err := url.Parse(primaryImage)
	if err != nil {
		return ""
	}
	metadataHash := sha256.Sum256(metadata)
	query := parsed.Query()
	query.Set("loomarr", hex.EncodeToString(metadataHash[:]))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func metObjectRank(termDigest string, id int64) string {
	sum := sha256.Sum256([]byte(termDigest + ":" + strconv.FormatInt(id, 10)))
	return hex.EncodeToString(sum[:])
}

func supportedMetImage(mediaType string) bool {
	return mediaType == "image/jpeg" || mediaType == "image/png"
}

func metRepresentationName(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "image"
	}
	return path.Base(parsed.Path)
}
