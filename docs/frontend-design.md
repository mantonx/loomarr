# Loomarr Client Platform — Architecture & Design System

**Status:** P3.5 shared system complete; Shield and Web parity migration active under #970 · companion to [`design.md`](design.md)
**Precedence:** the main design doc is authoritative for *behavior* (endpoints, flows, auth, phases). This doc is authoritative for *how the frontend looks and is built*. Conflicts → main doc wins on what, this doc wins on how; fix the loser in the same PR.
**Language policy (main doc §14) applies:** the web implementation compiles to static assets embedded in the Go binary; Expo and React Native are the approved client build/runtime exception and do not add an application backend.

## 0. Refinement authority and migration state

The maintainer has authorized cross-platform consolidation of the current
design system based on **Tamagui Core**, Expo, and `react-native-tvos`. Loomarr's existing Test Card
identity is the visual foundation: its chroma bar, calibrated broadcast palette, Geist typography,
and broadcast-console character are preserved. The complete target interfaces, acceptance evidence,
delivery sequence, and retirement contract live in
[`engineering/plans/shared-client-platform.md`](engineering/plans/shared-client-platform.md).

The sections below describe the **shipping legacy implementation** until each surface migrates. They
remain binding for code that still uses Tailwind/shadcn or Compose and are parity evidence. New shared design work follows the
migration plan and these precedence rules:

1. Loomarr owns the semantic interfaces; application code does not depend directly on Tamagui.
2. A migration is an implementation translation, not a refinement. Shield preserves the accepted
   Kotlin/Compose presentation through its React Native replacement; Web preserves the current Web
   presentation. Before implementation,
   every migrated surface records a parity inventory against that client's shipping references.
   Existing content, actions, states, and recovery paths remain present unless the maintainer
   explicitly approves an exception.
3. Product rules, deterministic states, artwork treatment, and appropriate visual primitives are
   shared; navigation, focus, safe-area/overscan, DOM semantics, and player transport sit behind
   platform seams.
4. Existing behavior, accessibility, authorization, pairing, and playout guarantees survive every
   migration PR. Shield supports the accepted signed sideload plus a Google Play Internal test from
   the same React Native source. A clean reinstall and fresh pairing remain acceptable between the
   ephemeral-key sideload and the separately signed Play installation; cross-channel update
   continuity is not promised.
   A clean Play install discovers local Loomarr servers and makes selection remote-friendly; manual
   URL entry is a troubleshooting fallback, never the normal onboarding path or a build instruction.
5. P3.5 shared-interface publication is complete. The 2026-09-03 maintainer decision authorizes the
   full Shield and Web parity migrations; other production native clients remain later work.
6. The accepted React Native Shield surface is releasable and the Compose implementation is
   retired. The current Web surface remains releasable until its parity gates pass, then its legacy
   implementation is retired rather than maintained indefinitely.

Brand identity and product iconography follow the consolidation contract as well. The canonical
Loomarr chroma bar, wordmark, lockups, favicon, launcher icons, TV banner, and store artwork are generated
from one shared vector definition and reviewed together in Storybook. Product glyphs use one shared
outlined family behind a Loomarr-owned interface with named sizes, stroke, state, and accessibility
rules. Pairing surfaces use the canonical chroma-and-Geist lockup, while the QR code carries the
contained Loomarr mark inside a high-contrast protected centre area and sufficient error correction
for reliable scanning. Paired native shells expose a confirmed **Disconnect this device** action;
the server revokes the current device before secure local state is cleared, and a failed revocation
remains retryable. A platform asset or one-off icon is not a new source of design truth.

---

## 1. Legacy design concept: Test Card

This section records the implemented visual system that forms the basis of the refined target.
A *test card* is the color-bars calibration image broadcasters
transmitted to prove the picture was true — the original pixel-perfect contract between a station
and every screen tuned to it. The current frontend used that metaphor for a broadcast-console
aesthetic whose correctness is enforced by the Playwright visual suite.

**Aesthetic direction:** a modern, dark broadcast console — calm surfaces, precise data, mono-set channel numbers — with retro-TV warmth used as *seasoning, not sauce*. Loomarr's product soul is era-matched: Saturday-morning cartoons with period cereal ads. The UI should feel like the master control room that makes that possible: professional first, nostalgic in the margins.

**Rules that keep it tasteful:**
- CRT/scanline/static flourishes appear **only** on idle surfaces — onboarding, empty states, the login screen. Never on data, tables, or forms.
- All flourishes are disabled under `prefers-reduced-motion` **and** in visual-test mode (determinism, §5).
- Nostalgia lives mostly in **microcopy** ("Dead air — create your first channel", "You're on the air") and in the `onair`/channel-number idiom, not in texture overlays.

---

## 2. Design tokens

Tokens are the single source of truth. **No raw color/size literals outside the token layer** — a hex code in a component is a review-blocking defect.

### 2.1 Color — the Test Card palette

