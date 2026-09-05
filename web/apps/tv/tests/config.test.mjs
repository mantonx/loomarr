import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { access, readFile } from "node:fs/promises";
import test from "node:test";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

test("keeps the TV proof isolated from the shipping application", async () => {
  const config = JSON.parse(await readFile(new URL("../app.json", import.meta.url), "utf8"));
  const tvPlugin = config.expo.plugins.find(
    (plugin) => Array.isArray(plugin) && plugin[0] === "@react-native-tvos/config-tv",
  );
  assert.equal(tvPlugin?.[1].androidTVRequired, true);
  assert.ok(config.expo.plugins.includes("../../scripts/with-memory-safe-android-build.cjs"));
  assert.equal(config.expo.name, "Loomarr TV Prototype");
  assert.equal(config.expo.slug, "loomarr-tv-prototype");
  assert.match(config.expo.ios.bundleIdentifier, /\.prototype$/);
  assert.match(config.expo.android.package, /\.prototype$/);
});

test("resolves each explicit Shield release channel to the permanent Android identity", async () => {
  for (const channel of ["sideload", "play"]) {
    const { stdout } = await execFileAsync("pnpm", ["exec", "expo", "config", "--json"], {
      cwd: new URL("..", import.meta.url),
      env: {
        ...process.env,
        LOOMARR_SHIELD_RELEASE_CHANNEL: channel,
        LOOMARR_ANDROID_VERSION_CODE: "1020003",
        LOOMARR_ANDROID_VERSION_NAME: "0.1.2-beta.3",
      },
    });
    const config = JSON.parse(stdout);

    assert.equal(config.name, "Loomarr");
    assert.equal(config.slug, "loomarr-tv");
    assert.equal(config.version, "0.1.2-beta.3");
    assert.equal(config.android.package, "loomarr.media");
    assert.equal(config.android.versionCode, 1020003);
  }
});

test("embeds the artifact version as the production TV client identity", async () => {
  const buildSource = await readFile(
    new URL("../../../scripts/build-shield-sideload.sh", import.meta.url),
    "utf8",
  );

  assert.match(buildSource, /export EXPO_PUBLIC_LOOMARR_CLIENT_VERSION="\$\{VERSION_NAME\}"/);
});

test("keeps the permanent identity unreachable outside the sideload build", async () => {
  const environment = { ...process.env };
  delete environment.LOOMARR_SHIELD_RELEASE_CHANNEL;
  delete environment.LOOMARR_ANDROID_VERSION_CODE;
  delete environment.LOOMARR_ANDROID_VERSION_NAME;
  const { stdout } = await execFileAsync("pnpm", ["exec", "expo", "config", "--json"], {
    cwd: new URL("..", import.meta.url),
    env: environment,
  });
  const config = JSON.parse(stdout);

  assert.equal(config.name, "Loomarr TV Prototype");
  assert.equal(config.slug, "loomarr-tv-prototype");
  assert.match(config.android.package, /\.prototype$/);
});

test("fails closed when a Shield release has an invalid channel or version metadata", async () => {
  const runConfig = (versionCode, channel = "sideload") =>
    execFileAsync("pnpm", ["exec", "expo", "config", "--json"], {
      cwd: new URL("..", import.meta.url),
      env: {
        ...process.env,
        LOOMARR_SHIELD_RELEASE_CHANNEL: channel,
        LOOMARR_ANDROID_VERSION_CODE: versionCode,
        LOOMARR_ANDROID_VERSION_NAME: "0.1.2-beta.3",
      },
    });

  await assert.rejects(runConfig("", "production"), /channel must be sideload or play/);
  await assert.rejects(runConfig(""), /requires Loomarr version name and code/);
  await assert.rejects(runConfig("0"), /version code must be between/);
  await assert.rejects(runConfig("2100000000"), /version code must be between/);
});

