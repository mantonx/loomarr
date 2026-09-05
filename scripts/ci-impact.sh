#!/usr/bin/env bash
# Classify changed repository paths into the smallest trustworthy CI gate set.
#
# Interface: pass paths as arguments, or one path per line on stdin. Pass --all
# when the caller cannot establish a trustworthy diff base. The command prints
# stable key=true|false records suitable for GitHub outputs. An unknown path
# fails closed by selecting every gate.
set -euo pipefail

readonly GATES=(
  contracts go go_full rust postgres web clients apple_mobile apple_tv
  expo_android_mobile expo_android_tv visual e2e tuner image docs agent android policy
)

selected=()
strict=false
unknown=false
force_all=false
for ((i = 0; i < ${#GATES[@]}; i++)); do
  selected[i]=false
done

select_gate() {
  local gate="$1" i
  for ((i = 0; i < ${#GATES[@]}; i++)); do
    if [[ "${GATES[$i]}" == "$gate" ]]; then
      selected[i]=true
      return
    fi
  done
  printf 'ci-impact: internal error: unknown gate %q\n' "$gate" >&2
  exit 2
}

select_all() {
  local i
  for ((i = 0; i < ${#GATES[@]}; i++)); do
    selected[i]=true
  done
}

select_all_native_clients() {
  select_gate apple_mobile
  select_gate apple_tv
  select_gate expo_android_mobile
  select_gate expo_android_tv
}

classify() {
  local path="$1"
  local known=false

  # Product Go. Release images compile and embed these source families.
  if [[ "$path" == cmd/releaseverify/*.go || "$path" == internal/releaseverify/* ]]; then
    known=true
    select_gate go
    select_gate policy
  elif [[ "$path" == *.go || "$path" == go.mod || "$path" == go.sum ]]; then
    known=true
    select_gate contracts
    select_gate go
    # The first Postgres activation is deliberately conservative. The integration
    # target compiles store, backend-transition, and app tests plus their broad Go
    # dependency closure; until a dependency-aware Postgres selector is proven in
    # shadow, every Go source change retains this gate.
    select_gate postgres
    select_gate image
    case "$path" in
      cmd/loomarr/*|internal/app/*|internal/testkit/*|go.mod|go.sum)
        select_gate go_full
        ;;
    esac
  fi

  case "$path" in
    Cargo.toml|Cargo.lock|rust-toolchain.toml|deny.toml|rust/*)
      known=true
      select_gate rust
      select_gate image
      ;;
    internal/store/migrations/*)
      known=true
      select_gate contracts
      select_gate go
      select_gate go_full
      select_gate postgres
      select_gate image
      ;;
    web/apps/mobile/*)
      known=true
      select_gate clients
      select_gate apple_mobile
      select_gate expo_android_mobile
      ;;
    web/apps/tv/*)
      known=true
      select_gate clients
      select_gate apple_tv
      select_gate expo_android_tv
      select_gate android
      ;;
    web/packages/lan-discovery-native/*)
      known=true
      select_gate clients
      select_gate apple_tv
      select_gate expo_android_tv
      select_gate android
      ;;
    web/packages/design-system/*|web/packages/ui/*|web/packages/ui-tv/*)
      known=true
      select_gate clients
      select_all_native_clients
      # Browser Storybook stories render these universal presentation packages
      # directly. Their output is therefore part of the committed visual contract.
      select_gate visual
      select_gate android
      ;;
    web/packages/api/*|web/packages/core/*|web/packages/fixtures/*)
      known=true
      select_gate web
      select_gate clients
      select_all_native_clients
      select_gate visual
      select_gate e2e
      select_gate tuner
      select_gate image
      select_gate android
      ;;
    web/packages/player/*)
      known=true
      select_gate web
      select_gate clients
      select_all_native_clients
      # The browser and native adapters share one transport contract. Browser playback
      # reaches the tuner matrix and production image; the native adapters reach every
      # supported client build and Android TV bundle.
      select_gate tuner
      select_gate image
      select_gate android
      ;;
    web/package.json|web/pnpm-lock.yaml|web/pnpm-workspace.yaml|web/.gitignore|web/biome.json|web/tsconfig.base.json)
      known=true
      select_gate web
      select_gate clients
      select_all_native_clients
      select_gate image
      select_gate visual
      select_gate e2e
      select_gate tuner
      select_gate android
      ;;
    web/.dependency-cruiser.cjs|web/turbo.json|web/.rnstorybook/*|web/apps/web/client-platform-proof.html|web/apps/web/src/client-platform-proof/*|web/apps/web/tests/client-platform-proof.*|web/apps/web/vite.client-platform.config.ts)
      known=true
      select_gate clients
      ;;
    web/scripts/test-apple-client.sh|web/scripts/test-apple-client.test.mjs|web/scripts/test-apple-client-cache-test.sh|web/scripts/apple-simulator.xcconfig|web/scripts/apple-compilation-cache.xcconfig|web/scripts/validate-apple-compilation-cache.sh|web/scripts/validate-apple-compilation-cache-test.sh|web/scripts/filter-react-native-pods-notice.awk|web/scripts/filter-react-native-pods-notice.test.mjs)
      known=true
      select_gate contracts
      select_gate apple_mobile
      select_gate apple_tv
      ;;
    web/scripts/build-android-client.sh|web/scripts/with-memory-safe-android-build.cjs|web/scripts/with-memory-safe-android-build.test.cjs)
      known=true
      select_gate contracts
      select_gate expo_android_mobile
      select_gate expo_android_tv
      select_gate android
      ;;
    web/scripts/check-imports.mjs|web/scripts/check-imports.test.mjs)
      known=true
      select_gate clients
      ;;
    web/scripts/*)
      known=true
      select_gate contracts
      select_gate web
      select_gate clients
      select_all_native_clients
      select_gate image
      select_gate visual
      select_gate e2e
      select_gate tuner
      select_gate android
      ;;
    web/*)
      known=true
      select_gate web
      select_gate image
      case "$path" in
        web/apps/web/src/*.test.ts|web/apps/web/src/*.test.tsx|web/apps/web/src/*.spec.ts|web/apps/web/src/*.spec.tsx)
          ;;
        web/apps/web/src/*)
          # Storybook can reach any shipping runtime module through an alias import. Until a
          # committed, generator-verified dependency closure exists, runtime source is the safe
          # visual boundary; unit-test-only modules above are not part of that graph.
          select_gate visual
          ;;
      esac
      case "$path" in
        web/apps/web/src/*.test.ts|web/apps/web/src/*.test.tsx|web/apps/web/src/*.spec.ts|web/apps/web/src/*.spec.tsx|web/apps/web/src/*.stories.ts|web/apps/web/src/*.stories.tsx)
          ;;
        web/apps/web/src/*)
          # The tuner matrix imports the shipping SPA and exercises its real HLS controller.
          # Until a committed dependency closure proves a narrower boundary, every runtime
          # source can change that path; unit/spec/story-only modules are not shipped.
          select_gate tuner
          ;;
      esac
      case "$path" in
        web/packages/tokens/*|web/apps/web/public/*|web/apps/web/tests/visual/*|*.stories.ts|*.stories.tsx|*.snap|*.css)
          select_gate visual
          ;;
      esac
      case "$path" in
        web/packages/tokens/*)
          # Tokens feed the production stylesheet consumed by the real tuner SPA build.
          select_gate tuner
          ;;
      esac
      case "$path" in
        web/apps/web/tests/e2e/*|web/apps/web/tests/smoke/*|web/apps/web/src/auth/*|web/apps/web/src/routes/*|web/apps/web/src/wizard/*)
          select_gate e2e
          ;;
      esac
      case "$path" in
        web/apps/web/tests/e2e/tuner-*|web/apps/web/src/channels/channel-watch/*|web/apps/web/src/channels/guide-*|web/apps/web/src/channels/tuner-*|web/apps/web/src/channels/use-channel-tuner/*|web/apps/web/src/channels/use-hls-player/*)
          select_gate tuner
          ;;
      esac
      case "$path" in
        web/package.json|web/pnpm-lock.yaml|web/apps/web/package.json|web/apps/web/playwright*|web/apps/web/.storybook/*)
          select_gate visual
          select_gate e2e
          select_gate tuner
          ;;
        web/apps/web/components.json|web/apps/web/drive.mjs|web/apps/web/index.html|web/apps/web/tsconfig.json|web/apps/web/tsr.config.json|web/apps/web/vite.config.ts)
          select_gate visual
          select_gate e2e
          select_gate tuner
          ;;
      esac
      ;;
    api/openapi.yaml)
      known=true
      select_gate contracts
      select_gate go
      select_gate go_full
      select_gate web
      select_gate clients
      select_all_native_clients
      select_gate visual
      select_gate e2e
      select_gate tuner
      select_gate image
      select_gate android
      ;;
    api/vendor/*)
      known=true
      select_gate contracts
      select_gate go
      ;;
    android/*)
      known=true
      select_gate android
      ;;
    store-listing/android-tv/*)
      known=true
      select_gate android
      ;;
    Dockerfile|LICENSE|THIRD_PARTY_NOTICES.md)
      known=true
      select_gate contracts
      select_gate image
      ;;
    observability/*)
      known=true
      select_gate contracts
      ;;
    .dockerignore)
      known=true
      select_gate image
      ;;
    docs/help/*)
      known=true
      select_gate contracts
      select_gate go
      select_gate go_full
      select_gate image
      select_gate docs
      ;;
    docs/design.md|docs/configuration.md|docs/dev/ci.md|docs/dev/commands.md|docs/install/*|README.md)
      known=true
      select_gate docs
      select_gate policy
      ;;
    docs/*|CHANGELOG.md|CODE_OF_CONDUCT.md|CONTRIBUTING.md|SECURITY.md|CLAUDE.md|AGENTS.md|CONTEXT.md|PROGRESS.md|docs-site/*|.agents/*|.claude/*|.vale|.vale/*|.vale.ini|lychee.toml|.markdownlint*|.github/CODEOWNERS|.github/ISSUE_TEMPLATE/*|.github/PULL_REQUEST_TEMPLATE.md)
      known=true
      select_gate docs
      ;;
    design/*)
      known=true
      select_gate docs
      ;;
	internal/testkit/postgresimage/image.go|internal/testkit/postgresimage/image.txt)
		known=true
		select_gate contracts
		select_gate go
		select_gate go_full
		select_gate postgres
		select_gate image
		select_gate policy
		;;
	internal/testkit/ryukimage/image.txt)
		known=true
		select_gate postgres
      select_gate policy
      ;;
    internal/testkit/fixtures/*)
      known=true
      select_gate go
      select_gate go_full
      ;;
    internal/fillereval/corpus/*)
      known=true
      select_gate contracts
      select_gate go
      ;;
    internal/eval/testdata/*)
      known=true
      select_gate contracts
      select_gate go
      ;;
    internal/recommend/testdata/*)
      known=true
      select_gate contracts
      select_gate go
      ;;
    internal/plannerreference/testdata/*)
      known=true
      select_gate contracts
      select_gate go
      ;;
    internal/fillercorpus/corpus/*)
      known=true
      select_gate contracts
      select_gate go
      ;;
    internal/fillereval/*.md)
      known=true
      select_gate docs
      ;;
    internal/web/dist/*)
      known=true
      select_gate contracts
      select_gate go
      select_gate go_full
      select_gate image
      select_gate web
      ;;
    brand-assets.lock.json)
      known=true
      select_gate clients
      select_gate android
      ;;
    scripts/*)
      known=true
      case "$path" in
        scripts/agent*) select_gate agent; select_gate policy ;;
        scripts/apple-compilation-cache*) select_gate contracts; select_gate apple_mobile; select_gate apple_tv; select_gate policy ;;
        scripts/ensure-container-image.sh) select_gate contracts; select_gate postgres; select_gate visual; select_gate e2e; select_gate tuner; select_gate policy ;;
        scripts/run-playwright-container.sh) select_gate contracts; select_gate visual; select_gate e2e; select_gate tuner; select_gate policy ;;
        # Android ancestry reuse evaluates intervening paths with this classifier. Until the
        # produced evidence binds its identity, the authority cannot exempt its own changes.
        scripts/ci-impact.sh) select_gate android; select_gate policy ;;
        scripts/ci-impact*|scripts/ci-dispatch-scope*|scripts/ci-run-metrics*|scripts/ci-merge-queue-policy*|scripts/testdata/ci-*) select_gate policy ;;
        scripts/dev-*) select_gate contracts; select_gate agent ;;
        # The validator is the other half of that authority and likewise requires fresh evidence.
        scripts/validate-android-release-source.sh|scripts/download-android-ci-artifact.sh|scripts/sign-android-ci-artifact.sh) select_gate android; select_gate policy ;;
        scripts/android-*.sh|scripts/build-android-beta.sh|scripts/check-android-release-env.sh|scripts/generate-android-tv-brand.sh|scripts/publish-android-beta.sh|scripts/test-android-release.sh|scripts/test-android-release-emulator.sh) select_gate android ;;
        scripts/generate-brand-assets.mjs|scripts/check-brand-assets.mjs) select_gate clients; select_gate android ;;
        scripts/check-fe-bundle.mjs) select_gate web; select_gate image ;;
        *) select_gate contracts ;;
      esac
      ;;
    mk/agent.mk)
      known=true
      select_gate contracts
      select_gate agent
      select_gate policy
      ;;
    mk/check.mk)
      known=true
      select_gate contracts
      select_gate go
      select_gate go_full
      select_gate rust
      select_gate postgres
      select_gate policy
      ;;
    mk/eval.mk)
      known=true
      select_gate contracts
      select_gate go
      select_gate policy
      ;;
    mk/build.mk)
      known=true
      select_gate contracts
      select_gate go
      select_gate rust
      select_gate image
      select_gate agent
      select_gate policy
      ;;
    mk/store.mk)
      known=true
      select_gate contracts
      select_gate go
      select_gate postgres
      select_gate policy
      ;;
    mk/contracts.mk)
      known=true
      select_gate contracts
      select_gate go
      select_gate docs
      select_gate policy
      ;;
    mk/docs.mk)
      known=true
      select_gate docs
      select_gate policy
      ;;
    mk/frontend.mk)
      known=true
      select_gate web
      select_gate clients
      select_all_native_clients
      select_gate visual
      select_gate e2e
      select_gate tuner
      select_gate image
      select_gate android
      select_gate policy
      ;;
    mk/smoke.mk)
      known=true
      select_gate contracts
      select_gate policy
      ;;
    mk/android.mk)
      known=true
      select_gate android
      select_gate policy
      ;;
    Makefile)
      known=true
      select_all
      ;;
    .github/workflows/ci.yml)
      known=true
      select_gate policy
      ;;
    .github/workflows/ci-agent.yml)
      known=true
      select_gate agent
      select_gate policy
      ;;
    .github/workflows/ci-rust-contracts.yml|.github/workflows/ci-image-certification.yml)
      known=true
      select_gate rust
      select_gate policy
      ;;
    .github/workflows/ci-go-contracts.yml)
      known=true
      select_gate contracts
      select_gate policy
      ;;
    .github/workflows/ci-go.yml)
      known=true
      select_gate go
      select_gate policy
      ;;
    .github/workflows/ci-postgres.yml)
      known=true
      select_gate postgres
      select_gate policy
      ;;
    .github/workflows/ci-frontend.yml)
      known=true
      select_gate web
      select_gate policy
      ;;
    .github/workflows/ci-clients.yml)
      known=true
      select_gate clients
      select_gate policy
      ;;
    .github/workflows/ci-apple-mobile.yml)
      known=true
      select_gate apple_mobile
      select_gate policy
      ;;
    .github/workflows/ci-apple-tv.yml)
      known=true
      select_gate apple_tv
      select_gate policy
      ;;
    .github/workflows/ci-apple-cache-validation.yml)
      known=true
      select_gate apple_mobile
      select_gate apple_tv
      select_gate policy
      ;;
    .github/workflows/apple-compilation-cache.yml)
      known=true
      select_gate apple_mobile
      select_gate apple_tv
      select_gate policy
      ;;
    .github/workflows/ci-playwright.yml)
      known=true
      select_gate visual
      select_gate e2e
      select_gate policy
      ;;
    .github/workflows/ci-tuner.yml)
      known=true
      select_gate tuner
      select_gate policy
      ;;
    .github/workflows/ci-image.yml)
      known=true
      select_gate image
      select_gate policy
      ;;
    .github/workflows/ci-docs.yml)
      known=true
      select_gate docs
      select_gate policy
      ;;
    .github/workflows/ci-android.yml)
      known=true
      select_gate android
      select_gate policy
      ;;
    .github/workflows/*)
      known=true
      select_gate policy
      ;;
    renovate.json|.github/dependabot.yml) # retired-ok: deleting the legacy config must still select policy gates
	  known=true
	  select_gate contracts
	  select_gate policy
	  ;;
    docker/*|.air.toml|.env.example|.golangci.yml|.node-version|.editorconfig|.gitignore|.vscode/*|.github/actionlint.yaml|.github/actionlint.yml|skills-lock.json)
      known=true
      select_gate contracts
      case "$path" in
        docker/compose.dev*|.node-version) select_gate agent ;;
        docker/compose.yaml) select_gate go; select_gate go_full ;;
      esac
      ;;
  esac

  if [[ "$known" == false ]]; then
    printf 'ci-impact: unknown path %q; selecting every gate\n' "$path" >&2
    unknown=true
    select_all
  fi
}

while (($#)); do
  case "$1" in
    --check-known)
      strict=true
      shift
      ;;
    --all)
      force_all=true
      shift
      ;;
    --)
      shift
      break
      ;;
    *) break ;;
  esac
done

if [[ "$force_all" == true ]]; then
  if (($#)); then
    printf 'ci-impact: --all does not accept paths\n' >&2
    exit 2
  fi
  select_all
elif (($#)); then
  for path in "$@"; do
    [[ -n "$path" ]] && classify "$path"
  done
else
  while IFS= read -r path; do
    [[ -n "$path" ]] && classify "$path"
  done
fi

if [[ "$strict" == true && "$unknown" == true ]]; then
  exit 1
fi

for ((i = 0; i < ${#GATES[@]}; i++)); do
  printf '%s=%s\n' "${GATES[$i]}" "${selected[$i]}"
done
