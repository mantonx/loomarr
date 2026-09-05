const assert = require("node:assert/strict");
const { test } = require("node:test");
const { addWorkspaceBundleInputs } = require("./with-workspace-bundle-inputs.cjs");

test("adds shared workspace sources to every Android bundle task", () => {
  const generated = addWorkspaceBundleInputs("plugins {\n}\n\nreact {\n}\n");

  assert.match(generated, /tasks\.withType\(com\.facebook\.react\.tasks\.BundleHermesCTask\)/);
  assert.match(generated, /rootDir\}\/\.\.\/\.\.\/\.\.\/packages/);
  assert.match(generated, /include "\*\*\/\*\.js".*"\*\*\/\*\.tsx".*"\*\*\/\*\.json"/);
  assert.match(generated, /pnpm-lock\.yaml/);
  assert.match(generated, /metro\.config\.cjs/);
  assert.equal(addWorkspaceBundleInputs(generated), generated);
});

test("fails closed when Expo's generated app build shape changes", () => {
  assert.throws(
    () => addWorkspaceBundleInputs("plugins {\n}\n"),
    /Could not find the React Android block/,
  );
});
