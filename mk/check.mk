## ---- proportional local verification -----------------------------------

.PHONY: verify
verify: ## run affected local evidence; SCOPE=all runs the comprehensive repository audit
	@case "$(SCOPE)" in "") BASE="$(or $(BASE),origin/main)" ./scripts/agent.sh verify ;; all) ./scripts/agent.sh verify-all ;; *) echo "verify: unknown SCOPE=$(SCOPE) (want empty or all)" >&2; exit 2 ;; esac

## ---- explicit complete audit --------------------------------------------

.PHONY: check-static
check-static: rust-check fmt shellcheck privacy-verify observability-verify vet platform-vet tags-verify vet-tags lint agent-harness-test compose-verify release-verify go-race-verify ## repository contracts without the unit-test suite (CI runs this once beside test shards)

.PHONY: observability-verify
observability-verify: ## validate the metric manifest, Prometheus rules, and Grafana provisioning (needs Docker and jq)
	@./scripts/verify-observability.sh
	@./scripts/observability-dev-test.sh

.PHONY: rust-check rust-test-worker rust-audit rust-fuzz
rust-check: rust-test-worker ## format, lint, build, and test the required Rust image worker
	$(CARGO) fmt --all -- --check
	$(CARGO) clippy --workspace --all-targets --all-features --locked -- -D warnings
	$(CARGO) test --workspace --all-features --locked

rust-test-worker: ## build the debug Rust image worker required by Go unit tests
	LOOMARR_RELEASE=dev $(CARGO) build --locked -p loomarr-image

rust-audit: ## check Rust advisories, licences, and dependency sources (needs cargo-deny)
	$(CARGO) deny check advisories licenses sources
	$(CARGO) deny --manifest-path rust/loomarr-image/fuzz/Cargo.toml check advisories licenses sources

rust-fuzz: ## fuzz the bounded Rust image protocol/decoder; optional FUZZ_SECONDS (needs nightly + cargo-fuzz)
	@seconds="$${FUZZ_SECONDS:-60}"; \
	  cd rust/loomarr-image; \
	  $(CARGO) +$(RUST_FUZZ_TOOLCHAIN) fuzz run protocol_decoder -- -max_total_time="$$seconds" -max_len=1048576

.PHONY: fmt
fmt: ## gofmt -l (fails if any file needs formatting)
	@out=$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*')); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

.PHONY: shellcheck
shellcheck: ## shellcheck every repository shell script
	shellcheck -S style $(SHELL_SCRIPTS)

.PHONY: privacy-verify
privacy-verify: ## captured private fixture literals must not re-enter the tracked tree
	@./scripts/check-private-fixtures.sh

.PHONY: vet
vet: ## go vet
	$(GO) vet $(PKG)

.PHONY: platform-vet
platform-vet: ## go vet the opposite Linux/macOS target to catch platform-only compile drift
	@case "$$($(GO) env GOOS)" in \
		darwin) GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) vet $(PKG) ;; \
		linux) GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) vet $(PKG) ;; \
		*) GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) vet $(PKG); \
		   GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) vet $(PKG) ;; \
	esac

.PHONY: vet-tags
vet-tags: ## go vet over custom-tagged sources
	$(GO) vet -tags '$(CUSTOM_TAGS)' $(PKG)

.PHONY: tags-verify
# Runs BEFORE vet-tags in `check`: it is ~0.1s and it validates the very list vet-tags consumes,
# so a missing tag is named before anything is compiled with an incomplete one.
tags-verify: ## the Makefile's TAGS list matches every //go:build tag in the tree, both ways
	@TAGS='$(TAGS)' ./scripts/check-tags.sh

.PHONY: lint
# ⚠ `--build-tags` WIDENS the file set, it never narrows it: files with no `//go:build` line
# compile under every tag set. Verified there is no negated constraint (`!ffmpeg`) anywhere in
# the tree — one of those WOULD be dropped by this flag, silently creating the blind spot this
# change exists to close. Re-check before adding a negated tag.
lint: ## golangci-lint v2 (run via `go run` so no global install needed)
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run --build-tags '$(TAGS_CSV)' $(PKG)

