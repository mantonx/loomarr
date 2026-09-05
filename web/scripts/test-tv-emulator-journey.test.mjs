import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { describe, it } from "node:test";

const journey = readFileSync(new URL("test-tv-emulator-journey.sh", import.meta.url), "utf8");
const server = readFileSync(new URL("tv-emulator-fixture-server.mjs", import.meta.url), "utf8");

describe("TV emulator journey", () => {
  it("builds the production TV root for the connected emulator and starts with fresh storage", () => {
    assert.match(journey, /EXPO_PUBLIC_LOOMARR_URL=/);
    assert.match(journey, /getprop ro\.product\.cpu\.abi/);
    assert.match(journey, /arm64-v8a\|x86_64/);
    assert.match(journey, /LOOMARR_ANDROID_ARCHITECTURES="\$\{emulator_architecture\}"/);
    assert.match(journey, /build-android-client\.sh" tv/);
    assert.match(
      readFileSync(new URL("build-android-client.sh", import.meta.url), "utf8"),
      /expo export:embed[\s\S]*--reset-cache/,
    );
    assert.match(journey, /install -r/);
    assert.match(journey, /shell pm clear/);
    assert.match(journey, /shell am start -W -n "\$\{PACKAGE_ID\}\/\.MainActivity"/);
  });

  it("drives every item 33 remote and lifecycle checkpoint", () => {
    for (const checkpoint of [
      "fresh pairing code",
      "Watching home",
      "Guide",
      "Watching after Back",
      "Surf",
      "Surf tune",
      "number entry",
      "number tune",
      "Watching after number tune",
      "background event-stream release",
      "foreground catalog refresh",
      "foreground retune",
      "disconnect confirmation",
      "device revocation",
    ]) {
      assert.match(journey, new RegExp(checkpoint));
    }
    assert.match(journey, /key KEYCODE_DPAD_CENTER/);
    assert.match(journey, /key KEYCODE_BACK/);
    assert.match(journey, /key KEYCODE_DPAD_LEFT/);
    assert.equal(journey.match(/key KEYCODE_7/g)?.length, 2);
    assert.match(server, /Classic Animation", 77/);
    assert.match(journey, /key KEYCODE_HOME/);
  });

  it("asserts rendered UI and authenticated server effects rather than timing alone", () => {
    assert.match(journey, /uiautomator dump/);
    assert.match(journey, /wait_for_ui/);
    assert.match(journey, /wait_for_state/);
    assert.match(server, /request\.headers\.authorization === `Bearer \$\{deviceToken\}`/);
    assert.match(server, /state\.playUrlChannels\.push\(channelId\)/);
    assert.match(server, /state\.eventDisconnects \+= 1/);
    assert.match(server, /state\.revocations \+= 1/);
  });
});
