# Shared client platform inventory

**Date:** 2026-09-03
**Status:** parity baseline for #970
**Plan:** [`plans/shared-client-platform.md`](plans/shared-client-platform.md)

This historical inventory compared the supplied local design artifact with the Web and former
Kotlin/Compose Android TV implementations. For #970, each shipping presentation was authoritative:
Web stays visually Web, and Shield's accepted React Native replacement preserves the former
Kotlin/Compose presentation. The supplied artifact informs later refinements but did not override
either parity target during implementation replacement.

## Evidence inspected

- `/home/fictional/Downloads/Shared file archive(4).zip`, especially
  `Loomarr TV + iOS.dc.html`, rendered locally rather than inferred from the unavailable hosted
  artifact.
- The former Android TV sources and committed Roborazzi references from the baseline revision,
  before P5c retired the Kotlin application and its screenshot harness.
- Current web Guide, Watch/player, artwork, channel-identity, token, Storybook, visual, and generated
  client modules.
- Current product behavior in `docs/design.md`, especially auth, Guide, Watch, player, and TV
  navigation contracts.

At inventory time the client surface included 213 production web TSX files, 114 web stories, 159
web component tests, 26 Android production Kotlin files, and 17 Kotlin test files. This was a staged
migration of proven behavior, not a greenfield demo; P5c subsequently retired those Kotlin files.

## What the supplied artifact actually contains

| Platform | Rendered surfaces | Useful direction | Missing contract |
| --- | --- | --- | --- |
| Android TV / Shield | Watching chrome, grouped Surf rail, 54-row Guide | watching-first shell, lightweight bottom chrome, direct number tune, grouped Surf, focus-tracking Guide detail | pairing, failure/loading, artwork behavior, long metadata, actual focus restoration, playback failures, 4K proof |
| Apple TV | Surf overlay | same information model with a lift/brightness focus treatment | complete Guide, Watching controls, tvOS Back/Menu equivalents, playback integration |
| iPhone | Watch/channel list and approval queue | content-forward navigation, search, watch-anywhere, approve-on-the-go | Guide detail, player controls, setup/auth, safe areas, accessibility, error states |
| iPad | landscape rail + player + tonight schedule | useful wide-touch composition and persistent channel rail | portrait, multitasking widths, interaction and player-state behavior |

The mock labels are annotations, not application copy. Phrases such as “row 1 of 1,” “OK tune,” or
“Menu filters” do not enter the UI merely because a composite uses them to explain interaction.
Controls must communicate their purpose through the platform interaction and accessible semantics.

## Current implementation assessment

### Keep as product truth

| Existing asset | Why it survives | Target home |
| --- | --- | --- |
| `api/openapi.yaml` and generated `@loomarr/api` modules | authoritative names, types, query functions, and compile-time drift detection | `packages/api` |
| `@loomarr/core` validation, SSE invalidation, formatters, and domain contracts | already platform-neutral; prevents client-specific wire/domain inventions | `packages/core` |
| deterministic fixtures and story scenarios | one reproducible state vocabulary for web/native stories and tests | `packages/fixtures` |
| pairing and revocable device-token behavior | correct least-authority native auth; cannot regress to an admin token | native transport adapter + shared auth state |
| signed play-URL and prepared-playback contract | the real playback seam shared by browser and native clients | `packages/player` interface + transport adapters |
| programme identity and guide endpoints | server remains authoritative for title, episode, time, artwork, and channel | shared Guide data module |
| TV navigation behavior in `TvNavigation.kt` | exact number matching, bounded recent history, grouped channels, explicit focus graph | shared pure TypeScript behavior with TV adapter tests |
| web `Image` behavior and Android `RemoteArtwork` behavior | both encode valuable placeholder, geometry, failure, and remote-loading requirements | shared Artwork interface + web/native adapters |
| release identity `loomarr.media`, versioning, launcher, and banner | identity is independent from presentation technology | sideloadable Android/Expo build |

“Keep” means keep the behavior or interface. Kotlin or Tailwind implementation does not survive
automatically.

### Adapt behind explicit seams

| Existing implementation | Adaptation |
| --- | --- |
| TanStack Router and browser cookie/CSRF transport | remain web adapters; Expo Router and paired bearer transport satisfy separate interfaces |
| `useHlsPlayer` and the web `VideoPlayer` state | split transport from player/overlay state; retain hls.js only in the web transport adapter |
| Android `PlaybackClient`, `GuideClient`, `PairingClient`, and `DeviceStore` | port the contracts and negative tests to generated TypeScript/native adapters; do not copy private DTOs |
| Android Guide cursor and focus graph | move pure selection math into `ui-tv`; bind it to `react-native-tvos` focus primitives in the TV app |
| Android channel catalog SSE/refetch behavior | express as shared query/invalidation behavior and retain GET as authoritative after reconnect |
| web and Android story/screenshot fixtures | consolidate the domain scenario; keep separate renderer baselines at platform dimensions |
| existing launcher icon, TV banner, and store metadata | preserve identity requirements, then regenerate presentation assets from the adopted visual language |

