//go:build eval

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/suggest"
	"github.com/loomarr/loomarr/internal/tmdb"
)

// buildSuggester constructs the REAL suggester exactly as main.go does — real LLM
// provider (from LLM_*), real catalog (real library search + real TMDB), real
// validator — so the eval exercises the production path, not a mock. Returns an
// error (skips the eval) when the required env isn't configured.
func buildSuggester() (*suggest.Suggester, ScheduleMaterializer, *observedProvider, error) {
	clients, err := buildEvalClients()
	if err != nil {
		return nil, nil, nil, err
	}
	suggester, observed, err := buildSuggesterWithClients(clients)
	if err != nil {
		return nil, nil, nil, err
	}
	return suggester, NewLiveScheduleMaterializer(clients.library, clients.tmdb), observed, nil
}

type evalClients struct {
	library *library.Client
	tmdb    *tmdb.Client
}

// buildEvalClients constructs only evidence adapters. It deliberately cannot
// construct or call an inference provider, so live schedule preflight can fail
// closed before any model resource is available to spend.
func buildEvalClients() (evalClients, error) {
	libURL := os.Getenv("LIBRARY_URL")
	libTok := os.Getenv("LIBRARY_TOKEN")
	if libURL == "" || libTok == "" {
		return evalClients{}, fmt.Errorf("LIBRARY_URL + LIBRARY_TOKEN required for the eval")
	}
	flavor := library.Emby
	if os.Getenv("LIBRARY_FLAVOR") == "jellyfin" {
		flavor = library.Jellyfin
	}
	lib := library.New(flavor, libURL, libTok, "loomarr-eval")

	tmdbKey := os.Getenv("TMDB_API_KEY")
	if tmdbKey == "" {
		return evalClients{}, fmt.Errorf("TMDB_API_KEY required for the eval (grounding + discovery)")
	}
	tm := tmdb.New(tmdbKey)
	return evalClients{library: lib, tmdb: tm}, nil
}

func buildSuggesterWithClients(clients evalClients) (*suggest.Suggester, *observedProvider, error) {
	cat := catalog.New(clients.library, clients.tmdb).WithPresence(libPresence{clients.library})
	provider, err := buildProvider()
	if err != nil {
		return nil, nil, err
	}
	observed := &observedProvider{inner: provider}
	return suggest.New(observed, cat, clients.tmdb, 10).WithRatings(clients.tmdb), observed, nil
}

// observedProvider records only structural evaluation evidence—tool mode and
// candidate counts, never prompts, titles, credentials, or model output. This is
// what separates retrieval failures from model-selection failures in a scorecard.
type observedProvider struct {
	inner  llm.Provider
	mu     sync.Mutex
	obs    Observation
	ledger *providerResourceLedger
}

type Observation struct {
	ModelCalls          int    `json:"modelCalls"`
	ToolCalls           int    `json:"toolCalls"`
	TitleCalls          int    `json:"titleCalls"`
	GenreCalls          int    `json:"genreCalls"`
	KeywordCalls        int    `json:"keywordCalls"`
	NetworkCalls        int    `json:"networkCalls"`
	CastCalls           int    `json:"castCalls"`
	CreatorCalls        int    `json:"creatorCalls"`
	PeopleCalls         int    `json:"peopleCalls"`
	CandidatesSurfaced  int    `json:"candidatesSurfaced"`
	GroundingStage      string `json:"groundingStage"`
	generatorCalls      []InferenceCall
	generatorTokens     int
	generatorSpend      exactDecimal
	generatorUsageErr   string
	generatorTokenKnown bool
	generatorSpendKnown bool
	generatorBudgetErr  string
	toolMessagesSeen    int
	toolCallsSeen       int
}

func (p *observedProvider) Name() string { return p.inner.Name() }

func (p *observedProvider) Begin() {
	p.mu.Lock()
	p.obs = Observation{generatorSpend: zeroDecimal()}
	p.ledger = nil
	p.mu.Unlock()
}

func (p *observedProvider) beginResourceRun(limits ResourceBudget, run, suite *resourceAccumulator) {
	p.mu.Lock()
	p.ledger = &providerResourceLedger{limits: limits, run: run, suite: suite}
	p.mu.Unlock()
}

