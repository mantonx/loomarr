package fillercorpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"
)

// InventorySchemaVersion is deliberately not backward compatible. Schema 1
// could describe only one Archive.org collection; schema 2 let split planning
// invent campaign and source-family identity after acquisition; schema 3 could
// not represent one immutable item discovered by multiple bounded captures.
const InventorySchemaVersion = 4

const (
	TransportHTTPS = "https"
	TransportLocal = "local"
)

var authorityMediaHosts = map[string][]string{
	"loc.gov/national-screening-room": {"tile.loc.gov"},
	"images.nasa.gov":                 {"images-assets.nasa.gov"},
	"cdc.gov":                         {"www.cdc.gov"},
	"commons.wikimedia.org":           {"upload.wikimedia.org"},
	MetAuthority:                      {metImageHost},
}

func mediaHostRules(authority string) ([]string, bool) {
	if strings.HasPrefix(authority, "archive.org/") && len(strings.TrimPrefix(authority, "archive.org/")) > 0 {
		return []string{"archive.org", ".archive.org"}, true
	}
	rules, ok := authorityMediaHosts[authority]
	return rules, ok
}

// Inventory freezes source-neutral candidate identity before rights review or
// media download. Capture records explain how adapters stayed within their
// declared acquisition ceilings; Cases are the only reviewable media.
type Inventory struct {
	SchemaVersion int             `json:"schemaVersion"`
	SnapshotAt    time.Time       `json:"snapshotAt"`
	Captures      []Capture       `json:"captures"`
	Cases         []InventoryCase `json:"cases"`
}

type Capture struct {
	CaptureID              string    `json:"captureId"`
	Transport              string    `json:"transport"`
	Authority              string    `json:"authority"`
	Collection             string    `json:"collection,omitempty"`
	RoleHint               string    `json:"roleHint"`
	SnapshotAt             time.Time `json:"snapshotAt"`
	MaxRequests            int       `json:"maxRequests"`
	RequestsUsed           int       `json:"requestsUsed"`
	MaxResponseBytes       int64     `json:"maxResponseBytes"`
	ResponseBytes          int64     `json:"responseBytes"`
	MaxPredictedMediaBytes int64     `json:"maxPredictedMediaBytes"`
	PredictedMediaBytes    int64     `json:"predictedMediaBytes"`
	MaxWallTimeMS          int64     `json:"maxWallTimeMs"`
	WallTimeMS             int64     `json:"wallTimeMs"`
	SearchSHA256           string    `json:"searchSha256,omitempty"`
	SearchRetrievedAt      time.Time `json:"searchRetrievedAt,omitempty"`
	CacheHits              int       `json:"cacheHits,omitempty"`
}

type InventoryCase struct {
	CaseID                  string                  `json:"caseId"`
	CaptureIDs              []string                `json:"captureIds"`
	Authority               string                  `json:"authority"`
	ItemID                  string                  `json:"itemId"`
	Title                   string                  `json:"title"`
	RoleHints               []string                `json:"roleHints"`
	Collection              []string                `json:"collection,omitempty"`
	Creator                 []string                `json:"creator,omitempty"`
	SubjectTerms            []string                `json:"subjectTerms,omitempty"`
	Campaign                string                  `json:"campaign,omitempty"`
	SourceFamily            string                  `json:"sourceFamily,omitempty"`
	Date                    string                  `json:"date,omitempty"`
	LicenseURL              string                  `json:"licenseUrl,omitempty"`
	RightsAssertions        []string                `json:"rightsAssertions"`
	PossibleCopyrightStatus []string                `json:"possibleCopyrightStatus,omitempty"`
	ItemURL                 string                  `json:"itemUrl"`
	MetadataURL             string                  `json:"metadataUrl"`
	MetadataCache           string                  `json:"metadataCache,omitempty"`
	MetadataRetrievedAt     time.Time               `json:"metadataRetrievedAt"`
	MetadataSHA256          string                  `json:"metadataSha256"`
	Evidence                []InventoryEvidence     `json:"evidence,omitempty"`
	AllowedMediaHosts       []string                `json:"allowedMediaHosts"`
	Representation          InventoryRepresentation `json:"representation"`
}

