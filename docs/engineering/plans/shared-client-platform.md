# Shared client platform migration

**Status:** P5a accepted; P5b React Native Play beta and Web parity migration active in #970
**Date:** 2026-09-04
**Decision owner:** maintainer  
**Companion contract:** [`docs/frontend-design.md`](../../frontend-design.md)
**Current-state inventory:** [`docs/engineering/client-platform-inventory.md`](../client-platform-inventory.md)
**Completeness ledger:** [`docs/engineering/client-design-system-coverage.md`](../client-design-system-coverage.md)

## Outcome

Loomarr will consolidate the separately implemented Tailwind/shadcn Web and Compose TV presentations
behind a Loomarr-owned client platform built on **Tamagui Core**.
The target serves the embedded web app, iOS and Android touch clients, and Android TV and Apple TV
clients without pretending that pointer, touch, and remote control are the same interaction.

The 2026-09-03 maintainer decision in
[#970](https://github.com/loomarr/loomarr/issues/970) authorizes the complete Shield and Web
migration. It does **not** authorize a redesign: Web preserves the current approved Web presentation
and behavior, while Shield preserves the current approved Kotlin/Compose presentation and behavior.
The two clients are not made visually identical in this program. Refinements begin only after both
legacy implementations have been retired.

Shield supports both a signed sideload and a Google Play internal beta during this migration. A
clean uninstall, reinstall, and fresh pairing are accepted for the sideload cutover. The Play build
uses Google-managed Play App Signing plus a separately protected, durable upload key; it does not
need update continuity with the already accepted ephemeral-key sideload. Installed credential
migration, public/open Play rollout, cross-channel update continuity, and a long-lived Kotlin
rollback renderer remain outside this program.

## Why the current approach is being replaced

The present contract deliberately shares tokens and logic but forbids shared component
implementations. That decision produced three costs that now outweigh its benefits:

1. web and TV encode the same visual rules independently, so the visual language drifts;
2. the token layer describes implementation colors and Tailwind mechanics more often than product
   meaning; and
3. a new mobile client would add a third implementation before the product has one design system
   worth reproducing.

The current palette, chroma-bar identity, Geist typography, primitives, and screenshots are parity
evidence. Existing behavior, accessibility, authorization, pairing, and playout guarantees remain
requirements. Visual or interaction refinements discovered during migration are recorded for later;
they are not bundled into the implementation replacement.

## Non-negotiable product invariants

- `docs/design.md` continues to own behavior. A client may expose an existing capability differently
  but may not weaken roles, approvals, grounding, CSRF/session behavior, paired-device revocation,
  or fail-closed authorization.
- Wire types and query functions continue to come from committed OpenAPI and the generated
  `@loomarr/api` package. Shared UI does not invent DTOs.
- Web remains an offline-capable static build embedded in the Go binary. Native clients connect to
  the operator-configured public Loomarr URL and never bundle a household admin token.
- The physical Shield remains the acceptance device. An emulator or screenshot is useful evidence,
  not a substitute for D-pad, playback, background/foreground, and clean-reinstall testing on that
  hardware.
- TV navigation must work with five-way D-pad, OK, Back, and number keys. Menu may be a shortcut but
  never the only path to a function. Back ultimately returns to the platform home screen.
- Layout is authored in logical units. The same composition must fill 1920x1080 and 3840x2160
  output without hard-coded pixel coordinates, clipping, or a smaller centered canvas.
- Every migrated surface starts with a parity inventory against its shipping client and committed
  references. The migration may not change hierarchy or treatment or silently drop content, actions,
  states, or recovery paths; any exception requires an explicit maintainer decision.
- Loomarr starts in its dark presentation on every fresh web or native client. Light and
  system-following presentations remain supported user choices, but the host operating system does
  not silently replace the product default before the person chooses one.
- Every migration PR leaves the currently selected production client shippable. The Compose source
  for `loomarr.media` is retired after a React Native build under the same identity passes the
  clean-sideload and fresh-pairing Shield acceptance journey and a React Native TV App Bundle passes
  the Play beta release gate.

## Target modules and seams

The existing `web/` pnpm workspace remains the workspace root during migration so the Go embed and
current frontend harness do not move at the same time as the UI architecture. Renaming it is a
separate post-parity decision.

```text
web/
  apps/
    web/                 # Vite delivery adapter; current router may remain
    mobile/              # Expo Router; iOS and Android touch targets
    tv/                  # Expo + react-native-tvos; Android TV and Apple TV targets
  packages/
    api/                 # generated wire models and query functions
    core/                # platform-neutral domain logic and validation
    fixtures/            # deterministic domain scenarios
    design-system/       # Tamagui config, semantic tokens, fonts, icons, primitives
    ui/                  # shared viewer and product modules
    ui-tv/               # focus, remote, overscan, guide-virtualization adapters
    player/              # shared playback state interface plus platform adapters
```

Each package remains a deep module under [`web/packages/README.md`](../../../web/packages/README.md):
callers use root entry points, implementation stays private, tests exercise the same interface, and
the dependency graph remains acyclic.

### What is shared

| Shared implementation | Adapter-owned implementation |
| --- | --- |
| semantic tokens, themes, type roles, spacing, radii, motion contracts | font loading and platform font metrics |
| artwork, channel identity, metadata, badges, actions, loading/error states | web DOM semantics where React Native semantics are insufficient |
| Guide data shaping, time-axis math, selection intent, now/next state | router history, deep links, portals, safe areas, overscan |
| player state, overlay state machine, tune intent, previous-channel history | hls.js on web; native player transport on iOS/Android/TV |
| component states, deterministic fixtures, story contracts | pointer/keyboard, touch/gesture, and remote/focus behavior |

One source file is not a success when it is littered with platform branches. Shared modules expose a
small product interface; platform adapters satisfy the seams that genuinely vary. A second adapter
justifies a seam. Hypothetical abstractions do not.

## Tamagui decision

Use **`@tamagui/core`**, not the predesigned Tamagui UI kit, as the candidate styling and primitive
implementation. Loomarr owns its semantic interface and visual language. Application code imports
Loomarr modules rather than Tamagui directly; only `packages/design-system` and narrowly documented
adapter code may import Tamagui.

The optimizing compiler is **not** enabled in the scaffold PR. All features work at runtime, and
adding the compiler before representative code exists would add build complexity without measurable
evidence. Benchmark the uncompiled slice first, then test the compiler as an isolated optimization;
retain it only when it improves the production artifacts without changing behavior or source files.

NativeWind, Gluestack, Tailwind, shadcn, Base UI, CVA, and Compose are not co-foundations.
Tailwind/shadcn/Base UI/CVA and Compose remain legacy implementations only while their current
production consumers are being moved. Wrapping them behind a new export does not count as migration.
They are deleted after parity, and a permanent import/dependency gate prevents their return.

## Post-parity visual direction

The following direction remains a refinement backlog, not acceptance criteria for the implementation
migration. Until #970 is complete, the Web and Shield references each remain authoritative for their
own client.

The replacement has no retro-theme name. It is simply Loomarr's product language:

- **Content first.** Programme artwork, channel identity, title, episode information, and time are
  visually primary; application chrome recedes.
- **Watching first on TV.** Playback is the home state. Guide and Surf are transient, edge-to-edge
  layers over a still-mounted player and dismiss without losing the tuned channel.
- **Dark-first, mode-aware surfaces.** Loomarr defaults to the dark broadcast-console presentation
  on first load and fresh install. Light provides an equally intentional daytime presentation, and
  system-following may be offered as an explicit saved choice. Playback may remain black, while
  ordinary surfaces, content, borders, focus, and scrims resolve through semantic theme roles.
- **Focus is a first-class state.** Focus is not hover with a larger scale. It has a visible ring or
  surface treatment, predictable movement, restored position, and no layout jump.
- **Artwork has a contract.** Programmes use 16:9 stills/backdrops with complete-image treatment;
  placeholder, missing, loading, and error states preserve geometry.
- **Density follows distance.** The information hierarchy is shared, while type sizes, hit targets,
  gutters, and disclosure differ for desktop, touch, and ten-foot viewing.
- **Motion explains state.** Overlay entrance/exit, focus movement, and tuning transitions may
  animate; decoration does not compete with playback, and reduced-motion is honored everywhere.
- **Launch identifies the station.** Web, mobile, and TV hand off their platform-native splash to
  the same Loomarr launch identity: the seven chroma segments rise left-to-right, then the Geist
  wordmark and tagline settle in. It is based on the supplied
  power-on mock, never blocks an already-ready client, and resolves immediately under reduced motion.

### Brand and iconography contract

Loomarr's identity is part of the shared system rather than a set of manually synchronized app
assets. The existing seven-segment chroma bar is the canonical symbol; its order and calibrated
colors are invariant. One canonical vector definition owns that symbol, the Geist wordmark, and
lockup geometry. It generates
the browser favicon, web manifest icons, mobile launcher icons, TV launcher/banner artwork, and store
listing derivatives. P1 shows every supported variation together: full-color and one-color symbol,
wordmark, horizontal and stacked lockups, light and dark grounds, minimum sizes, clear space, and the
small-icon reduction. The gallery renders each appropriate treatment on light and dark grounds.
Generated assets carry a drift test; hand-editing a platform derivative is a failing change.
The gallery also renders and replays the shared launch identity at pointer, touch, and TV densities;
client adapters own only the native splash handoff and readiness transition.

Product icons are a separate system from the Loomarr mark. Shared UI uses the outlined Lucide family
through a Loomarr-owned `Icon` interface backed by `lucide-react-native` and `react-native-svg`.
Named size roles, a single default stroke weight, optical exceptions, semantic colors, focus and
disabled treatments, and accessible-label rules are documented and rendered in the workshop. Apps
may not mix in an arbitrary icon family or invent local sizes. Platform-native symbols remain an
adapter option only where the platform convention conveys meaning that a shared glyph cannot.

Loading motion is also a shared vocabulary, not a collection of local spinners. P1 renders four
distinct jobs in both themes and every density: a compact activity glyph for short inline actions,
layout-preserving skeletons for unknown content, determinate progress for measured work, and the
Loomarr signal-acquisition treatment for player tuning. The latter refines the shipping tuner loader:
nine static-gray meter bars resolve left-to-right into signal amber beside a concise mono status.
Long waits expose useful stage or elapsed information rather than spinning silently. All variants
freeze into an informative final frame under reduced motion, and accessible status text carries the
meaning instead of decorative animation.

### Semantic token interface

The new token vocabulary names intent, not a CSS technique or a historical palette. The first slice
must cover at least:

```text
color.surface.{canvas,raised,overlay,focus}
color.content.{primary,secondary,muted,inverse}
color.state.{live,success,warning,danger,info}
color.action.{primary,secondary,focus,disabled}
space.{screen,section,control,inline}
type.{display,title,body,label,metadata,time,channelNumber}
radius.{control,card,overlay,round}
motion.{instant,focus,overlay,tune}
size.target.{pointer,touch,tv}
```

Platform scales map these roles to concrete values. Raw palette values remain private
implementation. The token generator continues to publish machine-readable artifacts while legacy
Web consumers exist; Tamagui configuration becomes the target source and generated CSS and JSON are
adapters rather than competing sources.

## Parity journey

The Shield replacement is one continuous user journey, not a gallery-only proof:

1. pair or authenticate without broadening authority;
2. enter the edge-to-edge Guide and load real guide data and artwork;
3. move through channels and airings with platform-correct navigation;
4. inspect the focused airing's title, series/season/episode, description, time, and artwork;
5. tune the focused channel and reach first frame;
6. reveal and dismiss the player overlay;
7. open Surf, browse channels, tune another channel, and return to the previous channel;
8. leave playback using platform-correct Back behavior.

The slice uses real generated client contracts and a configured Loomarr server. Storybook/MSW
fixtures cover deterministic states, but a mock-only demo does not pass.

The Web replacement covers every current production route. Component stories supply deterministic
states; route-level baselines prove that shell, responsive composition, and cross-component behavior
survive the renderer change.

## Acceptance evidence

### Visual and interaction

- Existing desktop/mobile Web baselines remain accepted unless an unavoidable renderer-level change
  is individually reviewed.
- Shield Pairing, Watching, Guide, Surf, loading, empty, error, and focus states are reviewed against
  the committed Kotlin references at the supported TV layouts.
- Guide and player surfaces fill the viewport. Safe-area/overscan padding belongs inside the
  composition and never creates an outer frame.
- One complete physical-Shield journey proves D-pad/OK/Back/number input, real playback,
  background/foreground recovery, and focus return.
- The 100-channel by four-hour Guide virtualizes without blank rows, time-axis drift, or focus loss.
- Browser keyboard and screen-reader behavior remain valid on every migrated Web surface.

### Correctness and safety

- Member/admin authorization negatives remain green, disabling a user or paired device kills the
  next authenticated request, and no native client stores the break-glass API token.
- Fresh pairing, signed-play-URL refresh, URL redaction, and session expiry are exercised end to end.
- Programme identity in Guide, Surf, overlay, and playback agrees with the authoritative guide and
  now/next responses; the client never substitutes fixture or cached metadata for live identity.
- Back, OK, D-pad, number-key, and tune behavior match the TV contract; no function depends solely on
  a Menu key.

### Performance and build health

- Existing web budgets remain: no JavaScript chunk above 500 KiB and no entry plus module preloads
  above 1 MiB uncompressed.
- Prepared-channel first frame stays within the existing playout tune budget and repeated surfing
  does not start encoders for prepared hits.
- The physical Shield journey has no visible frozen navigation or stale audio after backgrounding.
- Runtime-only Tamagui remains the accepted mode; compiler work is not part of parity migration.
- Local and CI tasks are affected-aware: native jobs do not run for an unrelated Go-only edit, and
  web-only story changes do not build both native applications unless a shared input changed.

### Maintainability and reuse

- `design-system`, `ui`, and `player` present documented root interfaces with no consumer deep
  imports and no dependency cycles.
- The same production source implements shared primitives and product rules for Web and Shield where
  their behavior and information hierarchy match; platform adapters own their deliberately distinct
  presentation, navigation, focus, and player transport mechanics.
- A source-sharing report identifies shared, adapter-specific, and duplicated code. Duplicated
  product rules block parity; duplicated platform mechanics do not.
- Removing Tamagui would require replacing the design-system implementation, not editing every
  screen. This deletion test is proved by import-graph enforcement.

The earlier adopt/revise/reject gate in #727 is superseded by the narrower 2026-09-03 decision in
#970. Shield acceptance is now a proportionate clean-sideload journey on the maintainer's physical
device; Web acceptance is route-by-route behavioral, accessibility, and visual parity.

## Delivery sequence

One row is one PR-sized phase. Later phases may be refined after evidence, but they may not collapse
the parity or retirement gates.

| Phase | Deliverable | Required proof |
| --- | --- | --- |
| P0a | This contract and dependency decision | docs and generated-doc gates |
| P0b | pnpm/Turborepo/Expo/Tamagui scaffold; no migrated production screen | web build unchanged; Expo iOS/Android/TV dev builds; affected-task tests |
| P1 | semantic tokens, fonts, brand assets, iconography, loading motion, primitive interfaces, fixtures, web/native Storybooks | token/asset drift, contrast, story coverage, web/native renders |
| P2 | shared Guide data/view modules and web integration | real API + deterministic visual/a11y gates; current web behavior retained |
| P3 | mobile/TV shells, pairing, confirmed self-disconnect, transport, and navigation adapters; shipping-screen parity inventory and dark-first pairing with canonical lockup and protected-centre branded QR | iPhone and Shield pair/self-disconnect/remote-revocation recovery evidence plus visual parity and QR-decode review |
| P3.5 | complete the shared design-system, product-UI, and platform-adapter interfaces required by the known web, mobile, and TV surfaces | coverage ledger has no unexplained gaps; web/native Storybooks exercise every supported theme, density, state, interaction, and motion mode; interface, visual, interaction, accessibility, drift, and import-boundary gates pass; real iPhone and 1080p/4K TV workshop evidence recorded |
| P4 | shared player interfaces plus complete React Native Shield parity | interface tests, emulator journey, current Kotlin references preserved |
| P5a | physical Shield clean-sideload acceptance | fresh pair, real playback, Watching/Guide/Surf remote traversal, background/foreground, maintainer visual acceptance |
| P5b | React Native Google Play internal-beta artifact and publication path | four-ABI AAB, 16 KiB alignment for every 64-bit ELF, deterministic version/package identity, protected durable upload signing, TV listing/manifest checks, internal-track-only publication |
| P5c | Compose retirement | Kotlin application/build/tests and Kotlin-only token/screenshot/release gates are absent; distribution-neutral listing assets survive; sideload and Play artifacts verify from the Kotlin-free tree |
| P6 | Web parity fixtures and browser-adapter foundation | route baselines, browser semantics, legacy-usage ledger never grows |
| P7 | Web production routes migrated in dependency-ordered cohorts | authorization, forms, accessibility, responsive and visual parity per cohort |
| P8 | retire Tailwind/shadcn/Base UI/CVA and transitional tokens | zero production legacy usage, clean retired identifiers, complete client and repository gates |

## Rollback and retirement

P5a acceptance established the former Kotlin application as historical Shield parity evidence.
P5a may intentionally discard its installed pairing record: the accepted cutover is
uninstall, install the React Native `loomarr.media` build, and pair again. P5b then establishes a
separate Google Play internal-beta channel from the accepted React Native source. Because the
accepted sideload used an ephemeral key, its installed copy may be uninstalled once before the
first Play install; cross-channel signature continuity is not a migration requirement. P5c removes
the Kotlin application, build, tests, and Kotlin-only release gates after the React Native sideload
acceptance and Play artifact verification.

Web migration uses route/surface ownership rather than two implementations mounted for the same user
journey. A surface switches only when its replacement passes its full contract. P6 and P7 may proceed
while the maintainer performs P5a/P5b acceptance, after P4 has stabilized the shared interfaces;
overlapping shared-package writers remain prohibited. The old implementation is deleted in the same
PR or the next explicitly paired retirement PR. Retiring framework identifiers adds them to
`scripts/check-retired.sh` as required by `AGENTS.md`.

## P0b scaffold evidence

P0b uses separate `mobile` and `tv` Expo applications because navigation and release identity are
real platform seams. Mobile owns Expo Router; TV owns a minimal root registration because the TV
config plugin does not support Expo Router. Both applications resolve the exact same
`react-native-tvos` version, which supports phone and TV targets, and both render the same
`ClientPlatformProof` source through the Loomarr-owned `design-system` interface.

The scaffold uses prototype-only bundle and package identifiers. It cannot overwrite the shipping
Compose application or claim the permanent mobile identity before P5a adoption. Runtime Tamagui is
proven through Android touch, iOS, Android TV, and Apple TV production JS bundles and a dedicated
Vite browser entry. After web-adapter React deduplication, the browser proof produces one 277.41 kB JavaScript
chunk (92.94 kB gzip) without mounting or changing a shipping route. No compiler or production
screen has been introduced.

Turborepo runs beneath `make clients`. Expo, Metro, and Turbo outputs are excluded from task inputs,
so a warmed unchanged graph restores all bundle tasks instead of rebuilding itself because of its
own logs. CI has a dedicated client gate: native-only source changes select clients without
selecting the legacy frontend, Playwright, tuner, or production-image families; workspace-root
dependency and tool changes fail wider because both graphs consume them.

Native Android proof builds are arm64-only and serialized. `make client-android-debug` defaults to
the mobile app; `CLIENT_APP=tv` selects TV. On Linux the command places the whole Gradle, Kotlin,
CMake, and Ninja process tree under a 3.75 GiB soft limit and 4 GiB hard limit, pins that tree to four
CPUs, uses one Gradle worker, keeps Kotlin compilation in-process, and injects one-slot CMake compile
and link pools through an Expo config plugin. The plugin registers those arguments on every Android
application and library subproject as its Gradle plugin is applied, so third-party Ninja graphs are
bounded too. `CMAKE_BUILD_PARALLEL_LEVEL` remains a defence for `cmake --build`; the generated pools
are the control that AGP's direct Ninja invocations actually consume. CPU affinity and the
process-tree limit remain fail-safe boundaries. Mobile and TV native builds are never run
concurrently.

The clean native proof is green for both generated Android targets: mobile produced a 76 MiB
`media.loomarr.mobile.prototype` APK in 5m35s and TV produced a 57 MiB
`media.loomarr.tv.prototype` APK in 2m15s. Both contain only `arm64-v8a`; the TV manifest is a
required Leanback application, marks touchscreen and faketouch optional, and exposes a Leanback
launcher activity. The proof also caught an optional-peer mismatch that Expo Doctor and production
JS bundles did not: Expo SDK 57 supports Reanimated 4.5.1 with Worklets 0.10.1, while pnpm had
auto-selected incompatible 4.6.0 and 0.12.1 releases. Both app manifests now pin Expo's supported
pair directly.

A 2026-08-25 regression proof caught that the earlier app-only pool did not reach Reanimated: AGP
launched Ninja directly with six Clang children despite `CMAKE_BUILD_PARALLEL_LEVEL=1`, pinned the
scope at its memory-high threshold, and had not completed after 30 minutes. With the all-subproject
generator, the real Worklets and Reanimated Ninja graphs contain depth-one pools on 38 and 106
compile edges respectively. Live sampling found one Clang child. The dependency-cold corrective
mobile proof completed in 6m07s; after parameterizing the generated pool and regenerating both app
projects, the final mobile target built and packaged in 4m43s and the independent TV target in
2m57s. None of those scopes reached its hard memory limit or recorded an OOM.

The Linux proof also generates both native Apple projects cleanly: the mobile project targets
iPhone and iPad (`TARGETED_DEVICE_FAMILY = "1,2"`, `SDKROOT = iphoneos`) while the TV project targets
Apple TV (`TARGETED_DEVICE_FAMILY = 3`, `SDKROOT = appletvos`). Xcode compilation and launch still
require macOS and remain explicit P0b acceptance evidence; a successful Metro bundle or Linux
prebuild is not recorded as a native Apple build. The `client-apple-simulator` target is the shared
local/CI verifier: its mobile and TV matrix legs generate the native project, install pods, make a
Release simulator build, boot the matching iOS or tvOS runtime, install and launch the application,
assert that its process remains alive, and retain a screenshot. A bundle-only result cannot make
P0b ready. Hosted builds keep CocoaPods and validated ExpoModulesJSI caches; opaque DerivedData is
not persisted between runners.

A measured `ccache` experiment was rejected. React Native found the Homebrew binary and configured
its Xcode wrappers, but both clean Release builds reported 0 cacheable calls, 0 hits, 0 misses, and
an empty cache; the cold TV and mobile jobs also grew to 19m10s and 28m27s. Keeping that integration
would add cost and dependency surface without accelerating this Expo/Xcode build graph.

The browser proof is also rendered, not bundle-only. At a 1440x900 viewport, the shared screen fills
the viewport and the 760x126 proof panel is centered at x=340, y=387 with no horizontal or vertical
overflow and no page exceptions. A server-render test runs through the same adapter aliases so a
second React runtime in a linked universal package fails the client gate instead of producing a
blank-but-successfully-bundled page.

## P1 visual-foundation evidence

P1 replaces the provisional scaffold styling with Loomarr's original brand contract, refined as a
cross-platform system. One checked-in JSON contract owns the seven-segment chroma order, calibrated
colors, wordmark, and identity outline; a hash-bound deterministic generator owns the platform asset
geometry and specifications. The generator produces web, mobile, TV, launcher, and store-listing
derivatives; `make brand-assets-verify` fails on a
hand-edited or stale derivative. Light and dark semantic themes, pointer/touch/TV density scales,
reduced-motion behavior, contrast assertions, Geist wordmark treatment, Lucide-backed product icons,
launch motion, inline activity, skeleton, progress, signal-acquisition loading, and the first shared
`ProgrammeCard` are exposed only through the Loomarr-owned `design-system` and `ui` roots.

The browser Storybook and its offline production build render the brand standards, generated asset
matrix, tokens, typography, spacing, iconography, launch sequence, loading vocabulary, and programme
states. Native Storybook uses the same stories. On the physical Shield at 3840x2160, its TV-specific
rail rendered the canonical lockup and visible focus treatment; D-pad Down moved focus between story
cards and OK selected the focused story. This proof uses the prototype package
`media.loomarr.tv.prototype`, which remains distinct from the permanent `loomarr.media` release
identity.

The complete `make clients` gate passes package-boundary enforcement, native Storybook generation
and typing, shared typechecks and tests, Expo Doctor (21/21 for both mobile and TV), production JS
exports for Android, iOS, Android TV, and Apple TV, and the Vite browser adapter build plus
server-render test. `make storybook-build` also passes. Workspace peer resolution is app-local and
React/ReactDOM are pinned to Expo SDK 57's 19.2.3 runtime so optional browser peers cannot create a
second native module graph. Web aliases map universal native host elements to `react-native-web` and
the SVG web adapter, making a native Flow import a red build instead of a runtime surprise.

The committed browser visual contract adds 34 reviewed desktop/mobile captures for brand standards,
light/dark themes, launch motion, loading states, iconography, and programme-card density/focus. A
fresh non-update `make fe-visual` run passes all 890 Storybook visual, motion, and accessibility
cases against the merged corpus.

## P2 shared-Guide evidence

P2 moves the product rules that every Guide presentation needs into the platform-neutral
`@loomarr/core` interface while leaving transport, DOM rendering, and focus events in their platform
adapters. The shared module consumes generated API objects directly and adds only served-window
geometry, live progress, channel broadcast/health facts, household-timezone formatting, compact
episode identity, and one stable selection model. Vertical movement retains a time anchor across
differently sized blocks; horizontal movement selects actual neighbours; every edge returns an
explicit boundary so a browser, touch, or remote adapter can enter surrounding controls without
copying Guide rules.

The existing web Guide now delegates window calculation, block geometry, channel state, time labels,
episode labels, and movement to that interface. Its current approved composition and visual
baselines remain unchanged; this phase does not infer the maintainer's pending updated Guide mock.
The shared module has 11 focused tests covering clipping, live progress, special episodes, timezone
formatting, exact time-boundary ownership, empty rows, all movement directions, and navigation
boundaries. `make clients` passes the package graph, shared types/tests, Expo Doctor, four native
bundles, and browser proof; `make fe` passes 198 frontend files / 1,585 tests plus production and
Storybook builds; a fresh immutable `make fe-visual` passes all 890 visual, motion, and accessibility
cases without changing a baseline. The final `make check` repository gate and `make docs-lint` also
pass on the publication head. P2 also closes an affected-CI hole exposed by this first real shared
core change: native Storybook consumes fixtures, fixtures consume core and API, and edits to any of
those three packages now select the shared-client and Apple-native gates as well as the web family.
The publication gate also exposed an existing integration-test race: library setting writes queue a
durable `system-health` run, and the test could count a token-rotation probe that was already in
flight as work caused by the later credential clear. The test now synchronizes on recorded job
completion before and after clear, proving the clear-triggered health pass and subsequent explicit
operations are dormant without relying on a sleep; 20 consecutive race-enabled repetitions pass.

## Open evidence, not open architecture

The architecture above is decided for parity migration. Runtime-only Tamagui is the accepted path;
the compiler previously enlarged representative bundles and is not part of #970. Current Web and
Shield tokens and assets migrate only when the new implementation reproduces their accepted output.
Later performance or visual refinement work may change an adapter, but it does not weaken the
Loomarr-owned interfaces or bypass the parity gates.
