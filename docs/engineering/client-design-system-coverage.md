# Shared client design-system completeness ledger

**Status:** P3.5 shared-interface publication evidence complete on PR #581; Shield and Web parity
adoption authorized by #970 and owned by P4-P8
**Owner:** shared client platform
**Parent plan:**
[`docs/engineering/plans/shared-client-platform.md`](plans/shared-client-platform.md)
**Shipping-surface inventory:**
[`docs/engineering/client-platform-inventory.md`](client-platform-inventory.md)

## Purpose

This ledger makes “the design system and building blocks are complete” a reviewable claim. It covers
the known embedded-web, mobile, and TV clients, including the viewer surfaces planned for P4/P7 and
the foundational controls required by the administrative web migration in P7. It does not require
inventing modules for hypothetical future features. A new product capability amends this ledger when
it introduces a genuinely new interaction or presentation contract.

Storybook is the executable workshop for deterministic states. It is necessary evidence, not a
substitute for rendering the production journey on browsers and real hardware.

The web workshop's official Themes toolbar drives the same `LoomarrProvider` used by clients. Dark
is the default; light and system-selected light/dark are interactive global modes, while a story may
pin a mode only when the mode itself is the state under review.

The official Themes addon declares React Native unsupported, so the on-device workshop consumes the
same `theme` story global through its native decorator instead of pretending the web manager addon
runs there. Dark is again the default, and every shared native story module publishes an explicit
light state; a source gate prevents a later module from silently becoming dark-only.

## Completion rules

P3.5 is complete only when all of these rules hold:

1. Every shared interface promised by P3.5 is **proven**, or the row has an explicit scope exclusion
   with a named later owner from the approved delivery sequence. An exclusion may defer production
   integration or real-device acceptance; it may not hide a missing shared interface promised here.
2. Product rules are implemented once behind root interfaces in `@loomarr/design-system`,
   `@loomarr/ui`, `@loomarr/core`, or `@loomarr/player`; callers do not deep-import implementations.
3. Platform mechanics live in adapters only where at least two implementations prove the seam:
   browser semantics/keyboard, touch/safe-area/gesture, TV focus/remote/overscan/virtualization, and
   player transport.
4. Applications may compose shared modules but may not establish competing tokens, icon families,
   typography, focus treatments, loading vocabulary, or product-state rules.
5. Every supported theme, density, content state, interaction state, and motion mode is rendered in
   the web workshop. Shared native stories render the same interface on touch and TV hosts.
6. Tests cross the same root interface as callers. Visual, interaction, accessibility, asset drift,
   contrast, reduced-motion, import-boundary, and public-interface checks fail red when the contract
   regresses.
7. The deletion test holds: replacing Tamagui changes the design-system implementation and narrow
   adapters, not every product module or application screen.

Status vocabulary:

- **proven** — shared root interface plus required automated and rendered evidence exist;
- **partial** — a shared interface exists but required variants, states, or adapters are missing;
- **legacy** — only the shipping web or Compose implementation exists;
- **missing** — no implementation exists at the target seam;
- **claimed** — an active workstream owns the seam, but unmerged work is not completion evidence.
- **excluded → Pn** — the shared P3.5 interface exists where stated, while the named later phase owns
  production integration, platform mechanics, or real-device acceptance. This is not completion
  evidence for that later phase.

## P3.5 scope resolution

P3.5 certifies the reusable visual language, root interfaces, adapters that already have two real
implementations, and their executable workshop contracts. It deliberately does **not** certify that
every production route uses them. The delivery sequence keeps that evidence staged:

- **P4** owns shared player interfaces and complete React Native Shield parity;
- **P5a** owns the accepted physical Shield clean-sideload journey;
- **P5b** owns the React Native Play Internal artifact and publication path;
- **P5c** owns Compose retirement after those React Native proofs;
- **P6** owns Web parity fixtures and browser-adapter foundations;
- **P7** owns route-by-route Web migration and browser form/accessibility parity; and
- **P8** owns retirement of Tailwind/shadcn/Base UI/CVA and transitional tokens after parity.

P6/P7 may proceed while Play Internal acceptance is underway because P4 has stabilized the shared
interfaces and P5a has passed on the maintainer's Shield. The earlier broad go/no-go evidence in
#727 is superseded by #970's accepted single-device model. P5b includes Play Internal distribution;
installed-pairing migration and wider Play rollout are not gates.