test("declares the shared TV journey and native playback boundaries", async () => {
  const manifest = JSON.parse(await readFile(new URL("../package.json", import.meta.url), "utf8"));

  assert.equal(manifest.dependencies["@loomarr/player"], "workspace:*");
  assert.equal(manifest.dependencies["@loomarr/ui-tv"], "workspace:*");
  assert.equal(manifest.dependencies["expo-video"], manifest.dependencies.expo);
  assert.equal(manifest.dependencies["react-native"], "npm:react-native-tvos@0.86.2-0");
  assert.match(manifest.scripts.bundle, /--platform android/);
  assert.match(manifest.scripts.bundle, /--platform ios/);
});

test("starts from SecureStore without the abandoned Compose credential bridge", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  assert.match(appSource, /createPairingCredentialStore/);
  assert.doesNotMatch(appSource, /createMigratingPairingCredentialStore|legacyPairingSource/);
  await assert.rejects(access(new URL("../src/legacy-pairing.ts", import.meta.url)), {
    code: "ENOENT",
  });
  await assert.rejects(access(new URL("../modules/loomarr-legacy-pairing", import.meta.url)), {
    code: "ENOENT",
  });
});

test("composes the production TV root around shared dark pairing and paired API transport", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  assert.match(appSource, /<SafeAreaProvider>/);
  assert.match(appSource, /<LoomarrProvider insets=\{insets\} theme="dark">/);
  assert.match(appSource, /<PairingShell/);
  assert.match(appSource, /allowServerEntry/);
  assert.match(appSource, /discovery=\{discovery\}/);
  assert.doesNotMatch(appSource, /Set EXPO_PUBLIC_LOOMARR_URL/);
  assert.match(appSource, /createPairingTransport/);
  assert.match(appSource, /validatePairingCredential/);
  assert.match(appSource, /createAuthenticatedFetch\(credential, onRevoked\)/);
  assert.match(appSource, /<TvPairedRoot credential=\{credential\} session=\{session\} \/>/);
});

test("hands the native splash to the shared Loomarr launch identity", async () => {
  const config = JSON.parse(await readFile(new URL("../app.json", import.meta.url), "utf8"));
  const manifest = JSON.parse(await readFile(new URL("../package.json", import.meta.url), "utf8"));
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");
  const splashPlugin = config.expo.plugins.find(
    (plugin) => Array.isArray(plugin) && plugin[0] === "expo-splash-screen",
  );

  assert.equal(manifest.dependencies["expo-splash-screen"], manifest.dependencies.expo);
  assert.equal(splashPlugin?.[1].backgroundColor, "#0B0C0E");
  assert.equal(splashPlugin?.[1].dark.backgroundColor, "#0B0C0E");
  assert.match(appSource, /SplashScreen\.preventAutoHideAsync\(\)/);
  assert.match(appSource, /<View onLayout=\{hideNativeSplash\} style=\{\{ flex: 1 \}\}>/);
  assert.match(appSource, /SplashScreen\.hide\(\)/);
  assert.match(
    appSource,
    /<BrandLaunch density="tv" onFinished=\{\(\) => setLaunchAnimationFinished\(true\)\} \/>/,
  );
  assert.match(appSource, /const launchMinimumMs = 1_200/);
  assert.match(appSource, /return \(\) => clearTimeout\(timer\)/);
  assert.ok(
    appSource.indexOf("<PairingShell") < appSource.indexOf("<BrandLaunch"),
    "pairing must initialize beneath the launch identity instead of waiting for its animation",
  );
});

