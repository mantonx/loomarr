/// <reference types="vitest/config" />
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

// The SPA is embedded in the Go binary and served same-origin at / (main doc §12).
// Build output lands in internal/web/dist, which internal/web/embed.go embeds.
// In dev, Vite proxies the API surface to the Go server on :8080 so the app runs
// against the real backend with cookie auth intact.
const BACKEND_PORT = process.env.LOOMARR_DEV_PORT ?? "8080";
const DEV_PORT = Number(process.env.LOOMARR_FE_PORT ?? "5173");
const API_TARGET = process.env.LOOMARR_API ?? `http://localhost:${BACKEND_PORT}`;
const webReact = fileURLToPath(new URL("./node_modules/react", import.meta.url));
const webReactDOM = fileURLToPath(new URL("./node_modules/react-dom", import.meta.url));
const reactNativeWeb = fileURLToPath(new URL("./node_modules/react-native-web", import.meta.url));
const reactNativeSvgWeb = fileURLToPath(
  new URL("./node_modules/react-native-svg/lib/module/elements.web.js", import.meta.url),
);
const proxied = [
  // /v1 covers the whole versioned surface INCLUDING /v1/playout (§9.1 V47: the playout streaming
  // routes moved under /v1, so the in-app HLS player's same-origin /v1/playout/hls/... URLs are
  // proxied to the Go server automatically — no separate entry needed).
  "/v1",
  "/hooks",
  "/docs",
  "/openapi.json",
  "/openapi.yaml",
  "/healthz",
  "/readyz",
  "/metrics",
];

// internal/web/embed.go declares `//go:embed all:dist`, which does not compile unless the
// directory exists — so .gitkeep is the one committed file in there (.gitignore un-ignores
// exactly that path). But `emptyOutDir: true` deletes it on every build, and a routine
// `git add -A` then stages the deletion. That has now broken the clean-clone build twice.
//
// Rewriting it after the bundle closes the loop at the cause, rather than re-adding the
// file and waiting for the next build to remove it again.
const keepEmbedDir = (): Plugin => ({
  name: "loomarr-keep-embed-dir",
  apply: "build",
  closeBundle() {
    const dir = fileURLToPath(new URL("../../../internal/web/dist", import.meta.url));
    mkdirSync(dir, { recursive: true });
    writeFileSync(join(dir, ".gitkeep"), "");
  },
});

export default defineConfig({
  // tanstackRouter must precede react() — it generates src/routeTree.gen.ts from the
  // file-based routes in src/routes before React compiles (design §14).
  plugins: [
    keepEmbedDir(),
    tanstackRouter({ target: "react", autoCodeSplitting: true }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    // The production Web root now mounts the same universal design-system provider as native
    // clients. Resolve its host primitives through the browser adapters and one React runtime;
    // otherwise Vite reaches react-native-tvos' untranspiled Flow entrypoint.
    alias: [
      { find: "@", replacement: fileURLToPath(new URL("./src", import.meta.url)) },
      { find: /^react$/, replacement: webReact },
      { find: /^react-dom$/, replacement: webReactDOM },
      { find: /^react-native$/, replacement: reactNativeWeb },
      { find: /^react-native-svg$/, replacement: reactNativeSvgWeb },
    ],
    dedupe: ["react", "react-dom", "react-native"],
    extensions: [".web.mjs", ".web.js", ".web.ts", ".web.tsx", ".mjs", ".js", ".ts", ".tsx", ".json"],
  },
  // This directory also contains client-platform-proof.html, whose entrypoint deliberately imports
  // React Native modules. Dev dependency discovery must stay rooted at the browser app; otherwise
  // Vite tries to parse react-native-tvos' Flow sources before the web aliases can apply.
  optimizeDeps: {
    entries: ["index.html"],
  },
  server: {
    port: DEV_PORT,
    // Multiple worktrees receive distinct deterministic ports. If one is unexpectedly occupied,
    // fail with the advertised URL instead of Vite silently incrementing to an unknown port.
    strictPort: true,
    proxy: Object.fromEntries(
      proxied.map((path) => [path, { target: API_TARGET, changeOrigin: true, ws: path === "/v1" }]),
    ),
  },
  build: {
    outDir: fileURLToPath(new URL("../../../internal/web/dist", import.meta.url)),
    // Wipes the directory each build, which is what deletes .gitkeep — see keepEmbedDir.
    emptyOutDir: true,
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    // The shared browser-player adapter loads hls.js dynamically from its owning package. Alias the
    // transport explicitly so production-wrapper tests exercise the public adapter with a bounded
    // MediaSource controller even when pnpm resolves the dependency from that package.
    alias: {
      "hls.js": fileURLToPath(new URL("./src/test/hls.mock.ts", import.meta.url)),
    },
    css: false,
    // jsdom units only — Playwright visual specs (tests/visual/*.spec.ts) run under
    // Playwright, not vitest, and Storybook stories are exercised by the visual suite.
    include: ["src/**/*.test.{ts,tsx}"],
    exclude: ["node_modules", "dist", "storybook-static", "tests/visual/**"],
    passWithNoTests: true,
  },
});
