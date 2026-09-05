# Android TV beta releases

Loomarr's accepted React Native TV client owns the permanent Play identity `loomarr.media`.
Prototype development builds use `media.loomarr.tv.prototype` and cannot replace a Play build.

The full rationale and current Google requirements are in
[`FINDINGS-android-tv-beta-distribution-2026-08-22.md`](../engineering/FINDINGS-android-tv-beta-distribution-2026-08-22.md).

## Release identity

[`web/apps/tv/android-release.json`](../../web/apps/tv/android-release.json) owns the next version
name. CI and the protected workflow independently derive and verify its Play code:

```text
major * 100000000 + minor * 1000000 + patch * 10000 + channel
```

`beta.N` uses 1–7999, `rc.N` uses 8001–8999, and a stable release uses 9999. For example,
`0.1.0-beta.1` is code `1000001`. The mapping is monotonic across beta, release candidate, stable,
and the next patch. Unsupported names fail before signing.

## One-time Play Console bootstrap

An account owner must do these steps; the Publishing API cannot create an application or accept
legal declarations.

1. Create **Loomarr** in Play Console and confirm the unclaimed package `loomarr.media` by uploading
   the first AAB. Treat the package as permanent.
2. Enrol in Play App Signing and let Play generate the app-signing key. Record both the Play
   app-signing SHA-256 fingerprint and Loomarr upload-key SHA-256 fingerprint in the release record.
3. Add Android TV as a form factor and complete App access, Data safety, content rating, target
   audience, ads, privacy policy, and the other required Console declarations truthfully.
4. Supply the 512 × 512 Play icon, 1024 × 500 feature graphic, separate 1280 × 720 TV banner, at
   least one real TV screenshot, and a description that names Android TV. Four real 1920 × 1080
   screenshots are the listing target.
5. Create the Internal tester list, add the Shield's Google account, feedback address, and review
   access instructions for a working Loomarr server.

The upload keystore is an upload credential, not the Play app-signing key. Back it up outside the
repository. Losing it requires an upload-key reset; losing the Play account is a different and more
serious incident.

## Protected GitHub environment

Create the `android-beta` environment with required reviewers and no unprotected deployment
branches. Add these environment secrets:

| Name | Value |
| --- | --- |
| `ANDROID_UPLOAD_KEYSTORE_BASE64` | Base64 of the upload PKCS12/JKS bytes |
| `ANDROID_UPLOAD_KEYSTORE_PASSWORD` | Upload keystore password |
| `ANDROID_UPLOAD_KEY_ALIAS` | Upload key alias |
| `ANDROID_UPLOAD_KEY_PASSWORD` | Upload private-key password |
| `GOOGLE_PLAY_SERVICE_ACCOUNT_JSON_BASE64` | Added only after manual bootstrap; base64 service-account JSON |

Add environment variable `ANDROID_UPLOAD_CERT_SHA256` with the upload certificate's SHA-256
fingerprint. The workflow compares it with both the keystore and signed AAB. A wrong key fails before
publication.

The service account is invited to Play Console only after the first manual AAB exists. Restrict it
to `loomarr.media` and testing-track release rights; do not grant account administration or
Production access. Enable the Google Play Developer API in its Cloud project.

## Artifact production and first signed AAB

Changing the release identity or any Android input makes the merge queue compile one unsigned AAB.
CI verifies and retains it with its exact commit, workflow run, immutable artifact id, and SHA-256
digest. Before the first automated publication, run **Android TV beta** from that exact `main` with:

- **Publish to Play** disabled.

The protected workflow accepts only the unique, unexpired Android artifact produced for its exact
current-main commit. It downloads by immutable artifact id, verifies source/run/digest and unsigned
state, signs with the protected upload key, proves all non-signature entries stayed byte-identical,
then repeats package, version, ABI, 16 KiB, launcher, banner, JavaScript, and certificate checks. It
retains the signed AAB and evidence for 30 days. It does not install Node, run Expo, Gradle, CMake, or
any Apple build. Download that first signed AAB and upload it manually only while enrolling in Play
App Signing; subsequent Internal releases use the publisher.

Do not use Internal App Sharing for acceptance; it re-signs artifacts with a disposable identity and
does not prove the beta update path.

## Automated testing-track release

After the manual bootstrap and service-account setup, dispatch the workflow with **Publish to Play**
enabled. The publisher opens one Play edit, uploads the exact signed and digest-verified AAB, replaces the
Internal track release, and commits the edit. Global concurrency prevents two edits from racing.
The workflow exposes no Closed, Open, or Production choice.

Before dispatch, the exact CI artifact must pass the clean-install journey on a Loomarr-owned Android
TV emulator on the maintainer's machine. The journey starts with empty app data and no embedded
server URL, observes the launcher artwork and process-dead launch animation, uses automatic LAN
discovery (plus the manual fallback in a separate case), completes pairing, restarts into Watching,
and proves the playbar hides after five seconds of remote inactivity. Agents never use the physical
Shield for this release gate.

Download the exact merge-queue artifact, then run the compile-free acceptance harness with one
explicit emulator serial:

```bash
LOOMARR_TV_EMULATOR_SERIAL=emulator-5554 \
  ./scripts/test-android-release-emulator.sh \
  .artifacts/android-ci/loomarr-tv-<version>-<code>-unsigned.aab \
  .artifacts/android-ci/loomarr-tv-<version>-<code>-unsigned.json
```

The harness refuses physical-device serials, verifies the producer digest and bundle identity,
creates device-specific APK splits with a disposable local key, and installs only those splits. It
then drives automatic DNS-SD pairing and a separate manual-address pairing against an isolated
fixture, checks the five-second playbar deadline, and retains screenshots, a paired cold-launch
recording, and digest-bound acceptance evidence under `.artifacts/emulator-proof/`. It downloads
the pinned official bundletool only when `LOOMARR_BUNDLETOOL_JAR` is not supplied; no application
source is compiled.

If publication fails, inspect the Play edit error and dispatch a new version only after determining
whether Play consumed the code. Never lower or reuse a code. Halt a bad rollout in Play Console;
Android cannot downgrade an installed app, so rollback is a new fixed version with a higher code.

## Shield acceptance

The physical Shield acceptance journey already passed with the React Native `loomarr.media`
sideload. That build used an ephemeral signing key, so Android may require it to be uninstalled
before the separately signed Play build can be installed. Losing that local pairing is accepted:

1. Opt the Shield account into the Internal test. If Android rejects the Play install because the
   signatures differ, uninstall the accepted sideload, then install Loomarr from its Play link.
2. Confirm Settings reports package `loomarr.media`, the expected version code/name, and the Play
   signing certificate. Pair it afresh to the production Loomarr server when needed.
3. Exercise playback, Guide, Surf, D-pad focus, Back-to-home, process restart, device restart, and
   both 1080p and 4K output. Record the installed version and evidence.
4. Revoke obsolete paired-device entries if the server still lists them.

An in-place second-version update proof and wider Play tracks are later release work, not acceptance
criteria for this Internal beta.
