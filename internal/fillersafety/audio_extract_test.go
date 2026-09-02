//go:build !windows

package fillersafety

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/testkit/execfixture"
)

func TestCandidateAudioExtractionAddsCalibratedBoundedContext(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "arguments")
	t.Setenv("LOOMARR_AUDIO_EXTRACT_ARGUMENTS", arguments)
	ffmpeg := execfixture.POSIX(t, "ffmpeg", `
printf '%s\n' "$@" > "$LOOMARR_AUDIO_EXTRACT_ARGUMENTS"
for destination do :; done
printf 'RIFF0000WAVE' > "$destination"
`)
	plan := proposalTestPlan(t)
	wav, err := (ffmpegCandidateAudioExtractor{path: ffmpeg}).Extract(context.Background(), plan, Candidate{ID: "candidate-a", StartMS: 500, EndMS: 1_500})
	if err != nil || string(wav) != "RIFF0000WAVE" {
		t.Fatalf("wav=%q err=%v", wav, err)
	}
	raw, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(string(raw))
	if !adjacentArguments(args, "-ss", "0.000") || !adjacentArguments(args, "-t", "2.500") {
		t.Fatalf("context was not clamped and expanded: %v", args)
	}
}

func adjacentArguments(arguments []string, first, second string) bool {
	for index := 1; index < len(arguments); index++ {
		if arguments[index-1] == first && arguments[index] == second {
			return true
		}
	}
	return false
}
