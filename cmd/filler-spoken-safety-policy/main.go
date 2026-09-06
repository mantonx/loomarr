// Command filler-spoken-safety-policy writes a private, versioned restricted-
// language policy from JSONL on stdin. It never echoes policy phrases.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

const maximumPolicyInputLineBytes = 4 << 10

type policyInput struct {
	Class  string `json:"class"`
	Mode   string `json:"mode,omitempty"`
	Phrase string `json:"phrase"`
}

type policyCapabilities struct {
	random  io.Reader
	publish func(fillerreview.TemporalSpokenSafetyPolicyBuildConfig) (fillerreview.TemporalSpokenSafetyPolicy, string, error)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, policyCapabilities{random: nil, publish: fillerreview.PublishTemporalSpokenSafetyPolicy}))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, capabilities policyCapabilities) int {
	flags := flag.NewFlagSet("filler-spoken-safety-policy", flag.ContinueOnError)
	flags.SetOutput(stderr)
	policyID := flags.String("policy-id", "", "opaque policy identity")
	generatedAtRaw := flags.String("generated-at", "", "fixed RFC3339 generation time")
	maximumGap := flags.Int64("maximum-inter-segment-gap-ms", 500, "maximum phrase gap across adjacent transcript segments")
	output := flags.String("output", "", "new private policy JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *policyID == "" || *generatedAtRaw == "" || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-safety-policy: --policy-id, --generated-at, and --output are required")
		return 2
	}
	generatedAt, err := time.Parse(time.RFC3339Nano, *generatedAtRaw)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-safety-policy: --generated-at must be RFC3339")
		return 2
	}
	var prohibited, ambiguous, prohibitedPrefixes, ambiguousPrefixes []string
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 1024), maximumPolicyInputLineBytes)
	for line := 1; scanner.Scan(); line++ {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var input policyInput
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			_, _ = fmt.Fprintf(stderr, "filler-spoken-safety-policy: input line %d is invalid JSON\n", line)
			return 1
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			_, _ = fmt.Fprintf(stderr, "filler-spoken-safety-policy: input line %d has trailing JSON\n", line)
			return 1
		}
		if input.Mode == "" {
			input.Mode = fillerreview.TemporalSpokenSafetyModeExactWords
		}
		switch {
		case input.Class == fillerreview.TemporalSpokenSafetyMatchProhibited && input.Mode == fillerreview.TemporalSpokenSafetyModeExactWords:
			prohibited = append(prohibited, input.Phrase)
		case input.Class == fillerreview.TemporalSpokenSafetyMatchAmbiguous && input.Mode == fillerreview.TemporalSpokenSafetyModeExactWords:
			ambiguous = append(ambiguous, input.Phrase)
		case input.Class == fillerreview.TemporalSpokenSafetyMatchProhibited && input.Mode == fillerreview.TemporalSpokenSafetyModeTokenPrefix:
			prohibitedPrefixes = append(prohibitedPrefixes, input.Phrase)
		case input.Class == fillerreview.TemporalSpokenSafetyMatchAmbiguous && input.Mode == fillerreview.TemporalSpokenSafetyModeTokenPrefix:
			ambiguousPrefixes = append(ambiguousPrefixes, input.Phrase)
		default:
			_, _ = fmt.Fprintf(stderr, "filler-spoken-safety-policy: input line %d has an unknown class\n", line)
			return 1
		}
	}
	if err := scanner.Err(); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-safety-policy: input exceeds the bounded line size")
		return 1
	}
	policy, digest, err := capabilities.publish(fillerreview.TemporalSpokenSafetyPolicyBuildConfig{
		PolicyID: *policyID, GeneratedAt: generatedAt, MaximumInterSegmentGapMS: *maximumGap,
		ProhibitedPhrases: prohibited, AmbiguousPhrases: ambiguous,
		ProhibitedPrefixes: prohibitedPrefixes, AmbiguousPrefixes: ambiguousPrefixes,
		Random: capabilities.random, OutputPath: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "filler-spoken-safety-policy: publish: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-spoken-safety-policy: wrote %d opaque rules (sha256 %s) to %s\n", len(policy.Rules), digest, *output)
	return 0
}
