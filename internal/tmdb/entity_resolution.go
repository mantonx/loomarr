package tmdb

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/provision"
)

const maxNetworkExportBytes = 4 << 20

type namedIdentity struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type personSearchResponse struct {
	Results []namedIdentity `json:"results"`
}

type resolvedDiscoveryEntities struct {
	networkID    int
	networkName  string
	castIDs      []int
	castNames    []string
	creatorIDs   []int
	creatorNames []string
}

func validateEntityQuery(query catalog.DiscoveryQuery) error {
	hasPeople := len(query.Cast) > 0 || len(query.Creators) > 0
	if query.Network != "" && hasPeople {
		return fmt.Errorf("tmdb discovery: network and person constraints cannot be combined")
	}
	if query.Network != "" && query.MediaType != provision.Series {
		return fmt.Errorf("tmdb discovery: network requires series media type")
	}
	if hasPeople && query.MediaType != provision.Movie {
		return fmt.Errorf("tmdb discovery: cast and creators require movie media type")
	}
	return nil
}

func (c *Client) resolveDiscoveryEntities(ctx context.Context, query catalog.DiscoveryQuery) (resolvedDiscoveryEntities, error) {
	var out resolvedDiscoveryEntities
	var err error
	if query.Network != "" {
		out.networkID, out.networkName, err = c.resolveNetwork(ctx, query.Network, query.OriginCountry)
		if err != nil {
			return resolvedDiscoveryEntities{}, err
		}
	}
	if len(query.Cast) > 0 {
		out.castIDs, out.castNames, err = c.resolvePeople(ctx, query.Cast)
		if err != nil {
			return resolvedDiscoveryEntities{}, fmt.Errorf("resolve cast: %w", err)
		}
	}
	if len(query.Creators) > 0 {
		out.creatorIDs, out.creatorNames, err = c.resolvePeople(ctx, query.Creators)
		if err != nil {
			return resolvedDiscoveryEntities{}, fmt.Errorf("resolve creators: %w", err)
		}
	}
	return out, nil
}

func (c *Client) resolvePeople(ctx context.Context, names []string) ([]int, []string, error) {
	ids := make([]int, 0, len(names))
	canonical := make([]string, 0, len(names))
	seenNames := make([]string, 0, len(names))
	seenIDs := make(map[int]bool, len(names))
	for _, requested := range names {
		requested = strings.TrimSpace(requested)
		duplicated := false
		for _, seen := range seenNames {
			if strings.EqualFold(seen, requested) {
				duplicated = true
				break
			}
		}
		if requested == "" || duplicated {
			return nil, nil, fmt.Errorf("person name %q is blank or duplicated", requested)
		}
		seenNames = append(seenNames, requested)
		q := url.Values{"query": {requested}, "include_adult": {"false"}, "page": {"1"}}
		var response personSearchResponse
		if err := c.get(ctx, "/search/person?"+q.Encode(), &response); err != nil {
			return nil, nil, err
		}
		matches := make(map[int]string)
		for _, candidate := range response.Results {
			if candidate.ID > 0 && strings.EqualFold(strings.TrimSpace(candidate.Name), requested) {
				matches[candidate.ID] = strings.TrimSpace(candidate.Name)
			}
		}
		if len(matches) != 1 {
			return nil, nil, fmt.Errorf("person name %q resolved to %d exact identities", requested, len(matches))
		}
		for id, name := range matches {
			if seenIDs[id] {
				return nil, nil, fmt.Errorf("person names resolve to duplicate identity %d", id)
			}
			seenIDs[id] = true
			ids = append(ids, id)
			canonical = append(canonical, name)
		}
	}
	return ids, canonical, nil
}

