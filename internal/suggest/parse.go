package suggest

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/provision"
)

// pick is one entry the model returns in its final JSON. It is UNTRUSTED — the
// ids are re-grounded against surfaced candidates before use (§8).
type pick struct {
	MediaType  string  `json:"mediaType"`
	TMDBID     int     `json:"tmdbId"`
	TVDBID     int     `json:"tvdbId"`
	Name       string  `json:"name"`
	Year       int     `json:"year"`
	Rationale  string  `json:"rationale"`
	Confidence float64 `json:"confidence"`
	// SeasonMin/SeasonMax: an OPTIONAL airing season window the model proposes for a
	// SERIES pick when the intent implies an era ("classic", "early", "first N
	// seasons"). Untrusted — validated + clamped downstream (an inverted/non-positive
	// range is dropped → all seasons). Airing scope only, NOT acquisition seasons.
	SeasonMin int `json:"seasonMin"`
	SeasonMax int `json:"seasonMax"`
}

// key derives the provisioning key a pick claims. Empty when the pick has no
// usable id (which makes it ungrounded → dropped).
func (p pick) key() string {
	mt := provision.MediaType(p.MediaType)
	if !mt.Valid() {
		return ""
	}
	t := provision.Title{MediaType: mt, TMDBID: p.TMDBID, TVDBID: p.TVDBID, Name: p.Name}
	k, err := t.Key()
	if err != nil {
		return ""
	}
	return string(k)
}

// pickPolicy is the UNTRUSTED ChannelPolicy the model proposes (programming-design
// §8: the LLM extracts, deterministic code enforces). Every field is loose/optional;
// groundPolicy validates + clamps it (off-ladder ceiling dropped, era bounded,
// series intersected with grounded ids) before it becomes a schedule.ChannelPolicy.
// The model never places a program — it only proposes rules, each machine-checked.
type pickPolicy struct {
	Audience struct {
		Ceiling string `json:"ceiling"` // e.g. "TV-Y7"; dropped if off the closed ladder
		Unrated string `json:"unrated"` // "exclude" | "allow"; ignored otherwise
	} `json:"audience"`
	Era struct {
		From int `json:"from"`
		To   int `json:"to"`
	} `json:"era"`
	Genres struct {
		Include []string `json:"include"`
		Exclude []string `json:"exclude"`
	} `json:"genres"`
	Ordering string `json:"ordering"` // "sequential" | "shuffle" | "syndication"
	Seasonal struct {
		Mode     string   `json:"mode"` // "off" | "auto" | "exclusive"
		Holidays []string `json:"holidays"`
	} `json:"seasonal"`
	// Rules are the model's proposed curation rules (§6.5/§6.6) from the closed preset
	// vocabulary — e.g. a weekend TNG marathon, December holiday programming. Each is a
	// {when, what, how} token triple; groundPolicy lowers + clamps them (unknown tokens
	// dropped, window bounded, daypart ceilings stricter-only). Optional.
	Rules []pickRule `json:"rules,omitempty"`
}

// pickRule is one UNTRUSTED curation rule the model proposes as preset TOKENS (§6.6): the
// model names WHEN/WHAT/HOW, deterministic code supplies the predicate/scope/ordering. The
// model never emits a raw hour range or predicate (that would be the model scheduling, §8).
type pickRule struct {
	When     string `json:"when"`               // e.g. "weekend", "primetime", "holiday:christmas"
	What     string `json:"what,omitempty"`     // e.g. "series:<key>", "kids", "all"; "" = inherit channel scope
	How      string `json:"how,omitempty"`      // e.g. "marathon", "syndication"; "" = inherit channel ordering
	Priority int    `json:"priority,omitempty"` // optional override; 0 = use the WHEN token's default
}

// finalOutput is the model's final JSON shape (§8 output contract): picks +
// rationale + an optional policy. The suggester classifies picks into lineup/
// acquisitions by their in_library flag, so the model needn't.
type finalOutput struct {
	ChannelName string      `json:"channelName"`
	Rationale   string      `json:"rationale"`
	Picks       []pick      `json:"picks"`
	Policy      *pickPolicy `json:"policy,omitempty"`
}

// parsePicks parses the model's final JSON. A malformed final output is an error
// (the job fails cleanly rather than emitting a garbage proposal).
//
// Some capable models (Claude especially) wrap their final JSON in a markdown code
// fence (```json ... ```) or add a sentence before/after it, even when the prompt
// says "ONLY JSON". extractJSONObject pulls the JSON object out of that wrapping so
// a well-formed proposal isn't rejected over presentation. Grounding is unaffected:
// this only decides whether we can READ the picks, never which picks survive — the
// surfaced-map chokepoint downstream is the actual grounding gate.
func parsePicks(content string) (finalOutput, error) {
	var out finalOutput
	if err := json.Unmarshal([]byte(extractJSONObject(content)), &out); err != nil {
		return finalOutput{}, fmt.Errorf("suggester: model final output is not valid JSON: %w", err)
	}
	return out, nil
}

