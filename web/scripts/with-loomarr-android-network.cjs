const { withAndroidManifest } = require("@expo/config-plugins");

function permitConfiguredLoomarrHttp(manifest) {
  const application = manifest.manifest?.application?.[0];
  if (!application?.$) {
    throw new Error("Could not find the Android application manifest while applying Loomarr networking");
  }

  application.$["android:usesCleartextTraffic"] = "true";
  return manifest;
}

function withLoomarrAndroidNetwork(config) {
  return withAndroidManifest(config, (manifestConfig) => {
    manifestConfig.modResults = permitConfiguredLoomarrHttp(manifestConfig.modResults);
    return manifestConfig;
  });
}

module.exports = withLoomarrAndroidNetwork;
module.exports.permitConfiguredLoomarrHttp = permitConfiguredLoomarrHttp;
