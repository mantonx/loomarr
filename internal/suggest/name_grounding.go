package suggest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/provision"
)

type nameGroundingResult struct {
	query      nameGroundingQuery
	candidates []catalog.Candidate
	err        error
}

type nameGroundingQuery struct {
	name     string
	proposed []pick
}

const maxPickNameQueries = 8

// groundPickNames treats model-authored names only as bounded Catalog search
// input. It replaces every claimed id with the one unambiguous exact candidate
// returned by the Catalog before adding that candidate to the surfaced set.
func (s *Suggester) groundPickNames(
	ctx context.Context,
	intent Intent,
	feedback []FeedbackSignal,
	picks []pick,
	surfaced map[provision.Key]catalog.Candidate,
	trace *DecisionTrace,
) ([]pick, error) {
	queries := make([]nameGroundingQuery, 0, min(len(picks), maxPickNameQueries))
	for _, proposed := range picks {
		mediaType := provision.MediaType(proposed.MediaType)
		name := strings.Join(strings.Fields(proposed.Name), " ")
		if !mediaType.Valid() || name == "" {
			continue
		}
		queryIndex := -1
		for index := range queries {
			if sameExactTitle(queries[index].name, name) {
				queryIndex = index
				break
			}
		}
		proposed.Name = name
		if queryIndex < 0 {
			if len(queries) == maxPickNameQueries {
				continue
			}
			queries = append(queries, nameGroundingQuery{name: name, proposed: []pick{proposed}})
			continue
		}
		duplicateConstraint := false
		for _, existing := range queries[queryIndex].proposed {
			if existing.MediaType == proposed.MediaType && existing.Year == proposed.Year {
				duplicateConstraint = true
				break
			}
		}
		if !duplicateConstraint {
			queries[queryIndex].proposed = append(queries[queryIndex].proposed, proposed)
		}
	}

	results := make([]nameGroundingResult, len(queries))
	var wg sync.WaitGroup
	for index, query := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidates, err := s.catalog.Search(ctx, query.name, catalog.ScopeAll, catalogSearchLimit)
			if err != nil {
				err = fmt.Errorf("ground proposed title %q: %w", query.name, err)
			}
			results[index] = nameGroundingResult{query: query, candidates: candidates, err: err}
		}()
	}
	wg.Wait()

	grounded := make([]pick, 0, len(results))
	groundedKeys := make(map[provision.Key]bool)
	for _, result := range results {
		if result.err != nil {
			return nil, result.err
		}
		for _, proposed := range result.query.proposed {
			candidate, found := exactCandidateForPick(result.candidates, proposed)
			if !found {
				continue
			}
			key, _ := candidate.Key()
			if groundedKeys[key] {
				continue
			}
			groundedKeys[key] = true
			ranked := rankGroundedCandidatesWithTrace(decisionRankQuery(intent), []catalog.Candidate{candidate}, feedback)
			mergeDecisionTrace(trace, &ranked.Trace)
			surfaced[key] = candidate
			proposed.MediaType = string(candidate.MediaType)
			proposed.TMDBID = candidate.TMDBID
			proposed.TVDBID = candidate.TVDBID
			proposed.Name = candidate.Name
			grounded = append(grounded, proposed)
		}
	}
	return grounded, nil
}

func exactCandidateForPick(candidates []catalog.Candidate, proposed pick) (catalog.Candidate, bool) {
	mediaType := provision.MediaType(proposed.MediaType)
	byKey := make(map[provision.Key]catalog.Candidate)
	for _, candidate := range candidates {
		if candidate.MediaType != mediaType || !sameExactTitle(candidate.Name, proposed.Name) ||
			(proposed.Year > 0 && candidate.Year != proposed.Year) {
			continue
		}
		key, keyErr := candidate.Key()
		if keyErr == nil {
			byKey[key] = candidate
		}
	}
	if len(byKey) != 1 {
		return catalog.Candidate{}, false
	}
	var candidate catalog.Candidate
	for _, exact := range byKey {
		candidate = exact
	}
	return candidate, true
}
