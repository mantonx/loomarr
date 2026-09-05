const assert = require("node:assert/strict");
const test = require("node:test");
const { permitConfiguredLoomarrHttp } = require("./with-loomarr-android-network.cjs");

test("permits the user-configured plain-HTTP Loomarr origin", () => {
  const manifest = { manifest: { application: [{ $: {} }] } };

  assert.equal(
    permitConfiguredLoomarrHttp(manifest).manifest.application[0].$[
      "android:usesCleartextTraffic"
    ],
    "true",
  );
});

test("fails closed when Expo's manifest shape changes", () => {
  assert.throws(
    () => permitConfiguredLoomarrHttp({ manifest: {} }),
    /Android application manifest/,
  );
});
