package prepared_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/loomarr/loomarr/internal/prepared"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestTransientInputDoesNotSerialize(t *testing.T) {
	t.Parallel()
	if _, err := json.Marshal(prepared.HTTPInput("http://media/original?api_key=secret")); !errors.Is(err, prepared.ErrTransientInput) {
		t.Fatalf("serialized transient input error = %v, want ErrTransientInput", err)
	}
}

type countingPackager struct {
	mu     sync.Mutex
	builds int
	err    error
}

type packagerFunc func(context.Context, string, prepared.Input, int, prepared.RenditionContract) (prepared.Output, error)

func (f packagerFunc) Package(
	ctx context.Context, workspace string, input prepared.Input, audioTrack int, rendition prepared.RenditionContract,
) (prepared.Output, error) {
	return f(ctx, workspace, input, audioTrack, rendition)
}

func (p *countingPackager) Package(
	_ context.Context, workspace string, _ prepared.Input, _ int, _ prepared.RenditionContract,
) (prepared.Output, error) {
	p.mu.Lock()
	p.builds++
	p.mu.Unlock()
	if p.err != nil {
		return prepared.Output{}, p.err
	}
	files := map[string]string{
		"media.m3u8":  "#EXTM3U\n#EXTINF:2,\nsegment.m4s\n#EXT-X-ENDLIST\n",
		"segment.m4s": "media",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(body), 0o600); err != nil {
			return prepared.Output{}, err
		}
	}
	return prepared.Output{Files: []string{"media.m3u8", "segment.m4s"}}, nil
}

func (p *countingPackager) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.builds
}

func preparedRequest(id string) prepared.Request {
	return prepared.Request{
		Source:    prepared.Source{ItemID: "item-" + id, SourceID: "source-" + id, Revision: "revision-" + id},
		Rendition: baseline("unused").Rendition,
	}
}

func newTestPreparer(
	t *testing.T, library *prepared.Library, packager prepared.Packager, access prepared.SourceAccess,
) *prepared.Preparer {
	t.Helper()
	return prepared.NewPreparer(prepared.PreparerDependencies{Library: library, Packager: packager, Access: access})
}

func TestPreparerLookupNeverOpensOrBuildsOnDemand(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	packager := &countingPackager{}
	access := &testkit.PreparedSourceAccess{Input: prepared.LocalInput("/media/movie.mkv")}
	preparer := newTestPreparer(t, lib, packager, access)

	if _, ok, err := preparer.Lookup(preparedRequest("movie")); err != nil || ok {
		t.Fatalf("cold Lookup = (_, %v, %v), want fast miss", ok, err)
	}
	if packager.count() != 0 || access.Calls() != 0 {
		t.Fatal("Lookup opened the source or started preparation")
	}
}

func TestPreparerSharesOnePublicationAcrossConcurrentRequests(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	packager := &countingPackager{}
	preparer := newTestPreparer(t, lib, packager, &testkit.PreparedSourceAccess{Input: prepared.LocalInput("/media/movie.mkv")})
	req := preparedRequest("movie")

	const callers = 8
	publications := make(chan prepared.Publication, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pub, err := preparer.Prepare(t.Context(), req)
			publications <- pub
			errs <- err
		}()
	}
	wg.Wait()
	close(publications)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	key := ""
	for pub := range publications {
		if key == "" {
			key = pub.Key
		}
		if pub.Key != key {
			t.Fatalf("publication key = %q, want shared %q", pub.Key, key)
		}
	}
	if packager.count() != 1 {
		t.Fatalf("package builds = %d, want 1", packager.count())
	}
	resolved, ok, err := preparer.Lookup(req)
	if err != nil || !ok || resolved.SourceFingerprint == "" {
		t.Fatalf("warm Lookup = (%#v, %v, %v), want ready specification", resolved, ok, err)
	}
}