Dark is Loomarr's default presentation on first load and fresh install across web, mobile, and TV.
The operating system's current appearance does not silently select a different initial Loomarr
theme. A person's explicit saved choice may select light or system-following mode, and the refined
system ships a first-class light theme across web and native. Components consume semantic surface,
content, border, focus, and scrim roles rather than assuming a dark ground. The chroma bar and its
calibrated broadcast accents do not change between modes; supporting neutrals and contrast pairings
do. Both themes are rendered and contrast-checked in Storybook.

**Static scale (neutrals — "the set"):**

| Token | Hex | Use |
| --- | --- | --- |
| `static-950` (bg) | `#0B0C0E` | App background |
| `static-900` (surface) | `#131519` | Cards, panels |
| `static-800` (surface-2) | `#1B1E24` | Nested/hover surfaces |
| `static-700` (border) | `#2A2E37` | Hairlines, dividers |
| `static-400` (muted) | `#8B93A3` | Secondary text, placeholders |
| `static-100` (text) | `#E7EAF0` | Body text |
| `static-0` | `#FFFFFF` | High-emphasis text |

**Broadcast accents (from the bars):**

| Token | Hex | Meaning / use | Contrast notes |
| --- | --- | --- | --- |
| `signal` (amber) | `#FFB020` | **Brand & primary actions**, focus ring, active nav | 10.7:1 on bg — AA everywhere; 8.8:1 on its tint |
| `onair` (red) | `#E5484D` | Live/actively-streaming indicators; destructive actions | 5.0:1 on bg; **on its tint use `onair-300`** |
| `onair-300` | `#E85A5F` | Badge/pill text on the onair tint | 5.0:1 on tint (computed with margin) |
| `suggest` (magenta) | `#D6409F` | **The AI color**: intent input focus, generation progress, proposal accents | 4.75:1 on bg; **on its tint use `suggest-300`** |
| `suggest-300` | `#DC5BAC` | Badge/pill text on the suggest tint | 5.0:1 on tint |
| `tune` (cyan) | `#4CC9E8` | Links, informational states, in-progress "tuning" | 10.1:1 on bg; 7.9:1 on tint |
| `lock` (green) | `#3DD68C` | Success, checklist pass, "signal locked" | 10.4:1 on bg; 8.2:1 on tint |
| `caution` (yellow) | `#F5D90A` | Warnings, drift flags, conflicts | 10.8:1 on its tint; dark text on solid fills |

**Tints are alpha washes, not fixed hexes** (adopted from the Claude Design prototype): a tint is `color-mix(in srgb, <accent> N%, transparent)` layered over the surface, with standard steps N ∈ {8, 12, 15, 30, 40}. One formula replaces six tint tokens and yields a consistent ramp per accent.

⚠ **The steps split into two jobs, and only one is contrast-gated.** Steps 8/12/15 are TEXT BACKGROUNDS — a badge or pill with accent copy on it — and the badge rule below governs them; the CI check validates each accent's `on` stop against the **15%** composite. Steps 30/40 are **fills only**: a pod segment, a progress bar, a block in the guide grid. Accent text does not clear AA on them (the `Design/Palette` page renders every step and shows exactly where the ramps fail), so putting a label on a `-30`/`-40` fill is out of contract. This was an unstated assumption until the palette page made the whole ramp visible — the rule had always been written as though 15% were the only step that existed.

**The badge/tint rule (learned the hard way, twice):** 11px badge text *is small text under WCAG* — the 4.5:1 bar applies regardless of how label-like it feels. On the composited 15% tints the base stops fail (onair 4.02:1, suggest 3.86:1); the `-300` stops pass with margin (`#E85A5F` → 4.54, `#DC5BAC` → 4.65). Every `accent-on-tint` pairing is machine-verified against the *composited* tint color; the token generator (§2.5) recomputes these ratios in CI, so a palette or alpha edit that breaks a pairing fails the build.

Additional statics from the prototype: **`signal-400` `#FFC14D`** (hover/active amber, 11.3:1 on card) and **`static-500` `#5A6170`** — 2.94:1 on cards, therefore restricted to **disabled states and decorative glyphs only**; any text carrying information uses `static-400` muted or better.

Semantic aliases map onto these (`primary`→signal, `destructive`→onair, `success`→lock, `warning`→caution, `info`→tune) so shadcn primitives restyle without edits. The lifecycle states (§ main doc §4) map: `wanted`/`requested` static-400 · `downloading` tune · `available` lock · `unavailable` static-400 strikethrough · drift caution.

Stored as OKLCH in the source of truth (Tailwind v4/shadcn convention); hex above is for human reference.

### 2.2 Typography

