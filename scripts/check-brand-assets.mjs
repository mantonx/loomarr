#!/usr/bin/env node

import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(fileURLToPath(new URL("..", import.meta.url)));
const lockPath = join(repoRoot, "brand-assets.lock.json");
const lock = JSON.parse(readFileSync(lockPath, "utf8"));
const sha256 = (path) => createHash("sha256").update(readFileSync(path)).digest("hex");
const failures = [];

const generatorPath = join(repoRoot, lock.generator);
if (sha256(generatorPath) !== lock.generatorSha256) {
  failures.push(`${lock.generator} changed without regeneration`);
}

const sourcePath = join(repoRoot, lock.source);
if (sha256(sourcePath) !== lock.sourceSha256) failures.push(`${lock.source} changed without regeneration`);

for (const [name, expected] of Object.entries(lock.derivatives)) {
  const path = join(repoRoot, name);
  if (!existsSync(path)) failures.push(`${name} is missing`);
  else if (sha256(path) !== expected) failures.push(`${name} drifted from the generated asset`);
}

if (failures.length > 0) {
  console.error(`brand asset drift:\n- ${failures.join("\n- ")}\nrun: make brand-assets`);
  process.exit(1);
}

console.log(`brand assets: ${Object.keys(lock.derivatives).length} derivatives match the canonical contract`);
