import type { PairingState } from "@loomarr/core/pairing";
import type { ServerDiscoverySnapshot } from "@loomarr/core/server-discovery";
import {
  Action,
  ActivityIndicator,
  BrandLockup,
  BrandMark,
  Field,
  QrCode,
  Screen,
  Surface,
  Text,
} from "@loomarr/design-system";
import { useEffect, useState, useSyncExternalStore } from "react";

import type { PairingShellProps } from "./pairing-shell.type";

const emptyDiscovery: ServerDiscoverySnapshot = { servers: [], status: "unavailable" };
const subscribeToNothing = () => () => undefined;
const readEmptyDiscovery = () => emptyDiscovery;

const PairingShell = ({
  allowServerEntry = false,
  density,
  discovery,
  discoveryForeground = false,
  initialServerUrl,
  renderPaired,
  session,
}: PairingShellProps) => {
  const state = useSyncExternalStore(session.subscribe, session.snapshot, session.snapshot);
  const discoveryState = useSyncExternalStore(
    discovery?.subscribe ?? subscribeToNothing,
    discovery?.snapshot ?? readEmptyDiscovery,
    discovery?.snapshot ?? readEmptyDiscovery,
  );
  const [serverUrl, setServerUrl] = useState(initialServerUrl ?? "");
  const [manualEntry, setManualEntry] = useState(!discovery);
  const [, setTick] = useState(0);

  useEffect(() => {
    void session.initialize(initialServerUrl);
    return () => session.stop();
  }, [initialServerUrl, session]);
  useEffect(() => {
    if (state.status !== "awaiting-approval") return;
    const timer = setInterval(() => setTick((value) => value + 1), 1_000);
    return () => clearInterval(timer);
  }, [state.status]);
  useEffect(() => {
    if (state.status !== "needs-server" || !discovery || !discoveryForeground) {
      discovery?.stop();
      return undefined;
    }
    discovery.start();
    return () => discovery.stop();
  }, [discovery, discoveryForeground, state.status]);

  if (state.status === "paired") return renderPaired(state);
  const content = pairingContent(
    state,
    density,
    session,
    allowServerEntry,
    serverUrl,
    setServerUrl,
    discovery ? discoveryState : undefined,
    manualEntry,
    setManualEntry,
  );
  const awaitingApproval = state.status === "awaiting-approval";
  return (
    <Screen alignItems="center" density={density} gap="$section" justifyContent="center">
      {awaitingApproval ? (
        density === "tv" ? (
          <BrandMark contained={false} decorative size={24} width={320} />
        ) : (
          <BrandLockup orientation="horizontal" size="medium" />
        )
      ) : null}
      <Surface
        borderRadius={density === "tv" && awaitingApproval ? 8 : undefined}
        gap={density === "tv" && awaitingApproval ? 0 : "$section"}
        level={density === "tv" && awaitingApproval ? "raised" : "overlay"}
        maxWidth={density === "tv" && awaitingApproval ? 760 : 620}
        padding="$section"
        width="100%"
      >
        {awaitingApproval ? null : <BrandLockup size={density === "tv" ? "large" : "medium"} />}
        {content}
      </Surface>
    </Screen>
  );
};

