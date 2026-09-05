## ---- frontend (Phase 13) -------------------------------------------------

WEB := web

.PHONY: fe-install
fe-install: ## install the web workspace (pnpm)
	cd $(WEB) && pnpm install --frozen-lockfile

.PHONY: fe-tokens
fe-tokens: ## regenerate design-token artifacts from packages/tokens (CI diff must be empty)
	cd $(WEB) && pnpm --filter @loomarr/tokens generate

.PHONY: fe-tokens-verify
fe-tokens-verify: fe-tokens ## regenerated token artifacts must match committed
	@git diff --exit-code web/packages/tokens/generated

.PHONY: brand-assets
brand-assets: ## regenerate favicon, launcher, TV, and store artwork from the shared brand contract
	node scripts/generate-brand-assets.mjs

.PHONY: brand-assets-verify
brand-assets-verify: ## verify every platform brand derivative matches the shared brand contract
	node --check scripts/generate-brand-assets.mjs
	node scripts/check-brand-assets.mjs

.PHONY: fe-codegen
fe-codegen: ## regenerate tokens + orval api client from api/openapi.yaml
	cd $(WEB) && pnpm codegen

.PHONY: fe-api-codegen
fe-api-codegen: ## regenerate only the orval api client from api/openapi.yaml
	cd $(WEB) && pnpm api

.PHONY: fe-lint
fe-lint: ## Biome lint + format check (web/)
	cd $(WEB) && pnpm lint

.PHONY: fe-lint-fix
fe-lint-fix: ## Biome autofix — format + safe lint fixes (web/)
	cd $(WEB) && pnpm biome check --write

# FE_SHARD is a CI-only passthrough (`make fe FE_SHARD=1/2`), with the same empty-by-default
# contract as GO_SHARD and the visual suite's separately validated shard input. A local `make fe`
# runs the whole suite. The shard COUNT lives in ci.yml's `matrix.shard`.
#
# ⚠ ONLY apps/web is sharded, and that is not arbitrary. 166 of the 172 test files live there;
# the other three packages hold 12 between them. More importantly `packages/core` and
# `packages/tokens` run plain `vitest run` WITHOUT --passWithNoTests, so any shard that handed
# them zero files would exit non-zero — a red CI caused purely by the split, appearing only at
# higher shard counts. They stay unsharded, which is both safe and free.
#
# ⚠ NO `--` BEFORE THE FLAG. `pnpm --filter X test -- --shard=1/2` passes `-- --shard=1/2` to
# vitest, which reads it as a FILENAME FILTER, matches nothing, falls back to everything, and
# exits 0 having run all 166 files. Measured while writing this: the `--` form reported "166
# passed" for BOTH shards — a green, doubled, entirely unsharded run that looks exactly like a
# working one. The form below reports 83 and 83.
FE_SHARD ?=
FE_SHARD_ARG := $(if $(FE_SHARD),--shard=$(FE_SHARD),)

.PHONY: fe
fe: ## biome + codegen + typecheck + unit tests + embedded SPA + storybook gallery
	cd $(WEB) && pnpm codegen && pnpm lint && pnpm --filter @loomarr/web... -r --parallel typecheck \
	  && pnpm --filter @loomarr/web... --filter '!@loomarr/web' -r --parallel test \
	  && pnpm --filter @loomarr/web test $(FE_SHARD_ARG) \
	  && pnpm --filter @loomarr/web build && pnpm --filter @loomarr/web build-storybook
	@touch internal/web/dist/.gitkeep

.PHONY: clients
clients: brand-assets-verify ## lint, test, typecheck, and bundle the shared browser, mobile, and TV scaffold
	cd $(WEB) && pnpm exec biome check apps/mobile apps/tv apps/web/client-platform-proof.html \
	  apps/web/src/client-platform-proof apps/web/tests/client-platform-proof.ssr.test.tsx \
	  apps/web/vite.client-platform.config.ts \
	  .rnstorybook native-stories packages/design-system packages/ui turbo.json \
	  && pnpm imports:check && pnpm lint:boundaries && pnpm native-storybook:check && pnpm clients:check

CLIENT_APP ?= mobile
.PHONY: client-android-debug
client-android-debug: fe-api-codegen ## memory-bounded arm64 debug build (CLIENT_APP=mobile|tv)
	cd $(WEB) && ./scripts/build-android-client.sh $(CLIENT_APP)

SHIELD_VERSION ?=
.PHONY: shield-sideload
shield-sideload: fe-api-codegen ## build and inspect a signed permanent-identity Shield APK (SHIELD_VERSION=x.y.z)
	@test -n "$(SHIELD_VERSION)" || { echo 'SHIELD_VERSION is required' >&2; exit 2; }
	cd $(WEB) && ./scripts/build-shield-sideload.sh "$(SHIELD_VERSION)"