test("ships DNS-SD and UDP LAN discovery without restoring Kotlin", async () => {
  const manifest = JSON.parse(await readFile(new URL("../package.json", import.meta.url), "utf8"));
  const adapter = await readFile(
    new URL(
      "../../../packages/lan-discovery-native/src/lan-discovery-native/lan-discovery-native.ts",
      import.meta.url,
    ),
    "utf8",
  );
  const nativeModule = await readFile(
    new URL(
      "../../../packages/lan-discovery-native/android/src/main/java/media/loomarr/tv/discovery/LoomarrLanDiscoveryModule.java",
      import.meta.url,
    ),
    "utf8",
  );

  assert.equal(manifest.dependencies["@loomarr/lan-discovery-native"], "workspace:*");
  assert.match(adapter, /NativeModules\.LoomarrLanDiscovery/);
  assert.match(nativeModule, /NsdManager/);
  assert.match(nativeModule, /_loomarr\._tcp\./);
  assert.match(nativeModule, /DatagramSocket/);
  assert.match(nativeModule, /LOOMARR_DISCOVER\/1/);
  assert.match(nativeModule, /51029/);
  assert.match(nativeModule, /address\.indexOf\('%'\)/);
  assert.match(nativeModule, /address\.indexOf\(':'\) >= 0/);
  assert.match(nativeModule, /"\[" \+ address \+ "\]"/);
  assert.doesNotMatch(nativeModule, /kotlin/i);
});

test("publishes the container-safe discovery port from the Loomarr container", async () => {
  const compose = await readFile(new URL("../../../../docker/compose.yaml", import.meta.url), "utf8");

  assert.match(compose, /loomarr:[\s\S]*ports:\s*\["51029:51029\/udp"\]/);
});

test("keeps the Watching inactivity callback stable across playback renders", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  assert.match(appSource, /const dismissControls = useCallback\(\(\) => setControlsVisible\(false\), \[\]\)/);
  assert.match(appSource, /onDismissControls=\{dismissControls\}/);
  assert.doesNotMatch(appSource, /onDismissControls=\{\(\) => setControlsVisible\(false\)\}/);
});

test("keeps the native player and Watching mounted beneath Guide and Surf", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  const watching = appSource.indexOf("<WatchingSurface");
  const overlay = appSource.indexOf('{active === "watching" ? null : (');
  assert.ok(watching >= 0, "the paired root must render the shared Watching surface");
  assert.ok(overlay > watching, "transient destinations must render after the mounted Watching surface");
  assert.match(appSource, /chromeVisible=\{active === "watching"\}/);
  assert.match(appSource, /player=\{<NativePlayerView style=\{\{ flex: 1 \}\} transport=\{transport\} \/>\}/);
  assert.match(appSource, /position: "absolute"/);
});

test("drives every Watching state from the generated catalog and authoritative Guide", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  assert.match(appSource, /createChannelCatalogPort\(runtime\.request\)/);
  assert.match(appSource, /createGuideSourcePort\(runtime\.request\)/);
  assert.match(appSource, /await controller\.reconcile\(await catalog\.list\(request\.signal\)\)/);
  assert.match(appSource, /watchingScheduleFromGuide\(/);
  assert.match(appSource, /loading=\{catalogLoading\}/);
  assert.match(appSource, /loadError=\{loadError\}/);
  assert.match(appSource, /if \(loadError\) refreshSafely\(\)/);
  assert.match(appSource, /else void controller\.retry\(\)/);
});

test("mounts the bounded authoritative Guide and returns tune intent to Watching", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  assert.match(appSource, /active === "guide"/);
  assert.match(appSource, /<GuideJourney/);
  assert.match(appSource, /tvGuideRowWindow\(/);
  assert.match(appSource, /controller=\{guide\}/);
  assert.match(appSource, /void controller\.tuneChannel\(channelId\)/);
  assert.match(appSource, /setActive\("watching"\)/);
});

test("mounts authoritative grouped Surf data with previous-Channel history", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  assert.match(appSource, /<SurfJourney/);
  assert.match(appSource, /controller=\{guide\}/);
  assert.match(appSource, /currentChannelId=\{snapshot\.channel\?\.id\}/);
  assert.match(appSource, /playableChannelIds=\{snapshot\.catalog\.map/);
  assert.match(appSource, /recentChannelIds=\{snapshot\.recentChannelIds\}/);
  assert.match(appSource, /void controller\.tuneChannel\(channelId\)/);
});