func (c *Client) resolveNetwork(ctx context.Context, requested, country string) (int, string, error) {
	requested = strings.TrimSpace(requested)
	identities, err := c.loadNetworkIdentities(ctx)
	if err != nil {
		return 0, "", err
	}
	matches := make([]namedIdentity, 0, 1)
	for _, identity := range identities {
		if strings.EqualFold(strings.TrimSpace(identity.Name), requested) {
			matches = append(matches, identity)
		}
	}
	if len(matches) == 1 {
		return matches[0].ID, strings.TrimSpace(matches[0].Name), nil
	}
	if len(matches) == 0 {
		return 0, "", fmt.Errorf("network name %q resolved to no exact identity", requested)
	}
	country = strings.ToUpper(strings.TrimSpace(country))
	if country == "" {
		return 0, "", fmt.Errorf("network name %q is ambiguous; include origin_country", requested)
	}
	filtered := make([]namedIdentity, 0, 1)
	for _, identity := range matches {
		var detail struct {
			ID            int    `json:"id"`
			Name          string `json:"name"`
			OriginCountry string `json:"origin_country"`
		}
		if err := c.get(ctx, "/network/"+strconv.Itoa(identity.ID), &detail); err != nil {
			return 0, "", err
		}
		if detail.ID != identity.ID || !strings.EqualFold(strings.TrimSpace(detail.Name), requested) {
			return 0, "", fmt.Errorf("network identity %d details disagree with export", identity.ID)
		}
		if strings.EqualFold(strings.TrimSpace(detail.OriginCountry), country) {
			filtered = append(filtered, namedIdentity{ID: detail.ID, Name: strings.TrimSpace(detail.Name)})
		}
	}
	if len(filtered) != 1 {
		return 0, "", fmt.Errorf("network name %q and origin_country %q resolved to %d identities", requested, country, len(filtered))
	}
	return filtered[0].ID, filtered[0].Name, nil
}

func (c *Client) loadNetworkIdentities(ctx context.Context) ([]namedIdentity, error) {
	c.networkMu.Lock()
	if len(c.networkIdentities) > 0 {
		cached := append([]namedIdentity(nil), c.networkIdentities...)
		c.networkMu.Unlock()
		return cached, nil
	}
	c.networkMu.Unlock()

	var lastErr error
	now := time.Now().UTC()
	for daysAgo := range 3 {
		day := now.AddDate(0, 0, -daysAgo)
		filename := "tv_network_ids_" + day.Format("01_02_2006") + ".json.gz"
		identities, err := c.fetchNetworkExport(ctx, filename)
		if err != nil {
			lastErr = err
			continue
		}
		c.networkMu.Lock()
		if len(c.networkIdentities) == 0 {
			c.networkIdentities = append([]namedIdentity(nil), identities...)
		}
		cached := append([]namedIdentity(nil), c.networkIdentities...)
		c.networkMu.Unlock()
		return cached, nil
	}
	return nil, fmt.Errorf("load TMDB network identity export: %w", lastErr)
}

func (c *Client) fetchNetworkExport(ctx context.Context, filename string) ([]namedIdentity, error) {
	requestURL := c.networkExportBaseURL + "/" + filename
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/gzip")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tmdb network export GET: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tmdb network export GET: status %d", resp.StatusCode)
	}
	zipped, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tmdb network export gzip: %w", err)
	}
	defer func() { _ = zipped.Close() }()
	blob, err := io.ReadAll(io.LimitReader(zipped, maxNetworkExportBytes+1))
	if err != nil {
		return nil, fmt.Errorf("tmdb network export read: %w", err)
	}
	if len(blob) > maxNetworkExportBytes {
		return nil, fmt.Errorf("tmdb network export exceeds %d bytes", maxNetworkExportBytes)
	}
	seen := make(map[int]bool)
	identities := make([]namedIdentity, 0)
	for lineNumber, line := range bytes.Split(blob, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var identity namedIdentity
		if err := json.Unmarshal(line, &identity); err != nil {
			return nil, fmt.Errorf("tmdb network export line %d: %w", lineNumber+1, err)
		}
		identity.Name = strings.TrimSpace(identity.Name)
		if identity.ID <= 0 || identity.Name == "" || seen[identity.ID] {
			return nil, fmt.Errorf("tmdb network export line %d has invalid or duplicate identity", lineNumber+1)
		}
		seen[identity.ID] = true
		identities = append(identities, identity)
	}
	if len(identities) == 0 {
		return nil, fmt.Errorf("tmdb network export is empty")
	}
	return identities, nil
}

func attachEntityEvidence(candidates []catalog.Candidate, entities resolvedDiscoveryEntities) {
	for i := range candidates {
		if entities.networkName != "" {
			candidates[i].Networks = []string{entities.networkName}
		}
		candidates[i].Cast = append([]string(nil), entities.castNames...)
		candidates[i].Creators = append([]string(nil), entities.creatorNames...)
	}
}

func joinIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}
