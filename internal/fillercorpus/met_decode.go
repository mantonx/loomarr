package fillercorpus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type metSearchResponse struct {
	Total     int     `json:"total"`
	ObjectIDs []int64 `json:"objectIDs"`
}

type metObject struct {
	ObjectID              int64   `json:"objectID"`
	IsPublicDomain        bool    `json:"isPublicDomain"`
	PrimaryImage          string  `json:"primaryImage"`
	Title                 string  `json:"title"`
	ArtistDisplayName     string  `json:"artistDisplayName"`
	ObjectDate            string  `json:"objectDate"`
	ObjectURL             string  `json:"objectURL"`
	Repository            string  `json:"repository"`
	CreditLine            string  `json:"creditLine"`
	RightsAndReproduction *string `json:"rightsAndReproduction"`
	Tags                  []struct {
		Term string `json:"term"`
	} `json:"tags"`
}

type metDiscovery struct {
	objectID int64
	terms    []string
}

func discoverMetObjectIDs(ctx context.Context, client *SourceClient, terms []string, selectionDigest string) ([]metDiscovery, string, time.Time, error) {
	searchHash := sha256.New()
	foundBy := make(map[int64][]string)
	var latest time.Time
	for _, term := range terms {
		raw, retrievedAt, err := getMetSource(ctx, client, metSearchURL(term))
		if err != nil {
			return nil, "", time.Time{}, fmt.Errorf("capture Met search %q: %w", term, err)
		}
		var response metSearchResponse
		if err := json.Unmarshal(raw, &response); err != nil || response.Total < 0 || response.Total != len(response.ObjectIDs) {
			return nil, "", time.Time{}, fmt.Errorf("capture Met search %q: incomplete response", term)
		}
		_, _ = searchHash.Write([]byte(strconv.Itoa(len(term))))
		_, _ = searchHash.Write([]byte{':'})
		_, _ = searchHash.Write([]byte(term))
		_, _ = searchHash.Write([]byte(strconv.Itoa(len(raw))))
		_, _ = searchHash.Write([]byte{':'})
		_, _ = searchHash.Write(raw)
		if retrievedAt.After(latest) {
			latest = retrievedAt
		}
		for _, id := range response.ObjectIDs {
			if id <= 0 {
				return nil, "", time.Time{}, fmt.Errorf("capture Met search %q: invalid object id", term)
			}
			if !slices.Contains(foundBy[id], term) {
				foundBy[id] = append(foundBy[id], term)
			}
		}
	}
	discovered := make([]metDiscovery, 0, len(foundBy))
	for id, foundTerms := range foundBy {
		discovered = append(discovered, metDiscovery{objectID: id, terms: foundTerms})
	}
	slices.SortFunc(discovered, func(left, right metDiscovery) int {
		return strings.Compare(metObjectRank(selectionDigest, left.objectID), metObjectRank(selectionDigest, right.objectID))
	})
	return discovered, hex.EncodeToString(searchHash.Sum(nil)), latest.UTC(), nil
}

func decodeMetObject(raw []byte, expectedID int64, requiredSubjectTerms, excludedSubjectTerms []string) (metObject, bool) {
	var object metObject
	if json.Unmarshal(raw, &object) != nil || object.ObjectID != expectedID || !object.IsPublicDomain ||
		strings.TrimSpace(object.Title) == "" || strings.TrimSpace(object.ArtistDisplayName) == "" ||
		!exactMetURL(object.PrimaryImage, metImageHost, "") || !metObjectPassesSubjectFilter(object, requiredSubjectTerms, excludedSubjectTerms) ||
		!exactMetURL(object.ObjectURL, metWebHost, "/art/collection/search/"+strconv.FormatInt(expectedID, 10)) {
		return metObject{}, false
	}
	return object, true
}

func metObjectPassesSubjectFilter(object metObject, required, excluded []string) bool {
	foundRequired := len(required) == 0
	for _, tag := range object.Tags {
		term := strings.TrimSpace(tag.Term)
		if slices.Contains(excluded, term) {
			return false
		}
		if slices.Contains(required, term) {
			foundRequired = true
		}
	}
	return foundRequired
}

func exactMetURL(raw, host, exactPath string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() != host || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return exactPath == "" || parsed.Path == exactPath
}

func metInventoryCase(object metObject, discoveryTerms []string, raw []byte, metadataURL string, retrievedAt time.Time, captureID, roleHint, mediaType string, bytes int64) InventoryCase {
	id := strconv.FormatInt(object.ObjectID, 10)
	assertions := []string{"Met object record isPublicDomain=true."}
	if value := strings.TrimSpace(object.Repository); value != "" {
		assertions = append(assertions, "Met repository assertion: "+value)
	}
	if value := strings.TrimSpace(object.CreditLine); value != "" {
		assertions = append(assertions, "Met credit-line assertion: "+value)
	}
	metadataHash := sha256.Sum256(raw)
	collections := []string{"Metropolitan Museum of Art"}
	for _, term := range discoveryTerms {
		collections = append(collections, "search-term:"+term)
	}
	var subjectTerms []string
	for _, tag := range object.Tags {
		if term := strings.TrimSpace(tag.Term); term != "" {
			subjectTerms = append(subjectTerms, term)
		}
	}
	slices.Sort(subjectTerms)
	subjectTerms = slices.Compact(subjectTerms)
	return InventoryCase{
		CaseID: CaseID(MetAuthority, id), CaptureIDs: []string{captureID}, Authority: MetAuthority, ItemID: id,
		Title: object.Title, RoleHints: []string{roleHint}, Collection: collections,
		Creator: []string{object.ArtistDisplayName}, SubjectTerms: subjectTerms, SourceFamily: "met-object:" + id, Date: object.ObjectDate,
		RightsAssertions: assertions, ItemURL: object.ObjectURL, MetadataURL: metadataURL,
		MetadataCache: sourceCacheKey(metadataURL) + ".json", MetadataRetrievedAt: retrievedAt.UTC(),
		MetadataSHA256:    hex.EncodeToString(metadataHash[:]),
		AllowedMediaHosts: []string{metImageHost},
		Representation:    InventoryRepresentation{Transport: TransportHTTPS, Name: metRepresentationName(object.PrimaryImage), URL: metOpenAccessDownloadURL(object.PrimaryImage, raw), MIMEType: mediaType, Bytes: bytes},
	}
}
