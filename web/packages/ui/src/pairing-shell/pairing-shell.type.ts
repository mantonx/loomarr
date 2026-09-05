import type { PairingCredential, PairingSession } from "@loomarr/core/pairing";
import type { ServerDiscovery } from "@loomarr/core/server-discovery";
import type { Density } from "@loomarr/design-system";
import type { ReactNode } from "react";

type PairingShellProps = {
  allowServerEntry?: boolean;
  density: Density;
  discovery?: ServerDiscovery;
  discoveryForeground?: boolean;
  initialServerUrl?: string;
  renderPaired(credential: PairingCredential): ReactNode;
  session: PairingSession;
};

export type { PairingShellProps };
