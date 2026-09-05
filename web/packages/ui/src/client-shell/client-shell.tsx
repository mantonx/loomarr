import { BrandLockup, Screen, Surface, Text } from "@loomarr/design-system";
import { View } from "react-native";

import { ClientNavigation, clientDestinationLabel } from "../client-navigation";
import { DeviceDisconnectAction } from "../device-disconnect";
import type { ClientShellProps } from "./client-shell.type";

const ClientShell = ({ active, density, onDisconnect, onNavigate, serverName }: ClientShellProps) => {
  return (
    <Screen density={density} gap="$section">
      <View style={{ alignItems: "center", flexDirection: "row", justifyContent: "space-between" }}>
        <BrandLockup size={density === "tv" ? "large" : "medium"} />
        <View style={{ alignItems: "flex-end", gap: density === "tv" ? 12 : 8 }}>
          <Text density={density} textRole="metadata">
            {serverName ? `Connected to ${serverName}` : "Connected"}
          </Text>
          <DeviceDisconnectAction density={density} onDisconnect={onDisconnect} serverName={serverName} />
        </View>
      </View>
      <Surface flex={1} gap="$control" justifyContent="center" level="canvas">
        <Text density={density} textRole="display">
          {clientDestinationLabel(active)}
        </Text>
        <Text density={density} maxWidth={720} textRole="body">
          Your paired client is ready. Guide and playback arrive through the same shared shell without
          changing device authority.
        </Text>
      </Surface>
      <ClientNavigation active={active} density={density} onNavigate={onNavigate} />
    </Screen>
  );
};

export { ClientShell };
