eval-contract: ## hermetic semantic-evaluation contracts; never contacts a model, Library, or TMDB
	LOOMARR_EVAL_CONTRACT_ONLY=1 $(GO) test -tags=eval ./internal/eval/ ./cmd/planner-cert-compare/ ./internal/recommend/ ./cmd/channel-recommend-cert/ ./cmd/channel-recommend-compare/ ./cmd/channel-recommend-diagnostic/

eval: ## semantic eval: real intents → real LLM → scored (needs LLM_*/LIBRARY_*/TMDB_API_KEY; NOT in the hermetic gate)
	$(GO) test -tags=eval -v -timeout 20m ./internal/eval/

eval-cert: ## certify exact intents and mandatory scheduled viewer outcomes; fails closed and writes a scorecard
	@eval "$$(./scripts/dev-env.sh export)"; \
	  report="$${LOOMARR_EVAL_OUT:-$$LOOMARR_ARTIFACT_DIR/semantic-certification.json}"; \
	  mkdir -p "$$(dirname "$$report")"; \
	  LOOMARR_EVAL_REQUIRED=1 LOOMARR_EVAL_OUT="$$report" \
	    $(GO) test -count=1 -tags=eval -v -timeout 20m ./internal/eval/

eval-planner-cert: ## compare one model against the frozen planner corpus; explicit, inference-spending, non-CI
	@eval "$$(./scripts/dev-env.sh export)"; \
	  report="$${LOOMARR_EVAL_OUT:-$$LOOMARR_ARTIFACT_DIR/planner-certification.json}"; \
	  summary="$${LOOMARR_EVAL_SUMMARY_OUT:-$$LOOMARR_ARTIFACT_DIR/planner-certification.md}"; \
	  mkdir -p "$$(dirname "$$report")" "$$(dirname "$$summary")"; \
	  report="$$(cd "$$(dirname "$$report")" && pwd -P)/$$(basename "$$report")"; \
	  summary="$$(cd "$$(dirname "$$summary")" && pwd -P)/$$(basename "$$summary")"; \
	  LOOMARR_EVAL_PLANNER_CERTIFICATION=1 LOOMARR_EVAL_REQUIRED=1 \
	  LOOMARR_EVAL_OUT="$$report" LOOMARR_EVAL_SUMMARY_OUT="$$summary" \
	    $(GO) test -count=1 -tags=eval -run '^TestPlannerModelCertification$$' -v -timeout 20m ./internal/eval/

eval-planner-smoke: ## replay one frozen base Intent per planner family; explicit, inference-spending, non-CI
	@eval "$$(./scripts/dev-env.sh export)"; \
	  report="$${LOOMARR_EVAL_OUT:-$$LOOMARR_ARTIFACT_DIR/planner-family-smoke.json}"; \
	  summary="$${LOOMARR_EVAL_SUMMARY_OUT:-$$LOOMARR_ARTIFACT_DIR/planner-family-smoke.md}"; \
	  mkdir -p "$$(dirname "$$report")" "$$(dirname "$$summary")"; \
	  report="$$(cd "$$(dirname "$$report")" && pwd -P)/$$(basename "$$report")"; \
	  summary="$$(cd "$$(dirname "$$summary")" && pwd -P)/$$(basename "$$summary")"; \
	  LOOMARR_EVAL_PLANNER_CERTIFICATION=1 LOOMARR_EVAL_PLANNER_FAMILY_SMOKE=1 \
	  LOOMARR_EVAL_REQUIRED=1 LOOMARR_EVAL_TRIALS=1 \
	  LOOMARR_EVAL_OUT="$$report" LOOMARR_EVAL_SUMMARY_OUT="$$summary" \
	    $(GO) test -count=1 -tags=eval -run '^TestPlannerModelCertification$$' -v -timeout 20m ./internal/eval/

planner-tool-diagnostic: ## probe the exact post-catalog-result model turn; explicit, inference-spending, non-CI
	$(GO) run ./cmd/planner-tool-diagnostic

channel-recommend-cert: ## certify inert channel concepts on the frozen recommendation corpus; explicit, inference-spending, non-CI
	@eval "$$(./scripts/dev-env.sh export)"; \
	  report="$${LOOMARR_RECOMMEND_OUT:-$$LOOMARR_ARTIFACT_DIR/channel-recommendation-certification.json}"; \
	  summary="$${LOOMARR_RECOMMEND_SUMMARY_OUT:-$$LOOMARR_ARTIFACT_DIR/channel-recommendation-certification.md}"; \
	  $(GO) run ./cmd/channel-recommend-cert \
	    --out "$$report" --summary "$$summary"

channel-recommend-cert-dry-run: ## emit the provider-free recommendation certification contract; no inference
	@eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/channel-recommend-cert --dry-run

channel-recommend-diagnostic: ## diagnose recommendation JSON transport on the disjoint development corpus; explicit, inference-spending, non-CI
	@eval "$$(./scripts/dev-env.sh export)"; \
	  report="$${LOOMARR_RECOMMEND_DIAGNOSTIC_OUT:-$$LOOMARR_ARTIFACT_DIR/channel-recommendation-diagnostic.json}"; \
	  $(GO) run ./cmd/channel-recommend-diagnostic --out "$$report"

