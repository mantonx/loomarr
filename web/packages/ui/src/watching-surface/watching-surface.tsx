import { Action, ActivityIndicator, ProgressTrack, Surface, Text } from "@loomarr/design-system";
import { useCallback, useEffect, useRef } from "react";
import { Pressable, View } from "react-native";

import { ChannelIdentity, ProgrammeIdentity } from "../identity";
import { TransientOverlay } from "../overlay";
import type { WatchingSurfaceProps } from "./watching-surface.type";
import { behindLabel, playbackMessage } from "./watching-surface-state";

const LoadingChannels = ({ density }: Pick<WatchingSurfaceProps, "density">) => (
  <View style={{ alignItems: "center", flex: 1, justifyContent: "center" }}>
    <ActivityIndicator
      accessibilityLabel="Loading channels"
      size={density === "tv" ? "tv" : density === "touch" ? "touch" : "default"}
    />
  </View>
);

const NumberEntry = ({ density, numberEntry }: Pick<WatchingSurfaceProps, "density" | "numberEntry">) => {
  if (!numberEntry?.digits) return null;
  if (density === "tv")
    return (
      <Surface
        alignItems="center"
        backgroundColor="$surfaceEntry"
        borderRadius={8}
        flexDirection="row"
        gap={16}
        left={48}
        paddingHorizontal={20}
        paddingVertical={12}
        position="absolute"
        top={48}
      >
        <Text density="tv" textRole="channelNumber">
          {`${numberEntry.digits.split("").join(" ")} _`}
        </Text>
        {numberEntry.channelName ? (
          <Text density="tv" numberOfLines={1} textRole="reading" tone="muted">
            {numberEntry.channelName}
          </Text>
        ) : null}
      </Surface>
    );
  return (
    <Surface gap={4} left={0} level="overlay" padding="$control" position="absolute" top={0}>
      <Text density={density} textRole="title">
        {`${numberEntry.digits.split("").join(" ")} _`}
      </Text>
      {numberEntry.channelName ? (
        <Text density={density} numberOfLines={1} textRole="metadata">
          {numberEntry.channelName}
        </Text>
      ) : null}
    </Surface>
  );
};

const TouchWatchingSurface = ({
  chromeVisible = true,
  controlsVisible = true,
  density,
  loading = false,
  loadError,
  numberEntry,
  onChannelDown,
  onChannelUp,
  onDismissControls,
  onGoLive,
  onOpenGuide,
  onOpenSurf,
  onPause,
  onPlay,
  onPrevious,
  onRetry,
  onShowControls,
  player,
  schedule,
  snapshot,
}: WatchingSurfaceProps) => {
  const recoverableFailure = Boolean(loadError) || snapshot.status === "failed";
  const message = loading ? undefined : playbackMessage(snapshot, loadError);
  return (
    <View style={{ backgroundColor: "#000", flex: 1 }}>
      <View style={{ bottom: 0, left: 0, position: "absolute", right: 0, top: 0 }}>{player}</View>
      {chromeVisible ? (
        <>
          {loading ? null : (
            <Pressable
              accessibilityLabel="Show playback controls"
              accessibilityRole="button"
              onPress={onShowControls}
              pointerEvents="auto"
              style={{ bottom: 0, left: 0, position: "absolute", right: 0, top: 0 }}
            />
          )}
          {loading ? <LoadingChannels density={density} /> : null}
          <NumberEntry density={density} numberEntry={numberEntry} />
          <TransientOverlay
            autoDismissMs={message || snapshot.status === "tuning" ? undefined : 5_000}
            density={density}
            onDismiss={onDismissControls}
            title="Playback controls"
            visible={!loading && (controlsVisible || Boolean(message) || snapshot.status === "tuning")}
          >
            {snapshot.channel ? (
              <ChannelIdentity
                channel={{
                  channelLogoState: "missing",
                  channelName: snapshot.channel.name,
                  channelNumber: String(snapshot.channel.number),
                }}
                density={density}
              />
            ) : null}
            {schedule?.now ? (
              <Surface backgroundColor="$transparent" borderWidth={0} gap="$inline">
                <ProgrammeIdentity density={density} programme={schedule.now} />
                {schedule.now.progressPercent === undefined ? null : (
                  <ProgressTrack percent={schedule.now.progressPercent} tone="live" width="100%" />
                )}
                {schedule.next ? (
                  <Text density={density} numberOfLines={1} textRole="metadata">
                    {`Next ${schedule.next.timeLabel} · ${schedule.next.title}`}
                  </Text>
                ) : null}
              </Surface>
            ) : null}
            {snapshot.status === "tuning" ? (
              <Text accessibilityLiveRegion="polite" density={density} textRole="metadata">
                Tuning…
              </Text>
            ) : null}
            {snapshot.livePlayback ? (
              <Text accessibilityLiveRegion="polite" density={density} textRole="metadata">
                {snapshot.livePlayback.mode === "live"
                  ? "Live"
                  : `${snapshot.livePlayback.mode === "paused" ? "Paused · " : ""}${behindLabel(snapshot.livePlayback.lagSeconds)}`}
              </Text>
            ) : null}
            {snapshot.livePlayback?.noticeRevision ? (
              <Text accessibilityLiveRegion="polite" density={density} textRole="body" tone="warning">
                Paused position expired. Returned to live.
              </Text>
            ) : null}
            {message ? (
              <Text
                accessibilityLiveRegion="polite"
                density={density}
                textRole="body"
                tone={recoverableFailure ? "danger" : "muted"}
              >
                {message}
              </Text>
            ) : null}
            <View style={{ flexDirection: "row", flexWrap: "wrap", gap: density === "touch" ? 8 : 6 }}>
              <Action
                density={density}
                disabled={!snapshot.previousChannelId}
                icon="previous"
                onPress={onPrevious}
                tone="secondary"
              >
                Previous
              </Action>
              <Action
                density={density}
                disabled={snapshot.catalog.length < 2}
                onPress={onChannelDown}
                tone="secondary"
              >
                Channel −
              </Action>
              <Action density={density} icon="guide" onPress={onOpenGuide} tone="secondary">
                Guide
              </Action>
              <Action density={density} icon="channels" onPress={onOpenSurf} tone="secondary">
                Surf
              </Action>
              {snapshot.status === "paused" ? (
                <Action density={density} disabled={!snapshot.channel} onPress={onPlay} tone="primary">
                  Play
                </Action>
              ) : (
                <Action density={density} disabled={!snapshot.channel} onPress={onPause} tone="secondary">
                  Pause
                </Action>
              )}
              {snapshot.livePlayback?.mode && snapshot.livePlayback.mode !== "live" ? (
                <Action density={density} disabled={!snapshot.channel} onPress={onGoLive} tone="secondary">
                  Go Live
                </Action>
              ) : null}
              <Action
                density={density}
                disabled={snapshot.catalog.length < 2}
                onPress={onChannelUp}
                tone="secondary"
              >
                Channel +
              </Action>
              {recoverableFailure ? (
                <Action density={density} onPress={onRetry} tone="primary">
                  Retry
                </Action>
              ) : null}
            </View>
          </TransientOverlay>
        </>
      ) : null}
    </View>
  );
};