The exclusions below resolve the former contradiction in which this ledger called its interface
implementation complete while leaving rows `partial`, `legacy`, or `claimed`. They narrow P3.5; they
do not pre-approve P4–P8 or turn their missing evidence green.

## Initial coverage audit

| Capability | Target module/interface | Current evidence on `main` | Status | P3.5 exit evidence |
| --- | --- | --- | --- | --- |
| Semantic color, type, spacing, radius, target, and motion roles | `design-system` tokens and provider | Dark/light themes, pointer/touch/TV densities, contrast and drift tests, browser/native foundation stories | proven | Preserve current gates while later rows consume semantic roles only |
| Loomarr mark, wordmark, lockups, chroma bar, favicons, launchers, TV banner, and launch motion | `design-system` brand | Canonical brand contract, generated derivatives, launch sequence, drift tests, light/dark stories | proven | Every shipping derivative remains generated and workshop-visible |
| Product iconography | `design-system` `Icon` | Loomarr-owned Lucide interface, named sizes/tones, browser/native stories | proven | No arbitrary icon family or local size vocabulary in migrated surfaces |
| Loading and progress | `design-system` loading interfaces | Activity, skeleton, determinate progress, and signal-acquisition treatments with reduced-motion tests/stories | proven | Add only missing product wait states discovered by the parity inventory |
| QR presentation | `design-system` `QrCode` | Protected-centre branded QR, decode tests, pairing use | proven | Preserve unbranded fallback and scanner-safe geometry |
| Layout, surfaces, typography, artwork, focus, and primitive actions | `design-system` primitives and layout | `Screen`, `Surface`, `Text`, `ArtworkFrame`, `FocusSurface`, `Action`, `Badge`, `Field`, and `ProgressTrack`; P3.5 now supplies provider-owned platform insets, a 48-unit TV overscan policy, an edge-to-edge viewport frame, semantic action states, and shared `AdaptiveSplit`, `ScrollFrame`, and `Disclosure` seams | proven | Preserve responsive wide/narrow composition, scrolling, progressive disclosure, and disabled/pressed/selected/error/focus contracts in product modules |
| Form and selection controls | `design-system` interaction and selection modules; host semantics adapters where required | Shared `Action`, `Field`, `Toggle`, `ChoiceGroup`, `Tabs`, `MenuList`, `SelectControl`, and `Hint` own their cross-platform presentation and state contracts with pointer/touch/TV stories; the browser adapter owns automatic tab traversal, menu traversal/dismissal, select focus return/Escape, and hint hover/focus triggers, with interaction and axe coverage; dialogs use the shared `ModalOverlay` | excluded → P6/P7 | Shared presentation and browser semantics are the P3.5 contract. P6/P7 add anchored placement or native long-press/D-pad adapters only when a production consumer proves the need, then certify native input behavior. |
| Feedback and recovery | `ui` `StatePanel` | Shared loading, empty, error, offline, retry, and permission treatments have useful accessible copy, one decisive recovery action, pointer/touch/TV stories, native stories, and centered loading geometry proof | proven | Preserve state vocabulary and recovery behavior as product modules consume it |
| Channel and programme identity | `ui` identity and metadata modules | Shared `ChannelIdentity`, `ProgrammeIdentity`, and composed `ProgrammeCard` own title, channel number/logo and initials fallback, series/season/episode, design-compliant airing label, description, badge, artwork state, and progress across pointer/touch/TV stories | proven | Preserve authoritative server metadata and deterministic missing-content fallbacks in Guide, Surf, overlays, and player chrome |
| Pairing and device recovery | `ui` `PairingShell`; `core/pairing` | Shared generated-contract state machine and presentation; iPhone/Shield pair, disconnect, and revocation evidence | proven | Remains dark-first, touch/remote reachable, mobile-responsive, and QR plus typed-code capable |
| Application navigation shell | `ui` `ClientNavigation` and `ClientShell` plus host Back adapters | Shared Watching/Guide/Surf destination intent, labelled selected-button navigation, icon vocabulary, keyboard reachability, TV preferred focus, and the Guide/Surf → Watching → platform-home Back rule now run in mobile and TV shells with web/native stories | excluded → P4/P7 | P4 replaces the Shield placeholders and proves route intent/Back/focus; P7 migrates the production Web shell and surrounding-control handoff. |
| Guide product rules | `core/guide`; `ui` `GuideSurface` and `GuideExperience` | Shared geometry, metadata formatting, selection, and movement rules now feed one responsive Guide surface with aligned time/channel rows, readable narrow-screen panning, filter availability, focus-following programme detail, artwork fallback, episode/fact/description metadata, household-timezone labels, and centred loading/empty/error/offline orchestration across pointer/touch/TV plus light/dark stories | excluded → P4/P7 | P4 adds the Shield virtualization/navigation adapter and proves tune/back/focus with the real API journey; P7 migrates the current Web Guide without visual or behavioral drift. |
| Guide TV mechanics | `ui-tv` Guide adapter | TV-only package now owns deterministic grid/filter D-pad movement, disabled-filter skipping, time-anchor retention, tune/filter activation intent, focus restoration after catalog change, and a bounded 100-channel row window with explicit position labels; a Storybook remote workshop proves the same controller through Arrow/Enter interaction in both themes | excluded → P4 | P4 connects native focus refs and the rendered row window, then proves remote repeat, overscan, focus restoration, and 1080p/4K behavior on emulator and Shield. |
| Overlay and modal composition | `ui` `ModalOverlay` and `TransientOverlay`; React Native host adapters | Shared blocking and non-modal interfaces now own scrim, web portal/focus trap/return, Escape/scrim dismissal, safe content padding, reduced motion, edge-to-edge playback composition, and bounded auto-dismiss across web/native stories; production disconnect consumes the modal | excluded → P4/P7 | P4 proves Shield Back/dismiss and focus behavior; P7 owns Web stacked-modal and remaining overlay parity. |
| Player state and transport | `player` root interface plus web/native adapters | P4 is dependency-stacked on this branch and owns the player package; no player package is part of the P3.5 publication unit | excluded → P4 | P4 owns shared state/tune/history, hls.js/native transport adapters, real first-frame, time-shift, lifecycle, and recovery evidence. |
| Player chrome and timeline | `ui` player presentation consuming `player` | The P3.5 vocabulary covers the underlying identity, progress, action, overlay, and responsive primitives; production player composition is intentionally absent from this publication unit | excluded → P4/P7 | P4 owns Shield anchoring, live/time-shift states, auto-dismiss, and remote traversal; P7 migrates the current browser presentation and keyboard controls. |
| Surf rail | `ui` `SurfRail`; `ui-tv` Surf adapter | Shared rail now preserves the still-mounted playback composition, explicit empty Favourites/Recent/All grouping, authoritative focused now/next identity, artwork fallback, progress, tune intent, honest client/server identity, narrow viewport bounds, and pointer/touch/TV plus light/dark stories; TV logic owns ordered traversal, catalog-change restoration, activation, and valid previous-channel intent | excluded → P4/P7 | P4 connects Shield remote events and focus refs and proves tune/Back/previous-channel behavior; P7 owns current Web viewer parity. |
| Browser application semantics | web adapters | P3.5 supplies browser adapters for the shared workshop and selection seams; the administrative app intentionally remains on its releasable legacy DOM presentation during migration | excluded → P7 | P7 migrates routes in cohorts and proves semantic HTML, forms, keyboard, screen-reader, reduced-motion, responsive, and visual parity before retiring the legacy stack. |
| Touch mechanics | mobile adapters | Pairing shell and destination shell run on iPhone; provider-owned safe-area values feed the shared viewport contract; the P3.5 workshop now has physical iOS 27 portrait/landscape, Dynamic Island, touch, scroll, disclosure, and keyboard evidence | excluded → future | Production mobile journeys are intentionally outside #970; the P3.5 workshop evidence is retained for their later migration. |
| TV mechanics | `ui-tv` root interfaces | P3.5 provides deterministic Guide/Surf navigation controllers, bounded row-window rules, overscan-aware viewport primitives, and remote workshop evidence | excluded → P4/P5 | P4 completes the production Shield journey; P5a proves it on the physical device, P5b adds Play Internal, and P5c retires Compose. |

