package suggest

import (
	"context"
	"errors"
	"fmt"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/reference"
)

// catalogToolName is the single tool the model may call. The grounding guarantee
// (§8) is enforced structurally: this is the ONLY way the model can name a title,
// and it returns real ids from the real library + TMDB.
const catalogToolName = "catalog_search"

// maxToolRounds bounds the tool-call loop so a misbehaving model can't spin
// forever (defense-in-depth; JOB_TIMEOUT is the outer bound).
const maxToolRounds = 6

// maxEmptyRetrievals gives the model exactly the initial search plus the
// alternate mode required by the prompt contract. A model that keeps searching
// after both return no candidates cannot become grounded in that run; letting it
// consume the remaining structural budget turns an honest empty retrieval into
// a misleading budget failure.
const maxEmptyRetrievals = 2

// catalogSearchLimit is how many candidates the catalog tool returns per call.
// Higher than the old hardcoded 12 so an abstract/themed intent surfaces enough
// rows for the model to ground a real lineup before truncation, without flooding
// a small local model's context.
const catalogSearchLimit = 24

// groundedTemp is the sampling temperature for the grounded/JSON turns. Low so the
// model adheres to the tool-call + JSON-schema contract rather than getting
// creative — tool-calling and structured output want determinism, not variety.
var groundedTemp = 0.2

// groundedMaxTokens bounds hosted-provider cost reservation and keeps a runaway
// final response from crowding the tool loop. Two thousand tokens comfortably fit
// the bounded proposal schema (at most 24 surfaced candidates and 10 actionable
// acquisitions) while avoiding an unbounded provider default on every turn.
const groundedMaxTokens = 2048

// chatOpts builds the per-turn ChatOptions with the tools + grounded sampling.
// temp lets the repair loop lower it further on a retry.
//
// JSONMode is deliberately OFF while tools are offered. Forcing format:json AND
// giving tools corrupts the tool-call channel on some models (e.g. Qwen3): they
// satisfy "output JSON" by emitting the tool call as a JSON OBJECT in the content
// field instead of using the native tool_calls array — so the grounding loop sees
// no tool call and mis-parses it as a (pick-less) final answer, yielding an empty
// proposal. The system prompt already mandates "reply with ONLY JSON" for the
// FINAL turn. Once retrieval returns candidates, the tool loop removes tools and
// JSONMode becomes true for finalization and every repair. (Caught live: qwen3:8b
// on a themed intent — correct genres, but the call landed in content, not tool_calls.)
func chatOpts(tools []llm.ToolSchema, temp float64) llm.ChatOptions {
	return llm.ChatOptions{
		Tools: tools, JSONMode: len(tools) == 0, Temperature: &temp, MaxTokens: groundedMaxTokens,
	}
}

// Validator re-checks a proposed acquisition against reality before it's
// actionable (§8): exists on TMDB AND not already present in the library. The
// catalog already tags in_library; this is the belt-and-suspenders re-validation
// the design mandates for anything that can trigger a download.
type Validator interface {
	// Exists reports whether a TMDB id is real (drops fabricated ids).
	Exists(ctx context.Context, mt provision.MediaType, tmdbID int) (bool, error)
}

// RatingSource fills the content rating for an acquisition NOT yet in the library
// (so with no library rating) from TMDB, at proposal time (§389 amendment). Optional:
// nil ⇒ acquisitions stay unrated until they land, then the reconciler heals them —
// this just lets a rated acquisition survive as a pending slot under an audience
// ceiling in the meantime instead of being dropped. TMDB coverage is sparse, so an
// empty answer is normal.
type RatingSource interface {
	ContentRating(ctx context.Context, mt provision.MediaType, tmdbID int) (string, error)
}

type FeedbackSource interface {
	Signals(context.Context, Intent) ([]FeedbackSignal, error)
}

// Suggester turns an intent into a grounded proposal (§8). It composes the
// provider-neutral LLM, the catalog (grounding tool + search), and the validator
// (TMDB exists-check).
type Suggester struct {
	llm        llm.Provider
	catalog    *catalog.Catalog
	validator  Validator
	ratings    RatingSource // optional acquisition-rating enrichment (§389)
	feedback   FeedbackSource
	references reference.Resolver
	maxAcq     int // default SUGGEST_MAX_ACQUISITIONS cap
}

func (s *Suggester) WithFeedback(source FeedbackSource) *Suggester {
	s.feedback = source
	return s
}

// WithRatings wires the acquisition-rating enrichment (§389 amendment). Returns the
// suggester for chaining; keeps New's signature stable.
func (s *Suggester) WithRatings(r RatingSource) *Suggester {
	s.ratings = r
	return s
}