const TvWatchingSurface = ({
  chromeVisible = true,
  controlsActivityKey = 0,
  controlsVisible = true,
  loading = false,
  loadError,
  numberEntry,
  onDismissControls,
  onGoLive,
  onOpenGuide,
  onPlay,
  onRetry,
  player,
  schedule,
  snapshot,
}: WatchingSurfaceProps) => {
  const message = loading ? undefined : playbackMessage(snapshot, loadError);
  const recoverableFailure = Boolean(loadError) || snapshot.status === "failed";
  const overlayVisible = !loading && (controlsVisible || Boolean(message) || snapshot.status === "tuning");
  const activityKeyRef = useRef(controlsActivityKey);
  const dismissControlsRef = useRef(onDismissControls);

  useEffect(() => {
    activityKeyRef.current = controlsActivityKey;
  }, [controlsActivityKey]);
  useEffect(() => {
    dismissControlsRef.current = onDismissControls;
  }, [onDismissControls]);
  const dismissControlsForActivity = useCallback(() => {
    if (activityKeyRef.current !== controlsActivityKey) return;
    dismissControlsRef.current();
  }, [controlsActivityKey]);

  useEffect(() => {
    if (!overlayVisible || message || snapshot.status === "tuning") return undefined;
    const timeout = setTimeout(dismissControlsForActivity, 5_000);
    return () => clearTimeout(timeout);
  }, [dismissControlsForActivity, message, overlayVisible, snapshot.status]);

  return (
    <View style={{ backgroundColor: "#000", flex: 1 }}>
      <View style={{ bottom: 0, left: 0, position: "absolute", right: 0, top: 0 }}>{player}</View>
      {chromeVisible ? (
        <>
          {loading ? null : (
            <Pressable
              accessibilityLabel="Open programme guide"
              accessibilityRole="button"
              focusable={snapshot.status !== "paused" && !recoverableFailure}
              hasTVPreferredFocus={snapshot.status !== "paused" && !recoverableFailure}
              onPress={onOpenGuide}
              pointerEvents="auto"
              style={{ bottom: 0, left: 0, position: "absolute", right: 0, top: 0 }}
            />
          )}
          {loading ? <LoadingChannels density="tv" /> : null}
          <NumberEntry density="tv" numberEntry={numberEntry} />
          {overlayVisible ? (
            <>
              {snapshot.channel ? (
                <Surface
                  alignItems="center"
                  backgroundColor="$surfaceIdentity"
                  borderRadius={8}
                  borderWidth={1}
                  flexDirection="row"
                  gap={12}
                  paddingHorizontal={16}
                  paddingVertical={8}
                  position="absolute"
                  right={48}
                  top={48}
                >
                  <Text density="tv" textRole="data" tone="signal">
                    {String(snapshot.channel.number).padStart(2, "0")}
                  </Text>
                  <Text density="tv" numberOfLines={1} textRole="compact">
                    {snapshot.channel.name.toUpperCase()}
                  </Text>
                  {snapshot.status === "playing" ? (
                    <Surface
                      backgroundColor="$stateSuccess"
                      borderRadius="$round"
                      borderWidth={0}
                      height={8}
                      width={8}
                    />
                  ) : null}
                </Surface>
              ) : null}
              <Surface
                backgroundColor="$surfaceChrome"
                borderRadius={0}
                borderWidth={0}
                bottom={0}
                gap={0}
                left={0}
                paddingTop={24}
                position="absolute"
                right={0}
              >
                {schedule?.now ? (
                  <Surface backgroundColor="$transparent" borderWidth={0} gap={0}>
                    <Surface
                      alignItems="center"
                      backgroundColor="$transparent"
                      borderWidth={0}
                      flexDirection="row"
                      gap={12}
                      paddingHorizontal={64}
                    >
                      <Text density="tv" flex={1} numberOfLines={1} textRole="headline">
                        {schedule.now.title}
                      </Text>
                      {schedule.now.episodeLabel ? (
                        <Text density="tv" textRole="data" tone="muted">
                          {schedule.now.episodeLabel}
                        </Text>
                      ) : null}
                      {schedule.now.facts?.[0] ? (
                        <Text density="tv" textRole="time" tone="muted">
                          {schedule.now.facts[0]}
                        </Text>
                      ) : null}
                      <Text density="tv" textRole="time" tone="muted">
                        {schedule.now.timeLabel}
                      </Text>
                    </Surface>
                    {schedule.now.progressPercent === undefined ? null : (
                      <ProgressTrack
                        height={5}
                        marginTop={12}
                        percent={schedule.now.progressPercent}
                        width="100%"
                      />
                    )}
                  </Surface>
                ) : null}
                {snapshot.status === "tuning" ? (
                  <Text
                    accessibilityLiveRegion="polite"
                    density="tv"
                    marginHorizontal={64}
                    marginTop={12}
                    textRole="metadata"
                  >
                    Tuning…
                  </Text>
                ) : null}
                {message ? (
                  <Text
                    accessibilityLiveRegion="polite"
                    density="tv"
                    marginHorizontal={64}
                    marginTop={12}
                    textRole="body"
                    tone={recoverableFailure ? "danger" : "muted"}
                  >
                    {message}
                  </Text>
                ) : null}
                {snapshot.livePlayback?.noticeRevision ? (
                  <Text
                    accessibilityLiveRegion="polite"
                    density="tv"
                    marginHorizontal={64}
                    marginTop={12}
                    textRole="body"
                    tone="warning"
                  >
                    Paused position expired. Returned to live.
                  </Text>
                ) : null}
                <Surface
                  alignItems="center"
                  backgroundColor="$transparent"
                  borderWidth={0}
                  flexDirection="row"
                  paddingBottom={16}
                  paddingHorizontal={64}
                  paddingTop={12}
                >
                  <Text density="tv" flex={1} numberOfLines={1} textRole="reading" tone="muted">
                    {schedule?.next
                      ? `Up next · ${schedule.next.timeLabel} — ${schedule.next.title}`
                      : "No later programme in this guide window"}
                  </Text>
                  {snapshot.livePlayback && snapshot.livePlayback.mode !== "live" ? (
                    <Text density="tv" textRole="metadata">
                      {`${snapshot.livePlayback.mode === "paused" ? "Paused · " : ""}${behindLabel(snapshot.livePlayback.lagSeconds)}`}
                    </Text>
                  ) : null}
                  <Text density="tv" marginLeft={24} textRole="metadata" tone="muted">
                    ▲▼ tune · ◀ channels · 0–9 jump · OK guide
                  </Text>
                </Surface>
                {snapshot.status === "paused" || recoverableFailure ? (
                  <Surface
                    backgroundColor="$transparent"
                    borderWidth={0}
                    flexDirection="row"
                    gap="$control"
                    paddingBottom={16}
                    paddingHorizontal={64}
                  >
                    {snapshot.status === "paused" ? (
                      <Action density="tv" disabled={!snapshot.channel} onPress={onPlay} tone="primary">
                        Play
                      </Action>
                    ) : null}
                    {snapshot.livePlayback?.mode && snapshot.livePlayback.mode !== "live" ? (
                      <Action density="tv" disabled={!snapshot.channel} onPress={onGoLive} tone="secondary">
                        Go Live
                      </Action>
                    ) : null}
                    {recoverableFailure ? (
                      <Action density="tv" onPress={onRetry} tone="primary">
                        Retry
                      </Action>
                    ) : null}
                  </Surface>
                ) : null}
              </Surface>
            </>
          ) : null}
        </>
      ) : null}
    </View>
  );
};

const WatchingSurface = (props: WatchingSurfaceProps) =>
  props.density === "tv" ? <TvWatchingSurface {...props} /> : <TouchWatchingSurface {...props} />;

export { WatchingSurface };
