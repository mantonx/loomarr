package suggest

import (
	"context"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/testkit/catalogfixture"
)

func TestParseDiscoveryQueryValidatesAndNormalizesScalarQualifiers(t *testing.T) {
	got, discovery, err := parseDiscoveryQuery(map[string]any{
		"genres":            []any{"Drama"},
		"original_language": " EN ",
		"origin_country":    "gb",
		"runtime_min":       float64(20),
		"runtime_max":       90,
		"vote_average_min":  7.5,
		"vote_count_min":    int64(100),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !discovery || got.OriginalLanguage != "en" || got.OriginCountry != "GB" ||
		got.RuntimeMin != 20 || got.RuntimeMax != 90 || got.VoteAverageMin != 7.5 || got.VoteCountMin != 100 {
		t.Fatalf("normalized discovery query = %+v discovery=%v", got, discovery)
	}
}

func TestParseDiscoveryQueryValidatesAndNormalizesGroundedEntityQualifiers(t *testing.T) {
	movie, discovery, err := parseDiscoveryQuery(map[string]any{
		"media_type": "movie",
		"cast":       []any{" Tom Hanks ", "Meg Ryan"},
		"creators":   []any{" Nora Ephron "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !discovery || movie.MediaType != "movie" || len(movie.Cast) != 2 || movie.Cast[0] != "Tom Hanks" ||
		len(movie.Creators) != 1 || movie.Creators[0] != "Nora Ephron" {
		t.Fatalf("movie entity query = %+v discovery=%v", movie, discovery)
	}

	series, discovery, err := parseDiscoveryQuery(map[string]any{
		"media_type": "series", "network": " HBO ", "origin_country": "us",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !discovery || series.MediaType != "series" || series.Network != "HBO" || series.OriginCountry != "US" {
		t.Fatalf("series entity query = %+v discovery=%v", series, discovery)
	}
}

func TestParseDiscoveryQueryIgnoresProviderEmptyOptionalPlaceholders(t *testing.T) {
	got, discovery, err := parseDiscoveryQuery(map[string]any{
		"cast":              []any{""},
		"creators":          []any{""},
		"era":               "1990s",
		"genres":            []any{},
		"keywords":          []any{},
		"media_type":        "series",
		"network":           "ABC",
		"origin_country":    "",
		"original_language": "",
		"query":             "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !discovery || got.Network != "ABC" || got.MediaType != "series" || got.YearFrom != 1990 || got.YearTo != 1999 {
		t.Fatalf("normalized provider placeholders = %+v discovery=%v", got, discovery)
	}
	if len(got.Cast) != 0 || len(got.Creators) != 0 || got.OriginalLanguage != "" || got.OriginCountry != "" {
		t.Fatalf("empty optional placeholders survived normalization: %+v", got)
	}
}

func TestProjectCatalogArgumentsKeepsAuthoritativeNetworkQualifiers(t *testing.T) {
	got, ok := projectCatalogArguments(map[string]any{
		"cast": []any{"Tiffani Thiessen"}, "creators": []any{"Jeff Franklin"},
		"era": "1990-1999", "genres": []any{"Comedy", "Family"}, "keywords": []any{"TGIF"},
		"media_type": "series", "network": "ABC", "origin_country": "US",
		"original_language": "en", "query": "TGIF", "runtime_min": float64(20),
		"runtime_max": float64(60), "vote_average_min": 6.5, "vote_count_min": float64(100),
	})
	if !ok {
		t.Fatal("valid series/network route was not projected")
	}
	query, discovery, err := parseDiscoveryQuery(got)
	if err != nil || !discovery {
		t.Fatalf("projected network route = (%+v, %v, %v), want valid discovery", query, discovery, err)
	}
	if query.Network != "ABC" || query.OriginalLanguage != "en" || query.OriginCountry != "US" ||
		query.RuntimeMin != 20 || query.RuntimeMax != 60 || query.VoteAverageMin != 6.5 || query.VoteCountMin != 100 {
		t.Fatalf("projected network route lost authoritative qualifiers: %+v", query)
	}
}

func TestProjectCatalogArgumentsRejectsMalformedDiscardedFieldsAndOtherRoutes(t *testing.T) {
	tests := []map[string]any{
		{"media_type": "series", "network": "ABC", "genres": []any{"Drama"}, "cast": []any{17}},
		{"media_type": "series", "network": "ABC", "genres": []any{"Drama"}, "query": 17},
		{"media_type": "series", "network": 17, "genres": []any{"Drama"}},
		{"media_type": "movie", "query": "The Matrix", "cast": []any{"Keanu Reeves"}},
		{"media_type": "movie", "cast": []any{"Tom Hanks"}, "genres": []any{"Comedy"}},
	}
	for _, args := range tests {
		if got, ok := projectCatalogArguments(args); ok {
			t.Fatalf("unsafe or unrelated arguments %#v projected to %#v", args, got)
		}
	}
}

func TestRunToolKeepsAllQualifiersOnAlreadyValidStrictCall(t *testing.T) {
	corpus := &catalogfixture.Corpus{}
	s := New(nil, catalog.New(nil, corpus), nil, 10)
	_, _, _ = s.runTool(context.Background(), llm.ToolCall{
		Name: catalogToolName,
		Arguments: map[string]any{
			"media_type": "series", "network": "ABC", "genres": []any{"Comedy"},
			"keywords": []any{"TGIF"}, "era": "1990s", "origin_country": "US",
			"original_language": "en", "runtime_min": float64(20), "runtime_max": float64(60),
			"vote_average_min": 6.5, "vote_count_min": float64(100),
		},
	}, Intent{}, nil)
	discoveries := corpus.Discoveries()
	if len(discoveries) != 1 {
		t.Fatalf("catalog discoveries = %d, want one strict call", len(discoveries))
	}
	got := discoveries[0].Query
	if got.OriginalLanguage != "en" || got.RuntimeMin != 20 || got.RuntimeMax != 60 ||
		got.VoteAverageMin != 6.5 || got.VoteCountMin != 100 {
		t.Fatalf("strict call lost compatible qualifiers: %+v", got)
	}
}

func TestRunToolDoesNotProjectMalformedNonEmptyFields(t *testing.T) {
	tests := []map[string]any{
		{"media_type": "series", "network": 17, "genres": []any{"Drama"}},
		{"media_type": "series", "network": "ABC", "query": 17},
		{"media_type": "series", "network": "ABC", "query": "   "},
		{"media_type": "series", "network": "ABC", "genres": []any{"Drama"}, "cast": []any{17}},
		{"media_type": "series", "network": "ABC", "runtime_min": 20.5},
		{"media_type": "series", "network": "ABC", "cast": []any{"Tiffani Thiessen"}, "genres": []any{17}},
		{"media_type": "series", "network": "ABC", "cast": []any{"Tiffani Thiessen"}, "keywords": []any{17}},
		{"media_type": "series", "network": "ABC", "cast": []any{"Tiffani Thiessen"}, "genres": []any{"   "}},
		{"media_type": "series", "network": "ABC", "creators": []any{"Jeff Franklin"}, "keywords": []any{"\t"}},
		{"media_type": "series", "network": "ABC", "cast": []any{"Tiffani Thiessen"}, "era": 1990},
	}
	for _, arguments := range tests {
		corpus := &catalogfixture.Corpus{}
		s := New(nil, catalog.New(nil, corpus), nil, 10)
		result, candidates, _ := s.runTool(context.Background(), llm.ToolCall{
			Name: catalogToolName, Arguments: arguments,
		}, Intent{}, nil)
		if !strings.Contains(result, `"error"`) || len(candidates) != 0 {
			t.Fatalf("malformed arguments %#v returned (%s, %+v), want bounded tool error", arguments, result, candidates)
		}
		if len(corpus.Searches()) != 0 || len(corpus.Discoveries()) != 0 {
			t.Fatalf("malformed arguments %#v reached the Catalog", arguments)
		}
	}
}

func TestParseDiscoveryQueryRejectsMalformedOrBroadeningInputs(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "bad language", args: map[string]any{"genres": []any{"Drama"}, "original_language": "english"}, want: "two-letter"},
		{name: "non-string query", args: map[string]any{"network": "ABC", "media_type": "series", "query": 17}, want: "query must be"},
		{name: "blank non-empty query", args: map[string]any{"network": "ABC", "media_type": "series", "query": "   "}, want: "query must be"},
		{name: "non-string genre", args: map[string]any{"network": "ABC", "media_type": "series", "genres": []any{17}}, want: "genres must be"},
		{name: "blank non-empty genre", args: map[string]any{"network": "ABC", "media_type": "series", "genres": []any{"   "}}, want: "genres must be"},
		{name: "non-string keyword", args: map[string]any{"network": "ABC", "media_type": "series", "keywords": []any{17}}, want: "keywords must be"},
		{name: "blank non-empty keyword", args: map[string]any{"network": "ABC", "media_type": "series", "keywords": []any{"\t"}}, want: "keywords must be"},
		{name: "bad era", args: map[string]any{"era": "whenever"}, want: "year, decade"},
		{name: "non-string era", args: map[string]any{"network": "ABC", "media_type": "series", "era": 1990}, want: "era must be"},
		{name: "non-string country", args: map[string]any{"genres": []any{"Drama"}, "origin_country": 44}, want: "two-letter"},
		{name: "fractional runtime", args: map[string]any{"genres": []any{"Drama"}, "runtime_min": 20.5}, want: "integer"},
		{name: "inverted runtime", args: map[string]any{"genres": []any{"Drama"}, "runtime_min": 90, "runtime_max": 20}, want: "must not exceed"},
		{name: "vote average above scale", args: map[string]any{"genres": []any{"Drama"}, "vote_average_min": 10.1}, want: "at most 10"},
		{name: "zero vote average", args: map[string]any{"genres": []any{"Drama"}, "vote_average_min": 0}, want: "greater than 0"},
		{name: "mixed search modes", args: map[string]any{"query": "Alien", "origin_country": "US"}, want: "cannot be combined"},
		{name: "network without series type", args: map[string]any{"network": "HBO"}, want: "requires media_type series"},
		{name: "network on movie", args: map[string]any{"media_type": "movie", "network": "HBO"}, want: "requires media_type series"},
		{name: "cast without movie type", args: map[string]any{"cast": []any{"Tom Hanks"}}, want: "require media_type movie"},
		{name: "creator on series", args: map[string]any{"media_type": "series", "creators": []any{"David Simon"}}, want: "require media_type movie"},
		{name: "network and people", args: map[string]any{"media_type": "series", "network": "HBO", "cast": []any{"Idris Elba"}}, want: "cannot be combined"},
		{name: "malformed cast", args: map[string]any{"media_type": "movie", "cast": []any{"Tom Hanks", 31}}, want: "cast must be"},
		{name: "duplicate creator", args: map[string]any{"media_type": "movie", "creators": []any{"Nora Ephron", " nora ephron "}}, want: "duplicate"},
		{name: "blank network", args: map[string]any{"media_type": "series", "network": "  "}, want: "network must be"},
		{name: "empty request", args: map[string]any{}, want: "provide query"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseDiscoveryQuery(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parse error = %v, want substring %q", err, tt.want)
			}
		})
	}
}
