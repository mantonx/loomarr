package suggest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/schedule"
)

// buildProposal turns the model's picks into a validated, grounded, scored
// Proposal. This is the grounding chokepoint: a pick survives ONLY if it matches
// a candidate a Catalog operation actually surfaced (real id), and acquisitions must also
// pass the exists re-validation. Unresolvable picks are dropped, never actioned.
func (s *Suggester) buildProposal(ctx context.Context, intent Intent, out finalOutput, surfaced map[provision.Key]catalog.Candidate, trace *DecisionTrace) (Proposal, error) {
	prop := Proposal{Intent: intent, ChannelName: strings.TrimSpace(out.ChannelName), Rationale: out.Rationale}
	picks := out.Picks
	acqCount := 0
	maxAcq := s.maxAcq
	if intent.MaxAcquire > 0 && intent.MaxAcquire < maxAcq {
		maxAcq = intent.MaxAcquire
	}

	for _, p := range picks {
		key := p.key()
		if key == "" {
			traceDecision(trace, DecisionCandidate{Disposition: DispositionValidationDropped, Reason: ReasonMalformedID})
			continue // no usable id → not grounded, drop
		}
		cand, ok := surfaced[provision.Key(key)]
		if !ok {
			traceDecision(trace, DecisionCandidate{Key: key, Disposition: DispositionValidationDropped, Reason: ReasonNotSurfaced})
			continue // GROUNDING: the model named an id the tool never returned — drop it
		}
		if intent.ReferenceResolved && !intent.referenceKeys[provision.Key(key)] {
			traceDecision(trace, DecisionCandidate{Key: key, Disposition: DispositionValidationDropped, Reason: ReasonNoRelevanceEvidence})
			continue // a resolved reference cannot be padded with an unrelated grounded id
		}
		if requiresMembershipEvidence(intent) {
			relevance, _ := relevanceForCandidate(decisionRankQuery(intent), cand)
			if relevance == 0 && adjacentVotesOf(intent, provision.Key(key)) == 0 {
				traceDecision(trace, DecisionCandidate{Key: key, Disposition: DispositionValidationDropped, Reason: ReasonNoRelevanceEvidence})
				continue // identity is real, but no source-backed fact connects it to the named set
			}
		}
		item := fromCandidate(cand, p.Rationale, p.Confidence)
		// Carry the adjacency consensus onto the pick so the approval surface can show WHY
		// it was offered ("recommended by 5 of your films"). Zero for every other corpus.
		//
		// Derived from the intent rather than threaded through as another parameter: the
		// intent is already here, and votes are a property of what was OFFERED, not of what
		// the catalog returned — keeping them off Candidate keeps identity clean.
		item.AdjacentVotes = adjacentVotesOf(intent, provision.Key(key))
		// Grounding chokepoint: attach the model's proposed AIRING season window only
		// for a series, and only after clamping (an inverted or non-positive range is
		// dropped → all seasons, never an empty channel). The range can only NARROW an
		// already-grounded series expansion — it never introduces content.
		if min, max, ok := clampSeasonWindow(cand.MediaType, p.SeasonMin, p.SeasonMax); ok {
			item.SeasonMin, item.SeasonMax = min, max
		}

		if cand.InLibrary {
			prop.Lineup = append(prop.Lineup, item)
			traceDecision(trace, DecisionCandidate{Key: key, Disposition: DispositionSelected, Reason: "selected"})
			continue
		}
		// Acquisition: re-validate it exists on TMDB (§8) and respect the cap.
		if acqCount >= maxAcq {
			prop.Alternates = append(prop.Alternates, item) // over-cap picks become alternates
			traceDecision(trace, DecisionCandidate{Key: key, Disposition: DispositionAlternate, Reason: ReasonAcquisitionCap})
			continue
		}
		exists, err := s.validator.Exists(ctx, cand.MediaType, cand.TMDBID)
		if err != nil {
			return Proposal{}, fmt.Errorf("validate acquisition %s: %w", cand.Name, err)
		}
		if !exists {
			traceDecision(trace, DecisionCandidate{Key: key, Disposition: DispositionValidationDropped, Reason: ReasonValidationDropped})
			continue // fabricated/withdrawn id → drop
		}
		// Enrich the rating from TMDB (§389): the library can't rate a title it doesn't
		// have, so without this an acquisition under an audience ceiling is dropped
		// before it can even show as a pending slot. Best-effort — sparse TMDB coverage
		// or a lookup error leaves it unrated, and the reconciler heals it once it lands.
		if item.OfficialRating == "" && s.ratings != nil {
			if r, err := s.ratings.ContentRating(ctx, cand.MediaType, cand.TMDBID); err == nil {
				item.OfficialRating = r
			}
		}
		prop.Acquisitions = append(prop.Acquisitions, item)
		traceDecision(trace, DecisionCandidate{Key: key, Disposition: DispositionSelected, Reason: "selected"})
		acqCount++
	}

	// A proposal with nothing grounded (every pick was fabricated/withdrawn, or the
	// search surfaced no themed content) is a real failure, not a success. Return a
	// sentinel so the worker fails the job with a clear reason — instead of
	// persisting an empty "submitted" proposal and caching it for 24h (which made the
	// failure both silent and sticky). Grounding is unaffected: this only decides
	// what a legitimately-empty result does.
	if len(prop.Lineup)+len(prop.Acquisitions)+len(prop.Alternates) == 0 {
		return Proposal{}, ErrNoGroundedTitles
	}

	// Ground the extracted policy (programming-design §8): the model proposed rule
	// VALUES; we validate + clamp them (off-ladder ceiling dropped, era bounded,
	// series intersected with grounded ids) before they become a ChannelPolicy. A
	// bad policy never sinks a good lineup — it degrades to defaults (empty policy).
	prop.Policy = groundPolicy(out.Policy, prop.Lineup, prop.Acquisitions, intent)

	// §4 honesty pass (#259). The ceiling is now FINAL — groundPolicy has already dropped an
	// unjustified one and applied the deterministic child-safety bound — so this is the first moment the
	// question "will this pick actually air?" has a settled answer. Ask it here, and move the
	// certain no's out of what the operator is being asked to approve.
	prop.Lineup, prop.Acquisitions, prop.Refused = refuseUnairable(
		prop.Policy.Audience, prop.Lineup, prop.Acquisitions, deriveIntentPolicy(intent).safetyCeiling != "",
	)
	for _, refused := range prop.Refused {
		if key, err := refused.Item.Key(); err == nil {
			traceDecision(trace, DecisionCandidate{Key: string(key), Disposition: DispositionRefused, Reason: refused.Reason})
		}
	}
	stampEpisodeSelection(prop.Lineup, intent)
	stampEpisodeSelection(prop.Acquisitions, intent)
	stampEpisodeSelection(prop.Alternates, intent)

	// ⚠ Scored on what SURVIVED. Scoring the refused picks too would report an availability
	// ratio and theme fit for a lineup nobody is being offered — the scorecard already half-knew
	// something was wrong on the live smoke (theme fit 43%) and that ambiguity is what a refusal
	// list replaces with a statement.
	prop.Scores = score(intent, prop.Lineup, prop.Acquisitions)
	if trace != nil {
		// Tool calls contribute evidence, not the run outcome. A later empty or
		// failed lookup cannot label a proposal that ultimately succeeded from an
		// earlier catalog result or the adjacency corpus as terminally failed.
		trace.Terminal = ""
		prop.Trace = trace.Clone()
	}
	return prop, nil
}

