import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const sourceRoots = [
  "../apps/web/src/",
  "../apps/mobile/app/",
  "../apps/tv/src/",
  "../packages/api/src/",
  "../packages/core/src/",
  "../packages/fixtures/src/",
  "../packages/player/src/",
  "../packages/tokens/src/",
  "../packages/ui/src/",
].map((path) => fileURLToPath(new URL(path, import.meta.url)));

// These are composition roots for the shared client platform today. The legacy Web application
// joins this list cohort-by-cohort in P7; adding it prematurely would describe unfinished migration
// work as an invariant instead of making the migrated roots fail closed now.
const sharedClientAppRoots = [
  "../apps/mobile/app/",
  "../apps/tv/src/",
  "../apps/web/src/client-platform-proof/",
].map((path) => fileURLToPath(new URL(path, import.meta.url)));

// These files are useful catalogs for tests, stories, and editor tooling. A production module
// importing one makes its dependency surface ambiguous and, for a value import, can pull an entire
// domain back across a route boundary (frontend-design §4.4).
const CATALOG_IMPORTS = new Set([
  "@/auth",
  "@/channels",
  "@/components/loomarr",
  "@/components/ui",
  "@/events",
  "@/filler",
  "@/help",
  "@/lib",
  "@/palette",
  "@/people",
  "@/queue",
  "@/settings",
  "@/suggest",
  "@/wizard",
  "@loomarr/api",
  "@loomarr/core",
]);

const isToolingFile = (path) =>
  path.includes("/test/") ||
  path.endsWith(".test.ts") ||
  path.endsWith(".test.tsx") ||
  path.endsWith(".stories.ts") ||
  path.endsWith(".stories.tsx");

const sourceFiles = (directory) =>
  readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return sourceFiles(path);
    return /\.(?:ts|tsx)$/.test(entry.name) && !isToolingFile(path) ? [path] : [];
  });

// Remove comments while preserving every character position. This keeps an import-looking code
// sample in a comment from becoming a false positive and lets diagnostics point at the original
// line and column without needing a second parser dependency.
const withoutComments = (source) => {
  let result = "";
  let state = "code";
  for (let i = 0; i < source.length; i += 1) {
    const char = source[i];
    const next = source[i + 1];
    if (state === "line") {
      if (char === "\n") {
        state = "code";
        result += char;
      } else result += " ";
    } else if (state === "block") {
      if (char === "*" && next === "/") {
        result += "  ";
        state = "code";
        i += 1;
      } else result += char === "\n" ? char : " ";
    } else if (state === "single" || state === "double" || state === "template") {
      result += char;
      if (char === "\\" && next) {
        result += next;
        i += 1;
      } else if (
        (state === "single" && char === "'") ||
        (state === "double" && char === '"') ||
        (state === "template" && char === "`")
      ) {
        state = "code";
      }
    } else if (char === "/" && next === "/") {
      result += "  ";
      state = "line";
      i += 1;
    } else if (char === "/" && next === "*") {
      result += "  ";
      state = "block";
      i += 1;
    } else {
      result += char;
      if (char === "'") state = "single";
      else if (char === '"') state = "double";
      else if (char === "`") state = "template";
    }
  }
  return result;
};