// WithReferences enables bounded source resolution for pasted public pages. A
// nil resolver makes a URL-backed Intent fail closed before model inference.
func (s *Suggester) WithReferences(resolver reference.Resolver) *Suggester {
	s.references = resolver
	return s
}

// New builds a Suggester. maxAcq is the default acquisition cap (§8 quota).
func New(provider llm.Provider, cat *catalog.Catalog, v Validator, maxAcq int) *Suggester {
	if maxAcq <= 0 {
		maxAcq = 10
	}
	return &Suggester{llm: provider, catalog: cat, validator: v, maxAcq: maxAcq}
}

// Suggest runs the grounded generation for an intent (§8). It drives the tool-use
// loop (model → catalog_search → results → model → final JSON), parses the
// model's picks, resolves each against the catalog (grounding: drop anything not
// resolvable to a real id), re-validates acquisitions (exists on TMDB), and
// scores the result deterministically. Returns a Proposal ready for the approval
// queue — NEVER auto-executed (§8 human-in-the-loop).
// maxRepairs bounds the JSON-repair re-asks after the tool loop produces a final
// turn that's empty or not valid schema JSON. Small — a model that can't produce
// the schema in a couple of nudges won't in ten. Separate from maxToolRounds.
const maxRepairs = 2

// StructuralBounds is the production Suggester's closed worst-case envelope for
// one Suggest call. Evaluation and operational diagnostics consume this value so
// they cannot drift from the actual tool loop by copying private constants.
type StructuralBounds struct {
	MaxModelCalls         int
	MaxToolCalls          int
	MaxCandidatesSurfaced int
}

// ProductionBounds returns the one authoritative structural/call envelope for a
// Suggest invocation: the initial generation, one grounding retry, and each
// bounded schema repair may each consume the complete tool loop.
func ProductionBounds() StructuralBounds {
	maxCalls := maxToolRounds * (maxRepairs + 2)
	return StructuralBounds{
		MaxModelCalls: maxCalls, MaxToolCalls: maxCalls,
		MaxCandidatesSurfaced: maxCalls * catalogSearchLimit,
	}
}

const groundingRetryPrompt = `You returned no grounded picks without finding usable catalog candidates. ` +
	`You MUST call catalog_search now. Use title search for a named title, genre discovery for a genre, ` +
	`or keywords for a holiday, motif, franchise, or topic. Then select only ids the tool returns.`

