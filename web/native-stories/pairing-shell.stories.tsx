import { PairingSession } from "@loomarr/core";
import { Surface, Text } from "@loomarr/design-system";
import { PairingShell } from "@loomarr/ui";
import type { Meta, StoryObj } from "@storybook/react-native";
import { useMemo } from "react";

type PairingScenario = "error" | "loading" | "ready";

const PairingStory = ({
  density,
  scenario = "ready",
}: {
  density: "touch" | "tv";
  scenario?: PairingScenario;
}) => {
  const session = useMemo(
    () =>
      new PairingSession({
        createTransport: () => ({
          poll: async () => ({ status: "pending" }),
          start: async (_deviceName, signal) => {
            if (scenario === "error") throw new Error("The Loomarr server could not be reached.");
            if (scenario === "loading")
              return new Promise((_resolve, reject) =>
                signal.addEventListener("abort", () => reject(new Error("Story stopped")), {
                  once: true,
                }),
              );
            return {
              body: {
                deviceCode: "storybook-device-code",
                expiresAt: "2026-08-24T12:10:00Z",
                interval: 5,
                userCode: "WMQJ-QVFJ",
              },
              serverDate: "Sun, 24 Aug 2026 12:00:00 GMT",
            };
          },
        }),
        deviceName: density === "tv" ? "Living Room TV" : "iPhone",
        now: () => Date.now(),
        sleep: (_milliseconds: number, signal: AbortSignal) =>
          new Promise<void>((_resolve, reject) =>
            signal.addEventListener("abort", () => reject(new Error("Story stopped")), { once: true }),
          ),
        store: {
          clear: async () => {},
          read: async () => undefined,
          write: async () => {},
        },
      }),
    [density, scenario],
  );
  return (
    <PairingShell
      density={density}
      initialServerUrl="https://loomarr.projectguacamole.com"
      renderPaired={() => (
        <Surface>
          <Text textRole="title">Paired</Text>
        </Surface>
      )}
      session={session}
    />
  );
};

const meta = {
  title: "Loomarr Components/Pairing Shell",
  component: PairingStory,
  args: { density: "touch" },
} satisfies Meta<typeof PairingStory>;

type Story = StoryObj<typeof meta>;
const Touch: Story = {};
const Tv: Story = { args: { density: "tv" } };
const TvError: Story = { args: { density: "tv", scenario: "error" } };
const TvLoading: Story = { args: { density: "tv", scenario: "loading" } };
const Light: Story = { globals: { theme: "light" } };

export default meta;
export { Light, Touch, Tv, TvError, TvLoading };
