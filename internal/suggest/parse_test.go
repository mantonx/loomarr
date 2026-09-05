package suggest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/provision"
)

// The external-LLM smoke found Claude wraps its final JSON in a ```json fence even
// when told "ONLY JSON". parsePicks must read the object out of that wrapping — but
// must NOT be fooled by a brace inside a string, and must still reject true garbage.
func TestParsePicks_UnwrapsAndValidates(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantErr   bool
		wantPicks int
		wantRat   string
	}{
		{
			name:      "bare json (gpt-4o-mini)",
			content:   `{"rationale":"r","picks":[{"mediaType":"movie","tmdbId":603,"name":"The Matrix"}]}`,
			wantPicks: 1, wantRat: "r",
		},
		{
			name:      "markdown-fenced json (claude)",
			content:   "```json\n{\"rationale\":\"r\",\"picks\":[{\"mediaType\":\"movie\",\"tmdbId\":603,\"name\":\"The Matrix\"}]}\n```",
			wantPicks: 1, wantRat: "r",
		},
		{
			name:      "prose before and after",
			content:   "Here is the channel:\n{\"rationale\":\"r\",\"picks\":[]}\nHope that helps!",
			wantPicks: 0, wantRat: "r",
		},
		{
			name:      "brace inside a string value is not the object end",
			content:   `{"rationale":"a {weird} title","picks":[{"mediaType":"movie","tmdbId":1,"name":"Brace } Face"}]}`,
			wantPicks: 1, wantRat: "a {weird} title",
		},
		{
			name:    "no json object at all is still an error",
			content: "I could not find anything.",
			wantErr: true,
		},
		{
			name:    "truncated/unbalanced json still errors",
			content: `{"rationale":"r","picks":[`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := parsePicks(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got picks=%v rat=%q", out.Picks, out.Rationale)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(out.Picks) != tt.wantPicks {
				t.Errorf("picks = %d, want %d", len(out.Picks), tt.wantPicks)
			}
			if out.Rationale != tt.wantRat {
				t.Errorf("rationale = %q, want %q", out.Rationale, tt.wantRat)
			}
		})
	}
}

func TestToolResultCarriesGroundedDiscoveryEvidence(t *testing.T) {
	candidates := []catalog.Candidate{{
		MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix", OriginalLanguage: "en",
		OriginCountries: []string{"US"}, RuntimeMinutes: 136, VoteAverage: 8.2, VoteCount: 26000,
		Keywords: []string{"artificial reality"}, Networks: []string{"HBO"},
		Cast: []string{"Keanu Reeves"}, Creators: []string{"Lana Wachowski", "Lilly Wachowski"},
	}}

	got := toolResult(candidates)
	if len(got) != 1 || got[0].OriginalLanguage != "en" || got[0].RuntimeMinutes != 136 ||
		got[0].VoteAverage != 8.2 || got[0].VoteCount != 26000 ||
		!reflect.DeepEqual(got[0].OriginCountries, []string{"US"}) ||
		!reflect.DeepEqual(got[0].Keywords, []string{"artificial reality"}) ||
		!reflect.DeepEqual(got[0].Networks, []string{"HBO"}) ||
		!reflect.DeepEqual(got[0].Cast, []string{"Keanu Reeves"}) ||
		!reflect.DeepEqual(got[0].Creators, []string{"Lana Wachowski", "Lilly Wachowski"}) {
		t.Fatalf("tool discovery evidence = %+v", got)
	}
}

// userPrompt switches to a REFINE framing when the intent carries refine inputs: it
// leads with "already exists" + the current lineup + the change, and re-asserts the
// grounding requirement — rather than the fresh "Build a channel" framing.
func TestUserPrompt_RefineFraming(t *testing.T) {
	fresh := userPrompt(Intent{Description: "90s action"})
	if !strings.Contains(fresh, "Build a channel:") {
		t.Errorf("fresh prompt missing the build framing:\n%s", fresh)
	}

	refine := userPrompt(Intent{
		Description:   "90s action",
		RefineText:    "add more Schwarzenegger",
		CurrentLineup: []LineupContext{{Name: "The Matrix", Year: 1999}, {Name: "Point Break", Year: 1991}},
	})
	for _, want := range []string{
		"already exists",
		"The Matrix (1999)",
		"Point Break (1991)",
		"add more Schwarzenegger",
		"catalog tool", // grounding re-asserted for refine picks
	} {
		if !strings.Contains(refine, want) {
			t.Errorf("refine prompt missing %q:\n%s", want, refine)
		}
	}
	if strings.Contains(refine, "Build a channel:") {
		t.Error("refine prompt should NOT use the fresh build framing")
	}
}
