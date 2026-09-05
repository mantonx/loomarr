const shieldReleaseConfig = (config, environment = process.env) => {
  const channel = environment.LOOMARR_SHIELD_RELEASE_CHANNEL;
  if (!channel) return config;
  if (channel !== "sideload" && channel !== "play") {
    throw new Error("Shield release channel must be sideload or play");
  }

  const version = environment.LOOMARR_ANDROID_VERSION_NAME;
  const rawVersionCode = environment.LOOMARR_ANDROID_VERSION_CODE;
  const versionCode = Number(rawVersionCode);
  if (!version || !rawVersionCode || !/^\d+$/.test(rawVersionCode)) {
    throw new Error("Shield release requires Loomarr version name and code");
  }
  if (!Number.isSafeInteger(versionCode) || versionCode < 1 || versionCode >= 2_100_000_000) {
    throw new Error("Shield release version code must be between 1 and 2099999999");
  }

  return {
    ...config,
    name: "Loomarr",
    slug: "loomarr-tv",
    version,
    android: {
      ...config.android,
      package: "loomarr.media",
      versionCode,
    },
  };
};

module.exports = ({ config }) => shieldReleaseConfig(config);
module.exports.shieldReleaseConfig = shieldReleaseConfig;
