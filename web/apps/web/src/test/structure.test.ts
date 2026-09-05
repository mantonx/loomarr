import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// Folder-per-module conformance (FE conventions rule 3 + 5): every unit is its OWN
// FOLDER holding `name.ts(x)`, optional `name.type.ts`, its test, and an `index.ts`
// barrel — no loose implementation files.
//
// This test exists because the convention was written down, agreed, and broken twice
// anyway (`src/auth/`, `src/wizard/`). Its FIRST version then repeated the same mistake
// one level up: it checked a hardcoded list of directories, so `src/suggest/` and
// `src/events/` — added later — were never checked at all, and a module that landed
// OUTSIDE `src/` escaped entirely. An allowlist someone must remember to extend is the
// very thing this is supposed to replace, so it now DISCOVERS what to check.
const APP = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const WEB = join(APP, "..", "..");

const isDir = (p: string) => statSync(p).isDirectory();
const entries = (p: string) => readdirSync(p);
const dirsIn = (p: string) => entries(p).filter((e) => isDir(join(p, e)));

// Files that are never a module implementation: barrels, types, tests, stories.
//
// ⚠ `.d.ts` is in this list as a RULE, not an exception. An ambient declaration exports
// nothing and runs nothing — it describes modules that live outside the source tree
// (`declare module "@fontsource-variable/geist"`), so there is no unit for it to be a
// folder of, and a barrel re-exporting it would re-export nothing. Adding the filename to
// ROOT_FILES instead would be exactly the hand-maintained allowlist this test's own
// comment says it exists to replace.
const isImplementation = (file: string) =>
  /\.tsx?$/.test(file) &&
  file !== "index.ts" &&
  !file.endsWith(".type.ts") &&
  !file.endsWith(".d.ts") &&
  !/\.(test|stories)\./.test(file);

// Directories a source tree may hold that are not module containers:
//  - `test`    cross-cutting specs + vitest setup, belonging to no single module
//  - `routes`  file-based routing: TanStack owns those filenames, they ARE the convention
//  - `generated` orval output, explicitly never restructured
const EXEMPT_DIRS = new Set(["test", "routes", "generated", "node_modules", "dist"]);

// Files allowed to sit at the root of a source tree (entry points + generated artifacts).
const ROOT_FILES = new Set(["main.tsx", "routeTree.gen.ts"]);

// An executable entry point is not a module: it runs on import, so a barrel re-exporting
// it would fire its side effects (packages/tokens' generator writes files). Rather than
// keep a hand-maintained exception list — the mistake this whole test exists to fix —
// entry points are DISCOVERED from the package's own scripts.
const entryPoints = (pkgRoot: string): Set<string> => {
  try {
    const pkg = readFileSync(join(pkgRoot, "package.json"), "utf8");
    const scripts = String(JSON.parse(pkg).scripts ? JSON.stringify(JSON.parse(pkg).scripts) : "");
    return new Set(Array.from(scripts.matchAll(/src\/([\w/.-]+\.tsx?)/g), (m) => m[1] as string));
  } catch {
    return new Set();
  }
};

// Walks a source tree and returns every violation, so a failure names all of them at
// once rather than one per run.
const violations = (root: string, pkgRoot = join(root, "..")): string[] => {
  const entries_ = entryPoints(pkgRoot);
  const found: string[] = [];

  const walk = (dir: string, depth: number) => {
    const here = entries(dir);
    const loose = here.filter(isImplementation);

    for (const file of loose) {
      const rel = relative(root, join(dir, file));
      // At the tree root, only entry points may be loose.
      if (depth === 0) {
        if (!ROOT_FILES.has(file)) found.push(`${rel} — a module must live in its own folder`);
        continue;
      }
      // Elsewhere, an implementation file must be named for the folder holding it.
      const folder = dir.split("/").pop();
      if (file.replace(/\.tsx?$/, "") !== folder) {
        found.push(`${rel} — should be named for its folder (${folder}) or live in its own`);
      }
      // An entry point needs no barrel — importing it would run it.
      if (!here.includes("index.ts") && !entries_.has(rel)) {
        found.push(`${rel} — module folder is missing an index.ts barrel`);
      }
    }

    for (const child of dirsIn(dir)) {
      if (EXEMPT_DIRS.has(child)) continue;
      walk(join(dir, child), depth + 1);
    }
  };

  walk(root, 0);
  return found;
};

describe("folder-per-module conformance", () => {
  it("apps/web/src follows folder-per-module", () => {
    expect(violations(join(APP, "src"))).toEqual([]);
  });

  // The convention says "applies EVERYWHERE — apps/web AND packages/* src modules", so
  // the check does too. packages/api/generated is orval output and stays exempt.
  for (const pkg of dirsIn(join(WEB, "packages"))) {
    it(`packages/${pkg}/src follows folder-per-module`, () => {
      expect(violations(join(WEB, "packages", pkg, "src"))).toEqual([]);
    });
  }

  // The slip this session: a mistyped `cd` created `apps/web/suggest/` instead of
  // `apps/web/src/suggest/`. Source outside the source tree is invisible to every other
  // check here, so it gets its own.
  it("apps/web holds no source directories outside src/", () => {
    const known = new Set([
      "src",
      "tests",
      "public",
      "node_modules",
      "storybook-static",
      "dist",
      // Playwright writes these; both are gitignored build artifacts.
      "test-results",
      "playwright-report",
    ]);
    const stray = dirsIn(APP).filter((d) => !known.has(d) && !d.startsWith("."));
    expect(stray, "source belongs under src/ (or tests/ for Playwright suites)").toEqual([]);
  });
});

describe("production application root", () => {
  it("mounts the shared design-system provider without changing the dark production theme", () => {
    const source = readFileSync(join(APP, "src", "main.tsx"), "utf8");
    const providerStart = source.indexOf('<LoomarrProvider theme="dark">');
    const queryProvider = source.indexOf("<QueryClientProvider", providerStart);
    const routerProvider = source.indexOf("<RouterProvider", queryProvider);
    const providerEnd = source.indexOf("</LoomarrProvider>", routerProvider);

    expect(providerStart).toBeGreaterThan(-1);
    expect(queryProvider).toBeGreaterThan(providerStart);
    expect(routerProvider).toBeGreaterThan(queryProvider);
    expect(providerEnd).toBeGreaterThan(routerProvider);
  });
});