.PHONY: shield-sideload-test
shield-sideload-test: fe-api-codegen ## build with an ephemeral key, then clean-install and launch on the TV emulator
	cd $(WEB) && ./scripts/test-shield-sideload.sh "$(if $(SHIELD_VERSION),$(SHIELD_VERSION),0.1.0-beta.1)"

.PHONY: client-apple-simulator
client-apple-simulator: fe-api-codegen ## build and launch an Apple simulator proof (CLIENT_APP=mobile|tv; macOS)
	cd $(WEB) && ./scripts/test-apple-client.sh $(CLIENT_APP)

.PHONY: storybook
storybook: ## Storybook dev workshop on this worktree's isolated port
	@eval "$$(./scripts/dev-env.sh export)"; \
	  echo "storybook: http://localhost:$$LOOMARR_STORYBOOK_PORT"; \
	  cd $(WEB) && pnpm --filter @loomarr/web exec storybook dev -p "$$LOOMARR_STORYBOOK_PORT" --no-open

.PHONY: storybook-build
storybook-build: ## offline storybook-static build (what fe-visual snapshots)
	cd $(WEB) && pnpm --filter @loomarr/web build-storybook

# Playwright Docker image = the reference rasterizer (§5.2): Linux + software rendering,
# deterministic and identical to CI. Baselines are the *-linux.png it writes. Keep the
# tag pinned to the @playwright/test version in web/apps/web/package.json so the image's
# browsers match exactly. The container reuses the host's (JS-only) node_modules read
# through the bind mount and the browsers baked into the image — no in-container install,
# so the host's macOS binaries are never touched.
# `scripts/run-playwright-container.sh` owns the pinned image and every Docker argument before it.
# Make deliberately interpolates no caller-controlled value into these recipes: environment and
# command-line variables therefore cannot add Docker flags, replace the image, or escape into a
# second host command. The runner fixes CI=1, safely forwards GITHUB_ACTIONS as one array element,
# and validates the optional shard before adding it after the image boundary.
#
# Old public variables are explicitly unexported so recursive command-line values cannot execute
# merely while Make constructs a recipe environment. They are no longer supported interfaces.
unexport PW_DOCKER_USER PW_CI PW_REAL_CI PW_IMAGE PW_SHARD

.PHONY: ensure-playwright-image
ensure-playwright-image: ## use a cached Playwright image or pull it with bounded retries
	./scripts/run-playwright-container.sh ensure

# LOOMARR_PLAYWRIGHT_SHARD is an environment-only CI passthrough
# (`LOOMARR_PLAYWRIGHT_SHARD=--shard=1/4 make fe-visual`). Empty by default, so a local
# `make fe-visual` still runs the WHOLE suite. The runner accepts only N/M positive integers with
# N <= M; it never evaluates the value as Make or shell syntax.
#
# ⚠ The shard COUNT lives in ci.yml's `matrix.shard` and nowhere else — the denominator is
# derived there from `strategy.job-total`. Do not write a specific N into this file: the
# "1/2" that used to be in the line above outlived the 2-shard config it described.

.PHONY: fe-visual
fe-visual: storybook-build ensure-playwright-image ## Playwright visual + a11y over storybook-static, in the pinned Docker image (§5.2)
	./scripts/run-playwright-container.sh visual

.PHONY: fe-visual-update
fe-visual-update: storybook-build ensure-playwright-image ## regenerate the committed Linux baselines in the Docker image (sanctioned update path)
	./scripts/run-playwright-container.sh visual-update

# The e2e suite drives the REAL embedded SPA build, which Vite writes to
# internal/web/dist — OUTSIDE web/. So unlike fe-visual it mounts the repo ROOT, and
# runs from /work/web/apps/web (node_modules still resolves up to /work/web).
.PHONY: e2e
e2e: fe-build ensure-playwright-image ## wizard e2e smoke vs a mocked backend, in the pinned Docker image (13.3 gate)
	./scripts/run-playwright-container.sh e2e

.PHONY: tuner-e2e
tuner-e2e: fe-build ensure-playwright-image ## 100-Channel tuner controller matrix in Chromium, Firefox, and WebKit (§9.1)
	./scripts/run-playwright-container.sh tuner-e2e

.PHONY: tuner-e2e-host
tuner-e2e-host: fe-build ## 100-Channel tuner controller matrix in host-installed browsers (§9.1)
	cd web/apps/web && node_modules/.bin/playwright test --config=playwright.tuner.config.ts

.PHONY: e2e-update
e2e-update: fe-build ensure-playwright-image ## regenerate the committed e2e page snapshots (sanctioned update path)
	./scripts/run-playwright-container.sh e2e-update

# Just the SPA build the e2e suite serves (a subset of `make fe`, so the gate doesn't
# rebuild Storybook or re-run the unit suite to check a flow).
.PHONY: fe-build
fe-build:
	cd $(WEB) && pnpm codegen && pnpm --filter @loomarr/web build
