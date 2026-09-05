package playout

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/schedule"
)

type writeCloser struct{ bytes.Buffer }

func (*writeCloser) Close() error { return nil }

func TestPumpBlocksReportsOnlyAuthoritativeAiringTransitions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	identities := []AiringIdentity{
		{StartedAt: time.Unix(1, 0).UTC(), Kind: schedule.SlotProgram, ContentID: "episode"},
		{StartedAt: time.Unix(2, 0).UTC(), Kind: schedule.SlotFiller, ContentID: "commercial"},
	}
	next := 0
	source := BlockSource(func(context.Context, string, EncodePlan) (Block, error) {
		if next == len(identities) {
			cancel()
			return Block{}, context.Canceled
		}
		block := Block{
			Content:  io.NopCloser(strings.NewReader(string(rune('a' + next)))),
			Identity: identities[next],
		}
		next++
		return block, nil
	})
	var output writeCloser
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	pumpBlocks(ctx, &output, source, "channel", PlanBaseline, log)

	if got := output.String(); got != "ab" {
		t.Fatalf("mux input = %q, want both finite blocks", got)
	}
	line := logs.String()
	for _, want := range []string{
		"msg=\"playout: block first bytes from child\"", "child_first_byte_ms=",
		"msg=\"playout: block transition\"", "from_kind=program", "from_content=episode",
		"to_kind=filler", "to_content=commercial",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("transition log missing %q:\n%s", want, line)
		}
	}
}

// A tune-in burst can make a finite child reach EOF before its wall-clock Airing ends. Resolving
// immediately then returns the same Airing and replaying that response repeats the outgoing tail
// at the next programme or Clip boundary.
func TestPumpBlocksDoesNotReplayACleanlyFinishedAiring(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	programme := AiringIdentity{
		StartedAt: time.Unix(1, 0).UTC(), EndsAt: time.Now().Add(10 * time.Millisecond),
		Kind: schedule.SlotProgram, ContentID: "episode",
	}
	commercial := AiringIdentity{
		StartedAt: time.Unix(2, 0).UTC(), Kind: schedule.SlotFiller, ContentID: "commercial",
	}
	blocks := []Block{
		{Content: io.NopCloser(strings.NewReader("programme-tail")), Identity: programme},
		// The wall clock has not crossed the boundary yet, so resolution returns the same
		// Airing. Its bytes must not enter the mux a second time.
		{Content: io.NopCloser(strings.NewReader("programme-tail")), Identity: programme},
		{Content: io.NopCloser(strings.NewReader("commercial")), Identity: commercial},
	}
	next := 0
	source := BlockSource(func(context.Context, string, EncodePlan) (Block, error) {
		if next == len(blocks) {
			cancel()
			return Block{}, context.Canceled
		}
		block := blocks[next]
		next++
		return block, nil
	})
	var output writeCloser

	pumpBlocks(ctx, &output, source, "channel", PlanBaseline, nil)

	if got, want := output.String(), "programme-tailcommercial"; got != want {
		t.Fatalf("mux input = %q, want %q; the outgoing Airing was replayed", got, want)
	}
}
