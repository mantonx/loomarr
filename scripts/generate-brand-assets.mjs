#!/usr/bin/env node

import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const generatorPath = fileURLToPath(import.meta.url);
const contractPath = join(repoRoot, "web/packages/design-system/src/tokens/brand-contract.json");
const contract = JSON.parse(readFileSync(contractPath, "utf8"));
const fontPath =
  process.env.LOOMARR_GEIST_FONT ||
  join(
    repoRoot,
    "web/node_modules/.pnpm/@fontsource-variable+geist@5.3.0/node_modules/@fontsource-variable/geist/files/geist-latin-wght-normal.woff2",
  );

const outputs = [];
const write = (path, value) => {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, value);
  outputs.push(path);
};

const markDrawArgs = ({ barHeight, barWidth, left, top }) =>
  contract.chroma.flatMap((color, index) => [
    "-fill",
    color,
    "-draw",
    `rectangle ${left + index * barWidth},${top} ${left + (index + 1) * barWidth - 1},${top + barHeight - 1}`,
  ]);

const renderMark = ({ output, size, barWidthRatio = 0.075, barHeightRatio = 0.52 }) => {
  const barWidth = Math.round(size * barWidthRatio);
  const barHeight = Math.round(size * barHeightRatio);
  const left = Math.round((size - barWidth * 7) / 2);
  const top = Math.round((size - barHeight) / 2);
  mkdirSync(dirname(output), { recursive: true });
  execFileSync("magick", [
    "-size",
    `${size}x${size}`,
    `xc:${contract.ground}`,
    ...markDrawArgs({ barHeight, barWidth, left, top }),
    "-alpha",
    "off",
    "-type",
    "TrueColor",
    "-depth",
    "8",
    output,
  ]);
  outputs.push(output);
};

const renderLockup = ({ output, width, height, barWidth, barHeight, left, top, pointSize, textX, baseline }) => {
  mkdirSync(dirname(output), { recursive: true });
  execFileSync("magick", [
    "-size",
    `${width}x${height}`,
    `xc:${contract.ground}`,
    ...markDrawArgs({ barHeight, barWidth, left, top }),
    "-font",
    fontPath,
    "-pointsize",
    String(pointSize),
    "-kerning",
    String(pointSize * contract.wordmark.trackingEm),
    "-fill",
    contract.foreground,
    "-stroke",
    contract.foreground,
    "-strokewidth",
    String(pointSize / 80),
    "-annotate",
    `+${textX}+${baseline}`,
    contract.name,
    "-alpha",
    "off",
    "-type",
    "TrueColor",
    "-depth",
    "8",
    output,
  ]);
  outputs.push(output);
};

const favicon = `<svg width="32" height="32" viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Loomarr">
  <title>Loomarr</title>
  <defs><clipPath id="card"><rect x="2" y="2" width="28" height="28" rx="6.5" /></clipPath></defs>
  <rect x="2" y="2" width="28" height="28" rx="6.5" fill="${contract.ground}" />
  <g clip-path="url(#card)">
${contract.chroma.map((color, index) => `    <rect x="${2 + index * 4}" y="2" width="4" height="28" fill="${color}" />`).join("\n")}
  </g>
  <rect x="2.5" y="2.5" width="27" height="27" rx="6" fill="none" stroke="${contract.outline}" stroke-width="1" />
</svg>
`;

write(join(repoRoot, "web/apps/web/public/favicon.svg"), favicon);

renderMark({ output: join(repoRoot, "web/apps/web/public/icon-192.png"), size: 192 });
renderMark({ output: join(repoRoot, "web/apps/web/public/icon-512.png"), size: 512 });
renderMark({ output: join(repoRoot, "web/apps/mobile/assets/icon.png"), size: 1024 });
renderMark({ output: join(repoRoot, "web/apps/mobile/assets/adaptive-icon.png"), size: 1024 });
renderMark({ output: join(repoRoot, "web/apps/mobile/assets/splash-icon.png"), size: 1024 });
renderMark({
  output: join(repoRoot, "web/apps/tv/assets/icon.png"),
  size: 1024,
  barWidthRatio: 0.095,
  barHeightRatio: 0.7,
});
renderMark({
  output: join(repoRoot, "web/apps/tv/assets/adaptive-icon.png"),
  size: 1024,
  barWidthRatio: 0.095,
  barHeightRatio: 0.7,
});

renderLockup({
  output: join(repoRoot, "web/apps/tv/assets/tv-banner.png"),
  width: 1280,
  height: 720,
  barWidth: 40,
  barHeight: 53,
  left: 104,
  top: 332,
  pointSize: 112,
  textX: 452,
  baseline: 408,
});
renderLockup({
  output: join(repoRoot, "store-listing/android-tv/tv-banner-1280x720.png"),
  width: 1280,
  height: 720,
  barWidth: 40,
  barHeight: 53,
  left: 104,
  top: 332,
  pointSize: 112,
  textX: 452,
  baseline: 408,
});
renderLockup({
  output: join(repoRoot, "store-listing/android-tv/feature-graphic-1024x500.png"),
  width: 1024,
  height: 500,
  barWidth: 30,
  barHeight: 40,
  left: 110,
  top: 229,
  pointSize: 84,
  textX: 371,
  baseline: 286,
});
renderLockup({
  output: join(repoRoot, "store-listing/android-tv/play-icon-512x512.png"),
  width: 512,
  height: 512,
  barWidth: 15,
  barHeight: 30,
  left: 39,
  top: 245,
  pointSize: 42,
  textX: 170,
  baseline: 281,
});

write(
  join(repoRoot, "web/apps/web/public/manifest.webmanifest"),
  `${JSON.stringify(
    {
      name: "Loomarr",
      short_name: "Loomarr",
      start_url: "/",
      display: "standalone",
      background_color: contract.ground,
      theme_color: contract.ground,
      icons: [
        { src: "/icon-192.png", sizes: "192x192", type: "image/png" },
        { src: "/icon-512.png", sizes: "512x512", type: "image/png" },
      ],
    },
    null,
    2,
  )}\n`,
);

const sha256 = (path) => createHash("sha256").update(readFileSync(path)).digest("hex");
const manifest = {
  generator: relative(repoRoot, generatorPath),
  generatorSha256: sha256(generatorPath),
  source: relative(repoRoot, contractPath),
  sourceSha256: sha256(contractPath),
  derivatives: Object.fromEntries(outputs.sort().map((path) => [relative(repoRoot, path), sha256(path)])),
};
writeFileSync(join(repoRoot, "brand-assets.lock.json"), `${JSON.stringify(manifest, null, 2)}\n`);
console.log(`generated ${outputs.length} Loomarr brand derivatives`);