.PHONY: test
test: rust-test-worker eval-contract ## unit tests with their required Rust worker (never touch the network — §19)
# ⚠ **-timeout is set explicitly because Go's default is 10m PER PACKAGE and `internal/api` grew
# past it.** Measured 2026-08-09: that package alone is 267s locally under `-race`, and a CI runner
# is roughly twice as slow — so it tripped the default and the job died with `panic: test timed out
# after 10m0s`. The dump named one test at `(0s)`, which is the tell that nothing HUNG: tests were
# still starting when the alarm fired, and the package was simply long. A genuine hang shows the
# stuck test with a large duration beside it.
#
# ⚠ This is an infrastructure limit, not the gate — raising it weakens nothing, because the gate is
# the assertions and every one of them still runs. What it must not do is hide growth: `internal/api`
# is ~500 tests each paying a fresh SQLite open plus migrations, and the fix when this bites again is
# to share that setup, NOT to raise the number a second time.
#
# GO_SHARD is a CI-only passthrough (`make test GO_SHARD=1/2`): EMPTY by default, so a local
# `make test` — and `make verify SCOPE=all` — runs the whole tree. Sharding must
# never be implicit, or someone runs a fraction of the gate and reads the green as the whole thing.
# The shard COUNT lives in ci.yml's `matrix.shard`; see scripts/go-shard.sh for the split.
#
# ⚠ `&&`, not a `$(shell ...)` expansion. `$(shell)` swallows a non-zero exit and yields the empty
# string, and `go test` with NO packages exits 0 — so a bad GO_SHARD would have produced a silent
# green over zero tests, which is the exact failure this sharding must not be able to cause. Here a
# failing helper fails the recipe: `pkgs=$(...)` carries the substitution's status into the `&&`.
#
# `-race` is scoped, not tree-wide: scripts/go-race-policy.sh splits this shard's packages into the
# ones that run UNDER -race (the default — everything with any concurrency, incl. every httptest
# server) and a short, verified opt-out set of concurrency-free config/table-test packages that run
# without it, to skip the detector's ~2-3x overhead where no race is possible. `make go-race-verify`
# guards the opt-out list; the split is per-shard so it composes with GO_SHARD.
#
# ⚠ Each `go test` runs ONLY IF its list is non-empty. A shard whose opt-out subset is empty is
# normal (data-dependent on the split), but `go test` with no packages exits 0 — the same silent-
# green trap as above — so an empty list must SKIP the invocation, never invoke `go test` with none.
	@set -e; \
	pkgs="$$(./scripts/go-shard.sh $(GO_SHARD))"; \
	race_pkgs="$$(printf '%s\n' "$$pkgs" | ./scripts/go-race-policy.sh --race)"; \
	norace_pkgs="$$(printf '%s\n' "$$pkgs" | ./scripts/go-race-policy.sh --no-race)"; \
	if [ -n "$$race_pkgs" ]; then $(GO) test -race -timeout 25m $$race_pkgs; fi; \
	if [ -n "$$norace_pkgs" ]; then $(GO) test -timeout 25m $$norace_pkgs; fi

.PHONY: go-shard-verify
go-shard-verify: ## the GO_SHARD split must be a PARTITION of go list ./... (CI red on drift)
# ⚠ THIS IS A REAL GATE, not a sanity check. Sharding is the one optimization here that can
# QUIETLY SHRINK the suite: a split that drops a package does not fail — those tests simply never
# run, every shard reports success, and CI is green over code it never executed. Nothing else in
# the pipeline would notice. SHARDS must match ci.yml's `matrix.shard` count; CI passes it from
# `strategy.job-total` so the two cannot drift apart.
	@./scripts/go-shard.sh --verify $(or $(SHARDS),2)

.PHONY: go-race-verify
go-race-verify: ## every -race opt-out (scripts/go-race-policy.sh RACE_OFF) must be a real package
# ⚠ A GUARD, not decoration. The opt-out list is FAIL-SAFE by construction — race stays ON for
# anything not listed, so a forgotten new package is race-checked, never silently skipped. What this
# catches is the other drift: a RACE_OFF entry that no longer names a real package (renamed, moved,
# deleted). That entry would opt nothing out — harmless for coverage, but the list would be lying
# about what it excludes, so CI goes red until it is corrected.
	@./scripts/go-race-policy.sh --verify

.PHONY: test-ffmpeg
test-ffmpeg: ## media tests that EXECUTE ffmpeg (needs ffmpeg+ffprobe; not in comprehensive verification)
	$(GO) test -tags ffmpeg ./internal/mediatools/ ./internal/testkit/ -v
	$(GO) test -tags ffmpeg -run 'TestLive' ./internal/playout/ ./internal/api/ -v

.PHONY: eval-contract eval eval-cert eval-planner-cert eval-planner-smoke planner-tool-diagnostic channel-recommend-cert channel-recommend-cert-dry-run channel-recommend-compare eval-planner-compare eval-matrix filler-bakeoff-ollama filler-bakeoff-openrouter filler-bakeoff-transcribe filler-corpus-archive filler-corpus-cdc filler-corpus-commons filler-corpus-direct filler-corpus-download filler-corpus-inventory filler-corpus-lock filler-corpus-loc filler-corpus-met filler-corpus-nasa filler-corpus-pilot filler-corpus-pilot-rights-lock filler-corpus-pilot-rights-review filler-corpus-prepare filler-corpus-review filler-corpus-review-package filler-eval-contract filler-eval-cert filler-media-integrity-prepare filler-media-integrity-score filler-openrouter-snapshot filler-visual-corpus-nomination-prepare filler-visual-corpus-nomination-lock
