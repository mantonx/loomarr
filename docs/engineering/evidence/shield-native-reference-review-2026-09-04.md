# Shield native reference review — 2026-09-04

## Scope and method

This review covers GitHub issue #970 item 34 and the parity remediation tracked by #1008, based on
`d9ecaf08dce0b2f2ffdda70bda83ba88d877c325`. The embedded React Native Storybook ran as an Android
TV application on Android 36 Google TV emulator profiles with native framebuffers at:

- 1920×1080, 320 dpi (`1080p`)
- 3840×2160, 640 dpi (`4k`)

Both profiles therefore expose the same 960×540 dp composition. The capture command is
`pnpm --filter @loomarr/tv references:capture`; it detects the native framebuffer, selects each
Storybook state through a localhost-only controller bridged with `adb reverse`, and rejects logical
`wm size` overrides. The checked-in artifact test proves all 30 PNGs exist at their claimed physical
dimensions.

## Captured replacement contract

| Surface | Ready/focused | Loading | Empty | Error |
| --- | --- | --- | --- | --- |
| Pairing | [1080p](../../../web/apps/tv/tests/native-references/1080p/pairing.png) · [4K](../../../web/apps/tv/tests/native-references/4k/pairing.png) | [1080p](../../../web/apps/tv/tests/native-references/1080p/pairing-loading.png) · [4K](../../../web/apps/tv/tests/native-references/4k/pairing-loading.png) | Not applicable | [1080p](../../../web/apps/tv/tests/native-references/1080p/pairing-error.png) · [4K](../../../web/apps/tv/tests/native-references/4k/pairing-error.png) |
| Watching | [1080p](../../../web/apps/tv/tests/native-references/1080p/watching.png) · [4K](../../../web/apps/tv/tests/native-references/4k/watching.png) | [1080p](../../../web/apps/tv/tests/native-references/1080p/watching-loading.png) · [4K](../../../web/apps/tv/tests/native-references/4k/watching-loading.png) | [1080p](../../../web/apps/tv/tests/native-references/1080p/watching-empty.png) · [4K](../../../web/apps/tv/tests/native-references/4k/watching-empty.png) | [1080p](../../../web/apps/tv/tests/native-references/1080p/watching-error.png) · [4K](../../../web/apps/tv/tests/native-references/4k/watching-error.png) |
| Surf | [1080p](../../../web/apps/tv/tests/native-references/1080p/surf-focused.png) · [4K](../../../web/apps/tv/tests/native-references/4k/surf-focused.png) | [1080p](../../../web/apps/tv/tests/native-references/1080p/surf-loading.png) · [4K](../../../web/apps/tv/tests/native-references/4k/surf-loading.png) | [1080p](../../../web/apps/tv/tests/native-references/1080p/surf-empty.png) · [4K](../../../web/apps/tv/tests/native-references/4k/surf-empty.png) | [1080p](../../../web/apps/tv/tests/native-references/1080p/surf-error.png) · [4K](../../../web/apps/tv/tests/native-references/4k/surf-error.png) |
| Guide | [1080p](../../../web/apps/tv/tests/native-references/1080p/guide-focused.png) · [4K](../../../web/apps/tv/tests/native-references/4k/guide-focused.png) | [1080p](../../../web/apps/tv/tests/native-references/1080p/guide-loading.png) · [4K](../../../web/apps/tv/tests/native-references/4k/guide-loading.png) | [1080p](../../../web/apps/tv/tests/native-references/1080p/guide-empty.png) · [4K](../../../web/apps/tv/tests/native-references/4k/guide-empty.png) | [1080p](../../../web/apps/tv/tests/native-references/1080p/guide-error.png) · [4K](../../../web/apps/tv/tests/native-references/4k/guide-error.png) |

## Kotlin side-by-side findings

The committed Kotlin references are:

- Pairing at 1080p
- Watching at 1080p and 4K density
- Surf at 1080p and 4K density
- Guide at 1080p and 4K density

P5c retired these local Roborazzi files after the React Native native-reference and physical-Shield
gates replaced them. This note preserves the reviewed baseline names without leaving dead links to
deleted artifacts.

The review found that the replacement contract is complete, resolution-independent, and acceptable
as 1:1 presentation parity with the Kotlin references. Fixture content and version identities were
first aligned so the comparison exercises the same product state. The remediation then reproduced
the Kotlin surface geometry and information placement through shared design-system and UI roles:

- **Pairing:** the replacement reproduces the bare 320×24 dp brand strip, 760 dp card, equal columns,
  210 dp divider, 150 dp QR with the protected Loomarr centre mark, single-line server URL, pairing
  code, expiry, and refresh action. Native TV focus remains visible on the preferred refresh action.
- **Watching:** number entry and Channel identity occupy the same top corners. The full-width bottom
  bar reproduces the Kotlin title, episode facts, live-edge time, progress, next-programme line, and
  remote hints in the same order and at the same vertical boundaries.
- **Surf:** the replacement reproduces the 420 dp translucent rail, Kotlin Channel grouping and
  initial max-scroll viewport, selected programme identity, progress, row position, version
  identity, and tune/cancel hint.
- **Guide:** the replacement reproduces the 298 dp Channel rail, 12 dp position rail, 36 dp ruler,
  48 dp rows, 124 dp detail region, hourly labels, current-airing tint, selected Channel emphasis,
  programme focus, artwork fallback, and current-time rail.

No product-level geometry or information-placement deviation remains in the reviewed states. The
remaining pixel differences are renderer-level: Compose and React Native use slightly different
glyph rasterization and metrics, which can move an ellipsis within the same bounded label; the QR
encoders may select a different valid module mask for the same pairing payload; and native border
edges have minor subpixel antialiasing differences. These do not alter the visible contract or
interaction behavior.

Maintainer screenshot review after the initial merge caught that the React Native TV composition
explicitly disabled the QR centre mark. The earlier statement that an unbranded QR was parity was
incorrect. Issue #1008 was reopened, a component regression test was added, and fresh native 1080p
and 4K captures now retain the protected Loomarr centre mark. Only the two ready-pairing references
changed; unrelated animation-frame drift from recapture was discarded.

React Native 1080p and 4K captures do preserve the same logical composition; the only observed
cross-density difference is the expected one-second expiry countdown movement. All loading, empty,
error, and focused variants render the intended state without Storybook chrome or emulator scaling.

## Browser TV-contract corroboration

The same shared TV roles are rendered by the browser Storybook contract. The parity remediation
intentionally changed 21 desktop/mobile baselines, all belonging to TV-density stories; no pointer
or touch baseline changed. Each changed cohort was reviewed against the corresponding merge-group
artifact before regeneration. The review also exposed two non-visual regressions that were fixed
instead of being hidden by baseline updates: custom Guide filter buttons now retain the shared
Action contract's `aria-pressed` state, and the compact light-theme artwork fallback now uses a
contrast-safe semantic tone.

The sanctioned pinned-Docker update completed with 1,200 passing tests and two intentional skips.
A fresh immutable `make fe-visual` run then passed the same 1,200 tests with the same two skips,
covering screenshot equality, serious/critical axe violations, and the TV Guide D-pad contract.

## Acceptance decision

Item 34's capture and review evidence is complete, and the #1008 visual-parity remediation is
**accepted by this review**. The checked-in 1080p and 4K references are the replacement visual
contract for emulator states. Physical Shield behavior, installation, remote traversal, and real
playback remain explicitly untested here and are owned by issue #970 item 36; Kotlin deletion remains
gated on that maintainer acceptance.
