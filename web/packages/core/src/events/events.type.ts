// Frame payloads for the /v1/events SSE stream.
//
// ⚠ **GENERATED, not hand-mirrored.** These used to be interfaces written here by hand to
// match `map[string]string` literals at ten Go publish sites — one shape defined twice, in two
// languages, with nothing binding them. It drifted exactly as you would expect:
//
//   - `ChannelEvent` declared `id` while the backend has always sent `channelId`, so the field
//     read `undefined` forever. An `[k: string]: unknown` index signature hid it, and the
//     handler only invalidates by prefix, so nothing ever noticed.
//   - `LlmPullEvent.percent` was missing while the backend sent it all along, so the UI
//     recomputed a worse version and showed nothing during "starting".
//   - `SuggestionEvent.round` was a STRING, carrying a comment warning that declaring it a
//     number "would typecheck and then compare wrong at runtime" — a wart of the flat
//     string map, documented instead of fixed.
//
// The Go frames are typed DTOs now (internal/api/events.go), they reach api/openapi.yaml
// through huma's sse.Register, and orval generates these. `round` is a number and `channelId`
// is spelled right because the spec says so, not because someone remembered.
//
// Re-exported (rather than imported directly at each call site) so the SSE vocabulary still
// reads as one list, and so a frame added on the backend shows up here as a compile error
// rather than as a listener that never fires.

import type { ActivityEvent } from "@loomarr/api/models/activityEvent";
import type { ChannelEvent } from "@loomarr/api/models/channelEvent";
import type { DatabaseEvent } from "@loomarr/api/models/databaseEvent";
import type { FillerClipEvent } from "@loomarr/api/models/fillerClipEvent";
import type { FillerIngestEvent } from "@loomarr/api/models/fillerIngestEvent";
import type { FillerSplitEvent } from "@loomarr/api/models/fillerSplitEvent";
import type { HealthEvent } from "@loomarr/api/models/healthEvent";
import type { JobEvent } from "@loomarr/api/models/jobEvent";
import type { LLMPullEvent } from "@loomarr/api/models/lLMPullEvent";
import type { PlayoutEvent } from "@loomarr/api/models/playoutEvent";
import type { SuggestionEvent } from "@loomarr/api/models/suggestionEvent";
import type { SuggestionEventPhase } from "@loomarr/api/models/suggestionEventPhase";
import type { TitleEvent } from "@loomarr/api/models/titleEvent";

// Kept under the frontend's historical names so call sites don't churn: the Go type is
// LLMPullEvent (Go initialisms), the frontend has always called it LlmPullEvent, and
// SuggestionPhase reads better than SuggestionEventPhase at a use site.
type LlmPullEvent = LLMPullEvent;
type SuggestionPhase = SuggestionEventPhase;

interface EventHandlers {
  onTitle?: (e: TitleEvent) => void;
  onChannel?: (e: ChannelEvent) => void;
  onSuggestion?: (e: SuggestionEvent) => void;
  onLlmPull?: (e: LlmPullEvent) => void;
  onFillerIngest?: (e: FillerIngestEvent) => void;
  onFillerSplit?: (e: FillerSplitEvent) => void;
  onFillerClip?: (e: FillerClipEvent) => void;
  onJob?: (e: JobEvent) => void;
  onPlayout?: (e: PlayoutEvent) => void;
  onDatabase?: (e: DatabaseEvent) => void;
  onActivity?: (e: ActivityEvent) => void;
  onHealth?: (e: HealthEvent) => void;
}

interface EventStreamMessage {
  data: string;
}

interface EventStreamPort {
  addEventListener: (type: string, listener: (event: EventStreamMessage) => void) => void;
  close: () => void;
}

type EventStreamFactory = (url: string) => EventStreamPort;

export type {
  ActivityEvent,
  ChannelEvent,
  DatabaseEvent,
  EventHandlers,
  EventStreamFactory,
  EventStreamMessage,
  EventStreamPort,
  FillerClipEvent,
  FillerIngestEvent,
  FillerSplitEvent,
  HealthEvent,
  JobEvent,
  LlmPullEvent,
  PlayoutEvent,
  SuggestionEvent,
  SuggestionPhase,
  TitleEvent,
};