func (p *observedProvider) Chat(ctx context.Context, messages []llm.Message, opts llm.ChatOptions) (llm.Response, error) {
	p.observeToolCalls(messages)
	p.observeToolResults(messages)
	p.mu.Lock()
	ledger := p.ledger
	if ledger != nil {
		if message := ledger.beforeCall(); message != "" {
			p.obs.generatorBudgetErr = message
			p.mu.Unlock()
			return llm.Response{}, fmt.Errorf("generator provider call blocked: %w", errProviderBudgetExhausted)
		}
	}
	p.mu.Unlock()
	response, err := p.inner.Chat(ctx, messages, opts)
	p.mu.Lock()
	p.obs.ModelCalls++
	call := scrubAttribution(response.Attribution)
	observeGeneratorResourceUsage(&p.obs, call)
	if len(p.obs.generatorCalls) < suggest.ProductionBounds().MaxModelCalls {
		p.obs.generatorCalls = append(p.obs.generatorCalls, call)
	}
	if ledger != nil {
		if message := ledger.afterCall(call); message != "" {
			p.obs.generatorBudgetErr = message
			if err == nil {
				err = fmt.Errorf("generator provider usage rejected: %w", errProviderBudgetExhausted)
			}
		}
	}
	p.mu.Unlock()
	return response, err
}