// extractJSONObject unwraps a code fence / strips prose around the model's JSON. It now delegates
// to llm.ExtractJSONObject — the same logic the V44 filler tagger and vision tier need, promoted to
// the `llm` package so one implementation serves every caller (this used to be a package-private
// copy here). Kept as a thin local alias so the call sites in this file read unchanged.
func extractJSONObject(s string) string {
	return llm.ExtractJSONObject(s)
}

// toolResult is the JSON the catalog tool returns to the model — a trimmed
// candidate view with real ids and the in_library flag. Untrusted text from the
// library/TMDB (names) rides here but can never steer tools or reach secrets
// (§8): it is data the model selects from, not instructions.
type toolCandidate struct {
	MediaType string `json:"mediaType"`
	TMDBID    int    `json:"tmdbId,omitempty"`
	TVDBID    int    `json:"tvdbId,omitempty"`
	Name      string `json:"name"`
	Year      int    `json:"year,omitempty"`
	InLibrary bool   `json:"inLibrary"`
	// Genres + Overview let the model judge theme-fit from real metadata instead of
	// the title string (§8). Overview is truncated so a long synopsis doesn't blow
	// the context of a small local model across many candidates.
	Genres           []string `json:"genres,omitempty"`
	Overview         string   `json:"overview,omitempty"`
	OriginalLanguage string   `json:"originalLanguage,omitempty"`
	OriginCountries  []string `json:"originCountries,omitempty"`
	RuntimeMinutes   int      `json:"runtimeMinutes,omitempty"`
	VoteAverage      float64  `json:"voteAverage,omitempty"`
	VoteCount        int      `json:"voteCount,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	Networks         []string `json:"networks,omitempty"`
	Cast             []string `json:"cast,omitempty"`
	Creators         []string `json:"creators,omitempty"`
}

// overviewMax caps the per-candidate overview length sent to the model — enough to
// convey theme, short enough that 24 candidates fit a small model's context.
const overviewMax = 240

func toolResult(cands []catalog.Candidate) []toolCandidate {
	out := make([]toolCandidate, 0, len(cands))
	for _, c := range cands {
		out = append(out, toolCandidate{
			MediaType: string(c.MediaType), TMDBID: c.TMDBID, TVDBID: c.TVDBID,
			Name: c.Name, Year: c.Year, InLibrary: c.InLibrary,
			Genres: c.Genres, Overview: truncate(c.Overview, overviewMax),
			OriginalLanguage: c.OriginalLanguage,
			OriginCountries:  append([]string(nil), c.OriginCountries...),
			RuntimeMinutes:   c.RuntimeMinutes,
			VoteAverage:      c.VoteAverage,
			VoteCount:        c.VoteCount,
			Keywords:         append([]string(nil), c.Keywords...),
			Networks:         append([]string(nil), c.Networks...),
			Cast:             append([]string(nil), c.Cast...),
			Creators:         append([]string(nil), c.Creators...),
		})
	}
	return out
}

// truncate shortens s to at most n runes, appending an ellipsis when cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// parseEra turns an intent's era phrase into an inclusive year range (0 = open).
// Accepts a decade ("1990s" → 1990-1999), a range ("1985-1995"), or a single
// year ("1994" → that year). Returns (0,0) when it can't parse — discovery then
// applies no date filter.
func parseEra(era string) (from, to int) {
	e := strings.TrimSpace(strings.ToLower(era))
	if e == "" {
		return 0, 0
	}
	// Decade: "1990s" / "90s".
	if strings.HasSuffix(e, "s") {
		if y := fourDigitYear(strings.TrimSuffix(e, "s")); y > 0 {
			return y, y + 9
		}
	}
	// Range: "1985-1995" (also "1985 to 1995").
	sep := strings.NewReplacer(" to ", "-", "–", "-", "—", "-").Replace(e)
	if parts := strings.SplitN(sep, "-", 2); len(parts) == 2 {
		a, b := fourDigitYear(parts[0]), fourDigitYear(parts[1])
		if a > 0 && b > 0 {
			if a > b {
				a, b = b, a
			}
			return a, b
		}
	}
	// Single year.
	if y := fourDigitYear(e); y > 0 {
		return y, y
	}
	return 0, 0
}

// fourDigitYear extracts a plausible 4-digit year (1900–2099) from s, tolerating
// a 2-digit decade ("90" → 1990). Returns 0 if none.
func fourDigitYear(s string) int {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		switch {
		case n >= 1900 && n <= 2099:
			return n
		case n >= 0 && n <= 99: // 2-digit decade → 19xx (filler/broadcast era)
			return 1900 + n
		}
	}
	return 0
}
