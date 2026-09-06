// Command filler-spoken-model-review runs one independent, exhaustive,
// evidence-bound model review over an assembled spoken-safety draft.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/loomarr/loomarr/internal/fillersafetyreview"
)

const maximumAPIKeyBytes = 16 << 10

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-spoken-model-review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	plan := flags.String("plan", "", "private spoken-safety model review plan JSON")
	inputRoot := flags.String("input-root", "", "private assembled review-draft root")
	apiKeyFile := flags.String("api-key-file", "", "private OpenRouter API key file")
	ffmpeg := flags.String("ffmpeg", "ffmpeg", "exact ffmpeg executable")
	checkpoint := flags.String("checkpoint", "", "private crash-safe review checkpoint directory")
	output := flags.String("output", "", "new private model review JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *plan == "" || *inputRoot == "" || *apiKeyFile == "" || *ffmpeg == "" ||
		*checkpoint == "" || *output == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-model-review: exact plan, input root, API key file, ffmpeg, checkpoint, and output are required")
		return 2
	}
	apiKey, err := readAPIKey(*apiKeyFile)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-model-review:", err)
		return 1
	}
	result, err := fillersafetyreview.RunOpenRouter(context.Background(), fillersafetyreview.Config{
		PlanPath: *plan, InputRoot: *inputRoot, APIKey: apiKey, FFmpegPath: *ffmpeg,
		CheckpointDirectory: *checkpoint, OutputPath: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-model-review:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-spoken-model-review: reviewed %d cases in %d requests; %d rejected; %d prompt tokens; %d completion tokens; %d nano-USD charged; review sha256 %s\n",
		result.Cases, result.Requests, result.Rejected, result.PromptTokens, result.CompletionTokens,
		result.ChargedNanoUSD, result.ReviewSHA256)
	return 0
}

func readAPIKey(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 ||
		info.Size() <= 0 || info.Size() > maximumAPIKeyBytes {
		return "", fmt.Errorf("API key must be a non-empty private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read API key file")
	}
	key := strings.TrimSpace(string(raw))
	if key == "" || strings.ContainsAny(key, "\r\n\x00") {
		return "", fmt.Errorf("API key file contains invalid bytes")
	}
	return key, nil
}
