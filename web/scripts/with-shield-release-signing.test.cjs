const assert = require("node:assert/strict");
const test = require("node:test");
const { addShieldReleaseSigning } = require("./with-shield-release-signing.cjs");

const generatedBuild = `android {
    signingConfigs {
        debug {
            storeFile file('debug.keystore')
            storePassword 'android'
            keyAlias 'androiddebugkey'
            keyPassword 'android'
        }
    }
    buildTypes {
        debug {
            signingConfig signingConfigs.debug
        }
        release {
            signingConfig signingConfigs.debug
        }
    }
}
`;

test("signs only an explicit permanent-identity Shield release", () => {
  const generated = addShieldReleaseSigning(generatedBuild);

  assert.match(generated, /LOOMARR_SHIELD_RELEASE_CHANNEL/);
  assert.match(generated, /\["sideload", "play"\]\.contains/);
  assert.match(generated, /LOOMARR_ANDROID_KEYSTORE_PATH/);
  assert.match(generated, /LOOMARR_ANDROID_KEYSTORE_PASSWORD/);
  assert.match(generated, /LOOMARR_ANDROID_KEY_ALIAS/);
  assert.match(generated, /LOOMARR_ANDROID_KEY_PASSWORD/);
  assert.match(generated, /throw new GradleException/);
  assert.match(
    generated,
    /signingConfig loomarrShieldRelease \? signingConfigs\.release : signingConfigs\.debug/,
  );
  assert.equal(addShieldReleaseSigning(generated), generated);
});

test("fails closed when Expo's generated signing shape changes", () => {
  assert.throws(
    () => addShieldReleaseSigning("android {\n}\n"),
    /generated Android signing configuration/,
  );
});
