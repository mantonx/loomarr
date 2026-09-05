import brandContract from "./brand-contract.json";

const brandChroma = brandContract.chroma as [string, string, string, string, string, string, string];

const semanticThemes = {
  dark: {
    surface: {
      canvas: "#0B0C0E",
      raised: "#131519",
      elevated: "#1B1E24",
      overlay: "rgba(11, 12, 14, 0.88)",
      chrome: "rgba(11, 12, 14, 0.78)",
      entry: "rgba(11, 12, 14, 0.82)",
      identity: "rgba(11, 12, 14, 0.72)",
      focus: "#282C34",
    },
    content: {
      primary: "#E7EAF0",
      secondary: "#8B93A3",
      muted: "#8B93A3",
      inverse: "#0B0C0E",
    },
    border: {
      decorative: "#2A2E37",
      control: "#61646B",
      airing: "rgba(255, 176, 32, 0.4)",
    },
    state: {
      live: "#E85A5F",
      success: "#3DD68C",
      warning: "#F5D90A",
      danger: "#E85A5F",
      info: "#4CC9E8",
    },
    stateSurface: {
      live: "#331D21",
      success: "#19322A",
      warning: "#353217",
      danger: "#331D21",
      info: "#1C3038",
      airing: "rgba(255, 176, 32, 0.12)",
    },
    action: {
      primary: "#FFB020",
      secondary: "#E7EAF0",
      focus: "#FFB020",
      disabled: "#5A6170",
    },
    artwork: {
      placeholder: "#1B1E24",
      scrim: "rgba(11, 12, 14, 0.66)",
    },
  },
  light: {
    surface: {
      canvas: "#F7F8FA",
      raised: "#FFFFFF",
      elevated: "#E7EAF0",
      overlay: "rgba(255, 255, 255, 0.9)",
      chrome: "rgba(255, 255, 255, 0.78)",
      entry: "rgba(255, 255, 255, 0.82)",
      identity: "rgba(255, 255, 255, 0.72)",
      focus: "#FFF3D6",
    },
    content: {
      primary: "#17191D",
      secondary: "#515866",
      muted: "#69717F",
      inverse: "#FFFFFF",
    },
    border: {
      decorative: "#D2D6DE",
      control: "#747C8B",
      airing: "rgba(121, 80, 0, 0.4)",
    },
    state: {
      live: "#A5272C",
      success: "#116B45",
      warning: "#655500",
      danger: "#A5272C",
      info: "#08657A",
    },
    stateSurface: {
      live: "#F2DFDF",
      success: "#DBE9E3",
      warning: "#E8E6D9",
      danger: "#F2DFDF",
      info: "#DAE8EB",
      airing: "rgba(121, 80, 0, 0.12)",
    },
    action: {
      primary: "#795000",
      secondary: "#2A2E37",
      focus: "#8A5700",
      disabled: "#8B93A3",
    },
    artwork: {
      placeholder: "#E7EAF0",
      scrim: "rgba(11, 12, 14, 0.58)",
    },
  },
} as const;

const semanticColors = {
  brand: {
    signal: brandChroma[0],
    ground: brandContract.ground,
    foreground: brandContract.foreground,
    chroma: brandChroma,
  },
  ...semanticThemes.dark,
} as const;

const semanticSpace = {
  screen: 32,
  section: 24,
  control: 12,
  inline: 8,
} as const;

const semanticRadius = {
  control: 10,
  card: 16,
  overlay: 24,
  round: 999,
} as const;

const semanticMotion = {
  instant: 0,
  focus: 140,
  overlay: 220,
  tune: 280,
} as const;

const semanticTargets = {
  pointer: 40,
  touch: 48,
  tv: 64,
} as const;

const iconography = {
  size: {
    compact: 16,
    default: 20,
    control: 24,
    touch: 28,
    tv: 36,
  },
  strokeWidth: 2,
} as const;

const typography = {
  family: {
    body: {
      web: "'Geist Variable', Geist, ui-sans-serif, system-ui, sans-serif",
      native: "System",
    },
    data: {
      web: "'Geist Mono Variable', 'Geist Mono', ui-monospace, monospace",
      native: "monospace",
    },
  },
  pointer: {
    display: { size: 40, lineHeight: 44, weight: "700" },
    title: { size: 24, lineHeight: 30, weight: "650" },
    body: { size: 16, lineHeight: 24, weight: "400" },
    compact: { size: 13, lineHeight: 20, weight: "400" },
    caption: { size: 13, lineHeight: 18, weight: "400" },
    headline: { size: 20, lineHeight: 30, weight: "400" },
    label: { size: 14, lineHeight: 20, weight: "600" },
    section: { size: 14, lineHeight: 20, weight: "400" },
    metadata: { size: 12, lineHeight: 18, weight: "500" },
    time: { size: 13, lineHeight: 18, weight: "550" },
    data: { size: 16, lineHeight: 24, weight: "400" },
    reading: { size: 16, lineHeight: 24, weight: "400" },
    channelNumber: { size: 28, lineHeight: 32, weight: "650" },
    code: { size: 36, lineHeight: 43, weight: "400" },
  },
  touch: {
    display: { size: 38, lineHeight: 42, weight: "700" },
    title: { size: 24, lineHeight: 30, weight: "650" },
    body: { size: 17, lineHeight: 25, weight: "400" },
    compact: { size: 14, lineHeight: 21, weight: "400" },
    caption: { size: 14, lineHeight: 20, weight: "400" },
    headline: { size: 22, lineHeight: 33, weight: "400" },
    label: { size: 15, lineHeight: 21, weight: "600" },
    section: { size: 15, lineHeight: 22, weight: "400" },
    metadata: { size: 13, lineHeight: 18, weight: "500" },
    time: { size: 14, lineHeight: 20, weight: "550" },
    data: { size: 17, lineHeight: 25, weight: "400" },
    reading: { size: 17, lineHeight: 25, weight: "400" },
    channelNumber: { size: 30, lineHeight: 34, weight: "650" },
    code: { size: 40, lineHeight: 48, weight: "400" },
  },
  tv: {
    display: { size: 54, lineHeight: 60, weight: "700" },
    title: { size: 32, lineHeight: 39, weight: "650" },
    body: { size: 22, lineHeight: 31, weight: "400" },
    compact: { size: 20, lineHeight: 30, weight: "400" },
    caption: { size: 17, lineHeight: 26, weight: "400" },
    headline: { size: 30, lineHeight: 45, weight: "400" },
    label: { size: 18, lineHeight: 25, weight: "600" },
    section: { size: 20, lineHeight: 30, weight: "400" },
    metadata: { size: 16, lineHeight: 22, weight: "500" },
    time: { size: 18, lineHeight: 24, weight: "550" },
    data: { size: 24, lineHeight: 30, weight: "400" },
    reading: { size: 24, lineHeight: 36, weight: "400" },
    channelNumber: { size: 38, lineHeight: 43, weight: "650" },
    code: { size: 52, lineHeight: 62, weight: "400" },
  },
} as const;

type Density = keyof typeof semanticTargets;
type TextRole = keyof (typeof typography)["pointer"];

export type { Density, TextRole };
export {
  brandChroma,
  brandContract,
  iconography,
  semanticColors,
  semanticMotion,
  semanticRadius,
  semanticSpace,
  semanticTargets,
  semanticThemes,
  typography,
};