## Required workshop matrix

Every shared visual module declares the applicable cells rather than relying on one showcase story:

| Dimension | Required values |
| --- | --- |
| Theme | dark default, light, system-selected dark/light where supported |
| Density | pointer, touch, TV |
| Content | representative, long, short, missing, loading, empty, error, offline |
| Interaction | rest, hover where meaningful, pressed, selected, focused, disabled, invalid |
| Motion | normal and reduced |
| Viewport | mobile portrait, mobile landscape, desktop, 1920x1080 TV, 3840x2160 TV |
| Input | pointer, keyboard, screen reader, touch, D-pad/OK/Back, number key where applicable |

Story applicability is machine-readable and checked against each module's declared interface. A
meaningless combination may be excluded, but the story metadata records why; absence is not an
implicit exclusion.

## P3.5 evidence to date

The protected client matrix, including Apple mobile, Apple TV, the four Playwright visual and
accessibility shards, and the aggregate CI job, passed on PR #581 before its final rebase. After the
iOS 27 runtime reconciliation and merge of current `main`, the pinned local visual run passed 1,182
cases with two declared skips, the frontend suite passed 1,665 tests across 211 files, and the SPA
and Storybook production builds completed against the reviewed baselines. That reconciliation also
moved Storybook theme application before paint so browser-native controls cannot capture a light
`color-scheme` baseline inside an otherwise dark workshop surface.

