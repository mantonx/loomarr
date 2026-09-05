import { fileURLToPath } from "node:url";
import type { StorybookConfig } from "@storybook/react-vite";
import { mergeConfig } from "vite";

const webReact = fileURLToPath(new URL("../node_modules/react", import.meta.url));
const webReactDOM = fileURLToPath(new URL("../node_modules/react-dom", import.meta.url));
const reactNativeWeb = fileURLToPath(new URL("../node_modules/react-native-web", import.meta.url));
const reactNativeSvgWeb = fileURLToPath(
  new URL(
    "../../../packages/design-system/node_modules/react-native-svg/lib/module/elements.web.js",
    import.meta.url,
  ),
);

// Storybook 10 (react-vite) — the component workshop AND the offline gallery the visual
// suite snapshots (frontend-design §5). Stories are co-located with their components
// (folder-per-component). a11y runs live in the workshop via addon-a11y; the CI gate is
// a Playwright pass over `storybook build` output (`storybook-static`). Chromatic rejected.
const config: StorybookConfig = {
  stories: ["../src/**/*.stories.@(ts|tsx)"],
  addons: ["@storybook/addon-a11y", "@storybook/addon-themes"],
  // ⚠ **`srcset` cannot be exercised with a data: URI, and this directory is why.** A base64 data
  // URI ALWAYS contains a comma (`data:image/png;base64,…`) and a comma is srcset's candidate
  // separator, so a data-URI candidate is unloadable — every UI/Image story rendered an <img> at
  // naturalWidth 0 and its baseline captured the ThumbHash placeholder rather than an image, green
  // forever (#210). Remote URLs are banned in visual stories because they race the snapshot, so
  // neither of the two obvious options works.
  //
  // These assets are same-origin (no race) and comma-free (loadable in srcset). They live HERE
  // rather than in `public/` deliberately: `public/` ships inside the app bundle, and these are
  // story fixtures, not product assets.
  staticDirs: [
    "./story-assets",
    { from: "../../mobile/assets", to: "/generated-brand/mobile" },
    { from: "../../tv/assets", to: "/generated-brand/tv" },
    { from: "../../../../store-listing/android-tv", to: "/generated-brand/store" },
  ],
  framework: { name: "@storybook/react-vite", options: {} },
  core: { disableTelemetry: true },
  viteFinal: async (baseConfig) =>
    mergeConfig(baseConfig, {
      optimizeDeps: {
        exclude: ["lucide-react-native"],
        include: ["react-native-svg"],
      },
      resolve: {
        alias: [
          { find: /^react$/, replacement: webReact },
          { find: /^react-dom$/, replacement: webReactDOM },
          { find: /^react-native$/, replacement: reactNativeWeb },
          { find: /^react-native-svg$/, replacement: reactNativeSvgWeb },
        ],
        dedupe: ["react", "react-dom", "react-native"],
        extensions: [".web.mjs", ".web.js", ".web.ts", ".web.tsx", ".mjs", ".js", ".ts", ".tsx", ".json"],
      },
    }),
};

export default config;