test("routes the native remote through TV adapters and preserves platform-home Back", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  assert.match(appSource, /useTVEventHandler/);
  assert.match(appSource, /tvWatchingRemoteEventFromNative\(eventType, Date\.now\(\), eventKeyAction\)/);
  assert.match(appSource, /event && event\.key !== "select"/);
  assert.match(appSource, /reduceTvWatchingRemote\(remoteStateRef\.current, event\)/);
  assert.match(appSource, /onOpenGuide=\{\(\) => dispatchRemoteEvent\(\{ key: "select" \}\)\}/);
  assert.match(appSource, /controller\.tuneNumber\(intent\.digits\)/);
  assert.match(appSource, /numberEntry=\{tvNumberEntryPresentation\(remoteState, snapshot\.catalog\)\}/);
  assert.match(appSource, /if \(!destination\) return false/);
});

test("restarts Watching inactivity only for handled remote and tune activity", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  assert.match(appSource, /controlsActivityKey=\{controlsActivityKey\}/);
  assert.match(appSource, /if \(result\.handled\) showControlsForActivity\(\);/);

  const tuneHandlers = appSource.match(
    /onTune=\{\(channelId\) => \{\s*void controller\.tuneChannel\(channelId\);\s*showControlsForActivity\(\);\s*setActive\("watching"\);\s*\}\}/g,
  );
  assert.equal(tuneHandlers?.length, 2, "Guide and Surf tune paths must restart Watching inactivity");
});

test("restores Guide and Surf focus by identity through the TV registries", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  assert.match(appSource, /createTvGuideFocusRegistry/);
  assert.match(appSource, /createTvSurfFocusRegistry/);
  assert.match(appSource, /focusRegistry=\{guideFocusRegistry\}/);
  assert.match(appSource, /focusRegistry=\{surfFocusRegistry\}/);
  assert.match(appSource, /restoreSelection=\{restoreTvSurfSelection\}/);
});

test("wires authenticated artwork, channel invalidation, identity, versions, and disconnect", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  assert.match(appSource, /createNativeEventStreamFactory/);
  assert.match(appSource, /openEventStream\(/);
  assert.match(appSource, /Authorization: `Bearer \$\{runtime\.credential\.token\}`/);
  assert.match(appSource, /onChannel:/);
  assert.match(appSource, /refreshSafely\(\)/);
  assert.match(appSource, /void guide\.refresh/);
  assert.match(appSource, /<PairedNativeImage/);
  assert.match(appSource, /onDisconnect=\{\(\) => runtime\.session\.disconnect\(\)\}/);
  assert.match(appSource, /appConfig\.expo\.version/);
  assert.match(appSource, /serverVersion=\{serverVersion\}/);
});

test("releases playback and invalidation resources in the background before authoritative retune", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  assert.match(appSource, /createNativePlayerLifecycle\(\{ controller, refresh, transport \}\)/);
  assert.match(appSource, /AppState\.addEventListener\("change", \(state\) =>/);
  assert.match(appSource, /refreshRequest\.current\?\.abort\(\);\s+lifecycle\.enterBackground\(\)/);
  assert.match(appSource, /void lifecycle\.enterForeground\(\)\.catch/);
  assert.match(appSource, /throw error/);
  assert.match(appSource, /if \(closeStream\) return/);
  assert.match(appSource, /else closeActiveStream\(\)/);
  assert.match(appSource, /subscription\.remove\(\);\s+closeActiveStream\(\)/);
});

test("bounds LAN discovery to the foreground TV connection screen", async () => {
  const appSource = await readFile(new URL("../src/app.tsx", import.meta.url), "utf8");

  assert.match(appSource, /useState\(AppState\.currentState === "active"\)/);
  assert.match(appSource, /setAppForeground\(state === "active"\)/);
  assert.match(appSource, /discoveryForeground=\{appForeground\}/);
});
