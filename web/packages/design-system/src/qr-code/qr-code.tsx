import { View } from "@tamagui/core";
import { useMemo } from "react";
import Svg, { Path, Rect } from "react-native-svg";

import { BrandMark } from "../brand/brand";
import { matrixPath } from "../qr-code-matrix";
import { semanticColors } from "../tokens";

type QrCodeProps = {
  accessibilityLabel?: string;
  showBrandMark?: boolean;
  size?: number;
  value: string;
};

/**
 * Loomarr's machine-readable pairing mark. The fixed high-contrast colors and quiet zone are part
 * of the scanning contract, so product surfaces choose only the payload and logical size.
 */
const QrCode = ({
  accessibilityLabel = "Pair Loomarr with this QR code",
  showBrandMark = true,
  size = 180,
  value,
}: QrCodeProps) => {
  const path = useMemo(() => matrixPath(value, showBrandMark ? "H" : "L"), [showBrandMark, value]);
  const markSize = Math.round(size * 0.14);
  const markPadding = Math.max(3, Math.round(size * 0.018));
  const markPlateSize = markSize + markPadding * 2;
  const markInset = (size - markPlateSize) / 2;

  return (
    <View height={size} position="relative" width={size}>
      <Svg
        aria-label={accessibilityLabel}
        height={size}
        role="img"
        viewBox={`0 0 ${path.dimension} ${path.dimension}`}
        width={size}
      >
        <Rect fill={semanticColors.brand.foreground} height="100%" width="100%" />
        <Path d={path.commands} fill={semanticColors.brand.ground} />
      </Svg>
      {showBrandMark ? (
        <View
          alignItems="center"
          backgroundColor={semanticColors.brand.foreground}
          borderRadius={Math.round(markPlateSize * 0.2)}
          height={markPlateSize}
          justifyContent="center"
          left={markInset}
          position="absolute"
          top={markInset}
          width={markPlateSize}
        >
          <BrandMark contained decorative size={markSize} />
        </View>
      ) : null}
    </View>
  );
};

export type { QrCodeProps };
export { QrCode };
