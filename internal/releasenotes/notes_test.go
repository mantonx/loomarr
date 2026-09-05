package releasenotes

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/testkit/httpfixture"
)

const generatedNotes = `## What's Changed

* Add channel surfing by @alice in https://github.com/loomarr/loomarr/pull/12
* Fix playback selection by @bob in https://github.com/loomarr/loomarr/pull/14
* Update dependencies by @dependabot in https://github.com/loomarr/loomarr/pull/15

## New Contributors
* @alice made their first contribution in https://github.com/loomarr/loomarr/pull/12

**Full Changelog**: https://github.com/loomarr/loomarr/compare/v0.1.0...v0.2.0
`

func TestParseValidateAndRender(t *testing.T) {
	doc, err := Parse(generatedNotes)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Changes) != 3 || doc.Changes[0].Title != "Add channel surfing" {
		t.Fatalf("changes = %#v", doc.Changes)
	}
	classification := Classification{NewFeatures: []int{12}, BugFixes: []int{14}, Dependencies: []int{15}}
	got, err := Render(doc, classification)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## 🆕 New Features", "## 🐞 Bug Fixes", "## 📦 Dependencies", "## New Contributors", "**Full Changelog**"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered notes missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "## ✨ Improvements") {
		t.Errorf("empty category was rendered:\n%s", got)
	}
}

func TestValidationFailsClosed(t *testing.T) {
	doc, err := Parse(generatedNotes)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		value string
	}{
		{name: "missing", value: `{"12":"new_features","14":"bug_fixes"}`},
		{name: "invented", value: `{"12":"new_features","14":"bug_fixes","99":"dependencies"}`},
		{name: "duplicate key", value: `{"12":"new_features","12":"improvements","14":"bug_fixes","15":"dependencies"}`},
		{name: "extra field", value: `{"12":"new_features","14":"bug_fixes","15":"dependencies","summary":"maintenance"}`},
		{name: "invalid category", value: `{"12":"new_features","14":"bug_fixes","15":"surprise"}`},
		{name: "null value", value: `{"12":"new_features","14":"bug_fixes","15":null}`},
		{name: "trailing JSON", value: `{"12":"new_features","14":"bug_fixes","15":"dependencies"} {}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeClassification([]byte(tc.value), doc); err == nil {
				t.Fatal("unsafe classification was accepted")
			}
		})
	}
}

func TestClassificationSchemaRequiresOneCategoryPerPullRequest(t *testing.T) {
	doc := Document{Changes: make([]Change, 118)}
	for index := range doc.Changes {
		doc.Changes[index] = Change{Number: index + 1, Title: "Change " + strconv.Itoa(index+1)}
	}

	schema := classificationSchema(doc)
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	if len(properties) != len(doc.Changes) {
		t.Fatalf("property count = %d, want %d exact PR assignments", len(properties), len(doc.Changes))
	}
	required, ok := schema["required"].([]string)
	if !ok || len(required) != len(doc.Changes) {
		t.Fatalf("required = %#v, want every PR number", schema["required"])
	}
	for _, change := range doc.Changes {
		property, ok := properties[strconv.Itoa(change.Number)].(map[string]any)
		if !ok {
			t.Fatalf("missing schema property for PR #%d", change.Number)
		}
		if property["type"] != "string" {
			t.Fatalf("PR #%d type = %#v", change.Number, property["type"])
		}
		categories, ok := property["enum"].([]string)
		if !ok || len(categories) != len(classificationFields) {
			t.Fatalf("PR #%d categories = %#v", change.Number, property["enum"])
		}
	}
}

func TestOpenRouterUsesStructuredOutputAndValidatesResponse(t *testing.T) {
	doc, err := Parse(generatedNotes)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: httpfixture.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != OpenRouterEndpoint {
			t.Fatalf("endpoint = %s", req.URL)
		}
		if req.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("missing bearer key")
		}
		body, _ := io.ReadAll(req.Body)
		request := string(body)
		for _, want := range []string{`"type":"json_schema"`, `"strict":true`, `"require_parameters":true`, `\"number\":12`} {
			if !strings.Contains(request, want) {
				t.Errorf("request missing %s: %s", want, request)
			}
		}
		response := `{"choices":[{"message":{"content":"{\"12\":\"new_features\",\"14\":\"bug_fixes\",\"15\":\"dependencies\"}"}}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})}
	classification, err := (OpenRouter{APIKey: "secret", Client: client}).Classify(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(classification.NewFeatures) != 1 || classification.NewFeatures[0] != 12 {
		t.Fatalf("classification = %#v", classification)
	}
}