- **Geist** (UI) + **Geist Mono** (data) — one family pair, OFL-licensed, **self-hosted from the embed** (satisfies the main doc's offline rule *and* makes visual tests deterministic — no CDN font swaps).
- Mono is a design signature, not a garnish: **channel numbers, EPG times, state badges, external ids, durations** are always mono. If it came from a machine, it's set in mono.
- Scale: 12 / 13 / 14 (body) / 16 / 20 / 24 / 32, line-height 1.5 body · 1.2 headings. Channel-number display style: mono 24–32, tabular numerals.

### 2.3 Space, radius, elevation

- 4px grid (`space-1`=4 … `space-8`=32). Density: comfortable default; tables use compact row height (40px).
- Radius: `sm` 4 (inputs, badges) · `md` 8 (cards, buttons) · `lg` 12 (dialogs, hero surfaces).
- Elevation in dark UIs is **borders first, shadows second**: surface + `static-700` hairline; one soft shadow token reserved for overlays (dialogs, popovers, ⌘K).
- **Non-text contrast (WCAG 1.4.11):** the `static-700` hairline is 1.3–1.4:1 — fine for *decorative* dividers and card outlines (the surface fill does the identifying), but **no control may be identified by that hairline alone.** Controls get a fill difference + the focus ring; where a border is genuinely the boundary (e.g. an unfocused outline input), use **`border-control` `#61646B`** — computed ≥3:1 on both card and page.

### 2.4 Motion

- Interaction tokens remain `fast` 120ms · `base` 200ms, ease-out. Nothing interactive animates
  longer than 300ms except the suggester's generation shimmer. The one-time launch identity is a
  documented exception based on the supplied power-on mock: 340ms chroma segments at 40ms stagger
  and a 400ms word/tagline settle. The mock's broad broadcast roll is intentionally omitted after
  visual review because it cheapened the otherwise restrained identity motion.
- `prefers-reduced-motion` is honored globally (single CSS gate) and force-enabled in visual-test mode.
- Signature moments (used sparingly): checklist items "lock in" (static→clear, 200ms) during onboarding; a channel card's `onair` dot fades in when its first reconcile completes.
- Loading has four named jobs: compact inline activity, content skeleton, determinate progress, and
  signal acquisition. Preserve and refine the current tuner loader's gray-to-amber meter lock as the
  branded playback treatment; replace local `Loader2` decisions with the shared activity interface.
  A wait longer than three seconds must expose stage, elapsed time, or other useful progress context.

### 2.5 Token pipeline and migration

For legacy consumers, `web/packages/tokens` holds the TS source of truth and generates three
artifacts in CI:
1. `theme.css` — Tailwind v4 `@theme` variables for the web app,
2. a **Tailwind preset** — consumed by the legacy web implementation during migration,
3. `tokens.json` — consumed by legacy Web migration tooling.

CI fails if generated artifacts drift from source (`make fe-tokens` regenerates; diff must be empty). This is the same committed-artifact discipline as `api/openapi.yaml`.

The target source is `packages/design-system`'s Tamagui configuration and semantic token interface.
During migration it generates the legacy CSS, JSON, and Kotlin artifacts so there is still one source
per value. A token does not graduate into the target interface merely because it exists here: the
visual review classifies it as keep, redesign, adapt, or retire. Tailwind and Kotlin output are
adapters while legacy consumers remain and disappear with those consumers.

---

## 3. Legacy component library — three layers

**Layer 0 — tokens** (§2).
**Layer 1 — primitives:** shadcn/ui (new-york style, Tailwind v4), copy-in per its philosophy. Restyled **only** via tokens/CSS variables — never fork primitive logic. **Base UI** (`@base-ui/react`) underneath gives focus management and a11y for free; shadcn ships a Base UI variant of every component the app uses, so the copy-in path is unchanged. ⚠ "For free" has one documented exception: Base UI's Tooltip is **visual-only by design** (no `role="tooltip"`, no `aria-describedby`), so a tooltip whose content is information rather than a restatement of the trigger's label must declare its own description — see `FieldHelp` and design §14.
**Layer 2 — Loomarr components:** the actual product library, in `apps/web/src/components/loomarr/`. **Pages compose Layer-2 components; Tailwind utility soup is confined to Layers 1–2.** Every Layer-2 component: CVA variants, typed props from the orval client where applicable, a co-located Storybook story (§5) enumerating all states, and renders RFC 7807 errors through the shared `ErrorState`/field-error patterns — never raw JSON.

### Signature components (the vocabulary of the app)

| Component | Purpose | States to register |
| --- | --- | --- |
| `AppShell` | Nav rail + ⌘K + quiet server-version identity above the account footer + user menu. The compact rail exposes the full identity accessibly; admins link to System → About, while members are not sent into admin-only Settings. | member / admin / mobile-web collapsed |
| `PageHeader` | A page's single semantic title, optional explanatory copy, and optional page-level actions/status. Owns the page-edge gutter, divider, and responsive action stacking. | title only · title + description · with actions |
| `StateBadge` | Provisioning lifecycle chip (mono) | wanted · requested · downloading · available · unavailable · drift |
| `OnAirIndicator` | The red dot with a pulse (pulse ≤ reduced-motion) | off · live · reconciling |
| `ChannelCard` | Channel health at a glance: number (mono), name, now/next, managed badge | healthy · pending-slots · drift · error · creating |
| `NowNextStrip` | "Now: X · Next: Y" line for a channel | playing · flex/pod gap · empty |
| `IntentInput` | The hero — NL intent with `suggest` magenta focus ring + template chips | empty · focused · template-filled · submitting |
| `GenerationProgress` | SSE-driven suggester steps | searching · reasoning · scoring · done · failed |
| `ProposalReview` | Lineup + acquisitions with rationale, confidence, alternates; edit-via-search | draft · submitted · approved · denied · partially-edited |
| `PodTimeline` | A break visualized: bumper→ads→bumper with era/audience chips | matched · fallback-widened · bumper-card-only |
| `ClipCard` | Filler clip w/ kind/era/audience/category chips | tagged · untagged · ai-suggested-tags |
| `ChecklistItem` | Wizard/Settings check row | pending · running · pass · fail(+hint+doc-link) |
| `ApprovalQueueItem` | Admin queue row with one-click approve/deny | pending · approving · denied |
| `SearchCommand` | ⌘K palette over `/v1/search` scopes + channels + help | idle · results(in-library flag) · empty |
| `EmptyState` / `ErrorState` | Mandatory for every list / RFC7807 renderer | per-surface copy variants |

---

## 4. Target shared-client architecture

### 4.1 Workspace layout (pnpm, inside `web/` during migration)

```text
web/
  packages/api/            # orval output: wire models and query functions
  packages/core/           # validation, SSE, formatters, platform-neutral domain logic
  packages/fixtures/       # deterministic domain scenarios shared by stories and tests
  packages/design-system/  # semantic tokens, Tamagui config, fonts, icons, primitives
  packages/ui/             # shared product and viewer modules
  packages/ui-tv/          # D-pad focus, overscan, remote, and TV guide adapters
  packages/player/         # shared playback state interface + platform adapters
  packages/tokens/         # TRANSITIONAL generated adapters for legacy consumers
  apps/web/                # Vite delivery adapter; embedded in the Go binary
  apps/mobile/             # Expo Router; iOS and Android touch clients
  apps/tv/                 # Expo + react-native-tvos; Android TV and Apple TV clients
```

The directory remains named `web/` through parity migration to avoid coupling a workspace move to
the UI proof. A later rename is allowed only as an isolated mechanical change. Packages follow
[`web/packages/README.md`](../web/packages/README.md): root files are the interface, nested source is
private implementation, tests use the interface, and the graph is acyclic.

### 4.2 The sharing decision (explicit)

**Share product implementation where behavior and information hierarchy are the same; adapt the
interaction and platform mechanics that genuinely differ.**

| Shared implementation | Adapter-owned implementation |
| --- | --- |
| semantic tokens, themes, type roles, artwork, metadata, actions | font loading, safe area, overscan, DOM-only semantics |
| `packages/api`, `packages/core`, and deterministic fixtures | cookie/CSRF browser transport vs paired-device native transport |
| Guide shaping and time-axis math; player/overlay state machines | TanStack Router vs Expo Router; hls.js vs native playback |
| product component source where semantics match | pointer/keyboard, touch/gesture, D-pad/focus and remote events |
| story state contracts and fixture data | platform renderers, interaction tests, and visual baselines |

**Superseded decision, retained as migration history:** the original system rejected Tamagui and
react-native-web in favor of Tailwind/shadcn on web and a future NativeWind/React Native Reusables
client with no shared component implementation. That decision no longer governs new work. Tamagui
Core is the candidate implementation behind Loomarr-owned modules; NativeWind and Gluestack are not
parallel styling authorities. The full Tamagui UI theme is not adopted.

The candidate framework does not define the seam. Production applications import Loomarr packages,
not Tamagui. This keeps a rejected spike recoverable and makes replacing the implementation local.

### 4.3 Forms & state

- **TanStack Form + zod**, the zod schema passed straight to the form as a **Standard Schema** validator — no resolver adapter. Zod schemas live in `packages/core` so mobile reuses validation verbatim, and `@tanstack/form-core` lets it reuse the form logic too. (Not shadcn's `<Form>`: that wrapper is react-hook-form-bound and Loomarr hand-composes `Label`+`Input`, so there was nothing to inherit.)
- **No global state library.** TanStack Query owns server state (SSE-invalidated, main doc §12); local UI state is React state. Introducing zustand/jotai requires updating this doc first.

### 4.4 Route payloads are bounded

File-route code splitting only helps when a route's imports stay behind that route boundary. Production
code imports the nearest component or domain barrel (`@/components/ui/button`,
`@/components/loomarr/dashboard`, `@/filler/filler-page`), never an app-wide barrel such as
`@/components/ui`, `@/components/loomarr`, `@/channels`, `@/filler`, or `@/settings`. The same rule
applies to workspace packages: runtime code imports a tag/model subpath from `@loomarr/api` and a
single module from `@loomarr/core`, while their package roots remain the complete public catalogs for
tests and tooling. Using a catalog barrel in the running app lets the bundler merge unrelated screens
back into one eagerly preloaded chunk.

The production build enforces both sides of this contract: no JavaScript chunk may exceed 500 KiB, and
the entry script plus its module preloads may not exceed 1 MiB uncompressed. The first bound keeps one
heavy screen from becoming a parse cliff; the second catches a nominally split build that still downloads
the whole app before authentication or route selection.

The frontend lint gate also reads every production TypeScript import and rejects those catalog roots,
including type-only imports. That stricter source rule keeps the interface obvious in editors and prevents
a later value import from silently changing a type-only dependency into an eager runtime dependency. Tests
and stories may use the complete catalogs because they are tooling entrypoints rather than route payloads.

The embedded production server negotiates gzip for HTML, JavaScript, CSS, and other compressible static
formats. It prepares those representations once when its handler is constructed, not on every request;
the route budget above remains uncompressed so compression cannot hide parse and evaluation regressions.
The document also starts the initial `/v1/auth/me` read while that entry graph is downloading. The shared
API transport adopts that exact response once, preserving the route guard's error handling without putting
the session round trip after JavaScript evaluation or issuing a duplicate request.

---

## 5. Component workshop + pixel-perfect testing (Storybook + Playwright)

The component library lives in **Storybook** — the workshop for building and reviewing modules in
isolation. Story states and fixtures are shared; web and on-device Storybooks render the appropriate
adapters. Playwright remains the deterministic web renderer, while emulator/device captures and
interaction tests prove native behavior. **A browser rendering of React Native code is not TV
evidence.**

**Why Storybook over a hand-rolled `/__gallery`:** stories are the industry-standard component contract (CSF), they double as the dev workshop (controls, autodocs, the a11y panel), and they carry to the future mobile app (§4.2) via `@storybook/react-native`. The mechanics below preserve **every guarantee** of the earlier registry plan — offline, deterministic, committed baselines, 100%-coverage-enforced. **Chromatic is rejected on the record:** it is a hosted SaaS visual-diff service that would send our UI off-box and break the offline/self-hosted rule (§2.2, main doc §16); visual regression stays self-hosted Playwright against the offline `storybook-static` build.

### 5.1 Stories are the contract
- **Co-located CSF stories** — every Layer-1 primitive and every Layer-2 component has a `*.stories.tsx` beside it (folder-per-component), enumerating **every registered state**. Storybook 10 (`@storybook/react-vite`) indexes them; the built `storybook-static/` is the offline gallery. **A component without a story fails the build** — a coverage test enumerates the component barrels against the story index (the successor to "unregistered components are a lint error").

  ⚠ **Layer 1 is in scope, and was not always.** The original rule said "100% of Layer-2", on the premise that primitives are vendored shadcn nobody touches. That premise does not survive §2.5: primitives are restyled **through the token layer**, so a palette or alpha edit changes all nine of them — and with no primitive stories, neither the gallery nor the visual suite would show it. The tokens are precisely the thing most likely to change and least likely to be noticed, which makes the primitives the *last* place to skip coverage. A primitive's story enumerates its variants (every `Button` intent × size, every `Badge` tone), because those variants are what the token edit moves.

### 5.1a The library is browsable, not just complete

Coverage says every component *has* a story. It says nothing about whether a person can **find** one — and past ~30 components a flat alphabetical list stops being a gallery and becomes a lookup table you have to already know the answer to.

- **Stories are grouped by what a component is for**, not by where its file sits: `Design/*` (the system itself — palette, tokens, type, spacing), `Primitives/*` (Layer 1), then Layer 2 split by domain (`Channels/*`, `Guide/*`, `Filler/*`, `Setup/*`, `Shell/*`, `Feedback/*`). The folder layout stays flat and co-located; only the story `title` carries the grouping, so nothing moves on disk.
- **The design system documents itself in the workshop.** `Design/Palette` renders every accent with its hex, its tint ramp, and its **measured** contrast ratios — read from the generated token artifacts, never retyped, so the page cannot drift from what the build enforces (§2.5). `Design/Tokens` lists the full set; `Design/Typography` and `Design/Spacing` show the scales. The rules in §2 stop being prose only a maintainer has read and become the first thing anyone opening Storybook sees.
- **The `Design/*` pages are ordinary CSF stories, not MDX.** MDX would need `@storybook/addon-docs` (a new dependency, §14) to render prose that sits *beside* the values; a story renders the values themselves — it type-checks against `tokens.json`, gets a visual baseline like every other story, and cannot drift from the generated artifacts. Explanation rides along as rendered copy in the page. Executable documentation over described documentation, and no new dependency to justify.

### 5.1c Layer 2 must not re-implement Layer 1

§4.1 says primitives are restyled only through tokens and Layer 2 composes them. Nothing enforced the second half, and an audit found Layer 2 quietly growing its own primitives:

| Duplicated pattern | Where | Should be |
| --- | --- | --- |
| Small tinted chip, mono/uppercase | `Badge` (L1) **and** `StateBadge` (L2), each with its own `cva` and its own tint classes | `StateBadge` composes `Badge` |
| `font-mono text-[10px] uppercase tracking-…` caption | **21 occurrences across 11 components** | a `Caption` primitive |
| Status dot (`size-2 rounded-full`, pulse when live) | `GenerationProgress`, `GuideGrid`, `OnAirIndicator` | a `StatusDot` primitive |
| 15 Layer-2 components import **no** primitive at all | — | audited case by case; a leaf like `TvStatic` legitimately owns its markup, a card-shaped one does not |

**Why this matters more than tidiness.** The badge/tint contrast rule (§2.1) — the one learned the hard way, twice — is now encoded in `Badge` *and* `StateBadge` independently. Fix a stop in one and the other stays wrong, and **the contrast gate cannot tell**: it verifies token *pairings*, not which components consume them. Every duplicated pattern is a second place a design rule has to be remembered rather than inherited.

**The rule:** a visual pattern appearing in three or more Layer-2 components is a Layer-1 primitive, and Layer 2 composes it. Extraction is not a refactor to schedule later — the duplicate is the bug, because it is where the next contrast regression will land.

### 5.1b Story args come from `packages/fixtures`

§4.2 lists **"Storybook story *contracts* (CSF states) + `packages/fixtures` args"** as SHARED across web and the future mobile app: a component's states are defined once and each platform renders them. A story that hand-rolls DOMAIN data is not portable — it is a web-only literal the native Storybook cannot reuse.

**The rule:** any arg representing domain data (a channel, a clip, a proposal, a guide row, a title) comes from `@loomarr/fixtures`. Presentational args stay inline — a `size="lg"`, an `open` boolean, a `state` enum, a name-and-number pair are not domain data, and pushing them into a shared package would bloat it with values no other platform benefits from.

This is also what keeps the visual suite honest: fixtures are deterministic by construction (§4.2 — no `Date.now`, no randomness), while an inline literal invites a live clock straight into a snapshot.

**Audit result: the library already complies.** Eight stories import fixtures and the rest pass presentational args only — no story constructs a DTO inline. An earlier draft of this section reported "38 of 46 stories hand-roll their args", which was a miscount: it subtracted the fixture importers from the total and assumed the remainder were all domain data. Recorded because the wrong number nearly bought a 38-file migration that the codebase did not need.
- **`make fe-visual`** builds `storybook-static` on the host, then runs Playwright **inside the pinned official Playwright Docker image** (the reference rasterizer, §5.2) over every story at **two viewports** (1280×800 desktop, 390×844 mobile-web) with `maxDiffPixelRatio: 0.001`. The committed baselines are the `*-linux.png` that image produces; **the only sanctioned update path is `make fe-visual-update`** (same image), and baseline diffs are reviewed as images in the PR. The container reuses the host's JS-only `node_modules` and the browsers baked into the image — no in-container install, so a dev's native binaries are untouched.
- **`make storybook`** runs the dev workshop; **`make storybook-build`** produces the static gallery.
- Page-level snapshots cover key screens (each wizard step, Channels, Suggest workspace, Settings) with a shared `mask()` helper for dynamic regions.

### 5.2 Determinism kit (what makes pixel-perfect honest)
- **`make fe-visual` and CI both run in the official Playwright Docker image** — one rasterizer, one font stack — against the static `storybook-static` build (no dev server, no HMR). macOS/GPU rendering is *not* the reference and is expected to drift; the image is. Launch flags pin the rest: `--disable-gpu` (software GL), `--force-color-profile=srgb`, `--disable-lcd-text` (grayscale, non-subpixel text AA).
- Self-hosted Geist (§2.2), loaded in `.storybook/preview`; each test waits for the story to render, then awaits `document.fonts.ready`.
- Visual-test mode: Playwright forces `prefers-reduced-motion: reduce`, and each test injects `animation: none` before the shot — reduced-motion only *fast-forwards* (`duration: 0.001ms`), which leaves an **infinite** spinner frozen at a random frame; `animation: none` freezes it at its initial state. Snapshots target the **component element** (`#storybook-root`), not the centered page, whose fractional margins shift text AA run-to-run.
- A rare residual sub-pixel AA jitter is de-flaked by **test retries** — a real visual diff (or an a11y violation) reproduces and still fails every attempt, so retries never mask a regression.
- **Injected fixed clock** (all times render from a frozen date) and shared `packages/fixtures` data — the same "test card" fixtures the web and future mobile stories use; no `Date.now`/random.

### 5.3 Accessibility gate
- **a11y is enforced from both sides:** `@storybook/addon-a11y` (axe-core) surfaces violations live in the workshop as you author, and the CI gate runs **`@axe-core/playwright`** over every story in `storybook-static` — the *same* Playwright pass as the visual suite, so pixels and axe share one browser layer. Zero serious/critical violations, WCAG AA contrast — this exact class of failure is what the gate exists to catch.
- **Contrast is enforced twice:** the token generator (§2.5) recomputes every published fg/bg pairing at build time and fails CI on regression; axe verifies the rendered result.
- **Live regions:** SSE-driven state changes that matter to a person — a channel flipping ON AIR, a checklist item locking, a proposal completing — are announced via a single polite `aria-live` announcer (never one per component; a chorus of live regions is its own accessibility bug).
- **Badges:** stylized uppercase mono text pairs with a sentence-case `aria-label` ("On air", "Backfilling 4 of 7") so screen readers speak words, not letter-spaced shouting.
- **Forced colors:** one story smoke pass under `forced-colors: active` (Windows High Contrast) — layouts must survive; the CRT flourishes must vanish.

These suites join **phase 13's gate** in the main doc's build plan.

---

## 6. UX standards ("sensible elements")

### Onboarding — welcoming, on-theme
- Voice: warm broadcast metaphor without cosplay. "Let's get you on the air." Checklist items *lock in* (`lock` green) as each signal is acquired; the webhook handshake shows a live "listening…" state that flips on receipt.
- **Failures never blame.** Every red check = plain-language cause + the exact fix + a deep link into the embedded docs (backend contract, main doc §13). No stack traces in the wizard, ever.
- Resume-safe: the wizard reflects `GET /v1/setup/status` truth, so a browser refresh loses nothing.
- The finale is the payoff: the guided first channel ends with its `ChannelCard` flipping to **ON AIR** — the product's promise, demonstrated in the first ten minutes.

### Config that makes sense
- Settings' curated pages are **General · Connections · AI · Defaults · Notifications · System · Security**,
  with **All settings** as the searchable escape hatch (config-design §5).
- Per-key provenance: env-pinned fields render locked with a "set via environment" chip; everything else is editable with configure → validate → save (main doc §13/§15). No inputs that look editable but aren't.
- The re-runnable connection checklist is embedded at the top of Connections — Settings *is* the troubleshooting console.
- Destructive actions live in an isolated danger zone with typed confirmation (`onair` styling).

### People — scan first, manage in context
- The route title is **People**. Its primary surface is a searchable roster with role and status filters; identity, authentication source, role, request usage, and enabled state remain scannable without opening a record.
- Selecting a person opens a right-edge detail sheet on desktop and a full-width overlay on narrow screens. Focus is trapped while it is open, Escape and the close control dismiss it, and focus returns to the selected roster item.
- Access, request policy, active sessions, local-password reset, and account status live in the detail sheet. Each edit persists immediately through its existing endpoint; there is no page-level Save action.
- Self-demotion and self-disable remain unavailable. Password controls appear only for Loomarr-local credentials, and account disablement is isolated in the sheet's danger zone.
- Search and filters operate on the loaded roster and expose an explicit no-results state. The mobile roster uses compact cards rather than a horizontally scrolling desktop table.
- Media-server import is a focused dialog launched from the page header. It keeps imported accounts visible but unavailable for selection, distinguishes server-disabled and server-admin accounts, and searches or filters only the loaded candidate set. Select/clear-all affects only the visible importable results and never silently selects a hidden account.
- The import dialog previews the initial Loomarr role (a media-server admin initially becomes a Loomarr admin) and explains the credential lifecycle: Emby/Jellyfin validates the supplied password, then Loomarr stores only a non-reversible Argon2id verifier after successful sign-in and refreshes it after later provider sign-ins so outage access remains available. Sync is an explicit operation for identities already imported; it never provisions an unselected account.
- When no media server is configured, the import action opens a useful explanation with a direct route to Connections settings rather than becoming a dead or failing control. Import and sync success refresh the roster; failed operations retain the selection and render their RFC 7807 problem details.
- Local-account creation is a focused dialog launched from the page header, not a permanent panel below the roster. It is the direct fallback for someone without a media-server account or invitation path: the administrator supplies a username and password, Loomarr stores only its Argon2id verifier, and Loomarr owns later password reset. Member is the default role; creating an administrator requires an explicit choice.
- The local-account dialog trims usernames and rejects an empty username or a password shorter than eight characters before sending its existing generated request. RFC 7807 problem details remain visible in the dialog. Success clears every field, restores the member default, closes the dialog, and refreshes the roster; Cancel, Escape, and the close control clear the password and return focus to the header action. The dialog is full-width within the mobile gutter and keeps its actions reachable without horizontal overflow.
- An imported person's detail keeps password reset unavailable because Loomarr cannot change the provider's password. Its copy distinguishes provider ownership from offline readiness: Emby/Jellyfin owns password changes, while successful provider sign-ins create or refresh Loomarr's non-reversible Argon2id verifier for outage login.
- **Invite person** is a focused admin dialog beside Create local account and Import. It chooses a
  reserved local username or one exact importable Library account, previews the selected Loomarr role
  (member by default; administrator explicit), and accepts an optional contact email. Creation does
  not activate a person. Its ready state names the identity, role, and expiry and offers Copy link,
  Show QR, Send email when configured, Regenerate, and Revoke without making email a prerequisite.
- The roster and detail sheet compose Invitation lifecycle with the latest email outcome: Pending,
  Email delivered, Email failed (with retry), Redeemed, Expired, or Revoked. Delivery failure never
  removes the Invitation and always preserves QR/copy fallback. Closing or cancelling clears the
  plaintext grant from component state; it never enters local/session storage, a query key, telemetry,
  error copy, stories, or fixtures.
- Invitation QR uses the same design-system presentation as device pairing, including a labelled
  copy alternative and scan guidance, but its language always says **person invitation** and never
  device pairing. Regeneration replaces the rendered matrix and announces the change; focus stays on
  the initiating control rather than jumping to the image.
- `/join` is a public focused route with no application chrome. It removes the bearer from visible
  URL/history into memory before rendering, shows reserved identity, role, credential path, and
  expiry, and requires an explicit Join action. Local redemption collects and confirms a password;
  imported redemption collects the Library password and authenticates the exact reserved account.
  Refresh after URL cleanup intentionally cannot recover the bearer and shows a safe restart state.
  Invalid, expired, revoked, already-used, and concurrent-loser grants share non-enumerating copy.
- `/forgot-password` and `/reset-password` use the same focused shell. The request confirmation is
  identical regardless of eligibility; the reset route follows the same bearer cleanup and explicit
  submission rules as `/join`. Imported-person copy directs password changes to Emby/Jellyfin only
  after an authenticated person views their own credential details, never from the public response.

### Empty, error, loading
- **Every list has an empty state with exactly one next action.** "Dead air — create your first channel." · "No clips yet — drop files in the filler folder or point MeTube at a playlist." · "Queue's clear — nothing awaiting approval."
- Errors: RFC 7807 `title` in a toast (sonner) for mutations, inline field errors via TanStack Form for forms; retry offered only where the operation is idempotent.
- Loading: **skeletons, not spinners**, for anything list-shaped; the word "Tuning…" is reserved for suggester generation. SSE keeps surfaces live so loading states are rare after first paint.

### Responsive posture
- Desktop remains the primary administrative surface, while mobile web and the Expo client provide
  first-class read, approve, Guide, and Watch journeys. TV is watching-first rather than a scaled
  desktop admin UI. Shared information hierarchy does not imply identical density or navigation.
- TV layouts use logical units and fill both 1920x1080 and 3840x2160 output. Overscan/safe-area
  padding lives inside an edge-to-edge surface and never produces an outer frame.
- **One page-edge contract.** Every top-level route begins with `PageHeader`; nested route navigation may precede it, but may not restyle it. The header owns the 24px horizontal gutter, 16px vertical gutter, bottom rule, one `<h1>`, bounded explanatory copy, and page-level actions/status. At mobile widths those actions stack below the copy so the title never collapses into a narrow text column. Entity-detail and focused workflow headers may add domain identity, but keep the same gutter and title scale.
- **One navigation treatment per level.** `AppShell` owns primary navigation and `NavTabs` owns every route-level tab bar, including nested Settings pages. Pages do not hand-build a third active-state treatment.

---

## 7. Deliverables & integration with the build plan

- **Shared-client migration P0-P8:** the authoritative sequence and adoption evidence are in
  [`engineering/plans/shared-client-platform.md`](engineering/plans/shared-client-platform.md).
  The 2026-09-03 maintainer decision authorizes Shield and Web parity migration under #970. Web
  route cohorts may run while physical Shield acceptance is pending once shared interfaces are
  stable; visual refinements and the other native clients remain later work.

- **Phase 1** (main doc; also add the `web/packages` layout to its repo-layout block): `web/` workspace skeleton + `packages/tokens` with generators + self-hosted fonts + the `fe-tokens` make target.
- **Phase 13**: everything else here. **Gate additions:** story coverage = 100% of **Layer-1 primitives AND Layer-2 components** (each has a co-located `*.stories.tsx`); visual baselines committed for all stories at both viewports; axe clean (`addon-a11y` `test: 'error'`); `fe-visual` green in the Playwright Docker image.
- **Library-health gate** (§5.1a, §5.1b), enforced by the same coverage test rather than by review habit:
  - every Layer-1 primitive has a story enumerating its variants;
  - every story `title` carries a group prefix — a bare `Loomarr/<Name>` fails, so the sidebar cannot silently flatten again as the library grows;
  - the `Design/*` pages exist and read their values from the **generated** token artifacts (a hand-typed hex in a swatch is the drift §2.5 exists to prevent);
  - a story whose args contain domain data imports them from `@loomarr/fixtures`.

  **Written as a gate on purpose.** Every finding in §5.1a/§5.1c was a rule the library outgrew, not a rule anyone broke: 46 components under one flat namespace, nine primitives with no story coverage, `StateBadge` forking `Badge`, 21 hand-rolled captions across three off-scale sizes. None of that was visible to a gate, so none of it surfaced until someone went looking — and the one finding that turned out to be a miscount (§5.1b) is itself the argument for gates over audits: a number nobody can re-derive is a number that will be wrong. A convention that only lives in prose decays at exactly the rate the library grows.
- **Makefile additions:** `fe-tokens`, `storybook`, `storybook-build`, `fe-visual`, `fe-visual-update` (join the AGENTS.md command contract).
- This doc is a **seed doc**: incorporate as `docs/frontend-design.md` during phase 14; the palette table also feeds the docs site's own styling.
- **Visual reference (authoritative for look):** the Claude Design prototypes ship in-repo at `design/loomarr-prototype-desktop.dc.html` and `design/loomarr-prototype-mobile.dc.html` — recreate them pixel-perfectly per the handoff README (match visual output, not internal structure). Two reconciliation deltas apply on top of the prototypes: badge text on tints uses the `-300` stops (the prototypes predate the contrast calibration), and `static-500` text is demoted to disabled/decorative. Gallery baselines (§5) are judged against the prototypes-plus-deltas.
