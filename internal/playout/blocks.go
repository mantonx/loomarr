package playout

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/schedule"
)

// AiringIdentity is the scheduler-owned boundary metadata carried beside a finite transport block.
// It never enters the MPEG-TS payload; the supervisor uses it to identify real transitions without
// guessing from process exits or request timing.
type AiringIdentity struct {
	StartedAt       time.Time
	EndsAt          time.Time
	Kind            schedule.SlotKind
	ContentID       string
	ScheduleBlockID string
}

type Block struct {
	Content  io.ReadCloser
	Identity AiringIdentity
	// Format is the stable decoder shape carried by this block. The application adapter pins
	// the first value for the session and rejects a later prepared block that differs.
	Format BroadcastFormat
}

// BlockSource opens the finite MPEG-TS block that belongs on a Channel now. EOF is an Airing
// boundary: the supervisor closes that body, resolves again from the authoritative wall clock, and
// opens the next programme, Clip or fallback card.
type BlockSource func(context.Context, string, EncodePlan) (Block, error)

// BlockSpawner builds the production session spawner around finite, explicit blocks. One long-lived
// copy mux keeps output timestamps monotonic; Go owns the EOF-and-advance loop so an Airing boundary
// is no longer hidden inside a media-tool demuxer.
func BlockSpawner(ffmpeg string, source BlockSource, log *slog.Logger, observers ...*diagnostics.ProcessManager) Spawner {
	return func(ctx context.Context, channelID string, plan EncodePlan) (*Process, error) {
		var observer *diagnostics.ProcessManager
		if len(observers) > 0 {
			observer = observers[0]
		}
		proc, err := StartPipedObserved(ctx, ffmpeg, BlockMuxArgs(), log, nil, observer, diagnostics.ProcessSpec{
			Purpose: "playout_parent", ChannelID: channelID, Target: plan.String(),
		})
		if err != nil {
			return nil, err
		}
		if parentID := proc.ProcessRunID(); parentID != "" {
			ctx = diagnostics.WithProcessSpec(ctx, diagnostics.ProcessSpec{ParentRunID: parentID})
		}
		go pumpBlocks(ctx, proc.Stdin, source, channelID, plan, log)
		return proc, nil
	}
}

// BlockMuxArgs builds the one continuous transport mux fed by the block supervisor. Children have
// already copied or encoded into the session's stable broadcast format; this process only rebases
// their finite timestamp domains onto one monotonic MPEG-TS timeline.
func BlockMuxArgs() []string {
	return []string{
		"-hide_banner", "-loglevel", "error",
		"-progress", progressPipeArg(), "-nostats",
		// A copy-only prepared child can demux an entire fMP4 segment in one burst even with
		// input read-rate pacing. Pace the shared mux as the final authority so that burst is
		// absorbed by the pipe instead of overflowing a network viewer's bounded queue.
		"-readrate", "1.0",
		// The children already guarantee the broadcast stream shape. FFmpeg's defaults may
		// inspect several seconds of this live pipe before the copy mux emits anything; these
		// measured bounds still discover its video and audio streams without adding that delay.
		"-probesize", "256k", "-analyzeduration", "500000",
		"-f", "mpegts", "-i", "pipe:0",
		"-map", "0:v:0", "-map", "0:a:0",
		"-c", "copy",
		"-f", "mpegts", "-mpegts_flags", "+initial_discontinuity", "pipe:1",
	}
}

func pumpBlocks(
	ctx context.Context, dst io.WriteCloser, source BlockSource,
	channelID string, plan EncodePlan, log *slog.Logger,
) {
	defer func() { _ = dst.Close() }()
	var previous AiringIdentity
	previousFinishedCleanly := false
	for ctx.Err() == nil {
		// A genuine mid-Airing tune-in may use a finite read-rate burst to fill the viewer's
		// startup buffer. That child can consequently reach EOF before the wall-clock boundary.
		// Its bytes already cover the Airing through EndsAt, so resolving again before then would
		// return and replay the same outgoing tail.
		if previousFinishedCleanly && !waitForAiringBoundary(ctx, previous.EndsAt) {
			return
		}
		openStarted := time.Now()
		block, err := source(ctx, channelID, plan)
		if err != nil {
			if log != nil && ctx.Err() == nil {
				log.Warn("playout: block open failed; retrying", "channel", channelID, "plan", plan.String(), "err", err)
			}
			if !waitForBlockRetry(ctx) {
				return
			}
			continue
		}
		if previousFinishedCleanly && block.Identity.sameAiring(previous) {
			// EndsAt is authoritative when present, but identity is the final guard against clock
			// skew and legacy peers without that metadata. A cleanly-finished Airing has already
			// contributed all its bytes; never send it to the mux twice.
			_ = block.Content.Close()
			if !waitForBlockRetry(ctx) {
				return
			}
			continue
		}
		if previous != (AiringIdentity{}) && !block.Identity.sameAiring(previous) && log != nil {
			log.Info("playout: block transition",
				"channel", channelID,
				"from_kind", previous.Kind, "from_content", previous.ContentID,
				"to_kind", block.Identity.Kind, "to_content", block.Identity.ContentID,
				"schedule_block_id", block.Identity.ScheduleBlockID,
				"started_at", block.Identity.StartedAt)
		}
		content := &firstReadObserver{
			reader: block.Content,
			onFirst: func(n int) {
				if log != nil {
					log.Info("playout: block first bytes from child",
						"channel", channelID, "plan", plan.String(), "n", n,
						"child_first_byte_ms", time.Since(openStarted).Milliseconds())
				}
			},
		}
		n, copyErr := io.Copy(dst, content)
		closeErr := block.Content.Close()
		previous = block.Identity
		previousFinishedCleanly = n > 0 && copyErr == nil && closeErr == nil
		if copyErr != nil || closeErr != nil {
			if log != nil && ctx.Err() == nil {
				log.Warn("playout: block ended with an error; resolving current Airing",
					"channel", channelID, "plan", plan.String(), "copy_err", copyErr, "close_err", closeErr)
			}
		}
		// An empty success would otherwise hot-loop. Real programme and fallback responses always
		// carry MPEG-TS, so bounded retry is the honest failure posture.
		if n == 0 && !waitForBlockRetry(ctx) {
			return
		}
	}
}

type firstReadObserver struct {
	reader  io.Reader
	onFirst func(int)
	seen    bool
}

func (r *firstReadObserver) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 && !r.seen {
		r.seen = true
		r.onFirst(n)
	}
	return n, err
}

func (a AiringIdentity) sameAiring(other AiringIdentity) bool {
	return a.StartedAt.Equal(other.StartedAt) && a.Kind == other.Kind && a.ContentID == other.ContentID
}

func waitForAiringBoundary(ctx context.Context, endsAt time.Time) bool {
	if endsAt.IsZero() {
		return true
	}
	remaining := time.Until(endsAt)
	if remaining <= 0 {
		return true
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func waitForBlockRetry(ctx context.Context) bool {
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
