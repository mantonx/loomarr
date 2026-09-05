## ---- agent / worktree harness --------------------------------------------

.PHONY: agent-start agent-status agent-renew agent-prune agent-stop agent-env agent-baseline agent-verify agent-worktree agent-gc bootstrap doctor agent-harness-test agent-assets-verify
agent-start: ## register this worktree and its seams (TASK=... CLAIMS=a,b; optional DEPENDS_ON=task)
	@./scripts/agent.sh start "$(TASK)" "$(CLAIMS)" "$(DEPENDS_ON)"

agent-status: ## list tool-neutral agent sessions across every worktree
	@./scripts/agent.sh status

agent-renew: ## renew this worktree's claim lease (AGENT_LEASE_HOURS=4)
	@./scripts/agent.sh renew

agent-prune: ## remove expired entries from the shared agent registry
	@./scripts/agent.sh prune

agent-stop: ## release this worktree's task and shared-output claims
	@./scripts/agent.sh stop

agent-env: ## show this worktree's isolated ports, database, compose project, and artifact path
	@./scripts/agent.sh env

agent-baseline: ## share one cached make verify SCOPE=all result per clean commit/toolchain
	@./scripts/agent.sh baseline

agent-verify: verify ## compatibility alias for make verify

agent-worktree: ## create, claim, and bootstrap a sibling worktree (TOPIC=... CLAIMS=...; BASE/DEPENDS_ON for stacks)
	@COPY_ENV="$(or $(COPY_ENV),0)" BOOTSTRAP_SKIP_FE="$(or $(BOOTSTRAP_SKIP_FE),0)" BASE="$(BASE)" \
		AGENT_WORKTREE_LIMIT="$(or $(AGENT_WORKTREE_LIMIT),16)" ALLOW_WORKTREE_BACKLOG="$(or $(ALLOW_WORKTREE_BACKLOG),0)" \
		./scripts/agent.sh worktree "$(TOPIC)" "$(or $(TASK),$(TOPIC))" "$(CLAIMS)" "$(DEPENDS_ON)"

agent-gc: ## audit worktrees; APPLY=1 retires only exact clean merged PR heads
	@APPLY="$(or $(APPLY),0)" ./scripts/agent.sh gc

bootstrap: ## build the Rust worker and prepare frontend, isolated directories, and dev identity
	@./scripts/agent.sh bootstrap

doctor: ## verify toolchain and Docker readiness; report worktrees, ports, caches, and artifacts
	@./scripts/agent.sh doctor

agent-harness-test: agent-assets-verify ## regression-test coordination, worktree isolation, and shared-output claims
	@./scripts/agent-harness-test.sh

agent-assets-verify: ## verify the curated skill catalog and agent adapters agree
	@./scripts/check-agent-assets.sh

.PHONY: compose-verify
compose-verify: ## verify Traefik, database wiring, and pinned release images
	@./scripts/check-compose.sh

.PHONY: release-verify release-notes-preview
release-verify: ## verify release, CI acquisition, Android, and publication policy
	@./scripts/check-release-tag.sh --self-test
	@./scripts/check-release-image-absence.sh --self-test
	@./scripts/android-version-code.sh --self-test
	@./scripts/apple-compilation-cache-test.sh
	@./scripts/test-android-release-emulator-contract-test.sh
	@./web/scripts/test-apple-client-cache-test.sh
	@./web/scripts/validate-apple-compilation-cache-test.sh
	@$(GO) test ./internal/releaseverify
	@$(GO) run ./cmd/releaseverify -root .

release-notes-preview: ## generate validated release notes (TAG required; optional PREVIOUS_TAG and OUTPUT)
	@test -n "$(TAG)" || { echo "TAG is required (for example TAG=v0.2.0)" >&2; exit 2; }
	@mkdir -p .artifacts
	@PREVIOUS_TAG="$(PREVIOUS_TAG)" ./scripts/generate-release-notes.sh "$(TAG)" "$(or $(OUTPUT),.artifacts/release-notes-$(TAG).md)"

.PHONY: backup-restore-verify backup-restore-drill
backup-restore-verify: ## isolated SQLite backup, destructive replacement, restore, and state validation
	$(GO) test -race ./internal/store -run '^TestSQLiteBackupRestoreDrill$$' -count=1

backup-restore-drill: backup-restore-verify ## SQLite + Docker-backed Postgres backup/restore drills
	$(GO) test -race -tags=integration ./internal/store -run '^TestPostgresBackupRestoreDrill$$' -count=1
