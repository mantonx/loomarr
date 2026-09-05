import { LoomarrProvider } from "@loomarr/design-system";
import { type WatchingScheduleData, WatchingSurface, type WatchingSurfaceProps } from "@loomarr/ui";
import type { Meta, StoryObj } from "@storybook/react-native";
import { View } from "react-native";

const channel = { id: "nature", inAppPlayable: true, name: "Nature Documentaries", number: 21 };
type WatchingSnapshot = WatchingSurfaceProps["snapshot"];

const snapshot: WatchingSnapshot = {
  attemptId: 3,
  catalog: [channel],
  channel,
  livePlayback: { lagSeconds: 0, mode: "live", noticeRevision: 0, viewerTimeMs: 1_777_777_777_000 },
  previousChannelId: "games",
  recentChannelIds: ["games"],
  status: "playing",
};
const schedule: WatchingScheduleData = {
  next: { timeLabel: "2:30 AM", title: "Blue Planet — The Deep" },
  now: {
    badge: { label: "On now", tone: "live" },
    episodeLabel: "S2E4",
    facts: ["1996", "TV-PG", "Drama · Adventure"],
    progressPercent: 83,
    timeLabel: "2:00 AM–2:30 AM",
    title: "Nature Documentaries — “Pilot episode”",
  },
};

const Preview = ({
  loading,
  loadError,
  numberEntry,
  scheduleData = schedule,
  state = snapshot,
}: {
  loading?: boolean;
  loadError?: string;
  numberEntry?: WatchingSurfaceProps["numberEntry"];
  scheduleData?: WatchingScheduleData;
  state?: WatchingSnapshot;
}) => (
  <LoomarrProvider>
    <View style={{ height: "100%", width: "100%" }}>
      <WatchingSurface
        density={process.env.EXPO_PUBLIC_LOOMARR_STORYBOOK_DENSITY === "tv" ? "tv" : "touch"}
        loading={loading}
        loadError={loadError}
        numberEntry={numberEntry}
        onChannelDown={() => undefined}
        onChannelUp={() => undefined}
        onDismissControls={() => undefined}
        onGoLive={() => undefined}
        onOpenGuide={() => undefined}
        onOpenSurf={() => undefined}
        onPause={() => undefined}
        onPlay={() => undefined}
        onPrevious={() => undefined}
        onRetry={() => undefined}
        onShowControls={() => undefined}
        player={<View style={{ backgroundColor: "#1B1E24", flex: 1 }} />}
        schedule={scheduleData}
        snapshot={state}
      />
    </View>
  </LoomarrProvider>
);

const meta = {
  component: Preview,
  title: "Loomarr Components/Watching Surface",
} satisfies Meta<typeof Preview>;

type Story = StoryObj<typeof meta>;
const CurrentAndNext: Story = {};
const Loading: Story = {
  args: { loading: true, scheduleData: {}, state: { catalog: [], recentChannelIds: [], status: "empty" } },
};
const Tuning: Story = { args: { state: { ...snapshot, status: "tuning" } } };
const PlaybackError: Story = {
  args: { state: { ...snapshot, error: "The stream could not be decoded.", status: "failed" } },
};
const NumberEntry: Story = {
  args: { numberEntry: { channelName: "Nature Documentaries", digits: "21" } },
};
const Paused: Story = {
  args: {
    state: {
      ...snapshot,
      livePlayback: { lagSeconds: 23, mode: "paused", noticeRevision: 0, viewerTimeMs: 1_777_777_754_000 },
      status: "paused",
    },
  },
};
const BehindLive: Story = {
  args: {
    state: {
      ...snapshot,
      livePlayback: { lagSeconds: 83, mode: "behind", noticeRevision: 0, viewerTimeMs: 1_777_777_694_000 },
    },
  },
};
const EmptyChannel: Story = {
  args: { scheduleData: {}, state: { catalog: [], recentChannelIds: [], status: "empty" } },
};
const Light: Story = { globals: { theme: "light" } };

export default meta;
export {
  BehindLive,
  CurrentAndNext,
  EmptyChannel,
  Light,
  Loading,
  NumberEntry,
  Paused,
  PlaybackError,
  Tuning,
};