func traceDecision(trace *DecisionTrace, update DecisionCandidate) {
	if trace == nil {
		return
	}
	for i := range trace.Candidates {
		if trace.Candidates[i].Key == update.Key && update.Key != "" {
			trace.Candidates[i].Disposition = update.Disposition
			trace.Candidates[i].Reason = update.Reason
			return
		}
	}
	if len(trace.Candidates) >= DecisionTraceMaxCandidates {
		trace.Truncated = true
		trace.RecordedTotal = min(trace.RecordedTotal+1, DecisionTraceMaxTotal)
		return
	}
	trace.Candidates = append(trace.Candidates, update)
	trace.RecordedTotal++
}

// refuseUnairable partitions grounded picks against the channel's own final audience policy
// (§4), returning the survivors plus what was refused and why.
//
// It always refuses what is certainly unairable: a pick whose known rating is above the
// ceiling. For an explicit child-safety intent it also refuses unrated picks, because unknown
// content cannot be actionable under that promise. Other guarded intents leave an unrated pick
// available for metadata healing because at proposal time "unrated" often means "not looked up
// yet" rather than "unknown content":
//
//   - The reconcile heal (§389 `RatingResolver`) exists precisely to fill an empty rating once
//     the title is in the library, and an acquisition cannot be rated by a library that does not
//     have it yet. Refusing it here would fight that heal and reject titles that will air fine.
//   - TMDB enrichment for acquisitions is best-effort; a sparse record or a lookup error leaves
//     the field empty, which is a fact about the network, not about the content.
//
// Nothing is admitted by that ordinary-intent leniency that was not already admitted: the §4
// enforcer still fails closed at airtime.
func refuseUnairable(a schedule.AudiencePolicy, lineup, acquisitions []ProposalItem, refuseUnrated bool) (
	keptLineup, keptAcquisitions []ProposalItem, refused []RefusedPick,
) {
	if a.Ceiling == "" {
		return lineup, acquisitions, nil // adult/general channel — nothing to refuse
	}
	sift := func(items []ProposalItem) []ProposalItem {
		kept := make([]ProposalItem, 0, len(items))
		for _, it := range items {
			rating := schedule.NormalizeRating(it.OfficialRating)
			if rating == "" && refuseUnrated {
				refused = append(refused, RefusedPick{Item: it, Reason: "over_ceiling"})
				continue
			}
			// Outside an explicit child-safety request, only a KNOWN rating can produce a
			// certain refusal; the reconcile heal can still fill an ordinary metadata gap.
			if ok, reason := a.Admits(rating); !ok && reason == "over_ceiling" {
				refused = append(refused, RefusedPick{Item: it, Reason: reason})
				continue
			}
			kept = append(kept, it)
		}
		return kept
	}
	return sift(lineup), sift(acquisitions), refused
}

