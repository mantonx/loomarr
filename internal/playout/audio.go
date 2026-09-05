package playout

import (
	"context"
	"strings"
)

// Audio track selection (§9.1) — which of a file's audio tracks the viewer actually hears.
//
// # The bug this exists for
//
// ProgramArgs mapped `0:a:0`: the FIRST audio track, unconditionally. That is not "the
// original language" and not "the default track" — it is whatever the muxer happened to write
// first. A dev-install channel played Star Wars in RUSSIAN for exactly this reason: the release
// carried a Russian dub ahead of the English track, and `0:a:0` took it every time.
//
// Direct playback never showed it, which is why it survived. A media server applies the
// viewer's language preference when IT transcodes; internal playout calls ffmpeg itself and
// bypasses that machinery entirely, so the preference has to be reimplemented here or it does
// not exist.
//
// # Why the choice is made HERE and not in the ffmpeg args
//
// The obvious fix — `-map 0:a:m:language:eng?` with `-map 0:a:0` behind it as a fallback — does
// not work, in two independent ways, and both were verified against ffmpeg n8.1.2 rather than
// assumed:
//
//  1. ffmpeg REJECTS a trailing `?` on a metadata specifier: `-map 0:a:m:language:eng?` fails
//     with "Stream map '' matches no streams" and exits 234. The optional-map form is only
//     accepted on simple specifiers. So there is no way to express "…if present" for a
//     language match.
//  2. Even if it parsed, the two maps are not mutually exclusive. A file WITH an English track
//     would match both and emit TWO audio tracks, while a file without would emit one — and a
//     varying track count across programs breaks the parent's `-c copy` exactly like a varying
//     resolution does (see the map comment in ProgramArgs). The invariant is one video and
//     exactly one audio, always.
//
// So the decision is resolved to a concrete index BEFORE the args are built, and the args keep
// emitting a single unambiguous `-map 0:a:N`. That also keeps this unit-testable without
// executing ffmpeg: PickAudioTrack is a pure function over a track list.

// AudioTrack is one audio stream's identity, as far as selection cares.
type AudioTrack struct {
	// SourceIndex is the source-global stream index reported by ffprobe. Selection still returns
	// an audio-relative ordinal; Inventory retains this index so the measured observation preserves
	// the source's actual ordering rather than inventing global indexes.
	SourceIndex int
	// Language is the ISO 639-2 tag from the container, lowercased. Empty when untagged —
	// which is common enough that it must never be treated as a mismatch.
	Language string
}

// PickAudioTrack returns the INDEX AMONG AUDIO STREAMS to map, for a preferred language.
//
// The return value is the `N` in `-map 0:a:N`, not the absolute stream index: ffmpeg's `a:`
// specifier counts audio streams from zero, so a file whose first audio stream is stream 1
// still calls it `a:0`. Conflating the two would map the video stream as audio on most files.
//
// PREFERENCE, NEVER REQUIREMENT. Zero is returned for no match, no tracks, or no stated
// preference — always a valid map for any file with audio. A film with no English track plays
// in its own language, which is worse than a perfect match and far better than a channel that
// goes silent (or fails to encode) because a hard requirement could not be met.
//
// Matching is case-insensitive and trims whitespace: container tags are written by hundreds of
// different tools and "ENG" / "eng " are the same language.
func PickAudioTrack(tracks []AudioTrack, prefer string) int {
	prefer = strings.ToLower(strings.TrimSpace(prefer))
	if prefer == "" || len(tracks) == 0 {
		// No preference stated ⇒ ffmpeg's historical behaviour, preserved deliberately so an
		// operator who wants it can have it by clearing the setting.
		return 0
	}
	for i, t := range tracks {
		if strings.ToLower(strings.TrimSpace(t.Language)) == prefer {
			return i
		}
	}
	return 0
}

// AudioProber reports a source's audio tracks, in the order ffmpeg will number them.
//
// An interface so the resolver can be tested without exec, and so a failing probe is a normal
// return rather than a special case: nil tracks select index 0, which is what the code did
// before this existed. The concrete prober (FFprobeAudioNextTo) lives in probe.go, where all
// ffprobe use is consolidated.
type AudioProber func(ctx context.Context, input string) ([]AudioTrack, error)