func (s *Suggester) Suggest(ctx context.Context, intent Intent) (Proposal, error) {
	allAdjacent := intent.Adjacent
	var feedback []FeedbackSignal
	if s.feedback != nil {
		var err error
		feedback, err = s.feedback.Signals(ctx, intent)
		if err != nil {
			return Proposal{}, fmt.Errorf("load explicit discovery feedback: %w", err)
		}
		intent.Adjacent = filterAdjacentFeedback(intent.Adjacent, feedback)
	}
	referenceSeed, hasReference, referenceErr := s.groundReference(ctx, &intent)
	if referenceErr != nil {
		trace := DecisionTrace{Version: DecisionTraceVersion, Terminal: TerminalRetrievalFailure}
		cause := fmt.Errorf("%w: %v", ErrNoGroundedTitles, referenceErr)
		return Proposal{}, NewFailure(FailureCodeNoGroundedTitles, trace, cause)
	}
	if hasReference && len(referenceSeed.candidates) == 0 {
		trace := DecisionTrace{Version: DecisionTraceVersion, Terminal: ReasonRetrievalEmpty}
		cause := fmt.Errorf("%w: reference titles were not found in the configured catalog", ErrNoGroundedTitles)
		return Proposal{}, NewFailure(FailureCodeNoGroundedTitles, trace, cause)
	}
	messages := []llm.Message{
		{Role: llm.System, Content: systemPrompt},
		{Role: llm.User, Content: userPrompt(intent)},
	}
	tools := []llm.ToolSchema{catalogTool()}
	if hasReference {
		messages = append(messages, referenceSeed.messages...)
		tools = nil
	}

	// Track every candidate the tool surfaced this run, keyed by provisioning key.
	// A pick is grounded IFF it matches one of these — the model cannot smuggle in
	// an id the tool never returned. Threaded across the tool loop AND repair
	// re-asks, so grounding holds even when the final JSON is retried.
	surfaced := map[provision.Key]catalog.Candidate{}
	trace := DecisionTrace{Version: DecisionTraceVersion}
	mergeDecisionTrace(&trace, &referenceSeed.trace)
	temp := groundedTemp
	for _, candidate := range referenceSeed.candidates {
		if key, err := candidate.Key(); err == nil {
			surfaced[key] = candidate
		}
	}

	// PRE-SEED the adjacency corpus (§8.3) before generation. These are real catalog
	// candidates with real ids — the same shape a tool call produces — so seeding them here
	// widens what the model may pick from WITHOUT weakening the chokepoint: buildProposal
	// still accepts a pick iff it appears in `surfaced`, and these appear because the
	// catalog genuinely returned them. Merging after generation instead would append picks
	// nothing ever checked, which is precisely what grounding prevents.
	//
	// The model still chooses. An offered title it ignores is simply not picked.
	adjacentCandidates := make([]catalog.Candidate, 0, len(allAdjacent))
	for _, a := range allAdjacent {
		k := provision.Key(a.Key)
		mt, provider, id, ok := provision.ParseKey(k)
		if !ok {
			continue // an unparseable key could never be acquired; drop rather than offer
		}
		cand := catalog.Candidate{
			MediaType: mt, Name: a.Name, Year: a.Year, Source: catalog.ScopeAdjacent,
		}
		// Rebuild the external id from the key so the candidate's own Key() round-trips —
		// a candidate whose identity doesn't reproduce would be dropped by the grounding
		// resolve, silently making every adjacency pick unpickable.
		switch provider {
		case "tmdb":
			cand.TMDBID = id
		case "tvdb":
			cand.TVDBID = id
		default:
			continue
		}
		adjacentCandidates = append(adjacentCandidates, cand)
	}
	if len(adjacentCandidates) > 0 {
		adjacentRanked := rankGroundedCandidatesWithTrace(decisionRankQuery(intent), adjacentCandidates, feedback)
		mergeDecisionTrace(&trace, &adjacentRanked.Trace)
		for _, cand := range adjacentRanked.Candidates {
			if k, err := cand.Key(); err == nil {
				surfaced[k] = cand
			}
		}
	}

	// Progress is reported from INSIDE generate, at each real transition (§8). It used
	// to be announced here — one `searching` before the loop and `reasoning` only after
	// it returned — which made the UI read "Searching the library" for the whole run,
	// including every model turn. That named the fastest step (a catalog query) as the
	// explanation for the slowest (inference), so the one number the operator wanted —
	// why is this taking so long — was exactly what the display hid.

	// Generate → parse, with a bounded repair loop: if the model's final turn is
	// empty or malformed JSON, append a corrective nudge and re-ask at a lower
	// temperature. maxToolRounds bounds each generation; maxRepairs bounds the
	// re-asks. JOB_TIMEOUT + httpx.TimeoutLLM are the hard ceilings.
	repairs := 0
	groundingRetried := false
	finalizationOnly := hasReference
	emptyRetrievals := 0
	for {
		final, err := s.generate(ctx, &messages, tools, surfaced, &trace, temp, intent, feedback, &finalizationOnly, &emptyRetrievals)
		if err != nil {
			return Proposal{}, err
		}
		out, perr := parsePicks(final)
		if perr == nil {
			if len(surfaced) == 0 && len(out.Picks) > 0 {
				out.Picks, err = s.groundPickNames(ctx, intent, feedback, out.Picks, surfaced, &trace)
				if err != nil {
					trace.Terminal = TerminalRetrievalFailure
					return Proposal{}, NewFailure(FailureProvider, trace, err)
				}
				if len(out.Picks) == 0 {
					trace.Terminal = FailureSelectionEmpty
					return Proposal{}, NewFailure(FailureCodeNoGroundedTitles, trace, ErrNoGroundedTitles)
				}
			}
			reportProgress(ctx, PhaseScoring, 0)
			prop, buildErr := s.buildProposal(ctx, intent, out, surfaced, &trace)
			if errors.Is(buildErr, ErrNoGroundedTitles) && len(surfaced) == 0 && !groundingRetried {
				groundingRetried = true
				messages = append(messages, llm.Message{Role: llm.User, Content: groundingRetryPrompt})
				temp = temp / 2
				continue
			}
			if buildErr != nil {
				if errors.Is(buildErr, ErrNoGroundedTitles) {
					if trace.Terminal == "" {
						trace.Terminal = FailureSelectionEmpty
					}
					return Proposal{}, NewFailure(FailureCodeNoGroundedTitles, trace, buildErr)
				}
				return Proposal{}, NewFailure(FailureProvider, trace, buildErr)
			}
			return prop, nil
		}
		if repairs >= maxRepairs {
			trace.Terminal = TerminalMalformedExhausted
			return Proposal{}, NewFailure(FailureProvider, trace, fmt.Errorf("suggester: model output not valid after %d repairs: %w", maxRepairs, perr))
		}
		// Nudge the model to fix its output, and turn the temperature down further
		// so it adheres to the schema rather than getting creative.
		messages = append(messages, llm.Message{Role: llm.User, Content: repairPrompt})
		temp = temp / 2
		repairs++
	}
}

