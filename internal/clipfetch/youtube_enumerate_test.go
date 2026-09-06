package clipfetch_test

import (
	"context"
	"testing"

	"github.com/loomarr/loomarr/internal/clipfetch"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestYouTubeEnumeratorListsOnlyTheBoundedAuthorizedTarget(t *testing.T) {
	ytdlp := testkit.Executable(t, "yt-dlp", `#!/bin/sh
case "$*" in
  *--no-config*--flat-playlist*--skip-download*--dump-single-json*--playlist-end\ 2*https://www.youtube.com/@retroads/videos) ;;
  *) exit 9 ;;
esac
printf '%s\n' '{"playlist_count":3,"entries":[{"id":"one","url":"https://www.youtube.com/watch?v=one","title":"One reel","license":"Creative Commons","release_year":1987,"upload_date":"20260831","duration":92.5,"height":720},{"id":"two","webpage_url":"https://www.youtube.com/watch?v=two"}]}'
`)

	items, total, err := clipfetch.NewYouTubeEnumerator(ytdlp).Enumerate(
		context.Background(), "https://www.youtube.com/@retroads/videos", 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want source-declared 3", total)
	}
	if len(items) != 2 || items[0].ID != "one" || items[1].ID != "two" {
		t.Fatalf("items = %+v, want the two bounded playlist entries", items)
	}
	if items[0].URL != "https://www.youtube.com/watch?v=one" || items[1].URL != "https://www.youtube.com/watch?v=two" {
		t.Fatalf("item URLs = %+v, want downloadable per-item URLs", items)
	}
	if items[0].Title != "One reel" || items[0].License != "Creative Commons" ||
		items[0].ReleaseYear != 1987 || items[0].PublishedAt != "20260831" ||
		items[0].DurationMS != 92_500 || items[0].Height != 720 {
		t.Fatalf("metadata = %+v, want flat-playlist observations", items[0])
	}
}

func TestYouTubeEnumeratorRefusesAnUnavailableTool(t *testing.T) {
	if _, _, err := clipfetch.NewYouTubeEnumerator("").Enumerate(
		context.Background(), "https://www.youtube.com/playlist?list=PL1", 2); err == nil {
		t.Fatal("enumeration succeeded without yt-dlp")
	}
}