type InventoryRepresentation struct {
	Transport  string `json:"transport"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Path       string `json:"path,omitempty"`
	MIMEType   string `json:"mimeType"`
	Origin     string `json:"origin,omitempty"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256,omitempty"`
	SHA1       string `json:"sha1,omitempty"`
	MD5        string `json:"md5,omitempty"`
	DurationMS int64  `json:"durationMs,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

type InventoryEvidence struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type RightsDecision struct {
	InventorySHA256 string                 `json:"inventorySha256"`
	CaseID          string                 `json:"caseId"`
	CaptureIDs      []string               `json:"captureIds"`
	Authority       string                 `json:"authority"`
	ItemID          string                 `json:"itemId"`
	MetadataSHA256  string                 `json:"metadataSha256"`
	ReviewerID      string                 `json:"reviewerId"`
	ReviewedAt      time.Time              `json:"reviewedAt"`
	Decision        string                 `json:"decision"`
	Basis           string                 `json:"basis"`
	Redistributable bool                   `json:"redistributable"`
	RequiredCredit  string                 `json:"requiredCredit,omitempty"`
	Restrictions    []string               `json:"restrictions,omitempty"`
	HoldoutContract *HoldoutRightsContract `json:"holdoutContract,omitempty"`
}

func CaseID(authority, itemID string) string { return authority + "/" + itemID }

func NewCaptureID(authority, collection, roleHint string) string {
	return authority + "/" + collection + "/" + roleHint
}

func InventorySHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// DecodeInventory accepts exactly one strict schema-v4 JSON value. Older
// single-source artifacts fail closed rather than being silently adapted.
func DecodeInventory(reader io.Reader) (Inventory, error) {
	var value Inventory
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Inventory{}, fmt.Errorf("decode inventory: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Inventory{}, fmt.Errorf("decode inventory: trailing JSON value")
	}
	if failures := ValidateInventory(value); len(failures) != 0 {
		return Inventory{}, fmt.Errorf("invalid inventory: %s", strings.Join(failures, "; "))
	}
	return value, nil
}

func DecodeInventoryBytes(raw []byte) (Inventory, error) {
	return DecodeInventory(bytes.NewReader(raw))
}

// MergeInventories deterministically combines independently frozen adapter
// captures into the one mixed-authority artifact reviewed for certification.
func MergeInventories(inputs ...Inventory) (Inventory, error) {
	if len(inputs) == 0 {
		return Inventory{}, fmt.Errorf("merge inventories: at least one input is required")
	}
	result := Inventory{SchemaVersion: InventorySchemaVersion}
	captures := make(map[string]struct{})
	cases := make(map[string]int)
	for index, input := range inputs {
		if failures := ValidateInventory(input); len(failures) != 0 {
			return Inventory{}, fmt.Errorf("merge inventories: input %d: %s", index+1, strings.Join(failures, "; "))
		}
		if input.SnapshotAt.After(result.SnapshotAt) {
			result.SnapshotAt = input.SnapshotAt
		}
		for _, capture := range input.Captures {
			if _, duplicate := captures[capture.CaptureID]; duplicate {
				return Inventory{}, fmt.Errorf("merge inventories: duplicate capture %q", capture.CaptureID)
			}
			captures[capture.CaptureID] = struct{}{}
			result.Captures = append(result.Captures, capture)
		}
		for _, item := range input.Cases {
			if existingIndex, duplicate := cases[item.CaseID]; duplicate {
				existing := result.Cases[existingIndex]
				if !sameCapturedItem(existing, item) {
					return Inventory{}, fmt.Errorf("merge inventories: case %q has conflicting frozen identity", item.CaseID)
				}
				existing.CaptureIDs = sortedUnion(existing.CaptureIDs, item.CaptureIDs)
				existing.RoleHints = sortedUnion(existing.RoleHints, item.RoleHints)
				result.Cases[existingIndex] = existing
				continue
			}
			item.CaptureIDs = sortedUnion(nil, item.CaptureIDs)
			item.RoleHints = sortedUnion(nil, item.RoleHints)
			cases[item.CaseID] = len(result.Cases)
			result.Cases = append(result.Cases, item)
		}
	}
	slices.SortFunc(result.Captures, func(a, b Capture) int { return strings.Compare(a.CaptureID, b.CaptureID) })
	slices.SortFunc(result.Cases, func(a, b InventoryCase) int { return strings.Compare(a.CaseID, b.CaseID) })
	if failures := ValidateInventory(result); len(failures) != 0 {
		return Inventory{}, fmt.Errorf("merge inventories: %s", strings.Join(failures, "; "))
	}
	return result, nil
}

func sameCapturedItem(a, b InventoryCase) bool {
	a.CaptureIDs, b.CaptureIDs = nil, nil
	a.RoleHints, b.RoleHints = nil, nil
	return reflect.DeepEqual(a, b)
}

func sortedUnion(a, b []string) []string {
	values := append(append([]string(nil), a...), b...)
	slices.Sort(values)
	return slices.Compact(values)
}

type LaneInventoryOptions struct {
	SnapshotAt        time.Time
	Collection        string
	AllowedMediaHosts []string
}

// InventoryFromLane promotes one bounded, source-neutral discovery lane into
// the full rights-review contract without granting download authority.
func InventoryFromLane(lane Lane, opts LaneInventoryOptions) (Inventory, error) {
	if opts.SnapshotAt.IsZero() || strings.TrimSpace(lane.Authority) == "" || len(lane.Cases) == 0 {
		return Inventory{}, fmt.Errorf("promote lane: snapshot, authority, and cases are required")
	}
	role := ""
	for _, item := range lane.Cases {
		if len(item.RoleHints) == 0 || strings.TrimSpace(item.RoleHints[0]) == "" {
			return Inventory{}, fmt.Errorf("promote lane: case %q has no primary role hint", item.ItemID)
		}
		if role == "" {
			role = item.RoleHints[0]
		}
		if !slices.Contains(item.RoleHints, role) {
			return Inventory{}, fmt.Errorf("promote lane: case %q does not share capture role %q", item.ItemID, role)
		}
	}
	captureID := NewCaptureID(lane.Authority, opts.Collection, role)
	result := Inventory{SchemaVersion: InventorySchemaVersion, SnapshotAt: opts.SnapshotAt.UTC(), Captures: []Capture{{
		CaptureID: captureID, Transport: TransportHTTPS, Authority: lane.Authority, Collection: opts.Collection, RoleHint: role, SnapshotAt: opts.SnapshotAt.UTC(),
		MaxRequests: lane.MaxRequests, RequestsUsed: lane.RequestsUsed, MaxResponseBytes: lane.MaxResponseBytes, ResponseBytes: lane.ResponseBytes,
		MaxPredictedMediaBytes: lane.MaxPredictedMediaBytes, PredictedMediaBytes: lane.PredictedMediaBytes,
		MaxWallTimeMS: lane.MaxWallTimeMS, WallTimeMS: lane.WallTimeMS,
	}}}
	for _, item := range lane.Cases {
		result.Cases = append(result.Cases, InventoryCase{
			CaseID: CaseID(lane.Authority, item.ItemID), CaptureIDs: []string{captureID}, Authority: lane.Authority, ItemID: item.ItemID,
			Title: item.Title, RoleHints: item.RoleHints, Collection: nonEmptySlice(opts.Collection), LicenseURL: item.LicenseURL,
			RightsAssertions: item.RightsAssertions, ItemURL: item.ItemURL, MetadataURL: item.MetadataURL,
			MetadataRetrievedAt: item.MetadataRetrievedAt, MetadataSHA256: item.MetadataSHA256,
			AllowedMediaHosts: append([]string(nil), opts.AllowedMediaHosts...),
			Representation:    InventoryRepresentation{Transport: TransportHTTPS, Name: item.Representation.Name, URL: item.Representation.URL, MIMEType: item.Representation.MIMEType, Bytes: item.Representation.Bytes, SHA1: item.Representation.SHA1, MD5: item.Representation.MD5},
		})
	}
	if failures := ValidateInventory(result); len(failures) != 0 {
		return Inventory{}, fmt.Errorf("promote lane: %s", strings.Join(failures, "; "))
	}
	return result, nil
}

func nonEmptySlice(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return []string{value}
}

func ValidateInventory(value Inventory) []string {
	var failures []string
	if value.SchemaVersion != InventorySchemaVersion {
		failures = append(failures, fmt.Sprintf("schemaVersion is %d; want %d", value.SchemaVersion, InventorySchemaVersion))
	}
	if value.SnapshotAt.IsZero() {
		failures = append(failures, "snapshotAt is required")
	}
	if len(value.Captures) == 0 {
		failures = append(failures, "at least one capture is required")
	}
	if len(value.Cases) == 0 {
		failures = append(failures, "at least one case is required")
	}
	captures := map[string]Capture{}
	for _, capture := range value.Captures {
		if capture.CaptureID != NewCaptureID(capture.Authority, capture.Collection, capture.RoleHint) || strings.TrimSpace(capture.Authority) == "" || strings.TrimSpace(capture.RoleHint) == "" || capture.SnapshotAt.IsZero() || capture.SnapshotAt.After(value.SnapshotAt) ||
			!validCaptureTransport(capture) ||
			!validCeiling64(capture.MaxPredictedMediaBytes, capture.PredictedMediaBytes) ||
			!validCeiling64(capture.MaxWallTimeMS, capture.WallTimeMS) {
			failures = append(failures, fmt.Sprintf("capture %q has invalid identity or ceilings", capture.Authority))
		}
		if capture.SearchSHA256 != "" && !digest(capture.SearchSHA256, 64) {
			failures = append(failures, fmt.Sprintf("capture %q has invalid search digest", capture.Authority))
		}
		if (capture.SearchSHA256 == "") != capture.SearchRetrievedAt.IsZero() || (!capture.SearchRetrievedAt.IsZero() && capture.SearchRetrievedAt.After(value.SnapshotAt)) {
			failures = append(failures, fmt.Sprintf("capture %q has incomplete search identity", capture.CaptureID))
		}
		if _, duplicate := captures[capture.CaptureID]; duplicate {
			failures = append(failures, fmt.Sprintf("duplicate capture %q", capture.CaptureID))
		}
		captures[capture.CaptureID] = capture
	}
	seen := map[string]struct{}{}
	predictedByCapture := map[string]int64{}
	for _, item := range value.Cases {
		if item.CaseID != CaseID(item.Authority, item.ItemID) || strings.TrimSpace(item.Authority) == "" || strings.TrimSpace(item.ItemID) == "" {
			failures = append(failures, fmt.Sprintf("case %q has invalid source-neutral identity", item.CaseID))
		}
		if len(item.CaptureIDs) == 0 || !slices.IsSorted(item.CaptureIDs) || len(slices.Compact(append([]string(nil), item.CaptureIDs...))) != len(item.CaptureIDs) {
			failures = append(failures, fmt.Sprintf("case %q has invalid capture identities", item.CaseID))
		}
		transport := ""
		for _, captureID := range item.CaptureIDs {
			capture, ok := captures[captureID]
			if !ok || capture.Authority != item.Authority || !slices.Contains(item.RoleHints, capture.RoleHint) {
				failures = append(failures, fmt.Sprintf("case %q does not match capture %q", item.CaseID, captureID))
			} else if transport != "" && transport != capture.Transport {
				failures = append(failures, fmt.Sprintf("case %q mixes capture transports", item.CaseID))
			} else {
				transport = capture.Transport
			}
			predictedByCapture[captureID] += item.Representation.Bytes
		}
		if _, ok := seen[item.CaseID]; ok {
			failures = append(failures, fmt.Sprintf("duplicate case %q", item.CaseID))
		}
		seen[item.CaseID] = struct{}{}
		if strings.TrimSpace(item.Title) == "" || len(item.RoleHints) == 0 || slices.Contains(item.RoleHints, "") || len(item.RightsAssertions) == 0 || slices.Contains(item.RightsAssertions, "") || item.MetadataRetrievedAt.IsZero() || item.MetadataRetrievedAt.After(value.SnapshotAt) || !digest(item.MetadataSHA256, 64) {
			failures = append(failures, fmt.Sprintf("case %q has incomplete frozen metadata", item.CaseID))
		}
		if !canonicalOptionalStrings(item.SubjectTerms) {
			failures = append(failures, fmt.Sprintf("case %q has non-canonical subject terms", item.CaseID))
		}
		if (transport == TransportHTTPS && (!httpsURL(item.ItemURL) || !httpsURL(item.MetadataURL))) ||
			(transport == TransportLocal && ((item.ItemURL != "" && !httpsURL(item.ItemURL)) || (item.MetadataURL != "" && !httpsURL(item.MetadataURL)))) ||
			(item.LicenseURL != "" && !httpsURL(item.LicenseURL)) {
			failures = append(failures, fmt.Sprintf("case %q has an invalid evidence URL", item.CaseID))
		}
		if item.Representation.Bytes <= 0 || strings.TrimSpace(item.Representation.Name) == "" || strings.TrimSpace(item.Representation.MIMEType) == "" || !digestOptional(item.Representation.SHA256, 64) || !digestOptional(item.Representation.SHA1, 40) || !digestOptional(item.Representation.MD5, 32) {
			failures = append(failures, fmt.Sprintf("case %q has incomplete representation identity", item.CaseID))
		}
		if item.Representation.Transport != transport {
			failures = append(failures, fmt.Sprintf("case %q representation transport does not match its capture", item.CaseID))
		}
		if transport == TransportHTTPS {
			if err := ValidateMediaURL(item.Representation.URL, item.AllowedMediaHosts); err != nil {
				failures = append(failures, fmt.Sprintf("case %q: %v", item.CaseID, err))
			}
			knownRules, known := mediaHostRules(item.Authority)
			if !known || len(item.AllowedMediaHosts) == 0 {
				failures = append(failures, fmt.Sprintf("case %q has no supported authority host policy", item.CaseID))
			} else {
				for _, rule := range item.AllowedMediaHosts {
					if !slices.Contains(knownRules, rule) {
						failures = append(failures, fmt.Sprintf("case %q declares unsupported media-host rule %q", item.CaseID, rule))
					}
				}
			}
		} else if item.Representation.URL != "" || len(item.AllowedMediaHosts) != 0 || !safeRelativePath(item.Representation.Path) || !digest(item.Representation.SHA256, 64) || !validDirectEvidence(item.Evidence) {
			failures = append(failures, fmt.Sprintf("case %q has incomplete local media or evidence identity", item.CaseID))
		}
	}
	for id, capture := range captures {
		if predictedByCapture[id] != capture.PredictedMediaBytes {
			failures = append(failures, fmt.Sprintf("capture %q predicted media bytes do not match its cases", id))
		}
	}
	slices.Sort(failures)
	return failures
}

func canonicalOptionalStrings(values []string) bool {
	if !slices.IsSorted(values) {
		return false
	}
	previous := ""
	for _, value := range values {
		if value == "" || value != strings.TrimSpace(value) || value == previous {
			return false
		}
		previous = value
	}
	return true
}

func validCaptureTransport(capture Capture) bool {
	switch capture.Transport {
	case TransportHTTPS:
		return validCeiling(capture.MaxRequests, capture.RequestsUsed) && validCeiling64(capture.MaxResponseBytes, capture.ResponseBytes)
	case TransportLocal:
		return capture.MaxRequests == 0 && capture.RequestsUsed == 0 && capture.MaxResponseBytes == 0 && capture.ResponseBytes == 0
	default:
		return false
	}
}

func validDirectEvidence(evidence []InventoryEvidence) bool {
	kinds := map[string]bool{}
	for _, item := range evidence {
		if (item.Kind != "rights" && item.Kind != "provenance") || !safeRelativePath(item.Path) || item.Bytes <= 0 || !digest(item.SHA256, 64) {
			return false
		}
		kinds[item.Kind] = true
	}
	return kinds["rights"] && kinds["provenance"]
}

func safeRelativePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func ValidateMediaURL(rawURL string, allowedHosts []string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("invalid HTTPS media URL")
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range allowedHosts {
		rule := strings.ToLower(strings.TrimSpace(allowed))
		if host == rule || (strings.HasPrefix(rule, ".") && strings.HasSuffix(host, rule) && len(host) > len(rule)) {
			return nil
		}
	}
	return fmt.Errorf("media host %q is not allowlisted", host)
}

func digestOptional(value string, size int) bool {
	return value == "" || digest(strings.ToLower(value), size)
}
