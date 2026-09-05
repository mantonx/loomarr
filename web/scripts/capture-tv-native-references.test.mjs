import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const script = await readFile(new URL("./capture-tv-native-references.sh", import.meta.url), "utf8");
const tvUi = await readFile(new URL("../.rnstorybook/tv-ui.tsx", import.meta.url), "utf8");
const states = [
  "pairing",
  "pairing-loading",
  "pairing-error",
  "watching",
  "watching-loading",
  "watching-empty",
  "watching-error",
  "surf-focused",
  "surf-loading",
  "surf-empty",
  "surf-error",
  "guide-focused",
  "guide-loading",
  "guide-empty",
  "guide-error",
];

test("captures every Shield parity state at both supported TV layouts", () => {
  for (const state of states) {
    assert.match(script, new RegExp(`"${state}\\|`));
  }
  assert.match(script, /1920x1080:320/);
  assert.match(script, /capture_layout_name="1080p"/);
  assert.match(script, /3840x2160:640/);
  assert.match(script, /capture_layout_name="4k"/);
  assert.match(script, /readUInt32BE\(16\).*readUInt32BE\(20\)/s);
  assert.doesNotMatch(script, /wm size "\$\{size\}"/);
  assert.doesNotMatch(script, /wm density "\$\{density\}"/);
});

test("selects a known native story through a full-screen local capture controller", () => {
  assert.match(tvUi, /EXPO_PUBLIC_LOOMARR_STORYBOOK_CAPTURE_URL/);
  assert.match(tvUi, /retry = setTimeout\(\(\) => void readCapture\(\), 100\)/);
  assert.doesNotMatch(tvUi, /setInterval/);
  assert.match(tvUi, /storyHash\[captureStoryId\]\?\.type === "story"/);
  assert.match(tvUi, /Native reference \$\{captureStoryId\}/);
  assert.match(tvUi, /LOOMARR_NATIVE_REFERENCE_SELECTED:\$\{storyId\}/);
  assert.match(script, /adb -s "\$\{emulator_serial\}" reverse "tcp:\$\{CAPTURE_PORT\}"/);
  assert.match(script, /curl --fail --silent --request PUT --data-binary "\$\{story_id\}"/);
  assert.match(script, /logcat -d \| grep -Fq/);
  assert.doesNotMatch(script, /uiautomator/);
});

test("cleans local controller state even when capture fails", () => {
  assert.match(script, /trap cleanup EXIT INT TERM/);
  assert.match(script, /reverse --remove/);
  assert.match(script, /kill "\$\{server_pid\}"/);
});

test("checks in every native reference at its real framebuffer dimensions", async () => {
  for (const [layout, width, height] of [
    ["1080p", 1920, 1080],
    ["4k", 3840, 2160],
  ]) {
    for (const state of states) {
      const image = await readFile(
        new URL(`../apps/tv/tests/native-references/${layout}/${state}.png`, import.meta.url),
      );
      assert.equal(image.subarray(1, 4).toString(), "PNG", `${layout}/${state} is a PNG`);
      assert.equal(image.readUInt32BE(16), width, `${layout}/${state} width`);
      assert.equal(image.readUInt32BE(20), height, `${layout}/${state} height`);
    }
  }
});
