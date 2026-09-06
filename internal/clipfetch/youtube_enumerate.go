package clipfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// EnumeratedItem is one video listed from an operator-authorized YouTube target.
type EnumeratedItem struct {
	ID          string
	URL         string
	Title       string
	License     string
	ReleaseYear int
	PublishedAt string
	DurationMS  int
	Height      int
}

// YouTubeEnumerator lists one playlist or channel without downloading media.
// It deliberately accepts one URL only: source registration, rather than this type, owns the
// authorization boundary for what may be enumerated.
type YouTubeEnumerator struct{ ytDlpPath string }

func NewYouTubeEnumerator(ytDlpPath string) *YouTubeEnumerator {
	return &YouTubeEnumerator{ytDlpPath: ytDlpPath}
}

// Enumerate uses yt-dlp's documented flat-playlist JSON listing mode. --no-config prevents an
// operator's ambient yt-dlp configuration from adding downloads, extractors, or other scope.
func (e *YouTubeEnumerator) Enumerate(ctx context.Context, target string, limit int) ([]EnumeratedItem, int, error) {
	if e.ytDlpPath == "" {
		return nil, 0, fmt.Errorf("yt-dlp is unavailable")
	}
	if target == "" {
		return nil, 0, fmt.Errorf("youtube enumeration target is empty")
	}
	if limit < 1 {
		return []EnumeratedItem{}, 0, nil
	}
	args := []string{
		"--no-config", "--flat-playlist", "--skip-download", "--dump-single-json",
		"--playlist-end", fmt.Sprint(limit), target,
	}
	out, err := exec.CommandContext(ctx, e.ytDlpPath, args...).Output()
	if err != nil {
		return nil, 0, fmt.Errorf("yt-dlp list %s: %w", target, err)
	}
	var listing struct {
		PlaylistCount int `json:"playlist_count"`
		Entries       []struct {
			ID          string  `json:"id"`
			URL         string  `json:"url"`
			WebpageURL  string  `json:"webpage_url"`
			Title       string  `json:"title"`
			License     string  `json:"license"`
			ReleaseYear int     `json:"release_year"`
			UploadDate  string  `json:"upload_date"`
			Duration    float64 `json:"duration"`
			Height      int     `json:"height"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(out, &listing); err != nil {
		return nil, 0, fmt.Errorf("decode yt-dlp listing: %w", err)
	}
	items := make([]EnumeratedItem, 0, len(listing.Entries))
	for _, entry := range listing.Entries {
		url := entry.WebpageURL
		if url == "" {
			url = entry.URL
		}
		if entry.ID == "" || url == "" {
			continue
		}
		items = append(items, EnumeratedItem{
			ID: entry.ID, URL: url, Title: entry.Title, License: entry.License,
			ReleaseYear: entry.ReleaseYear, PublishedAt: entry.UploadDate,
			DurationMS: int(entry.Duration * 1000), Height: entry.Height,
		})
		if len(items) == limit {
			break
		}
	}
	total := listing.PlaylistCount
	if total == 0 {
		total = len(items)
	}
	return items, total, nil
}
