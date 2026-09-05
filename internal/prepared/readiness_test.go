package prepared

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadinessPersistsStableBindingsWithoutOperationalInputs(t *testing.T) {
	root := t.TempDir()
	library, err := NewLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	index, err := OpenReadiness(library)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Source: testSource("movie", 2), Rendition: baselineRendition()}
	key := BindingKey{ChannelID: "ch-1", LibraryItemID: "item-1"}
	if err := index.RememberBinding(key, Binding{
		Policy: "balanced", ChannelPolicy: "eng", Request: request,
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenReadiness(library)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Binding(key, "balanced", "eng")
	if !ok || got != request {
		t.Fatalf("Binding after restart = (%+v, %v), want %+v", got, ok, request)
	}
	body, err := os.ReadFile(filepath.Join(root, readinessMetadata))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/media/", "http://", "https://", "api_key", "token"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("readiness persisted operational input %q:\n%s", forbidden, body)
		}
	}
}

func TestReadinessReplacesStalePolicies(t *testing.T) {
	library, err := NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	index, err := OpenReadiness(library)
	if err != nil {
		t.Fatal(err)
	}
	key := BindingKey{ChannelID: "ch", LibraryItemID: "episode"}
	one := Request{Source: testSource("episode"), Rendition: baselineRendition()}
	two := one
	two.Source.AudioTrack = 1
	if err := index.RememberBinding(key, Binding{Policy: "one", Request: one}); err != nil {
		t.Fatal(err)
	}
	if err := index.RememberBinding(key, Binding{Policy: "two", ChannelPolicy: "jpn", Request: two}); err != nil {
		t.Fatal(err)
	}
	if _, ok := index.Binding(key, "one", ""); ok {
		t.Fatal("replaced source policy still resolves")
	}
	if got, ok := index.Binding(key, "two", "jpn"); !ok || got != two {
		t.Fatalf("replacement binding = (%+v, %v)", got, ok)
	}
}

func TestReadinessReconcileRemovesStaleBindingWhenReplacementIsUnavailable(t *testing.T) {
	library, err := NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	index, err := OpenReadiness(library)
	if err != nil {
		t.Fatal(err)
	}
	key := BindingKey{ChannelID: "ch", LibraryItemID: "movie"}
	request := Request{Source: testSource("movie"), Rendition: baselineRendition()}
	if err := index.RememberBinding(key, Binding{Policy: "policy", Request: request}); err != nil {
		t.Fatal(err)
	}
	if err := index.ReconcileBindings(nil, []BindingKey{key}); err != nil {
		t.Fatal(err)
	}
	if _, ok := index.Binding(key, "policy", ""); ok {
		t.Fatal("stale binding remained in memory")
	}
	reopened, err := OpenReadiness(library)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Binding(key, "policy", ""); ok {
		t.Fatal("stale binding remained after restart")
	}
}

func TestReadinessCorruptionDegradesToAReplaceableEmptyIndex(t *testing.T) {
	library, err := NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library.root, readinessMetadata), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	index, err := OpenReadiness(library)
	if err == nil || index == nil {
		t.Fatalf("OpenReadiness = (%v, %v), want usable empty index plus warning", index, err)
	}
	key := BindingKey{ChannelID: "ch", LibraryItemID: "item"}
	request := Request{Source: testSource("item"), Rendition: baselineRendition()}
	if err := index.RememberBinding(key, Binding{Policy: "policy", Request: request}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenReadiness(library)
	if err != nil {
		t.Fatalf("replacement index remained corrupt: %v", err)
	}
	if got, ok := reopened.Binding(key, "policy", ""); !ok || got != request {
		t.Fatalf("replacement binding = (%+v, %v)", got, ok)
	}
}