const findCatalogImports = (source) => {
  const searchable = withoutComments(source);
  const violations = [];
  const patterns = [
    /(?:^|[;\n])\s*(?:import|export)\s+(?:type\s+)?(?:[^"'`;]*?\s+from\s+)?(["'])([^"']+)\1/gm,
    /\bimport\s*\(\s*(["'])([^"']+)\1\s*\)/g,
  ];
  for (const pattern of patterns) {
    for (const match of searchable.matchAll(pattern)) {
      const importPath = match[2];
      if (!importPath || !CATALOG_IMPORTS.has(importPath)) continue;
      const offset = match.index + match[0].lastIndexOf(importPath);
      const before = source.slice(0, offset);
      const lineStart = before.lastIndexOf("\n") + 1;
      violations.push({
        importPath,
        line: before.split("\n").length,
        column: offset - lineStart + 1,
        offset,
      });
    }
  }
  return violations.sort((a, b) => a.offset - b.offset).map(({ offset: _, ...violation }) => violation);
};

const findFrameworkImports = (source) => {
  const searchable = withoutComments(source);
  const violations = [];
  const patterns = [
    /(?:^|[;\n])\s*(?:import|export)\s+(?:type\s+)?(?:[^"'`;]*?\s+from\s+)?(["'])(@tamagui\/[^"']+|tamagui)\1/gm,
    /\bimport\s*\(\s*(["'])(@tamagui\/[^"']+|tamagui)\1\s*\)/g,
  ];
  for (const pattern of patterns) {
    for (const match of searchable.matchAll(pattern)) {
      const importPath = match[2];
      if (!importPath) continue;
      const offset = match.index + match[0].lastIndexOf(importPath);
      const before = source.slice(0, offset);
      const lineStart = before.lastIndexOf("\n") + 1;
      violations.push({
        importPath,
        line: before.split("\n").length,
        column: offset - lineStart + 1,
        offset,
      });
    }
  }
  return violations.sort((a, b) => a.offset - b.offset).map(({ offset: _, ...violation }) => violation);
};

const findSharedImplementationImports = (source) => {
  const searchable = withoutComments(source);
  const violations = [];
  const patterns = [
    /(?:^|[;\n])\s*(?:import|export)\s+(?:type\s+)?(?:[^"'`;]*?\s+from\s+)?(["'])(@loomarr\/(?:core|design-system|player|ui|ui-tv)\/(?:src|tests)\/[^"']+)\1/gm,
    /\bimport\s*\(\s*(["'])(@loomarr\/(?:core|design-system|player|ui|ui-tv)\/(?:src|tests)\/[^"']+)\1\s*\)/g,
  ];
  for (const pattern of patterns) {
    for (const match of searchable.matchAll(pattern)) {
      const importPath = match[2];
      if (!importPath) continue;
      const offset = match.index + match[0].lastIndexOf(importPath);
      const before = source.slice(0, offset);
      const lineStart = before.lastIndexOf("\n") + 1;
      violations.push({
        importPath,
        line: before.split("\n").length,
        column: offset - lineStart + 1,
        offset,
      });
    }
  }
  return violations.sort((a, b) => a.offset - b.offset).map(({ offset: _, ...violation }) => violation);
};

const PRODUCT_AREA = /(?:player|guide|surf)/i;
const PRODUCT_STATE_ROLE =
  /(?:controller|state|snapshot|selection|history|groups?|journey|surface|experience|rail|data|layout|model|navigation|reducer|store)/i;
const PRODUCT_BEHAVIOR_PREFIX = /^(?:build|create|derive|move|reduce|restore|use)/;

const findProductStateDeclarations = (source) => {
  const searchable = withoutComments(source);
  const violations = [];
  const declarations = [
    /(?:^|[;\n])\s*(?:export\s+)?(?:default\s+)?(?:class|enum|interface|type)\s+([A-Za-z_$][\w$]*)/gm,
    /(?:^|[;\n])\s*(?:export\s+)?(?:default\s+)?(?:const|function|let|var)\s+([A-Za-z_$][\w$]*)/gm,
  ];
  for (const [index, pattern] of declarations.entries()) {
    for (const match of searchable.matchAll(pattern)) {
      const name = match[1];
      if (!name || !PRODUCT_AREA.test(name) || !PRODUCT_STATE_ROLE.test(name)) continue;
      if (index === 1 && !PRODUCT_BEHAVIOR_PREFIX.test(name)) continue;
      const offset = match.index + match[0].lastIndexOf(name);
      const before = source.slice(0, offset);
      const lineStart = before.lastIndexOf("\n") + 1;
      violations.push({
        name,
        line: before.split("\n").length,
        column: offset - lineStart + 1,
        offset,
      });
    }
  }
  return violations.sort((a, b) => a.offset - b.offset).map(({ offset: _, ...violation }) => violation);
};

const checkImports = (roots = sourceRoots) =>
  roots.flatMap((root) =>
    sourceFiles(root).flatMap((file) =>
      findCatalogImports(readFileSync(file, "utf8")).map((violation) => ({ file, ...violation })),
    ),
  );

const checkFrameworkImports = (roots = sourceRoots) =>
  roots.flatMap((root) =>
    sourceFiles(root).flatMap((file) =>
      findFrameworkImports(readFileSync(file, "utf8")).map((violation) => ({ file, ...violation })),
    ),
  );

const checkSharedImplementationImports = (roots = sourceRoots) =>
  roots.flatMap((root) =>
    sourceFiles(root).flatMap((file) =>
      findSharedImplementationImports(readFileSync(file, "utf8")).map((violation) => ({
        file,
        ...violation,
      })),
    ),
  );

const checkProductStateDeclarations = (roots = sharedClientAppRoots) =>
  roots.flatMap((root) =>
    sourceFiles(root).flatMap((file) =>
      findProductStateDeclarations(readFileSync(file, "utf8")).map((violation) => ({ file, ...violation })),
    ),
  );

const isMain = process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1];
if (isMain) {
  const catalogViolations = checkImports();
  const frameworkViolations = checkFrameworkImports();
  const implementationViolations = checkSharedImplementationImports();
  const productStateViolations = checkProductStateDeclarations();
  if (catalogViolations.length > 0) {
    console.error("Production imports must name the nearest module instead of a catalog root:");
    for (const violation of catalogViolations) {
      console.error(
        `  ${violation.file}:${violation.line}:${violation.column} imports ${violation.importPath}`,
      );
    }
    console.error("Use a component, endpoint, model, or core-module subpath (frontend-design §4.4).");
    process.exitCode = 1;
  }
  if (frameworkViolations.length > 0) {
    console.error(
      "Tamagui is private to @loomarr/design-system; production modules import Loomarr interfaces:",
    );
    for (const violation of frameworkViolations) {
      console.error(
        `  ${violation.file}:${violation.line}:${violation.column} imports ${violation.importPath}`,
      );
    }
    process.exitCode = 1;
  }
  if (implementationViolations.length > 0) {
    console.error("Shared package implementations are private; import a root entry point:");
    for (const violation of implementationViolations) {
      console.error(
        `  ${violation.file}:${violation.line}:${violation.column} imports ${violation.importPath}`,
      );
    }
    process.exitCode = 1;
  }
  if (productStateViolations.length > 0) {
    console.error("Shared-client apps compose Player, Guide, and Surf state through package interfaces:");
    for (const violation of productStateViolations) {
      console.error(`  ${violation.file}:${violation.line}:${violation.column} declares ${violation.name}`);
    }
    process.exitCode = 1;
  }
  if (
    catalogViolations.length === 0 &&
    frameworkViolations.length === 0 &&
    implementationViolations.length === 0 &&
    productStateViolations.length === 0
  ) {
    console.log("import-boundaries: clean");
  }
}

export {
  checkFrameworkImports,
  checkImports,
  checkProductStateDeclarations,
  checkSharedImplementationImports,
  findCatalogImports,
  findFrameworkImports,
  findProductStateDeclarations,
  findSharedImplementationImports,
};