channel-recommend-compare: ## compare channel-recommendation scorecards without inference
	@test -n "$$LOOMARR_RECOMMEND_SCORECARDS" || { echo "channel-recommend-compare: LOOMARR_RECOMMEND_SCORECARDS is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_RECOMMEND_SHARED_PROFILE" || { echo "channel-recommend-compare: LOOMARR_RECOMMEND_SHARED_PROFILE is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  report="$${LOOMARR_RECOMMEND_COMPARISON_OUT:-$$LOOMARR_ARTIFACT_DIR/channel-recommendation-comparison.json}"; \
	  summary="$${LOOMARR_RECOMMEND_COMPARISON_SUMMARY_OUT:-$$LOOMARR_ARTIFACT_DIR/channel-recommendation-comparison.md}"; \
	  mkdir -p "$$(dirname "$$report")" "$$(dirname "$$summary")"; \
	  $(GO) run ./cmd/channel-recommend-compare --out "$$report" --summary "$$summary" \
	    --shared-profile "$$LOOMARR_RECOMMEND_SHARED_PROFILE" $$LOOMARR_RECOMMEND_SCORECARDS

eval-planner-compare: ## compare two or more frozen planner scorecards without inference
	@test -n "$$LOOMARR_EVAL_SCORECARDS" || { echo "eval-planner-compare: LOOMARR_EVAL_SCORECARDS is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  report="$${LOOMARR_EVAL_COMPARISON_OUT:-$$LOOMARR_ARTIFACT_DIR/planner-comparison.json}"; \
	  summary="$${LOOMARR_EVAL_COMPARISON_SUMMARY_OUT:-$$LOOMARR_ARTIFACT_DIR/planner-comparison.md}"; \
	  mkdir -p "$$(dirname "$$report")" "$$(dirname "$$summary")"; \
	  $(GO) run -tags=eval ./cmd/planner-cert-compare --out "$$report" --summary "$$summary" $$LOOMARR_EVAL_SCORECARDS

planner-reference-host: ## bind one planner scorecard to exact local artifact and reference-host evidence; no inference
	@for name in SCORECARD CAPTURE EVIDENCE_DIR GENERATED_AT OUT; do \
	  value="$$(printenv "LOOMARR_PLANNER_REFERENCE_$$name" 2>/dev/null || true)"; \
	  test -n "$$value" || { echo "planner-reference-host: LOOMARR_PLANNER_REFERENCE_$$name is required" >&2; exit 2; }; \
	done; \
	$(GO) run ./cmd/planner-reference-host \
	  --scorecard "$$LOOMARR_PLANNER_REFERENCE_SCORECARD" \
	  --capture "$$LOOMARR_PLANNER_REFERENCE_CAPTURE" \
	  --evidence-dir "$$LOOMARR_PLANNER_REFERENCE_EVIDENCE_DIR" \
	  --generated-at "$$LOOMARR_PLANNER_REFERENCE_GENERATED_AT" \
	  --out "$$LOOMARR_PLANNER_REFERENCE_OUT"

eval-matrix: ## explicitly certify local + OpenRouter generation sequentially (manual, resource-heavy)
	@test -n "$$OPENROUTER_API_KEY" || { echo "eval-matrix: OPENROUTER_API_KEY is required" >&2; exit 2; }; \
	  test -n "$$OPENROUTER_MODEL" || { echo "eval-matrix: OPENROUTER_MODEL is required" >&2; exit 2; }; \
	  test -n "$$OPENROUTER_GENERATOR_PROVIDER" || { echo "eval-matrix: OPENROUTER_GENERATOR_PROVIDER is required" >&2; exit 2; }; \
	  test -n "$$OPENROUTER_JUDGE_PROVIDER" || { echo "eval-matrix: OPENROUTER_JUDGE_PROVIDER is required" >&2; exit 2; }; \
	  test "$$LOOMARR_EVAL_ALLOW_LOCAL" = "1" || { echo "eval-matrix: refusing local inference; confirm an idle host with sufficient RAM/VRAM, then set LOOMARR_EVAL_ALLOW_LOCAL=1" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  judge_model="$${OPENROUTER_JUDGE_MODEL:-$$OPENROUTER_MODEL}"; \
	  status=0; \
	  LOOMARR_EVAL_PROFILE=local \
	  LOOMARR_EVAL_OUT="$$LOOMARR_ARTIFACT_DIR/semantic-certification-local.json" \
	  LOOMARR_EVAL_JUDGE="$$judge_model" LOOMARR_EVAL_JUDGE_PROVIDER=openrouter \
	  LOOMARR_EVAL_JUDGE_UPSTREAM_PROVIDER="$$OPENROUTER_JUDGE_PROVIDER" \
	  LOOMARR_EVAL_JUDGE_URL=https://openrouter.ai/api/v1 \
	  LOOMARR_EVAL_JUDGE_API_KEY="$$OPENROUTER_API_KEY" \
	    $(MAKE) eval-cert || status=$$?; \
	  LLM_PROVIDER=openrouter LLM_URL=https://openrouter.ai/api/v1 \
	  LLM_MODEL="$$OPENROUTER_MODEL" LLM_API_KEY="$$OPENROUTER_API_KEY" \
	  LOOMARR_EVAL_GENERATOR_UPSTREAM_PROVIDER="$$OPENROUTER_GENERATOR_PROVIDER" \
	  LOOMARR_EVAL_PROFILE=openrouter \
	  LOOMARR_EVAL_OUT="$$LOOMARR_ARTIFACT_DIR/semantic-certification-openrouter.json" \
	  LOOMARR_EVAL_JUDGE="$$judge_model" LOOMARR_EVAL_JUDGE_PROVIDER=openrouter \
	  LOOMARR_EVAL_JUDGE_UPSTREAM_PROVIDER="$$OPENROUTER_JUDGE_PROVIDER" \
	  LOOMARR_EVAL_JUDGE_URL=https://openrouter.ai/api/v1 \
	  LOOMARR_EVAL_JUDGE_API_KEY="$$OPENROUTER_API_KEY" \
	    $(MAKE) eval-cert || status=$$?; \
	  exit "$$status"

filler-eval-contract: ## hermetic filler-admission corpus and selective-risk contracts
	$(GO) test ./internal/filleradmission/ ./internal/fillerbakeoff/ ./internal/fillercorpus/ ./internal/fillereval/ ./internal/fillerreview/ ./cmd/filler-bakeoff-ollama/ ./cmd/filler-bakeoff-openrouter/ ./cmd/filler-bakeoff-transcribe/ ./cmd/filler-cert/ ./cmd/filler-openrouter-snapshot/ ./cmd/filler-corpus/ ./cmd/filler-corpus-archive/ ./cmd/filler-corpus-commons/ ./cmd/filler-corpus-direct/ ./cmd/filler-corpus-download/ ./cmd/filler-corpus-inventory/ ./cmd/filler-corpus-loc/ ./cmd/filler-corpus-nasa/ ./cmd/filler-corpus-pages/ ./cmd/filler-corpus-pilot/ ./cmd/filler-corpus-pilot-rights-lock/ ./cmd/filler-corpus-pilot-rights-review/ ./cmd/filler-corpus-prepare/ ./cmd/filler-corpus-review/ ./cmd/filler-corpus-review-ollama/ ./cmd/filler-corpus-review-openrouter/ ./cmd/filler-corpus-rights-review/ ./cmd/filler-corpus-rights-lock/ ./cmd/filler-media-integrity-prepare/ ./cmd/filler-media-integrity-score/ ./cmd/filler-temporal-assess-ollama/ ./cmd/filler-temporal-assess-openrouter/ ./cmd/filler-temporal-calibration-report/ ./cmd/filler-temporal-compare/ ./cmd/filler-temporal-select/ ./cmd/filler-temporal-truth-select/ ./cmd/filler-temporal-truth-prepare/

filler-temporal-truth-select: ## select the private 48-case truth-review sample from frozen history without inference
	@for name in DRAFT SEED OUT A_PACKAGE A_MAP A_LABELS B_PACKAGE B_MAP B_LABELS C_PACKAGE C_MAP C_ADJUDICATIONS; do \
	  value="$$(printenv "LOOMARR_FILLER_TRUTH_$$name" 2>/dev/null || true)"; \
	  test -n "$$value" || { echo "filler-temporal-truth-select: LOOMARR_FILLER_TRUTH_$$name is required" >&2; exit 2; }; \
	done; \
	$(GO) run ./cmd/filler-temporal-truth-select \
	  --draft "$$LOOMARR_FILLER_TRUTH_DRAFT" --seed "$$LOOMARR_FILLER_TRUTH_SEED" --out "$$LOOMARR_FILLER_TRUTH_OUT" \
	  --a-package "$$LOOMARR_FILLER_TRUTH_A_PACKAGE" --a-map "$$LOOMARR_FILLER_TRUTH_A_MAP" --a-labels "$$LOOMARR_FILLER_TRUTH_A_LABELS" \
	  --b-package "$$LOOMARR_FILLER_TRUTH_B_PACKAGE" --b-map "$$LOOMARR_FILLER_TRUTH_B_MAP" --b-labels "$$LOOMARR_FILLER_TRUTH_B_LABELS" \
	  --c-package "$$LOOMARR_FILLER_TRUTH_C_PACKAGE" --c-map "$$LOOMARR_FILLER_TRUTH_C_MAP" --c-adjudications "$$LOOMARR_FILLER_TRUTH_C_ADJUDICATIONS"

filler-temporal-truth-prepare: ## build the sealed complete-span 48-case evidence set without inference
	@for name in SELECTION DRAFT DOWNLOAD_LEDGER MEDIA_ROOT PACKETS PACKET_ROOT TRANSCRIPTS EVIDENCE_OUT GENERATED_AT; do \
	  value="$$(printenv "LOOMARR_FILLER_TRUTH_$$name" 2>/dev/null || true)"; \
	  test -n "$$value" || { echo "filler-temporal-truth-prepare: LOOMARR_FILLER_TRUTH_$$name is required" >&2; exit 2; }; \
	done; \
	$(GO) run ./cmd/filler-temporal-truth-prepare \
	  --selection "$$LOOMARR_FILLER_TRUTH_SELECTION" --draft "$$LOOMARR_FILLER_TRUTH_DRAFT" \
	  --download-ledger "$$LOOMARR_FILLER_TRUTH_DOWNLOAD_LEDGER" --media-root "$$LOOMARR_FILLER_TRUTH_MEDIA_ROOT" \
	  --packets "$$LOOMARR_FILLER_TRUTH_PACKETS" --packet-root "$$LOOMARR_FILLER_TRUTH_PACKET_ROOT" \
	  --transcripts "$$LOOMARR_FILLER_TRUTH_TRANSCRIPTS" --out "$$LOOMARR_FILLER_TRUTH_EVIDENCE_OUT" \
	  --generated-at "$$LOOMARR_FILLER_TRUTH_GENERATED_AT" --scene-threshold "$${LOOMARR_FILLER_TRUTH_SCENE_THRESHOLD:-0.30}" \
	  --per-case-timeout "$${LOOMARR_FILLER_TRUTH_CASE_TIMEOUT:-5m}" \
	  --ocr-engine "$${LOOMARR_FILLER_TRUTH_OCR_ENGINE:-}" --ocr-source "$${LOOMARR_FILLER_TRUTH_OCR_SOURCE:-}" --ocr-version "$${LOOMARR_FILLER_TRUTH_OCR_VERSION:-}"

filler-temporal-assess-ollama: ## assess the sealed temporal challenge with a digest-pinned local model
	@test -n "$$LOOMARR_FILLER_TEMPORAL_PACKAGE" || { echo "filler-temporal-assess-ollama: LOOMARR_FILLER_TEMPORAL_PACKAGE is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_MODEL" || { echo "filler-temporal-assess-ollama: LOOMARR_FILLER_TEMPORAL_MODEL is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_MODEL_FAMILY" || { echo "filler-temporal-assess-ollama: LOOMARR_FILLER_TEMPORAL_MODEL_FAMILY is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_MODEL_DIGEST" || { echo "filler-temporal-assess-ollama: LOOMARR_FILLER_TEMPORAL_MODEL_DIGEST is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_ASSESSOR_ID" || { echo "filler-temporal-assess-ollama: LOOMARR_FILLER_TEMPORAL_ASSESSOR_ID is required" >&2; exit 2; }; \
	  $(GO) run ./cmd/filler-temporal-assess-ollama \
	    --package "$$LOOMARR_FILLER_TEMPORAL_PACKAGE" \
	    --model "$$LOOMARR_FILLER_TEMPORAL_MODEL" \
	    --model-family "$$LOOMARR_FILLER_TEMPORAL_MODEL_FAMILY" \
	    --model-digest "$$LOOMARR_FILLER_TEMPORAL_MODEL_DIGEST" \
	    --assessor-id "$$LOOMARR_FILLER_TEMPORAL_ASSESSOR_ID" \
	    --expected-cases "$${LOOMARR_FILLER_TEMPORAL_EXPECTED_CASES:-32}" \
	    --per-case-timeout "$${LOOMARR_FILLER_TEMPORAL_CASE_TIMEOUT:-10m}" \
	    --base-url "$${LOOMARR_FILLER_TEMPORAL_BASE_URL:-http://127.0.0.1:11434}" \
	    --out "$${LOOMARR_FILLER_TEMPORAL_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-temporal-assessment.json}"

filler-temporal-compare: ## compare two independent temporal assessment sets without inference
	@test -n "$$LOOMARR_FILLER_TEMPORAL_PACKAGE" || { echo "filler-temporal-compare: LOOMARR_FILLER_TEMPORAL_PACKAGE is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_FIRST" || { echo "filler-temporal-compare: LOOMARR_FILLER_TEMPORAL_FIRST is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_SECOND" || { echo "filler-temporal-compare: LOOMARR_FILLER_TEMPORAL_SECOND is required" >&2; exit 2; }; \
	  $(GO) run ./cmd/filler-temporal-compare \
	    --package "$$LOOMARR_FILLER_TEMPORAL_PACKAGE" \
	    --first "$$LOOMARR_FILLER_TEMPORAL_FIRST" \
	    --second "$$LOOMARR_FILLER_TEMPORAL_SECOND" \
	    --expected-cases "$${LOOMARR_FILLER_TEMPORAL_EXPECTED_CASES:-32}" \
	    --out "$${LOOMARR_FILLER_TEMPORAL_COMPARE_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-temporal-comparison.json}"

filler-temporal-select: ## derive an immutable stratified temporal calibration selection without inference
	@test -n "$$LOOMARR_FILLER_TEMPORAL_PACKAGE" || { echo "filler-temporal-select: LOOMARR_FILLER_TEMPORAL_PACKAGE is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_FIRST" || { echo "filler-temporal-select: LOOMARR_FILLER_TEMPORAL_FIRST is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_SECOND" || { echo "filler-temporal-select: LOOMARR_FILLER_TEMPORAL_SECOND is required" >&2; exit 2; }; \
	  $(GO) run ./cmd/filler-temporal-select \
	    --package "$$LOOMARR_FILLER_TEMPORAL_PACKAGE" \
	    --first "$$LOOMARR_FILLER_TEMPORAL_FIRST" \
	    --second "$$LOOMARR_FILLER_TEMPORAL_SECOND" \
	    --expected-cases "$${LOOMARR_FILLER_TEMPORAL_EXPECTED_CASES:-32}" \
	    --out "$${LOOMARR_FILLER_TEMPORAL_SELECTION_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-temporal-calibration-selection.json}"

filler-temporal-assess-openrouter: ## run a bounded paid temporal calibration on an exact snapshotted route
	@test -n "$$OPENROUTER_API_KEY" || { echo "filler-temporal-assess-openrouter: OPENROUTER_API_KEY is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_PACKAGE" || { echo "filler-temporal-assess-openrouter: LOOMARR_FILLER_TEMPORAL_PACKAGE is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_SELECTION" || { echo "filler-temporal-assess-openrouter: LOOMARR_FILLER_TEMPORAL_SELECTION is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_OPENROUTER_SNAPSHOT" || { echo "filler-temporal-assess-openrouter: LOOMARR_FILLER_TEMPORAL_OPENROUTER_SNAPSHOT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_MODEL" || { echo "filler-temporal-assess-openrouter: LOOMARR_FILLER_TEMPORAL_MODEL is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_MODEL_FAMILY" || { echo "filler-temporal-assess-openrouter: LOOMARR_FILLER_TEMPORAL_MODEL_FAMILY is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_PROVIDER" || { echo "filler-temporal-assess-openrouter: LOOMARR_FILLER_TEMPORAL_PROVIDER is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_PROVIDER_SLUG" || { echo "filler-temporal-assess-openrouter: LOOMARR_FILLER_TEMPORAL_PROVIDER_SLUG is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_ASSESSOR_ID" || { echo "filler-temporal-assess-openrouter: LOOMARR_FILLER_TEMPORAL_ASSESSOR_ID is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_MAX_REQUESTS" || { echo "filler-temporal-assess-openrouter: LOOMARR_FILLER_TEMPORAL_MAX_REQUESTS is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_MAX_SPEND_NANOUSD" || { echo "filler-temporal-assess-openrouter: LOOMARR_FILLER_TEMPORAL_MAX_SPEND_NANOUSD is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_MAX_CHARGE_NANOUSD" || { echo "filler-temporal-assess-openrouter: LOOMARR_FILLER_TEMPORAL_MAX_CHARGE_NANOUSD is required" >&2; exit 2; }; \
	  $(GO) run ./cmd/filler-temporal-assess-openrouter \
	    --package "$$LOOMARR_FILLER_TEMPORAL_PACKAGE" \
	    --selection "$$LOOMARR_FILLER_TEMPORAL_SELECTION" \
	    --snapshot "$$LOOMARR_FILLER_TEMPORAL_OPENROUTER_SNAPSHOT" \
	    --model "$$LOOMARR_FILLER_TEMPORAL_MODEL" \
	    --model-family "$$LOOMARR_FILLER_TEMPORAL_MODEL_FAMILY" \
	    --provider "$$LOOMARR_FILLER_TEMPORAL_PROVIDER" \
	    --provider-slug "$$LOOMARR_FILLER_TEMPORAL_PROVIDER_SLUG" \
	    --assessor-id "$$LOOMARR_FILLER_TEMPORAL_ASSESSOR_ID" \
	    --expected-package-cases "$${LOOMARR_FILLER_TEMPORAL_EXPECTED_CASES:-32}" \
	    --expected-calibration-cases "$${LOOMARR_FILLER_TEMPORAL_EXPECTED_CALIBRATION_CASES:-15}" \
	    --per-case-timeout "$${LOOMARR_FILLER_TEMPORAL_CASE_TIMEOUT:-5m}" \
	    --max-requests "$$LOOMARR_FILLER_TEMPORAL_MAX_REQUESTS" \
	    --max-spend-nanousd "$$LOOMARR_FILLER_TEMPORAL_MAX_SPEND_NANOUSD" \
	    --max-charge-nanousd "$$LOOMARR_FILLER_TEMPORAL_MAX_CHARGE_NANOUSD" \
	    --out "$${LOOMARR_FILLER_TEMPORAL_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-temporal-openrouter-result.json}"

filler-temporal-calibration-report: ## compare one hosted temporal result with its two bound local assessments
	@test -n "$$LOOMARR_FILLER_TEMPORAL_PACKAGE" || { echo "filler-temporal-calibration-report: LOOMARR_FILLER_TEMPORAL_PACKAGE is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_SELECTION" || { echo "filler-temporal-calibration-report: LOOMARR_FILLER_TEMPORAL_SELECTION is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_FIRST" || { echo "filler-temporal-calibration-report: LOOMARR_FILLER_TEMPORAL_FIRST is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_SECOND" || { echo "filler-temporal-calibration-report: LOOMARR_FILLER_TEMPORAL_SECOND is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_TEMPORAL_HOSTED_RESULT" || { echo "filler-temporal-calibration-report: LOOMARR_FILLER_TEMPORAL_HOSTED_RESULT is required" >&2; exit 2; }; \
	  $(GO) run ./cmd/filler-temporal-calibration-report \
	    --package "$$LOOMARR_FILLER_TEMPORAL_PACKAGE" \
	    --selection "$$LOOMARR_FILLER_TEMPORAL_SELECTION" \
	    --first "$$LOOMARR_FILLER_TEMPORAL_FIRST" \
	    --second "$$LOOMARR_FILLER_TEMPORAL_SECOND" \
	    --hosted-result "$$LOOMARR_FILLER_TEMPORAL_HOSTED_RESULT" \
	    --expected-package-cases "$${LOOMARR_FILLER_TEMPORAL_EXPECTED_CASES:-32}" \
	    --expected-calibration-cases "$${LOOMARR_FILLER_TEMPORAL_EXPECTED_CALIBRATION_CASES:-15}" \
	    --out "$${LOOMARR_FILLER_TEMPORAL_CALIBRATION_REPORT_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-temporal-calibration-report.json}"

filler-corpus-commons: ## freeze bounded Commons pilot and full-inventory artifacts
	@eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-commons \
	    --category "$${LOOMARR_FILLER_CORPUS_COMMONS_CATEGORY:-Advertising videos}" \
	    --role-hint "$${LOOMARR_FILLER_CORPUS_COMMONS_ROLE_HINT:-commercial}" \
	    --out "$${LOOMARR_FILLER_CORPUS_COMMONS_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-commons.json}" \
	    --inventory-out "$${LOOMARR_FILLER_CORPUS_COMMONS_INVENTORY_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-commons-inventory.json}" \
	    --cache-dir "$${LOOMARR_FILLER_CORPUS_COMMONS_CACHE:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-commons-cache}" \
	    --user-agent "$$LOOMARR_FILLER_CORPUS_USER_AGENT" \
	    --snapshot-at "$$LOOMARR_FILLER_CORPUS_COMMONS_SNAPSHOT_AT" \
	    --max-requests "$${LOOMARR_FILLER_CORPUS_COMMONS_MAX_REQUESTS:-10}" \
	    --max-pages "$${LOOMARR_FILLER_CORPUS_COMMONS_MAX_PAGES:-5}" \
	    --max-items "$${LOOMARR_FILLER_CORPUS_COMMONS_MAX_ITEMS:-10}" \
	    --max-response-bytes "$${LOOMARR_FILLER_CORPUS_COMMONS_MAX_RESPONSE_BYTES:-33554432}" \
	    --max-item-bytes "$${LOOMARR_FILLER_CORPUS_COMMONS_MAX_ITEM_BYTES:-536870912}" \
	    --max-total-bytes "$${LOOMARR_FILLER_CORPUS_COMMONS_MAX_TOTAL_BYTES:-3221225472}" \
	    --delay "$${LOOMARR_FILLER_CORPUS_COMMONS_DELAY:-250ms}" \
	    --max-wall-time "$${LOOMARR_FILLER_CORPUS_COMMONS_MAX_WALL_TIME:-2m}"

filler-corpus-cdc: ## freeze bounded CDC pilot and full-inventory artifacts
	@eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-pages \
	    --in internal/fillercorpus/corpus/seeds/cdc.json \
	    --out "$${LOOMARR_FILLER_CORPUS_CDC_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-cdc.json}" \
	    --inventory-out "$${LOOMARR_FILLER_CORPUS_CDC_INVENTORY_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-cdc-inventory.json}" \
	    --cache-dir "$${LOOMARR_FILLER_CORPUS_CDC_CACHE:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-cdc-cache}" \
	    --user-agent "$$LOOMARR_FILLER_CORPUS_USER_AGENT" \
	    --snapshot-at "$$LOOMARR_FILLER_CORPUS_CDC_SNAPSHOT_AT" \
	    --page-host www.cdc.gov \
	    --media-host www.cdc.gov \
	    --max-requests "$${LOOMARR_FILLER_CORPUS_CDC_MAX_REQUESTS:-20}" \
	    --max-items "$${LOOMARR_FILLER_CORPUS_CDC_MAX_ITEMS:-10}" \
	    --max-response-bytes "$${LOOMARR_FILLER_CORPUS_CDC_MAX_RESPONSE_BYTES:-16777216}" \
	    --max-item-bytes "$${LOOMARR_FILLER_CORPUS_CDC_MAX_ITEM_BYTES:-104857600}" \
	    --max-total-bytes "$${LOOMARR_FILLER_CORPUS_CDC_MAX_TOTAL_BYTES:-1073741824}" \
	    --delay "$${LOOMARR_FILLER_CORPUS_CDC_DELAY:-250ms}" \
	    --max-wall-time "$${LOOMARR_FILLER_CORPUS_CDC_MAX_WALL_TIME:-2m}"

filler-corpus-loc: ## freeze bounded LOC pilot and full-inventory artifacts
	@eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-loc \
	    --query "$$LOOMARR_FILLER_CORPUS_LOC_QUERY" \
	    --role-hint "$$LOOMARR_FILLER_CORPUS_LOC_ROLE_HINT" \
	    --out "$${LOOMARR_FILLER_CORPUS_LOC_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-loc.json}" \
	    --inventory-out "$${LOOMARR_FILLER_CORPUS_LOC_INVENTORY_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-loc-inventory.json}" \
	    --cache-dir "$${LOOMARR_FILLER_CORPUS_LOC_CACHE:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-loc-cache}" \
	    --user-agent "$$LOOMARR_FILLER_CORPUS_USER_AGENT" \
	    --snapshot-at "$$LOOMARR_FILLER_CORPUS_LOC_SNAPSHOT_AT" \
	    --max-requests "$${LOOMARR_FILLER_CORPUS_LOC_MAX_REQUESTS:-25}" \
	    --max-items "$${LOOMARR_FILLER_CORPUS_LOC_MAX_ITEMS:-10}" \
	    --max-response-bytes "$$LOOMARR_FILLER_CORPUS_LOC_MAX_RESPONSE_BYTES" \
	    --max-item-bytes "$$LOOMARR_FILLER_CORPUS_LOC_MAX_ITEM_BYTES" \
	    --max-total-bytes "$$LOOMARR_FILLER_CORPUS_LOC_MAX_TOTAL_BYTES" \
	    --delay "$${LOOMARR_FILLER_CORPUS_LOC_DELAY:-3100ms}" \
	    --max-wall-time "$${LOOMARR_FILLER_CORPUS_LOC_MAX_WALL_TIME:-3m}"

filler-corpus-nasa: ## freeze bounded NASA pilot and full-inventory artifacts
	@eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-nasa \
	    --query "$${LOOMARR_FILLER_CORPUS_NASA_QUERY:-trailer}" \
	    --role-hint "$${LOOMARR_FILLER_CORPUS_NASA_ROLE_HINT:-trailer}" \
	    --out "$${LOOMARR_FILLER_CORPUS_NASA_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-nasa.json}" \
	    --inventory-out "$${LOOMARR_FILLER_CORPUS_NASA_INVENTORY_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-nasa-inventory.json}" \
	    --cache-dir "$${LOOMARR_FILLER_CORPUS_NASA_CACHE:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-nasa-cache}" \
	    --user-agent "$$LOOMARR_FILLER_CORPUS_USER_AGENT" \
	    --snapshot-at "$$LOOMARR_FILLER_CORPUS_NASA_SNAPSHOT_AT" \
	    --max-requests "$${LOOMARR_FILLER_CORPUS_NASA_MAX_REQUESTS:-80}" \
	    --max-items "$${LOOMARR_FILLER_CORPUS_NASA_MAX_ITEMS:-10}" \
	    --max-response-bytes "$${LOOMARR_FILLER_CORPUS_NASA_MAX_RESPONSE_BYTES:-33554432}" \
	    --max-item-bytes "$${LOOMARR_FILLER_CORPUS_NASA_MAX_ITEM_BYTES:-536870912}" \
	    --max-total-bytes "$${LOOMARR_FILLER_CORPUS_NASA_MAX_TOTAL_BYTES:-3221225472}" \
	    --delay "$${LOOMARR_FILLER_CORPUS_NASA_DELAY:-250ms}" \
	    --max-wall-time "$${LOOMARR_FILLER_CORPUS_NASA_MAX_WALL_TIME:-2m}"

filler-corpus-pilot: ## lock the qualified metadata-only filler rights-yield pilot
	@test -n "$$LOOMARR_FILLER_CORPUS_PILOT_SNAPSHOT_AT" || { echo "filler-corpus-pilot: LOOMARR_FILLER_CORPUS_PILOT_SNAPSHOT_AT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_PILOT_LOCKED_AT" || { echo "filler-corpus-pilot: LOOMARR_FILLER_CORPUS_PILOT_LOCKED_AT is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-pilot \
	    --lane internal/fillercorpus/corpus/pilot/prelinger.json \
	    --lane internal/fillercorpus/corpus/pilot/loc.json \
	    --lane internal/fillercorpus/corpus/pilot/nasa.json \
	    --lane internal/fillercorpus/corpus/pilot/cdc.json \
	    --lane internal/fillercorpus/corpus/pilot/commons.json \
	    --snapshot-at "$$LOOMARR_FILLER_CORPUS_PILOT_SNAPSHOT_AT" \
	    --locked-at "$$LOOMARR_FILLER_CORPUS_PILOT_LOCKED_AT" \
	    --out "$${LOOMARR_FILLER_CORPUS_PILOT_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-pilot.json}"

filler-corpus-pilot-rights-review: ## prepare the inert five-lane pilot review packet
	@test -n "$$LOOMARR_FILLER_CORPUS_PILOT_REVIEW_PREPARED_AT" || { echo "filler-corpus-pilot-rights-review: LOOMARR_FILLER_CORPUS_PILOT_REVIEW_PREPARED_AT is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-pilot-rights-review \
	    --pilot "$${LOOMARR_FILLER_CORPUS_PILOT:-internal/fillercorpus/corpus/pilot/locked.json}" \
	    --out "$${LOOMARR_FILLER_CORPUS_PILOT_REVIEW_WORKSHEET:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-pilot-rights-review.json}" \
	    --csv-out "$${LOOMARR_FILLER_CORPUS_PILOT_REVIEW_CSV:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-pilot-rights-review.csv}" \
	    --prepared-at "$$LOOMARR_FILLER_CORPUS_PILOT_REVIEW_PREPARED_AT"

filler-corpus-pilot-rights-lock: ## lock completed pilot review into a non-authorizing yield report
	@test -n "$$LOOMARR_FILLER_CORPUS_PILOT_REVIEW_WORKSHEET" || { echo "filler-corpus-pilot-rights-lock: LOOMARR_FILLER_CORPUS_PILOT_REVIEW_WORKSHEET is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_PILOT_REVIEW_CSV" || { echo "filler-corpus-pilot-rights-lock: LOOMARR_FILLER_CORPUS_PILOT_REVIEW_CSV is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_PILOT_REVIEW_LOCKED_AT" || { echo "filler-corpus-pilot-rights-lock: LOOMARR_FILLER_CORPUS_PILOT_REVIEW_LOCKED_AT is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-pilot-rights-lock \
	    --pilot "$${LOOMARR_FILLER_CORPUS_PILOT:-internal/fillercorpus/corpus/pilot/locked.json}" \
	    --worksheet "$$LOOMARR_FILLER_CORPUS_PILOT_REVIEW_WORKSHEET" \
	    --completed-csv "$$LOOMARR_FILLER_CORPUS_PILOT_REVIEW_CSV" \
	    --out "$${LOOMARR_FILLER_CORPUS_PILOT_REVIEW_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-pilot-rights-result.json}" \
	    --locked-at "$$LOOMARR_FILLER_CORPUS_PILOT_REVIEW_LOCKED_AT"

filler-corpus-archive: ## freeze a bounded rights-filtered Archive.org corpus inventory
	@test -n "$$LOOMARR_FILLER_CORPUS_ARCHIVE_COLLECTION" || { echo "filler-corpus-archive: LOOMARR_FILLER_CORPUS_ARCHIVE_COLLECTION is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-archive \
	    --collection "$$LOOMARR_FILLER_CORPUS_ARCHIVE_COLLECTION" \
	    --query "$$LOOMARR_FILLER_CORPUS_ARCHIVE_QUERY" \
	    --out "$${LOOMARR_FILLER_CORPUS_ARCHIVE_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-archive.json}" \
	    --pilot-out "$$LOOMARR_FILLER_CORPUS_ARCHIVE_PILOT_OUT" \
	    --role-hint "$$LOOMARR_FILLER_CORPUS_ARCHIVE_ROLE_HINT" \
	    --cache-dir "$${LOOMARR_FILLER_CORPUS_ARCHIVE_CACHE:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-archive-cache}" \
	    --user-agent "$${LOOMARR_FILLER_CORPUS_USER_AGENT:-$$LOOMARR_FILLER_CORPUS_ARCHIVE_USER_AGENT}" \
	    --snapshot-at "$$LOOMARR_FILLER_CORPUS_ARCHIVE_SNAPSHOT_AT" \
	    --max-requests "$$LOOMARR_FILLER_CORPUS_ARCHIVE_MAX_REQUESTS" \
	    --max-items "$$LOOMARR_FILLER_CORPUS_ARCHIVE_MAX_ITEMS" \
	    --max-item-bytes "$$LOOMARR_FILLER_CORPUS_ARCHIVE_MAX_ITEM_BYTES" \
	    --max-total-bytes "$$LOOMARR_FILLER_CORPUS_ARCHIVE_MAX_TOTAL_BYTES" \
	    --delay "$${LOOMARR_FILLER_CORPUS_ARCHIVE_DELAY:-1s}" \
	    --max-wall-time "$${LOOMARR_FILLER_CORPUS_ARCHIVE_MAX_WALL_TIME:-1m}"

filler-corpus-inventory: ## combine strict source inventories for mixed-authority rights review
	@test -n "$$LOOMARR_FILLER_CORPUS_INVENTORIES" || { echo "filler-corpus-inventory: LOOMARR_FILLER_CORPUS_INVENTORIES is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  set --; \
	  for path in $$LOOMARR_FILLER_CORPUS_INVENTORIES; do set -- "$$@" --inventory "$$path"; done; \
	  $(GO) run ./cmd/filler-corpus-inventory "$$@" \
	    --out "$${LOOMARR_FILLER_CORPUS_INVENTORY:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-inventory.json}"

filler-corpus-direct: ## freeze an authored local cohort with rights and provenance evidence
	@test -n "$$LOOMARR_FILLER_CORPUS_DIRECT_MANIFEST" || { echo "filler-corpus-direct: LOOMARR_FILLER_CORPUS_DIRECT_MANIFEST is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_DIRECT_ROOT" || { echo "filler-corpus-direct: LOOMARR_FILLER_CORPUS_DIRECT_ROOT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_DIRECT_SNAPSHOT_AT" || { echo "filler-corpus-direct: LOOMARR_FILLER_CORPUS_DIRECT_SNAPSHOT_AT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_DIRECT_ITEMS" || { echo "filler-corpus-direct: LOOMARR_FILLER_CORPUS_DIRECT_ITEMS is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_DIRECT_MAX_BYTES" || { echo "filler-corpus-direct: LOOMARR_FILLER_CORPUS_DIRECT_MAX_BYTES is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-direct \
	    --manifest "$$LOOMARR_FILLER_CORPUS_DIRECT_MANIFEST" \
	    --root "$$LOOMARR_FILLER_CORPUS_DIRECT_ROOT" \
	    --out "$${LOOMARR_FILLER_CORPUS_DIRECT_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-direct.json}" \
	    --snapshot-at "$$LOOMARR_FILLER_CORPUS_DIRECT_SNAPSHOT_AT" \
	    --expected-items "$$LOOMARR_FILLER_CORPUS_DIRECT_ITEMS" \
	    --max-bytes "$$LOOMARR_FILLER_CORPUS_DIRECT_MAX_BYTES" \
	    --max-wall-time "$${LOOMARR_FILLER_CORPUS_DIRECT_MAX_WALL_TIME:-1m}"

filler-corpus-prepare: ## build an unlabeled corpus draft and bounded evidence packets
	@test -n "$$LOOMARR_FILLER_CORPUS_PROFILE" || { echo "filler-corpus-prepare: LOOMARR_FILLER_CORPUS_PROFILE is required" >&2; exit 2; }; \
	  case "$$LOOMARR_FILLER_CORPUS_PROFILE" in \
	    development) default_min=300; default_max=300 ;; \
	    certification) default_min=1426; default_max=1600 ;; \
	    *) echo "filler-corpus-prepare: LOOMARR_FILLER_CORPUS_PROFILE must be development or certification" >&2; exit 2 ;; \
	  esac; \
	  test -n "$$LOOMARR_FILLER_CORPUS_INVENTORY" || { echo "filler-corpus-prepare: LOOMARR_FILLER_CORPUS_INVENTORY is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_RIGHTS_APPROVALS" || { echo "filler-corpus-prepare: LOOMARR_FILLER_CORPUS_RIGHTS_APPROVALS is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_PREPARATION_PLAN" || { echo "filler-corpus-prepare: LOOMARR_FILLER_CORPUS_PREPARATION_PLAN is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_LOCAL_ROOT" || { echo "filler-corpus-prepare: LOOMARR_FILLER_CORPUS_LOCAL_ROOT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_MEDIA_DIR" || { echo "filler-corpus-prepare: LOOMARR_FILLER_CORPUS_MEDIA_DIR is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_PREPARED_AT" || { echo "filler-corpus-prepare: LOOMARR_FILLER_CORPUS_PREPARED_AT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_PREP_MAX_INPUT_BYTES" || { echo "filler-corpus-prepare: LOOMARR_FILLER_CORPUS_PREP_MAX_INPUT_BYTES is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_PREP_MAX_OUTPUT_BYTES" || { echo "filler-corpus-prepare: LOOMARR_FILLER_CORPUS_PREP_MAX_OUTPUT_BYTES is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-prepare \
	    --profile "$$LOOMARR_FILLER_CORPUS_PROFILE" \
	    --inventory "$$LOOMARR_FILLER_CORPUS_INVENTORY" \
	    --rights-approvals "$$LOOMARR_FILLER_CORPUS_RIGHTS_APPROVALS" \
	    --quarantine-inspection "$$LOOMARR_FILLER_CORPUS_QUARANTINE_INSPECTION" \
	    --plan "$$LOOMARR_FILLER_CORPUS_PREPARATION_PLAN" \
	    --local-root "$$LOOMARR_FILLER_CORPUS_LOCAL_ROOT" \
	    --remote-root "$$LOOMARR_FILLER_CORPUS_MEDIA_DIR" \
	    --draft-out "$${LOOMARR_FILLER_CORPUS_DRAFT:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-draft.json}" \
	    --packets-out "$${LOOMARR_FILLER_CORPUS_PACKETS:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-packets.jsonl}" \
	    --derivatives-root "$${LOOMARR_FILLER_CORPUS_DERIVATIVES:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-derivatives}" \
	    --prepared-at "$$LOOMARR_FILLER_CORPUS_PREPARED_AT" \
	    --ffmpeg "$${LOOMARR_FILLER_CORPUS_FFMPEG:-ffmpeg}" \
	    --min-items "$${LOOMARR_FILLER_CORPUS_PREP_MIN_ITEMS:-$$default_min}" \
	    --max-items "$${LOOMARR_FILLER_CORPUS_PREP_MAX_ITEMS:-$$default_max}" \
	    --max-input-bytes "$$LOOMARR_FILLER_CORPUS_PREP_MAX_INPUT_BYTES" \
	    --max-output-bytes "$$LOOMARR_FILLER_CORPUS_PREP_MAX_OUTPUT_BYTES" \
	    --max-wall-time "$${LOOMARR_FILLER_CORPUS_PREP_MAX_WALL_TIME:-6h}"

filler-corpus-download: ## download only rights-approved corpus media under hard ceilings
	@eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-download \
	    --inventory "$$LOOMARR_FILLER_CORPUS_INVENTORY" \
	    --rights-approvals "$$LOOMARR_FILLER_CORPUS_RIGHTS_APPROVALS" \
	    --out-dir "$$LOOMARR_FILLER_CORPUS_MEDIA_DIR" \
	    --ledger "$$LOOMARR_FILLER_CORPUS_DOWNLOAD_LEDGER" \
	    --user-agent "$${LOOMARR_FILLER_CORPUS_USER_AGENT:-$$LOOMARR_FILLER_CORPUS_ARCHIVE_USER_AGENT}" \
	    --generated-at "$$LOOMARR_FILLER_CORPUS_DOWNLOAD_GENERATED_AT" \
	    --profile "$$LOOMARR_FILLER_CORPUS_RIGHTS_PROFILE" \
	    --processor-id "$$LOOMARR_FILLER_CORPUS_PROCESSOR_ID" \
	    --processor-terms-sha256 "$$LOOMARR_FILLER_CORPUS_PROCESSOR_TERMS_SHA256" \
	    --max-requests "$$LOOMARR_FILLER_CORPUS_DOWNLOAD_MAX_REQUESTS" \
	    --max-items "$$LOOMARR_FILLER_CORPUS_DOWNLOAD_MAX_ITEMS" \
	    --max-bytes "$$LOOMARR_FILLER_CORPUS_DOWNLOAD_MAX_BYTES" \
	    --delay "$${LOOMARR_FILLER_CORPUS_DOWNLOAD_DELAY:-1s}"

filler-corpus-rights-review: ## prepare an inert worksheet from a frozen filler inventory
	@test -n "$$LOOMARR_FILLER_CORPUS_INVENTORY" || { echo "filler-corpus-rights-review: LOOMARR_FILLER_CORPUS_INVENTORY is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-rights-review \
	    --inventory "$$LOOMARR_FILLER_CORPUS_INVENTORY" \
	    --quarantine-inspection "$$LOOMARR_FILLER_CORPUS_QUARANTINE_INSPECTION" \
	    --out "$${LOOMARR_FILLER_CORPUS_RIGHTS_WORKSHEET:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-rights-review.json}" \
	    --csv-out "$${LOOMARR_FILLER_CORPUS_RIGHTS_CSV:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-rights-review.csv}" \
	    --prepared-at "$$LOOMARR_FILLER_CORPUS_RIGHTS_PREPARED_AT" \
	    --profile "$$LOOMARR_FILLER_CORPUS_RIGHTS_PROFILE" \
	    --agreement-id "$$LOOMARR_FILLER_CORPUS_AGREEMENT_ID" \
	    --agreement-sha256 "$$LOOMARR_FILLER_CORPUS_AGREEMENT_SHA256" \
	    --processor-id "$$LOOMARR_FILLER_CORPUS_PROCESSOR_ID" \
	    --processor-terms-sha256 "$$LOOMARR_FILLER_CORPUS_PROCESSOR_TERMS_SHA256" \
	    --min-items "$${LOOMARR_FILLER_CORPUS_RIGHTS_MIN_ITEMS:-1426}" \
	    --max-items "$${LOOMARR_FILLER_CORPUS_RIGHTS_MAX_ITEMS:-1600}"

filler-corpus-rights-lock: ## validate completed rights review CSV into approval JSONL
	@test -n "$$LOOMARR_FILLER_CORPUS_INVENTORY" || { echo "filler-corpus-rights-lock: LOOMARR_FILLER_CORPUS_INVENTORY is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_RIGHTS_WORKSHEET" || { echo "filler-corpus-rights-lock: LOOMARR_FILLER_CORPUS_RIGHTS_WORKSHEET is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_RIGHTS_CSV" || { echo "filler-corpus-rights-lock: LOOMARR_FILLER_CORPUS_RIGHTS_CSV is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-rights-lock \
	    --inventory "$$LOOMARR_FILLER_CORPUS_INVENTORY" \
	    --quarantine-inspection "$$LOOMARR_FILLER_CORPUS_QUARANTINE_INSPECTION" \
	    --worksheet "$$LOOMARR_FILLER_CORPUS_RIGHTS_WORKSHEET" \
	    --completed-csv "$$LOOMARR_FILLER_CORPUS_RIGHTS_CSV" \
	    --approvals-out "$${LOOMARR_FILLER_CORPUS_RIGHTS_APPROVALS:-$$LOOMARR_ARTIFACT_DIR/filler-corpus-rights-approvals.jsonl}" \
	    --locked-at "$$LOOMARR_FILLER_CORPUS_RIGHTS_LOCKED_AT" \
	    --profile "$$LOOMARR_FILLER_CORPUS_RIGHTS_PROFILE"

filler-corpus-lock: ## lock two blind filler-label batches into a certification manifest
	@test -n "$$LOOMARR_FILLER_CORPUS_DRAFT" || { echo "filler-corpus-lock: LOOMARR_FILLER_CORPUS_DRAFT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_REVIEW_A" || { echo "filler-corpus-lock: LOOMARR_FILLER_CORPUS_REVIEW_A is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_REVIEW_MAP_A" || { echo "filler-corpus-lock: LOOMARR_FILLER_CORPUS_REVIEW_MAP_A is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_REVIEW_B" || { echo "filler-corpus-lock: LOOMARR_FILLER_CORPUS_REVIEW_B is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_REVIEW_MAP_B" || { echo "filler-corpus-lock: LOOMARR_FILLER_CORPUS_REVIEW_MAP_B is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_LOCKED_AT" || { echo "filler-corpus-lock: LOOMARR_FILLER_CORPUS_LOCKED_AT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_OUT" || { echo "filler-corpus-lock: LOOMARR_FILLER_CORPUS_OUT is required" >&2; exit 2; }; \
	  $(GO) run ./cmd/filler-corpus \
	    --draft "$$LOOMARR_FILLER_CORPUS_DRAFT" \
	    --review-a "$$LOOMARR_FILLER_CORPUS_REVIEW_A" \
	    --map-a "$$LOOMARR_FILLER_CORPUS_REVIEW_MAP_A" \
	    --review-b "$$LOOMARR_FILLER_CORPUS_REVIEW_B" \
	    --map-b "$$LOOMARR_FILLER_CORPUS_REVIEW_MAP_B" \
	    --adjudications "$$LOOMARR_FILLER_CORPUS_ADJUDICATIONS" \
	    --locked-at "$$LOOMARR_FILLER_CORPUS_LOCKED_AT" \
	    --out "$$LOOMARR_FILLER_CORPUS_OUT"

filler-media-integrity-prepare: ## prepare a label-free media-integrity challenge without inference
	@test -n "$$LOOMARR_FILLER_MEDIA_INTEGRITY_AUTHORITY" || { echo "filler-media-integrity-prepare: LOOMARR_FILLER_MEDIA_INTEGRITY_AUTHORITY is required" >&2; exit 2; }; \
	  $(GO) run ./cmd/filler-media-integrity-prepare \
	    --authority "$$LOOMARR_FILLER_MEDIA_INTEGRITY_AUTHORITY" \
	    --media-quality "$$LOOMARR_FILLER_MEDIA_QUALITY_REPORT" \
	    --seed "$$LOOMARR_FILLER_MEDIA_INTEGRITY_SEED" \
	    --prepared-at "$$LOOMARR_FILLER_MEDIA_INTEGRITY_PREPARED_AT" \
	    --out "$$LOOMARR_FILLER_MEDIA_INTEGRITY_OUT"

filler-media-integrity-score: ## lock the private media-integrity comparison without inference
	@test -n "$$LOOMARR_FILLER_MEDIA_INTEGRITY_PACKAGE" || { echo "filler-media-integrity-score: LOOMARR_FILLER_MEDIA_INTEGRITY_PACKAGE is required" >&2; exit 2; }; \
	  $(GO) run ./cmd/filler-media-integrity-score \
	    --package "$$LOOMARR_FILLER_MEDIA_INTEGRITY_PACKAGE" \
	    --map "$$LOOMARR_FILLER_MEDIA_INTEGRITY_MAP" \
	    --media-quality "$$LOOMARR_FILLER_MEDIA_QUALITY_REPORT" \
	    --locked-at "$$LOOMARR_FILLER_MEDIA_INTEGRITY_LOCKED_AT" \
	    --out "$$LOOMARR_FILLER_MEDIA_INTEGRITY_REPORT"

filler-corpus-review: ## prepare one opaque randomized filler-label review batch
	@test -n "$$LOOMARR_FILLER_CORPUS_DRAFT" || { echo "filler-corpus-review: LOOMARR_FILLER_CORPUS_DRAFT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_REVIEW_BATCH" || { echo "filler-corpus-review: LOOMARR_FILLER_CORPUS_REVIEW_BATCH is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_REVIEW_PACKET" || { echo "filler-corpus-review: LOOMARR_FILLER_CORPUS_REVIEW_PACKET is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_REVIEW_MAP" || { echo "filler-corpus-review: LOOMARR_FILLER_CORPUS_REVIEW_MAP is required" >&2; exit 2; }; \
	  $(GO) run ./cmd/filler-corpus-review \
	    --draft "$$LOOMARR_FILLER_CORPUS_DRAFT" \
	    --batch-id "$$LOOMARR_FILLER_CORPUS_REVIEW_BATCH" \
	    --packet-out "$$LOOMARR_FILLER_CORPUS_REVIEW_PACKET" \
	    --map-out "$$LOOMARR_FILLER_CORPUS_REVIEW_MAP"

filler-corpus-review-package: ## materialize one verified identity-blind reviewer evidence package
	@test -n "$$LOOMARR_FILLER_CORPUS_DRAFT" || { echo "filler-corpus-review-package: LOOMARR_FILLER_CORPUS_DRAFT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_REVIEW_PACKET" || { echo "filler-corpus-review-package: LOOMARR_FILLER_CORPUS_REVIEW_PACKET is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_REVIEW_MAP" || { echo "filler-corpus-review-package: LOOMARR_FILLER_CORPUS_REVIEW_MAP is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_EVIDENCE_PACKETS" || { echo "filler-corpus-review-package: LOOMARR_FILLER_CORPUS_EVIDENCE_PACKETS is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_DERIVATIVES" || { echo "filler-corpus-review-package: LOOMARR_FILLER_CORPUS_DERIVATIVES is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_CORPUS_REVIEW_PACKAGE" || { echo "filler-corpus-review-package: LOOMARR_FILLER_CORPUS_REVIEW_PACKAGE is required" >&2; exit 2; }; \
	  $(GO) run ./cmd/filler-corpus-review-package \
	    --draft "$$LOOMARR_FILLER_CORPUS_DRAFT" \
	    --review-packet "$$LOOMARR_FILLER_CORPUS_REVIEW_PACKET" \
	    --alias-map "$$LOOMARR_FILLER_CORPUS_REVIEW_MAP" \
	    --evidence-packets "$$LOOMARR_FILLER_CORPUS_EVIDENCE_PACKETS" \
	    --corpus-root "$$LOOMARR_FILLER_CORPUS_DERIVATIVES" \
	    --out "$$LOOMARR_FILLER_CORPUS_REVIEW_PACKAGE" \
	    --materialize "$${LOOMARR_FILLER_CORPUS_REVIEW_MATERIALIZE:-hardlink}"

filler-corpus-review-ollama: ## complete one blind package with a digest-pinned local reviewer
	@test -n "$$LOOMARR_FILLER_REVIEW_PACKAGE" || { echo "filler-corpus-review-ollama: LOOMARR_FILLER_REVIEW_PACKAGE is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEW_TRANSCRIPTS" || { echo "filler-corpus-review-ollama: LOOMARR_FILLER_REVIEW_TRANSCRIPTS is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEW_MODEL" || { echo "filler-corpus-review-ollama: LOOMARR_FILLER_REVIEW_MODEL is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEW_MODEL_DIGEST" || { echo "filler-corpus-review-ollama: LOOMARR_FILLER_REVIEW_MODEL_DIGEST is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEWER_ID" || { echo "filler-corpus-review-ollama: LOOMARR_FILLER_REVIEWER_ID is required" >&2; exit 2; }; \
	  $(GO) run ./cmd/filler-corpus-review-ollama \
	    --package "$$LOOMARR_FILLER_REVIEW_PACKAGE" \
	    --transcripts "$$LOOMARR_FILLER_REVIEW_TRANSCRIPTS" \
	    --model "$$LOOMARR_FILLER_REVIEW_MODEL" \
	    --model-digest "$$LOOMARR_FILLER_REVIEW_MODEL_DIGEST" \
	    --reviewer-id "$$LOOMARR_FILLER_REVIEWER_ID" \
	    --expected-cases "$${LOOMARR_FILLER_REVIEW_EXPECTED_CASES:-300}" \
	    --per-case-timeout "$${LOOMARR_FILLER_REVIEW_CASE_TIMEOUT:-5m}" \
	    --base-url "$${LOOMARR_FILLER_REVIEW_BASE_URL:-http://127.0.0.1:11434}" \
	    --out "$${LOOMARR_FILLER_REVIEW_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-completed-review}"

filler-corpus-review-openrouter: ## complete one blind package through a bounded pinned hosted reviewer
	@test -n "$$OPENROUTER_API_KEY" || { echo "filler-corpus-review-openrouter: OPENROUTER_API_KEY is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEW_PACKAGE" || { echo "filler-corpus-review-openrouter: LOOMARR_FILLER_REVIEW_PACKAGE is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEW_TRANSCRIPTS" || { echo "filler-corpus-review-openrouter: LOOMARR_FILLER_REVIEW_TRANSCRIPTS is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEW_SNAPSHOT" || { echo "filler-corpus-review-openrouter: LOOMARR_FILLER_REVIEW_SNAPSHOT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEW_MODEL" || { echo "filler-corpus-review-openrouter: LOOMARR_FILLER_REVIEW_MODEL is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEW_PROVIDER" || { echo "filler-corpus-review-openrouter: LOOMARR_FILLER_REVIEW_PROVIDER is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEW_PROVIDER_SLUG" || { echo "filler-corpus-review-openrouter: LOOMARR_FILLER_REVIEW_PROVIDER_SLUG is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEWER_ID" || { echo "filler-corpus-review-openrouter: LOOMARR_FILLER_REVIEWER_ID is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEW_MAX_SPEND_NANOUSD" || { echo "filler-corpus-review-openrouter: LOOMARR_FILLER_REVIEW_MAX_SPEND_NANOUSD is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_REVIEW_MAX_CHARGE_NANOUSD" || { echo "filler-corpus-review-openrouter: LOOMARR_FILLER_REVIEW_MAX_CHARGE_NANOUSD is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-corpus-review-openrouter \
	    --package "$$LOOMARR_FILLER_REVIEW_PACKAGE" \
	    --transcripts "$$LOOMARR_FILLER_REVIEW_TRANSCRIPTS" \
	    --snapshot "$$LOOMARR_FILLER_REVIEW_SNAPSHOT" \
	    --model "$$LOOMARR_FILLER_REVIEW_MODEL" \
	    --provider "$$LOOMARR_FILLER_REVIEW_PROVIDER" \
	    --provider-slug "$$LOOMARR_FILLER_REVIEW_PROVIDER_SLUG" \
	    --reviewer-id "$$LOOMARR_FILLER_REVIEWER_ID" \
	    --expected-cases "$${LOOMARR_FILLER_REVIEW_EXPECTED_CASES:-300}" \
	    --max-requests "$${LOOMARR_FILLER_REVIEW_MAX_REQUESTS:-301}" \
	    --max-spend-nanousd "$$LOOMARR_FILLER_REVIEW_MAX_SPEND_NANOUSD" \
	    --max-charge-nanousd "$$LOOMARR_FILLER_REVIEW_MAX_CHARGE_NANOUSD" \
	    --per-case-timeout "$${LOOMARR_FILLER_REVIEW_CASE_TIMEOUT:-5m}" \
	    --base-url "$${LOOMARR_FILLER_REVIEW_BASE_URL:-https://openrouter.ai/api/v1}" \
	    --out "$${LOOMARR_FILLER_REVIEW_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-completed-review}"

filler-openrouter-snapshot: ## lock OpenRouter capability, endpoint-price, and ZDR metadata
	@test -n "$$OPENROUTER_API_KEY" || { echo "filler-openrouter-snapshot: OPENROUTER_API_KEY is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_OPENROUTER_MODELS" || { echo "filler-openrouter-snapshot: LOOMARR_FILLER_OPENROUTER_MODELS is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-openrouter-snapshot \
	    --models "$$LOOMARR_FILLER_OPENROUTER_MODELS" \
	    --out "$${LOOMARR_FILLER_OPENROUTER_SNAPSHOT:-$$LOOMARR_ARTIFACT_DIR/filler-openrouter-snapshot.json}" \
	    --base-url "$${LOOMARR_FILLER_BAKEOFF_BASE_URL:-https://openrouter.ai/api/v1}"

filler-bakeoff-openrouter: ## capture a bounded label-blind OpenRouter prediction ledger (paid/manual)
	@test -n "$$OPENROUTER_API_KEY" || { echo "filler-bakeoff-openrouter: OPENROUTER_API_KEY is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_BAKEOFF_MANIFEST" || { echo "filler-bakeoff-openrouter: LOOMARR_FILLER_BAKEOFF_MANIFEST is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_BAKEOFF_PACKETS" || { echo "filler-bakeoff-openrouter: LOOMARR_FILLER_BAKEOFF_PACKETS is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_BAKEOFF_CONFIG" || { echo "filler-bakeoff-openrouter: LOOMARR_FILLER_BAKEOFF_CONFIG is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_BAKEOFF_SNAPSHOT" || { echo "filler-bakeoff-openrouter: LOOMARR_FILLER_BAKEOFF_SNAPSHOT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_BAKEOFF_CORPUS_ROOT" || { echo "filler-bakeoff-openrouter: LOOMARR_FILLER_BAKEOFF_CORPUS_ROOT is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-bakeoff-openrouter \
	    --manifest "$$LOOMARR_FILLER_BAKEOFF_MANIFEST" \
	    --packets "$$LOOMARR_FILLER_BAKEOFF_PACKETS" \
	    --config "$$LOOMARR_FILLER_BAKEOFF_CONFIG" \
	    --snapshot "$$LOOMARR_FILLER_BAKEOFF_SNAPSHOT" \
	    --corpus-root "$$LOOMARR_FILLER_BAKEOFF_CORPUS_ROOT" \
	    --transcripts "$${LOOMARR_FILLER_BAKEOFF_TRANSCRIPTS:-}" \
	    --predictions "$${LOOMARR_FILLER_BAKEOFF_PREDICTIONS:-$$LOOMARR_ARTIFACT_DIR/filler-bakeoff-predictions.jsonl}" \
	    --base-url "$${LOOMARR_FILLER_BAKEOFF_BASE_URL:-https://openrouter.ai/api/v1}"

filler-bakeoff-ollama: ## capture a digest-pinned local filler prediction ledger (manual)
	@test -n "$$LOOMARR_FILLER_BAKEOFF_MANIFEST" || { echo "filler-bakeoff-ollama: LOOMARR_FILLER_BAKEOFF_MANIFEST is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_BAKEOFF_PACKETS" || { echo "filler-bakeoff-ollama: LOOMARR_FILLER_BAKEOFF_PACKETS is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_BAKEOFF_CONFIG" || { echo "filler-bakeoff-ollama: LOOMARR_FILLER_BAKEOFF_CONFIG is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_BAKEOFF_CORPUS_ROOT" || { echo "filler-bakeoff-ollama: LOOMARR_FILLER_BAKEOFF_CORPUS_ROOT is required" >&2; exit 2; }; \
	  $(GO) run ./cmd/filler-bakeoff-ollama \
	    --manifest "$$LOOMARR_FILLER_BAKEOFF_MANIFEST" \
	    --packets "$$LOOMARR_FILLER_BAKEOFF_PACKETS" \
	    --config "$$LOOMARR_FILLER_BAKEOFF_CONFIG" \
	    --corpus-root "$$LOOMARR_FILLER_BAKEOFF_CORPUS_ROOT" \
	    --transcripts "$${LOOMARR_FILLER_BAKEOFF_TRANSCRIPTS:-}" \
	    --predictions "$${LOOMARR_FILLER_BAKEOFF_PREDICTIONS:-$$LOOMARR_ARTIFACT_DIR/filler-bakeoff-ollama-predictions.jsonl}" \
	    --base-url "$${LOOMARR_FILLER_BAKEOFF_BASE_URL:-http://127.0.0.1:11434}"

filler-bakeoff-transcribe: ## capture digest-pinned shared filler transcripts (manual)
	@test -n "$$LOOMARR_FILLER_BAKEOFF_MANIFEST" || { echo "filler-bakeoff-transcribe: LOOMARR_FILLER_BAKEOFF_MANIFEST is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_BAKEOFF_PACKETS" || { echo "filler-bakeoff-transcribe: LOOMARR_FILLER_BAKEOFF_PACKETS is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_BAKEOFF_CONFIG" || { echo "filler-bakeoff-transcribe: LOOMARR_FILLER_BAKEOFF_CONFIG is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_BAKEOFF_CORPUS_ROOT" || { echo "filler-bakeoff-transcribe: LOOMARR_FILLER_BAKEOFF_CORPUS_ROOT is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_WHISPER_PATH" || { echo "filler-bakeoff-transcribe: LOOMARR_FILLER_WHISPER_PATH is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_WHISPER_MODEL" || { echo "filler-bakeoff-transcribe: LOOMARR_FILLER_WHISPER_MODEL is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  $(GO) run ./cmd/filler-bakeoff-transcribe \
	    --manifest "$$LOOMARR_FILLER_BAKEOFF_MANIFEST" \
	    --packets "$$LOOMARR_FILLER_BAKEOFF_PACKETS" \
	    --config "$$LOOMARR_FILLER_BAKEOFF_CONFIG" \
	    --corpus-root "$$LOOMARR_FILLER_BAKEOFF_CORPUS_ROOT" \
	    --whisper "$$LOOMARR_FILLER_WHISPER_PATH" \
	    --model "$$LOOMARR_FILLER_WHISPER_MODEL" \
	    --transcripts "$${LOOMARR_FILLER_BAKEOFF_TRANSCRIPTS:-$$LOOMARR_ARTIFACT_DIR/filler-bakeoff-transcripts.jsonl}"

filler-eval-cert: ## score captured filler decisions; never contacts a model or media source
	@test -n "$$LOOMARR_FILLER_EVAL_PREDICTIONS" || { echo "filler-eval-cert: LOOMARR_FILLER_EVAL_PREDICTIONS is required" >&2; exit 2; }; \
	  test -n "$$LOOMARR_FILLER_EVAL_GENERATED_AT" || { echo "filler-eval-cert: LOOMARR_FILLER_EVAL_GENERATED_AT is required" >&2; exit 2; }; \
	  test "$${LOOMARR_FILLER_EVAL_MAX_REQUESTS:-0}" -gt 0 || { echo "filler-eval-cert: positive LOOMARR_FILLER_EVAL_MAX_REQUESTS is required" >&2; exit 2; }; \
	  test "$${LOOMARR_FILLER_EVAL_MAX_SPEND_NANO_USD:-0}" -gt 0 || { echo "filler-eval-cert: positive LOOMARR_FILLER_EVAL_MAX_SPEND_NANO_USD is required" >&2; exit 2; }; \
	  test "$${LOOMARR_FILLER_EVAL_MAX_CONCURRENCY:-0}" -gt 0 || { echo "filler-eval-cert: positive LOOMARR_FILLER_EVAL_MAX_CONCURRENCY is required" >&2; exit 2; }; \
	  eval "$$(./scripts/dev-env.sh export)"; \
	  report="$${LOOMARR_FILLER_EVAL_OUT:-$$LOOMARR_ARTIFACT_DIR/filler-certification.json}"; \
	  $(GO) run ./cmd/filler-cert \
	    --manifest "$${LOOMARR_FILLER_EVAL_MANIFEST:-internal/fillereval/corpus/seed-v1.json}" \
	    --predictions "$$LOOMARR_FILLER_EVAL_PREDICTIONS" --report "$$report" \
	    --profile "$${LOOMARR_FILLER_EVAL_PROFILE:-replay}" \
	    --split "$${LOOMARR_FILLER_EVAL_SPLIT:-holdout}" \
	    --evidence-version "$$LOOMARR_FILLER_EVAL_EVIDENCE_VERSION" \
	    --prompt-version "$$LOOMARR_FILLER_EVAL_PROMPT_VERSION" \
	    --taxonomy-version "$$LOOMARR_FILLER_EVAL_TAXONOMY_VERSION" \
	    --policy-version "$$LOOMARR_FILLER_EVAL_POLICY_VERSION" \
	    --role-policy-version "$$LOOMARR_FILLER_EVAL_ROLE_POLICY_VERSION" \
	    --capability-snapshot "$$LOOMARR_FILLER_EVAL_CAPABILITY_SNAPSHOT" \
	    --price-snapshot "$$LOOMARR_FILLER_EVAL_PRICE_SNAPSHOT" \
	    --generated-at "$$LOOMARR_FILLER_EVAL_GENERATED_AT" \
	    --max-requests "$$LOOMARR_FILLER_EVAL_MAX_REQUESTS" \
	    --max-spend-nano-usd "$$LOOMARR_FILLER_EVAL_MAX_SPEND_NANO_USD" \
	    --max-concurrency "$$LOOMARR_FILLER_EVAL_MAX_CONCURRENCY"