### Defer until after parity

| Current area | Evidence | Later refinement direction |
| --- | --- | --- |
| palette and token names | `static-*`, `signal`, and Tailwind aliases expose palette and implementation history more than product intent | semantic roles for surfaces, content, action, state, artwork, focus, distance, and motion |
| typography scale | web scale is multiplied by 1.5 in generated Kotlin; the result preserves ratios but not a designed ten-foot hierarchy | platform scales behind shared roles, verified at real viewing distance and iPhone accessibility sizes |
| Watching chrome | current Android reference is visually heavy and the identity banner competes with programme metadata | transient, bottom-anchored, edge-to-edge overlay with channel identity, complete airing facts, progress, and automatic fade |
| Surf | current Android reference uses roughly half-screen opaque furniture and large empty space | content-density based width, transparent black playback overlay, grouped Favorites/Recent/All, visible focus and progress |
| Guide | current implementation proves focus and metadata but reads as a large table; artwork is secondary and the time hierarchy is weak | edge-to-edge programme surface, stable channel/time axes, focus-tracking artwork/detail, semantic programme states, explicit filters |
| channel identity | text/number blocks dominate and logo/artwork treatment differs by client | one ChannelIdentity interface with logo, monogram fallback, number, live state, and distance-specific density |
| administrative web chrome | Test Card styling is treated as identity even where it adds little to operator comprehension | quieter application shell and purposeful hierarchy; viewer primitives reused only where the intent matches |
| empty/loading/error treatment | multiple implementations inherit current palette and layout assumptions | shared state interfaces with platform presentation and RFC 7807 mapping |

### Retire only after parity

| Legacy asset | Retirement condition |
| --- | --- |
| Tailwind theme/preset, shadcn copies, CVA styling, and Base UI presentation | final web consumer migrated with keyboard, screen-reader, find-in-page, forms, menus, slider, tooltip, and visual gates green |
| Test Card palette/name as the design authority | semantic Tamagui token gallery approved and every legacy adapter generated from it or deleted |
| Compose `design/`, `guide/`, and Watching/Surf presentation | Expo TV build passes physical Shield parity after a clean sideload and fresh pairing |
| Kotlin client/network state that duplicates shared TypeScript | equivalent generated transport and shared state tests are green on real server behavior |
| Roborazzi presentation baselines | replacement native visual and device gates are committed and capable of failing on a real regression |
| legacy Android token generator | no Kotlin presentation consumer remains |

Package identity and distribution-neutral brand assets survive Compose. Internal Play publication
and protected upload signing are P5b; wider tracks and in-place update evidence are outside #970.

## First-slice module map

| Product rule | Shared module | Web adapter | Mobile adapter | TV adapter |
| --- | --- | --- | --- | --- |
| channel identity and artwork states | `design-system` / `ui` | DOM image, responsive sources, browser alt semantics | native image loading and safe area | native image loading and distance scale |
| guide window, airing width, focused detail | `ui` | pointer/keyboard + virtual rows | touch list/detail | D-pad focus graph + virtual rows/time axis |
| tune selection and previous-channel history | `player` | browser controller/hls.js | native controller/player | remote keys, number buffer, native player |
| overlay visibility and fade | `player` | pointer/keyboard activity | touch activity | remote activity and focus ownership |
| Surf grouping and selection | `ui` | responsive overlay | sheet/rail | overscan-safe transparent rail |
| authentication state | `core` interface | cookie + CSRF | paired bearer | paired bearer + TV code flow |

## Gaps the slice must expose honestly

- The mocks show favorite groups, but the current backend has no durable per-user favorites
  contract. The client may show the group only when authoritative favorite data exists; it may not
  fabricate persistence.
- The supplied TV Guide composite does not visibly demonstrate rich artwork even though the product
  and current Android detail surface support it. The replacement must include actual episode stills
  or backdrops and explicit missing/loading/error geometry.
- The iPhone and iPad composites are not a complete mobile application specification. Setup,
  authentication, error recovery, accessibility, portrait/landscape, and safe-area behavior come
  from the product contract and platform evidence.
- At inventory time the Compose player used Media3 directly and the React Native native-player
  adapter had not yet been selected. P4 resolved that seam and P5a accepted it on the Shield.
- Tamagui's compiler is not part of parity migration. Runtime-only Tamagui is the baseline; any
  compiler experiment is a later isolated performance change.

## Inventory conclusion

The reusable assets include each client's current look during #970 as well as the server contract,
authorization and pairing model, deterministic domain scenarios, tune/Guide behavior, release
identity, and the tests that describe those guarantees. The new design-system implementation
reproduces those presentations while concentrating product rules behind shared Loomarr interfaces.
Cross-client visual convergence begins only after the shipping TV client and Web legacy stack have
been replaced.
