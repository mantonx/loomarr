package suggest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/reference"
	"github.com/loomarr/loomarr/internal/textmatch"
)

const maxReferenceTitleQueries = 8

var namedSetAcronymPattern = regexp.MustCompile(
	`(?:(?i:\b(?:for|from|based\s+on)\s+)[A-Z][A-Z0-9&]{2,9}\b|\b[A-Z][A-Z0-9&]{2,9}(?i:\s+(?:lineup|block|channel)\b))`,
)

type referenceGrounding struct {
	candidates []catalog.Candidate
	trace      DecisionTrace
	messages   []llm.Message
}

// groundReference resolves a pasted public page before inference and
// exact-searches a bounded set of its title anchors. The synthesized assistant
// tool-call/result pair is an honest record of catalog work Loomarr already did;
// it lets the unchanged planner contract finalize from grounded ids in one turn.
func (s *Suggester) groundReference(ctx context.Context, intent *Intent) (referenceGrounding, bool, error) {
	rawURL, found := reference.URL(referenceIntentText(*intent))
	if !found {
		return referenceGrounding{}, false, nil
	}
	if s.references == nil {
		return referenceGrounding{}, true, errors.New("reference resolver is unavailable")
	}

	evidence, err := s.references.Lookup(ctx, reference.Lookup{URL: rawURL})
	if err != nil {
		return referenceGrounding{}, true, fmt.Errorf("resolve reference: %w", err)
	}
	titles := boundedReferenceTitles(evidence.TitleAnchors)
	if len(titles) == 0 {
		return referenceGrounding{}, true, errors.New("reference contains no title anchors")
	}

	intent.ReferenceResolved = true
	intent.ReferenceTitles = titles
	intent.referenceEvidence = evidence
	intent.referenceKeys = make(map[provision.Key]bool)

	byKey := make(map[provision.Key]catalog.Candidate)
	messages := make([]llm.Message, 0, len(titles)*2)
	for index, title := range titles {
		candidates, searchErr := s.catalog.Search(ctx, title, catalog.ScopeAll, catalogSearchLimit)
		if searchErr != nil {
			return referenceGrounding{}, true, fmt.Errorf("search reference title %q: %w", title, searchErr)
		}
		exact := make([]catalog.Candidate, 0, len(candidates))
		for _, candidate := range candidates {
			if !sameExactTitle(candidate.Name, title) {
				continue
			}
			key, keyErr := candidate.Key()
			if keyErr != nil {
				continue
			}
			byKey[key] = candidate
			exact = append(exact, candidate)
		}
		callID := fmt.Sprintf("loomarr-reference-catalog-%d", index+1)
		result, _ := json.Marshal(toolResult(exact))
		messages = append(messages,
			llm.Message{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{
				ID: callID, Name: catalogToolName, Arguments: map[string]any{"query": title},
			}}},
			llm.Message{Role: llm.Tool, ToolCallID: callID, Content: string(result)},
		)
	}

	candidates := make([]catalog.Candidate, 0, len(byKey))
	for _, candidate := range byKey {
		candidates = append(candidates, candidate)
	}
	ranked := rankGroundedCandidatesWithTrace(decisionRankQuery(*intent), candidates, nil)
	if len(ranked.Candidates) > catalogSearchLimit {
		ranked.Candidates = ranked.Candidates[:catalogSearchLimit]
	}
	for _, candidate := range ranked.Candidates {
		if key, keyErr := candidate.Key(); keyErr == nil {
			intent.referenceKeys[key] = true
		}
	}
	intent.referenceCandidates = append([]catalog.Candidate(nil), ranked.Candidates...)

	return referenceGrounding{
		candidates: ranked.Candidates,
		trace:      ranked.Trace,
		messages:   messages,
	}, true, nil
}

func referenceIntentText(intent Intent) string {
	return strings.Join([]string{
		intent.Description,
		intent.RefineText,
		strings.Join(intent.MustInclude, " "),
	}, " ")
}

// requiresMembershipEvidence identifies requests whose defining quality is
// belonging to a named set, rather than a fuzzy theme. Ordinary genre/mood
// requests remain model-judged because lexical overlap cannot encode synonyms.
func requiresMembershipEvidence(intent Intent) bool {
	if intent.ReferenceResolved {
		return true
	}
	text := referenceIntentText(intent)
	lower := strings.ToLower(text)
	return namedSetAcronymPattern.MatchString(text) ||
		strings.Contains(lower, "programming block") ||
		strings.Contains(lower, "lineup from") ||
		strings.Contains(lower, "line-up from")
}

func boundedReferenceTitles(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, min(len(values), maxReferenceTitleQueries))
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		key := strings.ToLower(value)
		if value == "" || len(value) > 120 || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
		if len(result) == maxReferenceTitleQueries {
			break
		}
	}
	return result
}

func sameExactTitle(candidate, anchor string) bool {
	return textmatch.ContainsPhrase(candidate, anchor) && textmatch.ContainsPhrase(anchor, candidate)
}