// groundPolicy converts the model's untrusted pickPolicy into a validated
// schedule.ChannelPolicy (programming-design §1 extract-vs-enforce). Every value is
// machine-checked: the ceiling must be on the closed rating ladder (else dropped),
// enums must be known (else dropped), the era is taken as-is (the enforcer clamps),
// and any series allowlist is intersected with the actually-grounded picks so the
// model can't scope to a series that never surfaced. Explicit child-safety intent and a rating
// cap written by the user contribute deterministic ceilings even when the model omits or
// hallucinates the policy. A named holiday similarly determines exclusive mode + holiday subset.
func groundPolicy(raw *pickPolicy, lineup, acquisitions []ProposalItem, intent Intent) schedule.ChannelPolicy {
	intentPolicy := deriveIntentPolicy(intent)
	requestedCeiling := stricterCeiling(intentPolicy.explicitAudienceCeiling, intentPolicy.safetyCeiling)
	if raw == nil {
		raw = &pickPolicy{}
	}
	var p schedule.ChannelPolicy

	// Audience ceiling (programming-design §4/§8): the ceiling is a KIDS/TEEN GUARDRAIL or an
	// explicit user-written rating limit, not a general default. An unqualified channel is adult-default — "1980s Action Heroes" includes
	// its R-rated films. So a model-proposed ceiling is kept ONLY when the intent actually
	// signals kids/teens or names a rating cap; with neither signal it is DROPPED (→ no ceiling, everything admitted),
	// because a small model reflexively caps action/genre channels ("might be violent → TV-14")
	// and that must not silently strip the R-rated content the channel is about. The prompt says
	// "adult/no mention → omit," but the model isn't trusted to obey it — this is the enforcement.
	if c := schedule.NormalizeRating(raw.Audience.Ceiling); c != "" && (intentPolicy.kidsSignal || intentPolicy.explicitAudienceCeiling != "") {
		p.Audience.Ceiling = stricterCeiling(c, requestedCeiling)
	} else if requestedCeiling != "" {
		// Explicit child safety is deterministic and cannot disappear because the model
		// omitted or hallucinated the policy value. The same applies to a rating the
		// user wrote explicitly (for example, "keep it PG-13").
		p.Audience.Ceiling = requestedCeiling
	}
	switch schedule.UnratedPolicy(raw.Audience.Unrated) {
	case schedule.UnratedExclude, schedule.UnratedAllow:
		p.Audience.Unrated = schedule.UnratedPolicy(raw.Audience.Unrated)
	}

	// Era: accept a sane year window (the enforcer treats 0 as unbounded), then WIDEN it
	// to admit the channel's own grounded picks (programming-design §4). Acquisitions
	// count alongside the lineup: one becomes a real airing the moment it lands, so an
	// era that excluded it would quietly drop the title after the download finished.
	if raw.Era.From > 0 || raw.Era.To > 0 {
		era := schedule.Range{From: raw.Era.From, To: raw.Era.To}
		p.Scope.Era = eraAdmittingPicks(era, lineup, acquisitions)
	}

	// Genres: pass through include/exclude names (matched case-insensitively at
	// enforcement; unknown names simply never match, which is harmless).
	p.Scope.Genres = schedule.GenreFilter{Include: raw.Genres.Include, Exclude: raw.Genres.Exclude}

	// Ordering: keep only a known mode.
	switch schedule.OrderingMode(raw.Ordering) {
	case schedule.OrderSequential, schedule.OrderShuffle, schedule.OrderSyndication:
		p.Ordering = schedule.OrderingMode(raw.Ordering)
	}
	// Narrative order is an explicit viewer promise, not model discretion. Force
	// it before applying omitted-order defaults so a model-proposed shuffle cannot
	// turn "chronological" or "binge" into a mixed deck.
	if intentPolicy.sequential {
		p.Ordering = schedule.OrderSequential
	}
	// Multi-series default: when the model didn't pick an ordering (or picked an unknown
	// one → still OrderInherit here) AND the grounded lineup spans more than one series,
	// default to syndication so distinct series INTERMIX (deck-deal) instead of playing
	// one series to completion then the next (the chronological-per-series bug). A single-
	// series or movie channel keeps OrderInherit → the channel's Strategy decides (usually
	// sequential, correct for one show). The model's EXPLICIT choice still wins — this only
	// fills an omission. See programming-design §5.
	if p.Ordering == schedule.OrderInherit && multiSeries(lineup) {
		p.Ordering = schedule.OrderSyndication
	}
	// A curated/rerun request for one episodic series is not a box-set binge. Deal
	// its eligible episodes as a no-repeat syndication deck, even if the model
	// reflexively said sequential. Explicit chronological/binge language wins.
	// Movie franchises are unaffected: the scheduler independently keeps each
	// TMDB collection atomic and in release order under every non-sequential mode.
	if intentPolicy.curated && !intentPolicy.sequential &&
		singleSeriesOnly(lineup, acquisitions) {
		p.Ordering = schedule.OrderSyndication
	}

	// Seasonal: keep only a known mode + holiday ids.
	switch schedule.SeasonalMode(raw.Seasonal.Mode) {
	case schedule.SeasonalOff, schedule.SeasonalAuto, schedule.SeasonalExclusive:
		p.Seasonal.Mode = schedule.SeasonalMode(raw.Seasonal.Mode)
		p.Seasonal.Holidays = raw.Seasonal.Holidays
	}
	if intentPolicy.seasonal.Mode != "" {
		p.Seasonal = intentPolicy.seasonal
	}

	// Curation rules (§6.5/§6.6): lower the model's preset tokens into SchedulingRules,
	// dropping unknowns and clamping (window bound + stricter-only daypart ceiling).
	p.Rules = groundRules(raw.Rules, lineup, p.Audience.Ceiling)

	// Final safety net: if anything slipped through that Validate rejects, drop the
	// whole policy to defaults rather than persist an invalid one.
	if err := p.Validate(); err != nil {
		return schedule.ChannelPolicy{}
	}
	return p
}