On 2026-08-26, the native TV workshop was generated and compiled as both arm64 and x86 React Native
Android TV development builds. The arm64 build installed on the physical Nvidia Shield and rendered
at its 3840x2160 output; the x86 build installed on the dedicated Android TV emulator and rendered
at its native 1920x1080 host resolution. In both environments, the TV-specific workshop rail showed
the canonical lockup and focus treatment, D-pad Down moved focus between story cards, and OK selected
the focused story. Logcat contained no React Native or Android fatal exception. The local captures
are retained under
`.artifacts/primary/design-system-device-evidence/`.

On 2026-08-31, the Release workshop launched and remained healthy on a physical iPhone 17 Pro Max
running iOS 27 beta 7. Explicit light and default-dark stories were exercised in portrait and
landscape. The pass covered rotation, the Dynamic Island and every safe edge, touch disclosure,
scrolling, field focus, keyboard avoidance and dismissal, value persistence, and responsive
stacked/side-by-side composition. The inspection found two real failures instead of rubber-stamping
the gate: the launch lockup's fixed native minimum height clipped its tagline in landscape, and the
Storybook decorator omitted the live iOS safe-area values used by the production root. Both were
fixed with regression coverage and recaptured successfully. The retained local manifest names the
accepted and diagnostic captures without recording device, signing-team, or account identifiers.

This completes the P3.5 physical-device exit evidence. It does not certify production viewer
gestures, Back, native modal focus, or assistive-technology behavior. Shield production behavior is
owned by P4/P5; mobile and Apple TV production behavior remains future work outside #970.

### Real-iPhone workshop capture

The prototype container permits both orientations so the P3.5 workshop can be inspected without
changing the build between captures. That evidence is retained for the later mobile production
migration; it does not add mobile scope to #970.

From the repository root, with Xcode 27 selected and a supported development iPhone attached,
trusted, and visible in `xcrun xctrace list devices`, generate from the template bundled with the
pinned Expo package, then build and launch the workshop on the named device:

```bash
cd web
EXPO_TEMPLATE="$(cd apps/mobile && node -p \
  "require.resolve('expo/package.json').replace(/package\.json$/, 'template.tgz')")"
pnpm --filter @loomarr/mobile exec expo prebuild --platform ios --clean --no-install \
  --template "$EXPO_TEMPLATE"
EXPO_PUBLIC_LOOMARR_STORYBOOK_DENSITY=touch STORYBOOK_ENABLED=true \
  pnpm --filter @loomarr/mobile exec expo run:ios --device "<device name>"
```

Retain the screenshots or recordings and a text manifest under
`.artifacts/primary/design-system-device-evidence/iphone/`. The manifest records the PR head SHA,
device model, iOS version, build configuration, capture time, and the stories exercised. Inspect at
least one dark and one explicit `Light` story in portrait and landscape. Across the applicable
stories, verify safe areas, touch targets, scrolling, disclosure, gestures, keyboard behavior, and
focus transfer/return. Summarize the result and honest limits in this ledger, `PROGRESS.md`, PR #581,
and issue #726; never commit device identifiers or other private device data.

## Publication gate

During implementation, affected package tests and focused Storybook checks are the normal feedback
loop. Publication uses the repository's current affected-verification interface and protected CI,
plus the phase-specific route or Shield evidence above. Expensive matrices are not rerun after
changes that cannot invalidate them; protected CI owns the final platform matrix.