func TestPreparerChangedRevisionProducesANewPublication(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	packager := &countingPackager{}
	preparer := newTestPreparer(t, lib, packager, &testkit.PreparedSourceAccess{Input: prepared.LocalInput("/media/movie.mkv")})
	oldRequest := preparedRequest("movie")
	old, err := preparer.Prepare(t.Context(), oldRequest)
	if err != nil {
		t.Fatal(err)
	}
	freshRequest := oldRequest
	freshRequest.Source.Revision = "revision-movie-2"
	if _, ok, err := preparer.Lookup(freshRequest); err != nil || ok {
		t.Fatalf("changed-revision Lookup = (_, %v, %v), want miss", ok, err)
	}
	fresh, err := preparer.Prepare(t.Context(), freshRequest)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Key == old.Key || packager.count() != 2 {
		t.Fatalf("changed source reused old publication: old=%q fresh=%q builds=%d", old.Key, fresh.Key, packager.count())
	}
}

func TestPreparerSelectedAudioTrackIsPartOfSourceIdentity(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	packager := &countingPackager{}
	preparer := newTestPreparer(t, lib, packager, &testkit.PreparedSourceAccess{Input: prepared.LocalInput("/media/movie.mkv")})
	one := preparedRequest("movie")
	two := one
	two.Source.AudioTrack = 1
	first, err := preparer.Prepare(t.Context(), one)
	if err != nil {
		t.Fatal(err)
	}
	second, err := preparer.Prepare(t.Context(), two)
	if err != nil {
		t.Fatal(err)
	}
	if first.Key == second.Key {
		t.Fatal("different selected audio tracks shared a publication")
	}
}

func TestPreparerFailedPackagingRemainsUnready(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("packager stopped")
	preparer := newTestPreparer(t, lib, &countingPackager{err: wantErr}, &testkit.PreparedSourceAccess{Input: prepared.LocalInput("/media/movie.mkv")})
	req := preparedRequest("movie")
	if _, err := preparer.Prepare(t.Context(), req); !errors.Is(err, wantErr) {
		t.Fatalf("Prepare error = %v, want %v", err, wantErr)
	}
	if _, ok, err := preparer.Lookup(req); err != nil || ok {
		t.Fatalf("failed-package Lookup = (_, %v, %v), want miss", ok, err)
	}
}

func TestPreparerRejectsSourceThatChangesWhilePackaging(t *testing.T) {
	t.Parallel()
	lib, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	packager := packagerFunc(func(_ context.Context, workspace string, _ prepared.Input, _ int, _ prepared.RenditionContract) (prepared.Output, error) {
		if err := os.WriteFile(filepath.Join(workspace, "media.m3u8"), []byte("#EXTM3U\n"), 0o600); err != nil {
			return prepared.Output{}, err
		}
		return prepared.Output{Files: []string{"media.m3u8"}}, nil
	})
	preparer := newTestPreparer(t, lib, packager, &testkit.PreparedSourceAccess{
		Input: prepared.LocalInput("/media/movie.mkv"), FailOnCall: 2,
	})
	req := preparedRequest("movie")
	if _, err := preparer.Prepare(t.Context(), req); !errors.Is(err, prepared.ErrSourceChanged) {
		t.Fatalf("Prepare error = %v, want ErrSourceChanged", err)
	}
	if _, ok, err := preparer.Lookup(req); err != nil || ok {
		t.Fatalf("changed-during-package Lookup = (_, %v, %v), want miss", ok, err)
	}
}

func TestPreparerLookupReusesACompletePublicationAfterRestart(t *testing.T) {
	library, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := preparedRequest("movie")
	access := &testkit.PreparedSourceAccess{Input: prepared.LocalInput("/media/movie.mkv")}
	first := newTestPreparer(t, library, &countingPackager{}, access)
	if _, err := first.Prepare(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	restarted := newTestPreparer(t, library, &countingPackager{err: errors.New("must not rebuild")}, access)
	specification, ok, err := restarted.Lookup(request)
	if err != nil || !ok || specification.SourceFingerprint == "" {
		t.Fatalf("Lookup after restart = (%+v, %v, %v), want existing publication", specification, ok, err)
	}
}