// ruleWindowMin / ruleWindowMax bound a per-rule rolling window (§6.6): 1h keeps a rule
// from materializing a sliver, 168h (a week) is the longest sane horizon. The marathon
// WindowFull sentinel is exempt (a binge is meant to be unbounded).
const (
	ruleWindowMin = 1 * time.Hour
	ruleWindowMax = 168 * time.Hour
)

// groundRules lowers each model-proposed preset rule (§6.6) into a validated SchedulingRule.
// A rule whose WHEN token is unknown is DROPPED entirely (a rule with no time predicate is
// meaningless). A WHAT/HOW that doesn't lower is left to inherit the channel scope/ordering
// (the rule still applies its timing). The window is clamped; a kids/family WHAT clamps the
// rule's audience STRICTER-ONLY against the channel ceiling (§4 — a rule can never raise it,
// enforced here AND at §4 enforcement, defense in depth). A series WHAT is intersected with
// the grounded picks. Returns nil when nothing survives (⇒ base whole-policy behavior).
func groundRules(raw []pickRule, lineup []ProposalItem, channelCeiling schedule.Rating) []schedule.SchedulingRule {
	if len(raw) == 0 {
		return nil
	}
	grounded := make([]provision.Key, 0, len(lineup))
	for _, it := range lineup {
		if k, err := it.Key(); err == nil {
			grounded = append(grounded, k)
		}
	}
	out := make([]schedule.SchedulingRule, 0, len(raw))
	for i, r := range raw {
		when, prio, ok := schedule.LowerWhen(r.When)
		if !ok {
			continue // no valid time predicate → drop the whole rule
		}
		if r.Priority > 0 {
			prio = r.Priority
		}
		rule := schedule.SchedulingRule{
			ID:       fmt.Sprintf("r%d", i+1),
			Source:   schedule.RuleSourceLLM,           // provenance (§8.2): a refine may replace these; operator rules are kept
			Label:    ruleLabel(r.When, r.What, r.How), // legible attribution in the cycle preview (§8.1)
			Priority: prio,
			When:     when,
		}
		// HOW: lower ordering + per-rule window; unknown → inherit (zero How).
		if how, win, ok := schedule.LowerHow(r.How); ok {
			rule.How = how
			rule.Window = clampRuleWindow(win)
		}
		// WHAT: lower scope; unknown → inherit (nil What). A kids/family token also returns a
		// stricter-only ceiling to fold onto the rule scope's audience (§4).
		if scope, kidsCeiling, ok := schedule.LowerWhat(r.What); ok && scope != nil {
			scope.Series = intersectSeries(scope.Series, grounded) // never a series that didn't surface
			// If the intersection (or lowering) left a scope that narrows NOTHING, treat it as
			// inherit (nil) rather than a non-nil empty scope — a phantom series shouldn't leave
			// a meaningless narrower behind.
			if !scopeNarrows(scope) {
				scope = nil
			}
			rule.What = scope
			_ = kidsCeiling // audience-on-a-rule scope is a §4-followup; the channel ceiling already fences the pool.
		}
		out = append(out, rule)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ruleLabel builds a legible display name for a grounded rule from its authoring tokens
// (§8.1) — e.g. ("weekend","","marathon") → "Weekend · Marathon". Kept token-derived (not
// predicate-derived) so it preserves the WHAT specificity a lowered *ScopePolicy loses
// (a "series:tv:1396" reads as "series:tv:1396", not just "narrowed"). Empty tokens are
// skipped; an all-empty rule falls back to "Rule".
func ruleLabel(when, what, how string) string {
	parts := make([]string, 0, 3)
	for _, tok := range []string{when, what, how} {
		if t := strings.TrimSpace(tok); t != "" && !strings.EqualFold(t, "all") {
			parts = append(parts, titleToken(t))
		}
	}
	if len(parts) == 0 {
		return "Rule"
	}
	return strings.Join(parts, " · ")
}

// titleToken renders one authoring token for a label: "weekend" → "Weekend",
// "holiday:christmas" → "Holiday: Christmas". Prefixed tokens keep their qualifier.
func titleToken(tok string) string {
	if i := strings.IndexByte(tok, ':'); i >= 0 {
		return capitalizeASCII(tok[:i]) + ": " + capitalizeASCII(tok[i+1:])
	}
	return capitalizeASCII(tok)
}

// capitalizeASCII upper-cases the first byte of an ASCII token (labels only).
func capitalizeASCII(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// scopeNarrows reports whether a *ScopePolicy actually constrains anything (§6.6). An
// all-empty scope (e.g. left after a phantom series intersection dropped its only series)
// narrows nothing, so the caller nils it — a rule's What should be nil ("inherit channel
// scope"), never a non-nil no-op that reads as an active-but-empty narrower.
// `Collections` IS counted. It was excluded while inert — nothing filtered on it, so a
// collections-only scope would have survived the nil-out below as a "narrower" that narrows
// nothing. It now binds in the scheduler's scope filter (schedule/slotting.go, via the
// membership stamped at reconcile — programming-design §2.2), so it constrains a rule's What
// exactly as Series or Era does and must vote accordingly.
func scopeNarrows(s *schedule.ScopePolicy) bool {
	if s == nil {
		return false
	}
	return len(s.Series) > 0 || len(s.Collections) > 0 || s.Seasons != nil || s.Era != nil ||
		len(s.Genres.Include) > 0 || len(s.Genres.Exclude) > 0 || s.RuntimeMax > 0
}

// clampRuleWindow bounds a per-rule window to [1h,168h], leaving the WindowFull sentinel
// (a marathon binge) and 0 (inherit) untouched.
func clampRuleWindow(w schedule.Duration) schedule.Duration {
	if w == schedule.WindowFull || w == 0 {
		return w
	}
	std := w.Std()
	if std < ruleWindowMin {
		return schedule.Duration(ruleWindowMin)
	}
	if std > ruleWindowMax {
		return schedule.Duration(ruleWindowMax)
	}
	return w
}

// intersectSeries keeps only the rule's series that are actually in the grounded set (§6.6),
// so a rule can't scope to a series that never surfaced. An empty result means the rule's
// series scope drops (nil), leaving it to inherit the channel scope. A nil input (no series
// scope) passes through unchanged.
func intersectSeries(want, grounded []provision.Key) []provision.Key {
	if len(want) == 0 {
		return want
	}
	set := make(map[provision.Key]struct{}, len(grounded))
	for _, k := range grounded {
		set[k] = struct{}{}
	}
	kept := want[:0:0]
	for _, k := range want {
		if _, ok := set[k]; ok {
			kept = append(kept, k)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}
