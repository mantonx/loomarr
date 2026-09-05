import { Action, type Density, Text } from "@loomarr/design-system";
import { useState } from "react";

import { ModalOverlay } from "../overlay";

interface DeviceDisconnectActionProps {
  density: Density;
  onDisconnect: () => Promise<void> | void;
  preferredFocus?: boolean;
  serverName?: string;
}

const DeviceDisconnectAction = ({
  density,
  onDisconnect,
  preferredFocus = false,
  serverName,
}: DeviceDisconnectActionProps) => {
  const [confirming, setConfirming] = useState(false);
  const [disconnecting, setDisconnecting] = useState(false);
  const [disconnectError, setDisconnectError] = useState(false);
  const disconnect = async () => {
    setDisconnecting(true);
    setDisconnectError(false);
    try {
      await onDisconnect();
    } catch {
      setDisconnectError(true);
      setDisconnecting(false);
    }
  };
  const cancel = () => {
    if (disconnecting) return;
    setConfirming(false);
    setDisconnectError(false);
  };

  return (
    <>
      <Action
        density={density}
        hasTVPreferredFocus={preferredFocus}
        onPress={() => setConfirming(true)}
        tone="secondary"
      >
        Disconnect device
      </Action>
      <ModalOverlay
        actions={[
          {
            disabled: disconnecting,
            label: "Keep connected",
            onPress: cancel,
            preferredFocus: true,
            tone: "secondary",
          },
          {
            disabled: disconnecting,
            label: disconnecting ? "Disconnecting…" : "Disconnect",
            onPress: () => void disconnect(),
            tone: "danger",
          },
        ]}
        density={density}
        description={`Loomarr will revoke this device’s credential on ${serverName ?? "the connected server"}. You can pair it again later.`}
        dismissible={!disconnecting}
        onDismiss={cancel}
        title="Disconnect this device?"
        visible={confirming}
      >
        {disconnectError ? (
          <Text density={density} textRole="metadata" tone="danger">
            Loomarr couldn’t disconnect this device. Check the connection and try again.
          </Text>
        ) : null}
      </ModalOverlay>
    </>
  );
};

export type { DeviceDisconnectActionProps };
export { DeviceDisconnectAction };