// generate runs the tool-call loop until the model returns a final (non-tool)
// turn, appending assistant/tool messages to *messages and recording surfaced
// candidates for grounding. Returns the final content (possibly empty — the
// caller's repair loop handles that).
func (s *Suggester) generate(ctx context.Context, messages *[]llm.Message, tools []llm.ToolSchema, surfaced map[provision.Key]catalog.Candidate, trace *DecisionTrace, temp float64, intent Intent, feedback []FeedbackSignal, finalizationOnly *bool, emptyRetrievals *int) (string, error) {
	if *finalizationOnly {
		tools = nil
	}
	for round := 0; round < maxToolRounds; round++ {
		// The model turn is about to block — say so BEFORE awaiting it. This is the
		// slow step (model load + inference), so reporting it afterwards would leave
		// whatever ran previously on screen for the entire wait. See §8: a phase names
		// what is happening now, not what is about to.
		reportProgress(ctx, PhaseReasoning, round+1)
		resp, err := s.llm.Chat(ctx, *messages, chatOpts(tools, temp))
		if err != nil {
			trace.Terminal = TerminalProviderFailure
			return "", NewFailure(FailureProvider, *trace, fmt.Errorf("llm chat: %w", err))
		}
		// Some OpenAI-compatible models can repeat a tool call from conversation
		// history even when the finalization request exposes no tools. Never execute
		// that unsolicited call: returning an empty final turn routes it through the
		// existing bounded JSON-repair path while preserving the original surfaced
		// candidates and hard call limits.
		if *finalizationOnly && resp.WantsTools() {
			return "", nil
		}
		if resp.WantsTools() {
			reportProgress(ctx, PhaseSearching, round+1)
			// The provider-neutral contract is sequential single-tool. Some hosted
			// models still emit parallel calls despite the prompt; accepting all of
			// them would let one round escape maxToolRounds and flood the next context.
			toolCalls := resp.ToolCalls[:1]
			*messages = append(*messages, assistantToolCallMsg(toolCalls))
			for _, tc := range toolCalls {
				result, cands, rankedTrace := s.runTool(ctx, tc, intent, feedback)
				mergeDecisionTrace(trace, &rankedTrace)
				for _, c := range cands {
					if k, err := c.Key(); err == nil {
						surfaced[k] = c
					}
				}
				*messages = append(*messages, llm.Message{
					Role: llm.Tool, Content: result, ToolCallID: tc.ID,
				})
				// A non-empty grounded result moves the conversation into a distinct
				// finalization phase. Leaving catalog_search available here caused Gemma,
				// Qwen, and gpt-oss to repeat the same useful search until the hard tool
				// boundary. Empty/error results retain the tool so the model can try the
				// alternate discovery mode. The state survives JSON repairs above.
				if len(cands) > 0 {
					*finalizationOnly = true
					tools = nil
				} else if rankedTrace.Terminal == ReasonRetrievalEmpty {
					*emptyRetrievals++
					if *emptyRetrievals >= maxEmptyRetrievals && len(surfaced) == 0 {
						trace.Terminal = ReasonRetrievalEmpty
						return "", NewFailure(FailureCodeNoGroundedTitles, *trace, ErrNoGroundedTitles)
					}
				}
			}
			continue
		}
		// Retain the assistant's final turn before the caller decides whether it
		// needs a schema or grounding repair. Both repair prompts refer to the
		// previous reply, and a model cannot correct unsupported ids (or malformed
		// JSON) if that reply is absent from the conversation it receives next.
		*messages = append(*messages, llm.Message{Role: llm.Assistant, Content: resp.Content})
		return resp.Content, nil
	}
	// Ran out of tool rounds without a final turn: expose the bounded terminal fact.
	trace.Terminal = FailureBudgetExhausted
	return "", NewFailure(FailureBudgetExhausted, *trace, errors.New("suggestion tool-round budget exhausted"))
}

// ErrNoGroundedTitles is returned when a run produced no grounded picks at all —
// the intent surfaced no themed, real content. The worker fails the job with this
// (a clear operator-facing reason), and it is NOT cached, so a re-submit re-runs.
var ErrNoGroundedTitles = errors.New("suggester: no grounded titles found for this intent")