const pairingContent = (
  state: PairingState,
  density: PairingShellProps["density"],
  session: PairingShellProps["session"],
  allowServerEntry: boolean,
  serverUrl: string,
  setServerUrl: (value: string) => void,
  discovery: ServerDiscoverySnapshot | undefined,
  manualEntry: boolean,
  setManualEntry: (value: boolean) => void,
) => {
  if (state.status === "loading")
    return (
      <>
        <ActivityIndicator accessibilityLabel="Connecting to Loomarr" />
        <Text density={density} textRole="body">
          Connecting to Loomarr…
        </Text>
      </>
    );
  if (state.status === "needs-server")
    return (
      <>
        <Text density={density} textRole="title">
          Find your Loomarr server
        </Text>
        <Text density={density} textRole="body">
          Choose a server on this network. You’ll approve this TV on the next screen.
        </Text>
        {discovery?.servers.map((server, index) => (
          <Action
            density={density}
            hasTVPreferredFocus={density === "tv" && index === 0}
            key={server.id}
            onPress={() => void session.pair(server.url)}
          >
            {`${discovery.servers.length === 1 ? `Connect to ${server.name}` : server.name}\n${server.url}`}
          </Action>
        ))}
        {discovery?.servers.length === 0 && discovery.status === "searching" ? (
          <Text accessibilityLiveRegion="polite" density={density} textRole="metadata">
            Searching this network…
          </Text>
        ) : null}
        {discovery?.error ? (
          <Text accessibilityLiveRegion="polite" density={density} textRole="metadata" tone="warning">
            {discovery.error}
          </Text>
        ) : null}
        {allowServerEntry ? (
          manualEntry ? (
            <>
              <Field
                accessibilityLabel="Loomarr server address"
                autoCapitalize="none"
                autoCorrect={false}
                density={density}
                hasTVPreferredFocus={density === "tv"}
                keyboardType="url"
                onChangeText={setServerUrl}
                onSubmitEditing={() => void session.pair(serverUrl)}
                placeholder="http://loomarr.local:8080"
                returnKeyType="go"
                value={serverUrl}
              />
              <Action density={density} onPress={() => void session.pair(serverUrl)}>
                Continue
              </Action>
            </>
          ) : (
            <Action
              density={density}
              hasTVPreferredFocus={density === "tv" && discovery?.servers.length === 0}
              onPress={() => setManualEntry(true)}
              tone="secondary"
            >
              Enter address manually
            </Action>
          )
        ) : (
          <Text density={density} textRole="metadata">
            Server selection is unavailable in this client.
          </Text>
        )}
      </>
    );
  if (state.status === "awaiting-approval") {
    const seconds = Math.max(0, Math.ceil((state.expiresAtMs - Date.now()) / 1_000));
    return (
      <>
        <Surface
          backgroundColor="$transparent"
          borderWidth={0}
          flexDirection={density === "tv" ? "row" : "column"}
          gap={density === "tv" ? 0 : "$section"}
          width="100%"
        >
          <Surface alignItems="center" backgroundColor="$transparent" borderWidth={0} flex={1} gap="$control">
            <Text density={density} textRole={density === "tv" ? "section" : "title"}>
              {density === "tv" ? "SCAN QR CODE" : "Scan QR Code"}
            </Text>
            <QrCode showBrandMark size={density === "tv" ? 150 : 180} value={state.verificationUriComplete} />
          </Surface>
          <Surface
            alignSelf="stretch"
            backgroundColor="$borderDecorative"
            borderWidth={0}
            height={density === "tv" ? "auto" : 1}
            minHeight={density === "tv" ? 210 : 1}
            width={density === "tv" ? 1 : "100%"}
          />
          <Surface
            alignItems="center"
            backgroundColor="$transparent"
            borderWidth={0}
            flex={1}
            gap="$control"
            justifyContent="flex-start"
          >
            <Text density={density} textRole={density === "tv" ? "section" : "title"}>
              {density === "tv" ? "VISIT WEBSITE" : "Visit Website"}
            </Text>
            <Text
              density={density}
              numberOfLines={1}
              selectable
              textRole={density === "tv" ? "metadata" : "time"}
              tone={density === "tv" ? "primary" : undefined}
            >
              {state.verificationUri}
            </Text>
            <Surface
              alignItems="center"
              backgroundColor="$transparent"
              borderWidth={0}
              gap="$inline"
              padding={density === "tv" ? 0 : "$control"}
              width="100%"
            >
              {density === "tv" ? null : (
                <Text density={density} textRole="metadata">
                  PAIRING CODE
                </Text>
              )}
              <Text
                accessibilityLabel={`Pairing code ${state.userCode}`}
                density={density}
                selectable
                textRole={density === "tv" ? "code" : "channelNumber"}
              >
                {state.userCode}
              </Text>
            </Surface>
          </Surface>
        </Surface>
        <Surface
          backgroundColor="$borderDecorative"
          borderWidth={0}
          height={1}
          marginVertical={density === "tv" ? 20 : 0}
          width="100%"
        />
        <Surface
          alignItems="center"
          backgroundColor="$transparent"
          borderWidth={0}
          gap={density === "tv" ? 12 : "$control"}
        >
          <Text accessibilityLiveRegion="polite" density={density} textRole="metadata">
            Expires in {Math.floor(seconds / 60)}:{String(seconds % 60).padStart(2, "0")}
          </Text>
          <Action
            density={density}
            hasTVPreferredFocus={density === "tv"}
            onPress={() => void session.pair(state.serverUrl)}
            tone="secondary"
          >
            Get a new code
          </Action>
        </Surface>
      </>
    );
  }
  if (state.status === "revoked")
    return (
      <>
        <Text density={density} textRole="title">
          This device was disconnected
        </Text>
        <Text density={density} textRole="body">
          Its credential is no longer valid. Pair it again to continue.
        </Text>
        <Action density={density} onPress={() => void session.pair(state.serverUrl)}>
          Pair again
        </Action>
      </>
    );
  if (state.status === "failed")
    return (
      <>
        <Text density={density} textRole="title">
          Couldn’t connect
        </Text>
        <Text density={density} textRole="body">
          {state.message}
        </Text>
        <Action density={density} onPress={() => void session.pair(state.serverUrl ?? serverUrl)}>
          Try again
        </Action>
        <Action
          density={density}
          onPress={() => {
            setManualEntry(false);
            session.chooseServer();
          }}
          tone="secondary"
        >
          Choose another server
        </Action>
      </>
    );
  return null;
};

export { PairingShell };