func (p *observedProvider) observeToolCalls(messages []llm.Message) {
	var calls []llm.ToolCall
	for _, message := range messages {
		if message.Role == llm.Assistant {
			calls = append(calls, message.ToolCalls...)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, call := range calls[p.obs.toolCallsSeen:] {
		p.obs.ToolCalls++
		network, _ := call.Arguments["network"].(string)
		cast := stringSliceAny(call.Arguments["cast"])
		creators := stringSliceAny(call.Arguments["creators"])
		switch {
		case strings.TrimSpace(network) != "":
			p.obs.NetworkCalls++
		case len(cast) > 0 && len(creators) > 0:
			p.obs.PeopleCalls++
		case len(cast) > 0:
			p.obs.CastCalls++
		case len(creators) > 0:
			p.obs.CreatorCalls++
		case len(stringSliceAny(call.Arguments["keywords"])) > 0:
			p.obs.KeywordCalls++
		case len(stringSliceAny(call.Arguments["genres"])) > 0:
			p.obs.GenreCalls++
		default:
			p.obs.TitleCalls++
		}
	}
	p.obs.toolCallsSeen = len(calls)
}

func (p *observedProvider) observeToolResults(messages []llm.Message) {
	var contents []string
	for _, message := range messages {
		if message.Role == llm.Tool {
			contents = append(contents, message.Content)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, content := range contents[p.obs.toolMessagesSeen:] {
		var candidates []json.RawMessage
		if json.Unmarshal([]byte(content), &candidates) == nil {
			p.obs.CandidatesSurfaced += len(candidates)
		}
	}
	p.obs.toolMessagesSeen = len(contents)
}

func (p *observedProvider) Snapshot(groundErr error) Observation {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.obs
	out.generatorCalls = slices.Clone(p.obs.generatorCalls)
	out.generatorSpend = p.obs.generatorSpend.clone()
	switch {
	case groundErr == nil:
		out.GroundingStage = "grounded"
	case !errors.Is(groundErr, suggest.ErrNoGroundedTitles) && strings.Contains(groundErr.Error(), "llm chat:"):
		out.GroundingStage = "provider_error"
	case !errors.Is(groundErr, suggest.ErrNoGroundedTitles):
		out.GroundingStage = "generation_error"
	case out.ToolCalls == 0:
		out.GroundingStage = "no_tool_call"
	case out.CandidatesSurfaced == 0:
		out.GroundingStage = "retrieval_empty"
	default:
		out.GroundingStage = "selection_empty"
	}
	return out
}

func observeGeneratorResourceUsage(observation *Observation, call InferenceCall) {
	tokens, ok := checkedAdd(call.Tokens.Prompt, call.Tokens.Completion)
	if !ok {
		observation.generatorUsageErr = "provider token usage is invalid or overflowing"
		return
	}
	observation.generatorTokens, ok = checkedAdd(observation.generatorTokens, tokens)
	if !ok {
		observation.generatorUsageErr = "generator token usage overflow"
		return
	}
	if tokens > 0 {
		observation.generatorTokenKnown = true
	}
	if call.ChargeStatus != InferenceChargeReported {
		if call.RequestedProvider == "ollama" {
			observation.generatorSpendKnown = true
		}
		return
	}
	observation.generatorSpendKnown = true
	if call.Charge.Currency != "USD" {
		observation.generatorUsageErr = "non-USD provider charge cannot satisfy the declared USD budget"
		return
	}
	charge, valid := parseExactDecimal(call.Charge.Amount)
	if !valid {
		observation.generatorUsageErr = "provider charge is invalid"
		return
	}
	if observation.generatorSpend.coefficient == nil {
		observation.generatorSpend = zeroDecimal()
	}
	observation.generatorSpend = observation.generatorSpend.add(charge)
}

func stringSliceAny(value any) []any {
	items, _ := value.([]any)
	return items
}

// buildProvider mirrors cmd/loomarr's buildProviderFor: Ollama for local, the
// OpenAI-compatible client otherwise. Driven by env (the same knobs production uses).
func buildProvider() (llm.Provider, error) {
	config, _ := certificationRoleConfigsFromEnv()
	if config.Model == "" {
		return nil, fmt.Errorf("LLM not configured (set LLM_PROVIDER/LLM_URL/LLM_MODEL[/LLM_API_KEY])")
	}
	return NewCertificationProvider(config)
}

func buildJudgeProvider() (llm.Provider, error) {
	_, config := certificationRoleConfigsFromEnv()
	return NewCertificationProvider(config)
}

func certificationRoleConfigsFromEnv() (CertificationProviderConfig, CertificationProviderConfig) {
	generator := CertificationProviderConfig{
		Provider: os.Getenv("LLM_PROVIDER"), BaseURL: os.Getenv("LLM_URL"),
		Model: os.Getenv("LLM_MODEL"), APIKey: os.Getenv("LLM_API_KEY"),
		UpstreamProvider: os.Getenv("LOOMARR_EVAL_GENERATOR_UPSTREAM_PROVIDER"),
	}
	judge := CertificationProviderConfig{
		Provider:         firstNonEmpty(os.Getenv("LOOMARR_EVAL_JUDGE_PROVIDER"), generator.Provider),
		BaseURL:          firstNonEmpty(os.Getenv("LOOMARR_EVAL_JUDGE_URL"), generator.BaseURL),
		Model:            firstNonEmpty(os.Getenv("LOOMARR_EVAL_JUDGE"), generator.Model),
		APIKey:           firstNonEmpty(os.Getenv("LOOMARR_EVAL_JUDGE_API_KEY"), generator.APIKey),
		UpstreamProvider: os.Getenv("LOOMARR_EVAL_JUDGE_UPSTREAM_PROVIDER"),
	}
	return generator, judge
}

// CertificationIdentitiesFromEnv resolves the exact role identities written to
// a scorecard without constructing either provider.
func CertificationIdentitiesFromEnv() (ModelIdentity, ModelIdentity) {
	generator, judge := certificationRoleConfigsFromEnv()
	return ModelIdentity{Provider: normalizedProviderIdentity(generator.Provider), Model: generator.Model},
		ModelIdentity{Provider: normalizedProviderIdentity(judge.Provider), Model: judge.Model}
}

func normalizedProviderIdentity(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return "ollama"
	}
	return provider
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// libPresence adapts the library client to catalog.LibraryPresence (mirrors main.go).
type libPresence struct{ lib *library.Client }

func (a libPresence) Present(ctx context.Context, mt provision.MediaType, tmdbID, tvdbID int) (catalog.Presence, bool, error) {
	kind, id := library.TMDB, strconv.Itoa(tmdbID)
	if tmdbID == 0 && tvdbID != 0 {
		kind, id = library.TVDB, strconv.Itoa(tvdbID)
	}
	lmt := library.Movie
	if mt == provision.Series {
		lmt = library.Series
	}
	d, present, err := a.lib.LookupDetail(ctx, kind, id, lmt)
	if err != nil || !present {
		return catalog.Presence{}, false, err
	}
	return catalog.Presence{LibraryItemID: d.ID, OfficialRating: d.OfficialRating, Genres: d.Genres}, true, nil
}

// mapIntent converts the corpus Intent to a suggest.Intent.
func mapIntent(i Intent) suggest.Intent {
	return suggest.Intent{
		Description: i.Description, Era: i.Era, Tone: i.Tone, RuntimeTgt: i.RuntimeTgt,
		MustInclude: i.MustInclude, MustExclude: i.MustExclude, MaxAcquire: i.MaxAcquire,
	}
}

// Result is the scored outcome of one case.
type Result struct {
	Case                       string          `json:"case"`
	Trial                      int             `json:"trial"`
	Failures                   []string        `json:"failures"` // all evaluation failures; empty means the trial passed
	FailureStage               FailureStage    `json:"failureStage,omitempty"`
	ThemeFit                   float64         `json:"themeFit"`
	Lineup                     int             `json:"lineup"`
	Acquisitions               int             `json:"acquisitions"`
	Ceiling                    string          `json:"ceiling"` // the extracted policy ceiling
	JudgeScore                 float64         `json:"judgeScore"`
	RelevanceScore             float64         `json:"relevanceScore"`
	SerendipityScore           float64         `json:"serendipityScore"`
	JudgeNote                  string          `json:"judgeNote"`
	JudgeError                 string          `json:"judgeError,omitempty"`
	ScheduledPrograms          []string        `json:"scheduledPrograms,omitempty"`
	GroundedCompletionExpected bool            `json:"groundedCompletionExpected"`
	GroundedCompletion         bool            `json:"groundedCompletion"`
	ToolOperationExpected      bool            `json:"toolOperationExpected"`
	CorrectToolOperation       bool            `json:"correctToolOperation"`
	SchemaValid                bool            `json:"schemaValid"`
	PolicyAccuracyExpected     bool            `json:"policyAccuracyExpected"`
	PolicyAccurate             bool            `json:"policyAccurate"`
	ProposalQualityExpected    bool            `json:"proposalQualityExpected"`
	ProposalQuality            bool            `json:"proposalQuality"`
	RecoveryExpected           bool            `json:"recoveryExpected"`
	RecoverySuccessful         bool            `json:"recoverySuccessful"`
	GeneratorCalls             []InferenceCall `json:"generatorCalls"`
	JudgeCalls                 []InferenceCall `json:"judgeCalls"`
	Observation
}

func (r Result) Passed() bool { return len(r.Failures) == 0 }

func (r *Result) addFailures(stage FailureStage, failures ...string) {
	if len(failures) == 0 {
		return
	}
	if r.FailureStage == "" {
		r.FailureStage = stage
	}
	r.Failures = append(r.Failures, failures...)
}

// deterministicChecks applies the corpus case's hard expectations to a proposal.
// These are the assertions a mock could never make — they run against the real
// grounded output. Returns the list of failures (empty = all gates passed).
func deterministicChecks(c Case, prop suggest.Proposal, groundErr error) []string {
	var f []string

	// No-fabrication case: the grounded set may be any size (even 0), but EVERY item
	// must have a real, resolvable id — the grounding guarantee. A clean empty/failed
	// result also satisfies it (nothing fabricated). A provider or generation
	// failure cannot certify the invariant because the production path did not run.
	if c.NoFabrication {
		if groundErr != nil && errors.Is(groundErr, suggest.ErrNoGroundedTitles) {
			if c.MinGrounded == 0 {
				return nil // an explicit abstention case fabricated nothing → passes
			}
			return []string{fmt.Sprintf("grounding failed: %v", groundErr)}
		}
		if groundErr != nil {
			return []string{fmt.Sprintf("evaluation failed before grounding could be assessed: %v", groundErr)}
		}
		for _, it := range allItems(prop) {
			if _, err := it.Key(); err != nil {
				f = append(f, fmt.Sprintf("FABRICATION: grounded item %q has no resolvable id: %v", it.Name, err))
			}
		}
		if len(f) > 0 {
			return f
		}
	}
	if groundErr != nil {
		return []string{fmt.Sprintf("grounding failed: %v", groundErr)}
	}
	if len(c.RequireTitles) > 0 || len(c.ForbidTitles) > 0 {
		switch c.TitleEvidence {
		case TitleEvidenceGrounded:
			groundedTitles := make(map[string]bool, len(prop.Lineup)+len(prop.Acquisitions))
			for _, item := range allItems(prop) {
				groundedTitles[normalizeExactTitle(item.Name)] = true
			}
			for _, required := range c.RequireTitles {
				normalized := normalizeExactTitle(required)
				if normalized == "" {
					f = append(f, "required grounded title must not be blank")
				} else if !groundedTitles[normalized] {
					f = append(f, fmt.Sprintf("required grounded title %q is missing", required))
				}
			}
			for _, forbidden := range c.ForbidTitles {
				normalized := normalizeExactTitle(forbidden)
				if normalized == "" {
					f = append(f, "forbidden grounded title must not be blank")
				} else if groundedTitles[normalized] {
					f = append(f, fmt.Sprintf("forbidden grounded title %q is present", forbidden))
				}
			}
		case TitleEvidenceScheduled:
			// Scheduled assertions run only after concrete materialization.
		default:
			f = append(f, fmt.Sprintf("exact title assertions require a valid evidence scope, got %q", c.TitleEvidence))
		}
	}

	if len(prop.Lineup) < c.MinLineup {
		f = append(f, fmt.Sprintf("lineup %d < required %d", len(prop.Lineup), c.MinLineup))
	}
	if len(prop.Acquisitions) < c.MinAcquisitions {
		f = append(f, fmt.Sprintf("acquisitions %d < required %d", len(prop.Acquisitions), c.MinAcquisitions))
	}
	if grounded := len(prop.Lineup) + len(prop.Acquisitions); grounded < c.MinGrounded {
		f = append(f, fmt.Sprintf("grounded titles %d < required %d", grounded, c.MinGrounded))
	}
	if len(c.RequireKeys) > 0 {
		grounded := make(map[provision.Key]bool, len(prop.Lineup)+len(prop.Acquisitions))
		for _, item := range allItems(prop) {
			if key, err := item.Key(); err == nil {
				grounded[key] = true
			}
		}
		for _, required := range c.RequireKeys {
			if !grounded[required] {
				f = append(f, fmt.Sprintf("required grounded key %q is missing", required))
			}
		}
	}
	groundedKeys := make(map[provision.Key]bool, len(prop.Lineup)+len(prop.Acquisitions))
	movies, series := 0, 0
	genres := map[string]bool{}
	for _, item := range allItems(prop) {
		if key, err := item.Key(); err == nil {
			groundedKeys[key] = true
		}
		switch item.MediaType {
		case provision.Movie:
			movies++
		case provision.Series:
			series++
		}
		if len(c.AllowedMediaTypes) > 0 && !slices.Contains(c.AllowedMediaTypes, item.MediaType) {
			f = append(f, fmt.Sprintf("grounded item %q has media type %q outside allowed set %v", item.Name, item.MediaType, c.AllowedMediaTypes))
		}
		for _, genre := range item.Genres {
			genres[strings.ToLower(strings.TrimSpace(genre))] = true
		}
	}
	for _, forbidden := range c.ForbidKeys {
		if groundedKeys[forbidden] {
			f = append(f, fmt.Sprintf("forbidden grounded key %q is present", forbidden))
		}
	}
	if movies < c.MinMovies {
		f = append(f, fmt.Sprintf("movies %d < required %d", movies, c.MinMovies))
	}
	if series < c.MinSeries {
		f = append(f, fmt.Sprintf("series %d < required %d", series, c.MinSeries))
	}
	if len(genres) < c.MinDistinctGenres {
		f = append(f, fmt.Sprintf("distinct genres %d < required %d", len(genres), c.MinDistinctGenres))
	}
	if c.ExpectCeiling != "" && string(prop.Policy.Audience.Ceiling) != c.ExpectCeiling {
		f = append(f, fmt.Sprintf("expected ceiling %q, extracted %q", c.ExpectCeiling, prop.Policy.Audience.Ceiling))
	}
	if c.ExpectSeasonalMode != "" && string(prop.Policy.Seasonal.Mode) != c.ExpectSeasonalMode {
		f = append(f, fmt.Sprintf("expected seasonal mode %q, extracted %q", c.ExpectSeasonalMode, prop.Policy.Seasonal.Mode))
	}
	if c.ExpectSeasonalHolidays != nil && !slices.Equal(prop.Policy.Seasonal.Holidays, c.ExpectSeasonalHolidays) {
		f = append(f, fmt.Sprintf("expected seasonal holidays %v, extracted %v", c.ExpectSeasonalHolidays, prop.Policy.Seasonal.Holidays))
	}
	if c.ExpectOrdering != "" && string(prop.Policy.Ordering) != c.ExpectOrdering {
		f = append(f, fmt.Sprintf("expected ordering %q, extracted %q", c.ExpectOrdering, prop.Policy.Ordering))
	}
	if c.MinThemeFit > 0 && prop.Scores.ThemeFit < c.MinThemeFit {
		f = append(f, fmt.Sprintf("themeFit %.2f < required %.2f", prop.Scores.ThemeFit, c.MinThemeFit))
	}

	// SAFETY: no grounded item may carry a rating above the forbidden ceiling — the
	// end-to-end audience guarantee, measured on the real grounded output.
	if c.ForbidRatingsAbove != "" {
		ceiling := schedule.NormalizeRating(c.ForbidRatingsAbove)
		for _, it := range allItems(prop) {
			r := schedule.NormalizeRating(it.OfficialRating)
			// A rated item ABOVE the ceiling is a hard failure. An unrated item is not
			// asserted here (the enforcer fails it closed at reconcile; the corpus's
			// ExpectCeiling covers the extraction side).
			if r != "" && !ratingAtOrBelow(r, ceiling) {
				f = append(f, fmt.Sprintf("grounded item %q is rated %q, above the forbidden ceiling %q",
					it.Name, it.OfficialRating, c.ForbidRatingsAbove))
			}
		}
	}
	for _, it := range allItems(prop) {
		for _, forbidden := range c.ForbidGenres {
			for _, genre := range it.Genres {
				if strings.EqualFold(strings.TrimSpace(genre), strings.TrimSpace(forbidden)) {
					f = append(f, fmt.Sprintf("grounded item %q carries forbidden genre %q", it.Name, genre))
				}
			}
		}
		for _, forbidden := range c.ForbidTitleTerms {
			if strings.Contains(strings.ToLower(it.Name), strings.ToLower(forbidden)) {
				f = append(f, fmt.Sprintf("grounded item %q contains forbidden title term %q", it.Name, forbidden))
			}
		}
	}

	// Era binding: every item with a known year must fall within the expected range
	// (with a small slack, since discovery era is fuzzy).
	if c.ExpectEraFrom > 0 && c.ExpectEraTo > 0 {
		const slack = 3
		for _, it := range allItems(prop) {
			if it.Year > 0 && (it.Year < c.ExpectEraFrom-slack || it.Year > c.ExpectEraTo+slack) {
				f = append(f, fmt.Sprintf("grounded item %q (%d) is outside the expected era %d-%d",
					it.Name, it.Year, c.ExpectEraFrom, c.ExpectEraTo))
			}
		}
	}
	return f
}

func normalizeExactTitle(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " "))
}

func allItems(p suggest.Proposal) []suggest.ProposalItem {
	out := append([]suggest.ProposalItem{}, p.Lineup...)
	return append(out, p.Acquisitions...)
}

// ratingAtOrBelow reports whether item rating r is at or below ceiling on the
// shared ladder. Both must be mappable; an unmappable r returns false (caller
// handles the unrated case separately).
func ratingAtOrBelow(r, ceiling schedule.Rating) bool {
	// NormalizeRating already mapped both onto the ladder; reuse the schedule
	// package's own comparison by round-tripping through a tiny policy check would
	// be heavy — instead rank locally via the exported normalize + a fixed order.
	return ladderIndex(r) >= 0 && ladderIndex(ceiling) >= 0 && ladderIndex(r) <= ladderIndex(ceiling)
}

// ladderIndex mirrors schedule's rating ladder order for the eval's own comparison
// (kept local so the eval doesn't need an exported rank). Unmapped → -1.
func ladderIndex(r schedule.Rating) int {
	switch r {
	case "TV-Y":
		return 0
	case "TV-Y7":
		return 1
	case "TV-G", "G":
		return 2
	case "TV-PG", "PG":
		return 3
	case "TV-14", "PG-13":
		return 4
	case "TV-MA", "R", "NC-17":
		return 5
	}
	return -1
}
