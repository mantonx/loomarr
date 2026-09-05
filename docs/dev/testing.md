# Testing

Unit tests never touch the network. External services are mocked through `internal/testkit` —
extend it rather than writing a private mock.

## The routine loop

```bash
go test -race -run TestName ./internal/<pkg>/ # while editing
make verify BASE=origin/main                  # once the change is stable
```

`make verify` classifies the diff through the same fail-closed impact policy as CI and runs the
affected local evidence. It reports locally executable gates separately from specialized and
platform-dependent gates owned by protected CI, and its completion line names only local evidence
that ran successfully. For Go changes, both golangci-lint and tests consume the affected
reverse-dependency package closure. Pull-request and merge-queue lanes provide protected remote evidence.

`make verify SCOPE=all` is the explicit complete-repository audit. Run it when a maintainer requests a full
audit, when changing the gate/classifier machinery, or when diagnosing a selection boundary. It is
not a task-start ritual, an edit-loop command, or a requirement for every Go change.

The checks selected by each Make target are listed in the [command reference](commands.md), which is
generated from the Makefile so it cannot drift. Enumerating them here could, and did.

### `make vet-tags` isn't redundant

Files behind `//go:build ffmpeg|eval|integration` are invisible to plain `go vet ./...` and to
golangci-lint — both ask the build system which files exist, and it honours the constraint.

That blind spot ran for months: `go vet ./...` exited 0 while the tagged run exited 1, and the
one test proving programmes sequence hadn't compiled in several releases. A tagged `go build`
wouldn't catch it either, since `go build` skips `_test.go` and most tagged files are tests.

## The layers

| Layer | Command | Proves | In `check`? |
| --- | --- | --- | --- |
| Go unit | `make test` | The backend, hermetically | ✅ |
| Tagged compile | `make vet-tags` | Build-tagged code compiles | ✅ |
| Doc claims | part of `make test` | Help pages don't contradict the code | ✅ |
| Store conformance | `make test-pg` | SQLite and Postgres behave identically | CI |
| Frontend units | `make fe` | Components and domain logic | CI |
| Visual + a11y | `make fe-visual` | Every story, two viewports, pixel + axe | CI |
| e2e | `make e2e` | The embedded SPA through first-run | CI |
| ffmpeg | `make test-ffmpeg` | Programmes sequence through real ffmpeg | manual |
| LLM eval | `make eval` | Real intents against a real model | manual |
| LLM certification | `make eval-cert` | Exact starter/adversarial corpus executed with a versioned scorecard | release/manual |
| LLM matrix | `make eval-matrix` | Same corpus on local and OpenRouter generation, judged through OpenRouter | release/manual |
| Channel recommendation | `make channel-recommend-cert` | Inert Channel Concepts on a digest-pinned synthetic holdout | release/manual |
| Hosted filler bakeoff | `make filler-bakeoff-openrouter` | Locked label-blind filler packets through one pinned paid route profile | release/manual |
| Local filler bakeoff | `make filler-bakeoff-ollama` | Same locked packets through one digest-pinned loopback model | release/manual |
| Rust supply chain | `make rust-audit` | Cargo advisories, licences, and sources | weekly + manual |
| Rust fuzz | `make rust-fuzz` | Bounded worker protocol and decoder do not crash | weekly + manual |
| SSO | `make test-sso` | OIDC against real Authelia + Authentik | manual |
| Maintainer smoke | `make smoke` | The real stack end to end | manual |

Store conformance is one suite over two backends — don't fork the assertions per dialect.

### Semantic evaluation versus certification

`make eval` is exploratory and exits cleanly when its real library, TMDB, or LLM configuration is
absent. `make eval-cert` is an assertion: missing configuration, a skipped/unexecuted case, a hard
grounding or negative-constraint failure, any judge-stage failure for a non-empty rubric, or an
unwritable scorecard makes the command fail. It always bypasses Go's test cache and writes
`$LOOMARR_ARTIFACT_DIR/semantic-certification.json` unless `LOOMARR_EVAL_OUT` selects another path.
Scorecard schema v7 records its schema/corpus version, separate requested generator and judge
provider/model identities, trial profile, and bounded structural observations—never credentials or
ambiguous top-level provider/model compatibility fields. Hard predicates cover
exact named includes/excludes, holiday policy, rating limits, ownership mix, and concrete scheduled
programme identities/order; a non-empty Proposal or favorable judge paragraph cannot substitute for
one. `RequireTitles` and `ForbidTitles` compare normalized whole titles in the case's explicit
`grounded` or `scheduled` evidence scope. Normalization trims, folds case, and collapses internal
whitespace; it never turns a substring or near title into a match. `ForbidTitleTerms` is the distinct
intentional substring heuristic. Every failed trial records its first failure as `retrieval`, `generation`, `deterministic`,
`structural_budget`, `schedule`, `judge`, or `budget_exhausted`, and the scorecard counts failed trials
under the same labels; later failures do not rewrite that first-stage diagnosis. `no_tool_call` and
`retrieval_empty` classify as retrieval; provider/model errors, malformed generation, and empty
selection after surfaced candidates classify as generation. Judge evidence requires
explicit finite overall, relevance, and serendipity scores in `0..1` plus a non-blank reason.
`Runner.Run` revalidates those facts for every `Judge` implementation; incomplete, NaN/infinite,
out-of-range, or blank-reason output is a judge failure rather than a defaulted or clamped score. Fixture cases materialize
`schedule.DesiredLineup` in the hermetic gate. Real-provider cases run serially for an explicit trial
count and report pass rate plus min/median/max overall quality, relevance, and serendipity: novelty only scores when it
remains defensibly on-theme. Per-case tool-call and surfaced-candidate budgets fail deterministically.
Structural diagnostics record the grounding stage, tool mode, candidate count, generation failure,
and schedule materialization failure, so a low score points at the layer to tune. Real inference still
remains outside comprehensive verification and certifies only the requested model/provider configuration, catalog
snapshot, and corpus version in the artifact.

Every run prints and records a deterministic worst-case call budget before constructing any Library,
TMDB, generator, or judge client. For `C` cases and `T` trials, maximum generator calls are
`C × T × suggest.ProductionBounds().MaxModelCalls`, maximum judge calls are `C × T`, and the total is
their sum. `LOOMARR_EVAL_MAX_CALLS_PER_RUN` and `LOOMARR_EVAL_MAX_CALLS_PER_SUITE` are mandatory for
`LOOMARR_EVAL_REQUIRED=1`. The run ceiling covers
`suggest.ProductionBounds().MaxModelCalls + 1`; the suite ceiling covers the computed total and is at
least the run ceiling. Absent, malformed, overflowing, or undersized values fail before client work;
the removed `LOOMARR_EVAL_MAX_CALLS` name is neither read nor accepted. An absent required-mode `LOOMARR_EVAL_TRIALS` defaults to three; an explicit malformed,
zero, negative, or integer-overflowing value fails rather than falling back. Budget multiplication
and addition use checked integer arithmetic, so an unrepresentable envelope fails before clients
without reporting wrapped values. Exploratory `make eval` reports the budget but does not require a ceiling. Required runs
using the default or explicit `ollama` generator or judge additionally require
`LOOMARR_EVAL_ALLOW_LOCAL=1` at the same preflight boundary. Fully hosted runs need no local acknowledgement, and no evaluation
target provisions or starts Ollama.

Required runs additionally require positive per-case-trial and whole-suite token and spend ceilings:
`LOOMARR_EVAL_MAX_TOKENS_PER_RUN`, `LOOMARR_EVAL_MAX_SPEND_PER_RUN`,
`LOOMARR_EVAL_MAX_TOKENS`, and `LOOMARR_EVAL_MAX_SPEND`. Spend is an exact nonnegative USD decimal;
tokens are provider-reported prompt plus completion totals. The shared Runner ledger checks those
ceilings before generator/judge boundaries and after each bounded call record. Exhaustion records the
closed `budget_exhausted` stage and prevents later calls/runs; it is not merely calculated after the
suite. Missing provider attribution stays unknown and is never converted to fabricated usage.
Scorecard schema v12 also embeds a canonical quality run snapshot when corpus, generator model, and
profile are declared. It binds the requested model/provider, unanimously reported resolved model
(or explicit missing resolution), profile as the budget profile, executable version, and accounting
availability without retaining prompts, endpoints, credentials, payloads, or provider generation ids.
Required certification therefore requires an identifier-shaped `LOOMARR_EVAL_PROFILE`; the name is
the snapshot's budget profile and distinguishes runs made under different declared resource envelopes.
With a hard ceiling active, missing token or hosted-spend facts fail as `budget_exhausted`; local
Ollama remains explicitly non-billed rather than receiving an invented charge. A missing hosted
usage fact also latches the shared suite ledger uncertain: every later trial or case is rejected
before its generator, tools, or judge can run. When the provider call simultaneously has an earlier
retrieval or generation failure, that boundary remains the current trial's first-failure stage; the
latched budget uncertainty still blocks all subsequent provider work.

A required hosted example therefore declares every independent bound explicitly:

```sh
LOOMARR_EVAL_REQUIRED=1 \
LOOMARR_EVAL_PROFILE=hosted-bounded-v1 \
LOOMARR_EVAL_MAX_CALLS_PER_RUN=25 \
LOOMARR_EVAL_MAX_CALLS_PER_SUITE=1500 \
LOOMARR_EVAL_MAX_TOKENS_PER_RUN=50000 \
LOOMARR_EVAL_MAX_TOKENS=3000000 \
LOOMARR_EVAL_MAX_SPEND_PER_RUN=0.25 \
LOOMARR_EVAL_MAX_SPEND=15.00 \
make eval-cert
```

All real proposal and live-schedule cases receive nonzero `MaxToolCalls` and
`MaxCandidatesSurfaced` from that same exported production bound. The shipped action-marathon case
allows only movie media identities, the negative-constraint case pins an exact forbidden grounded
Key, and the Sunday-morning nature/travel/food case requires grounded genre diversity, so closed media
mix, exact exclusion, and diversity gates are durable corpus behavior rather than test-only fields.

Each trial's scorecard keeps separate bounded generator and judge call evidence. Every call records
only requested/resolved provider and model, prompt/completion/reasoning/cached/cache-write/image/audio/
video token counts, exact provider-reported decimal charge and currency, attempts, and latency. Charge
status is explicit: `reported` requires a nonnegative plain decimal of at most 64 runes and a three-letter
uppercase currency code; `missing` stays empty, and `invalid` discards provider-controlled text
without inferring or truncating a value.
Credentials, endpoints, prompts, raw responses, provider payloads, and generation ids are excluded.
Missing wire resolution or attempt metadata stays empty/zero, never copied from the requested route
or changed to attempt one. Missing attribution is serialized as zero/empty fields rather than guessed
from configured model identities. Generator evidence is capped at the production Suggester call bound and judge evidence at
one call per trial. The scorecard separately records the uncapped observed generator-call count;
exceeding the production maximum is a `structural_budget` failure even though the serialized call
evidence remains bounded. Corpus version `2026-08-27.8` owns the production structural budgets plus
the durable exact-key/title exclusion, exact named-title inclusion, closed movie-only media mix, and
genre-diversity assertions.

Hermetic Runner and schedule certification tests inject the eval-only semantic recording `Judge`
and cross the public `Judge.Score(ctx, JudgeEvidence)` seam. That test double validates the bounded
typed grounded, structural, and scheduled evidence it receives, returns a deliberate score and
detail, records its call count, and lets the test assert the resulting scorecard behavior. Wrong
typed evidence must fail at this seam even if a renderer could otherwise repair, infer, or rebound
it. Certification tests must not use prompt-substring assertions or private prompt parsers.

Production `modelJudge` renderer and provider request/response tests are supplemental wire coverage
only. They may prove serialization, routing, response parsing, and attribution through hermetic
provider observations, but they cannot certify Runner or schedule semantics and cannot substitute
provider-visible prompt text for the typed `JudgeEvidence` supplied by `Runner`.

The durable schedule-outcome contract covers an owned curated series, a separate owned holiday
episode case, and an atomic release-ordered movie franchise. Acquisition-only holiday discoveries
are not playable evidence. Hermetic Runner tests label their fixed episode identities as synthetic
test evidence; live certification never copies those expectations. Instead set both
`LOOMARR_EVAL_LIVE_SCHEDULE=1` and `LOOMARR_EVAL_SCHEDULE_EVIDENCE=/path/to/snapshot.json`. Snapshot
schema 1 declares a non-empty snapshot id, complete scheduling-relevant Library episode evidence for
the curated and holiday series, and owned Indiana Jones movie evidence for TMDB collection 84. Both
series objects must use the exact Key `series:tmdb:456`; substituting another otherwise-consistent
series snapshot fails before external adapters or providers are used. Each prepared live schedule
case has a non-empty schedule-specific judge rubric and quality floors, so its materialized concrete
program sample crosses the same live judge seam as proposal evidence. Looking up an unknown prepared
case name is an explicit error, never an assertion-free case.
Each scheduled episode in that bounded sample retains its grounded identity, title, season/episode
range, year, rating, community rating, overview, and tags so holiday/highlight intent is assessable;
an opaque identity alone is insufficient. A malformed Proposal item Key fails before `Judge.Score`
and never renders as an empty grounded key.
The JSON object has exactly `schemaVersion`, `snapshotId`, `curated`, `holiday`, and `franchise`;
unknown fields, a second JSON value, or malformed trailing bytes fail. Snapshot ids use only ASCII letters, digits, `.`, `_`, and `-` so
they are safe in corpus identity. Each series object has exactly `key`, `name`, `libraryItemId`,
`episodes`, `requiredPrograms`, and `forbiddenPrograms`. Every episode records `libraryItemId`,
`title`, `durationMs`, `season`, `episode`, and the
present scheduling signals among `episodeEnd`, `year`, `officialRating`, `communityRating`,
`overview`, and `tags`. The episode array is the complete ordered `ListEpisodes` result, not a chosen
subset. The required and forbidden arrays are nonempty, disjoint, refer only to present episode
identities, and together classify every episode. `franchise` contains `movies` and
`requiredSequence`; each movie records `key`, `name`, `libraryItemId`, `durationMs`, and
`collectionId`, while the pinned sequence is exactly TMDB movie Keys 85, 87, and 89 in canonical
release order and live collection 84.
Before constructing a generator or judge provider, the eval re-reads Library episodes/runtimes and
TMDB collection identity and requires an exact match. Preflight never calls the scheduler or selector;
the later Runner compares production output against the snapshot's pinned concrete oracle. The
snapshot id is included in the scorecard corpus version. Missing, incomplete, sparse, circular, or
drifted evidence fails closed before inference.
Every declared series and movie first crosses the same ownership-binding check:
`library.LookupDetail` receives its exact TMDB media type/id, must report it present, and must return
the snapshot's `libraryItemId`. Episode enumeration and movie runtime validation happen only after
that cross-binding succeeds, so a self-consistent unrelated Library item fails before inference.
Prepared materializers also enforce exact per-case Lineup ownership: curated and holiday accept only
their snapshot series Key, and franchise accepts only movie Keys 85, 87, and 89. An extra playable
Lineup Key is a schedule-stage failure; a missing required Key remains a deterministic failure.
Acquisition picks are not materialized and are outside this check.
The two live series cases still send canonical semantic viewer requests through the real generator:
`Classic Simpsons reruns from the golden era, curated for variety` for curated highlights and
`Christmas episodes of The Simpsons already in my library` for the owned holiday case. Snapshot
names and episode expectations are independent evidence, not substitutes for those requests.

Without the live opt-in, exploration reports one explicit schedule-corpus omission and continues
with proposal cases. `LOOMARR_EVAL_REQUIRED=1` fails before provider construction unless both the
opt-in and consistent evidence are present, so `make eval-cert` cannot certify a proposal-only
subset. Both adapters enter the same pure `schedule.ComputeDesiredAt` projection.
`make eval-contract` always disables the live test before any adapter is constructed.

### Planner reference-host evidence

`make planner-reference-host` is the provider-free evidence-publication step for the stock-model
bake-off tracked in #831. It does not run `ollama`, pull or load a model, start inference, contact a
provider, or spend. It accepts the exact planner scorecard bytes, one normalized schema-v1 capture,
and one directory containing these fourteen bounded raw captures:

- `huggingface-model.json` retained during the authorized model acquisition and
  `gguf-sha256.txt` computed from the exact local GGUF;
- `ollama-version.json`, `ollama-list.json`, `ollama-show-request.json`, and
  `ollama-show.json`;
- `ollama-load-request.json`, `ollama-ps-cold-before.json`,
  `ollama-ps-warm-before.json`, and `ollama-ps-after.json`;
- `sw-vers.txt`, `uname.txt`, `sysctl-hw-memsize.txt`, and `system-profiler.json`.

The normalized capture declares `planner-reference-host-v1`, its run id and times, the exact
scorecard digest/size, model artifact and source identities, runtime/host facts, benchmark protocol,
selected-model residency, and a digest/size for each raw file. The model block requires an explicit
tag plus Ollama digest; pinned source repository/revision; GGUF filename/digest; quantization and the
production 8K context; and template, Modelfile, and license identities. The runtime block requires native
`arm64` macOS, exact Ollama/macOS/hardware/chip facts, and at least 64 GiB physical unified memory.
The protocol records profile, production context/output limits, temperature, an explicitly unset seed,
one cold start, one empty-prompt warm-up load, and one to ten measured warm trials. Every scorecard
case must report that same trial count. Cold-before evidence must show the selected model absent;
warm-before and after evidence must bind the exact tag/digest/context, agree with each other, and agree
with the scorecard's measured `/api/ps` RAM/VRAM maximum.

The source metadata is a prerequisite, not something this workflow downloads: preserve the exact
Hugging Face model API response with `blobs=true` when acquisition is separately authorized, and keep
the exact GGUF used to create the Ollama model. On the October reference host, set the candidate paths
and the already-approved evaluation ceilings, then run this sequence from a clean evidence directory.
`curl` below talks only to loopback Ollama; `make eval-planner-cert` is the sole inference step and must
not be run without the separate #831 execution authority and aggregate-budget headroom.

```sh
set -eu
MODEL='hf.co/owner/repository:Q4_K_M'
PROFILE='m5-pro-candidate-q4km'
RUN_ID='2026-10-m5-pro-candidate-q4km'
GGUF='/absolute/path/candidate-Q4_K_M.gguf'
HF_METADATA='/absolute/path/huggingface-model-blobs.json'
SCORECARD='/absolute/path/planner-certification.json'
CAPTURE='/absolute/path/reference-capture.json'
EVIDENCE_DIR='/absolute/path/reference-evidence'
OUT='/absolute/path/planner-reference-manifest.json'
TRIALS=3

: "${LOOMARR_EVAL_MAX_CALLS_PER_RUN:?required}"
: "${LOOMARR_EVAL_MAX_CALLS_PER_SUITE:?required}"
: "${LOOMARR_EVAL_MAX_TOKENS_PER_RUN:?required}"
: "${LOOMARR_EVAL_MAX_TOKENS:?required}"
: "${LOOMARR_EVAL_MAX_SPEND_PER_RUN:?required}"
: "${LOOMARR_EVAL_MAX_SPEND:?required}"
export LOOMARR_EVAL_MAX_CALLS_PER_RUN LOOMARR_EVAL_MAX_CALLS_PER_SUITE
export LOOMARR_EVAL_MAX_TOKENS_PER_RUN LOOMARR_EVAL_MAX_TOKENS
export LOOMARR_EVAL_MAX_SPEND_PER_RUN LOOMARR_EVAL_MAX_SPEND
test ! -e "$EVIDENCE_DIR"
mkdir -m 700 "$EVIDENCE_DIR"
cp "$HF_METADATA" "$EVIDENCE_DIR/huggingface-model.json"
shasum -a 256 "$GGUF" >"$EVIDENCE_DIR/gguf-sha256.txt"

curl --fail --silent --show-error http://127.0.0.1:11434/api/version \
  >"$EVIDENCE_DIR/ollama-version.json"
curl --fail --silent --show-error http://127.0.0.1:11434/api/tags \
  >"$EVIDENCE_DIR/ollama-list.json"
jq -cn --arg model "$MODEL" '{model:$model}' \
  >"$EVIDENCE_DIR/ollama-show-request.json"
curl --fail --silent --show-error -H 'Content-Type: application/json' \
  --data-binary @"$EVIDENCE_DIR/ollama-show-request.json" \
  http://127.0.0.1:11434/api/show >"$EVIDENCE_DIR/ollama-show.json"
sw_vers >"$EVIDENCE_DIR/sw-vers.txt"
uname -m >"$EVIDENCE_DIR/uname.txt"
sysctl -n hw.memsize >"$EVIDENCE_DIR/sysctl-hw-memsize.txt"
system_profiler -json SPHardwareDataType >"$EVIDENCE_DIR/system-profiler.json"

ollama stop "$MODEL" >/dev/null 2>&1 || true
curl --fail --silent --show-error http://127.0.0.1:11434/api/ps \
  >"$EVIDENCE_DIR/ollama-ps-cold-before.json"
jq -cn --arg model "$MODEL" \
  '{model:$model,prompt:"",stream:false,keep_alive:"30m",options:{num_ctx:8192}}' \
  >"$EVIDENCE_DIR/ollama-load-request.json"
curl --fail --silent --show-error -H 'Content-Type: application/json' \
  --data-binary @"$EVIDENCE_DIR/ollama-load-request.json" \
  http://127.0.0.1:11434/api/generate >/dev/null
curl --fail --silent --show-error http://127.0.0.1:11434/api/ps \
  >"$EVIDENCE_DIR/ollama-ps-warm-before.json"
STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LLM_PROVIDER=ollama LLM_URL=http://127.0.0.1:11434 LLM_MODEL="$MODEL" \
  LOOMARR_EVAL_ALLOW_LOCAL=1 LOOMARR_EVAL_PROFILE="$PROFILE" \
  LOOMARR_EVAL_TRIALS="$TRIALS" LOOMARR_EVAL_OUT="$SCORECARD" \
  make eval-planner-cert
COMPLETED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
curl --fail --silent --show-error http://127.0.0.1:11434/api/ps \
  >"$EVIDENCE_DIR/ollama-ps-after.json"
```

Build the normalized capture from those exact bytes. `jq -rj` is intentional: it hashes each JSON
string without adding a newline. The macOS `stat -f %z` form records exact byte counts.

```sh
SOURCE_REPOSITORY="$(jq -er .id "$EVIDENCE_DIR/huggingface-model.json")"
SOURCE_REVISION="$(jq -er .sha "$EVIDENCE_DIR/huggingface-model.json")"
GGUF_FILE="$(basename "$GGUF")"
GGUF_SHA256="$(awk 'NR==1 {print $1}' "$EVIDENCE_DIR/gguf-sha256.txt")"
LICENSE_ID="$(jq -er .cardData.license "$EVIDENCE_DIR/huggingface-model.json")"
OLLAMA_DIGEST="$(jq -er --arg model "$MODEL" \
  '[.models[] | select(.name==$model and .model==$model)] | if length==1 then .[0].digest else error("model count") end' \
  "$EVIDENCE_DIR/ollama-list.json")"
QUANTIZATION="$(jq -er --arg model "$MODEL" \
  '[.models[] | select(.name==$model and .model==$model)] | if length==1 then .[0].details.quantization_level else error("model count") end' \
  "$EVIDENCE_DIR/ollama-list.json")"
TEMPLATE_SHA256="$(jq -rj .template "$EVIDENCE_DIR/ollama-show.json" | shasum -a 256 | awk '{print $1}')"
MODELFILE_SHA256="$(jq -rj .modelfile "$EVIDENCE_DIR/ollama-show.json" | shasum -a 256 | awk '{print $1}')"
LICENSE_SHA256="$(jq -rj .license "$EVIDENCE_DIR/ollama-show.json" | shasum -a 256 | awk '{print $1}')"
OLLAMA_VERSION="$(jq -er .version "$EVIDENCE_DIR/ollama-version.json")"
MACOS_VERSION="$(awk -F: '$1=="ProductVersion" {gsub(/^[[:space:]]+/,"",$2); print $2}' "$EVIDENCE_DIR/sw-vers.txt")"
MACOS_BUILD="$(awk -F: '$1=="BuildVersion" {gsub(/^[[:space:]]+/,"",$2); print $2}' "$EVIDENCE_DIR/sw-vers.txt")"
ARCHITECTURE="$(tr -d '[:space:]' <"$EVIDENCE_DIR/uname.txt")"
HARDWARE_MODEL="$(jq -er '.SPHardwareDataType | if length==1 then .[0].machine_model else error("hardware count") end' "$EVIDENCE_DIR/system-profiler.json")"
CHIP="$(jq -er '.SPHardwareDataType | if length==1 then .[0].chip_type else error("hardware count") end' "$EVIDENCE_DIR/system-profiler.json")"
MEMORY_BYTES="$(tr -d '[:space:]' <"$EVIDENCE_DIR/sysctl-hw-memsize.txt")"
AFTER_RAM="$(jq -er --arg model "$MODEL" '[.models[] | select(.name==$model and .model==$model)] | if length==1 then .[0].size-.[0].size_vram else error("model count") end' "$EVIDENCE_DIR/ollama-ps-after.json")"
AFTER_VRAM="$(jq -er --arg model "$MODEL" '[.models[] | select(.name==$model and .model==$model)] | if length==1 then .[0].size_vram else error("model count") end' "$EVIDENCE_DIR/ollama-ps-after.json")"
SCORECARD_SHA256="$(shasum -a 256 "$SCORECARD" | awk '{print $1}')"
SCORECARD_BYTES="$(stat -f %z "$SCORECARD")"
EVIDENCE_JSON="$(for path in "$EVIDENCE_DIR"/*; do
  jq -cn --arg kind "$(basename "$path")" \
    --arg sha256 "$(shasum -a 256 "$path" | awk '{print $1}')" \
    --argjson bytes "$(stat -f %z "$path")" '{kind:$kind,sha256:$sha256,bytes:$bytes}'
done | jq -s .)"

jq -n --arg runId "$RUN_ID" --arg startedAt "$STARTED_AT" --arg completedAt "$COMPLETED_AT" \
  --arg scorecardSha256 "$SCORECARD_SHA256" --argjson scorecardBytes "$SCORECARD_BYTES" \
  --arg tag "$MODEL" --arg digest "$OLLAMA_DIGEST" --arg sourceRepository "$SOURCE_REPOSITORY" \
  --arg sourceRevision "$SOURCE_REVISION" --arg ggufFile "$GGUF_FILE" --arg ggufSha256 "$GGUF_SHA256" \
  --arg quantization "$QUANTIZATION" --arg templateSha256 "$TEMPLATE_SHA256" \
  --arg modelfileSha256 "$MODELFILE_SHA256" --arg licenseId "$LICENSE_ID" \
  --arg licenseSha256 "$LICENSE_SHA256" --arg ollamaVersion "$OLLAMA_VERSION" \
  --arg macosVersion "$MACOS_VERSION" --arg macosBuild "$MACOS_BUILD" \
  --arg architecture "$ARCHITECTURE" --arg hardwareModel "$HARDWARE_MODEL" --arg chip "$CHIP" \
  --arg profile "$PROFILE" --argjson memory "$MEMORY_BYTES" --argjson trials "$TRIALS" \
  --argjson ram "$AFTER_RAM" --argjson vram "$AFTER_VRAM" --argjson evidence "$EVIDENCE_JSON" \
  '{schemaVersion:1,contract:"planner-reference-host-v1",runId:$runId,startedAt:$startedAt,
    completedAt:$completedAt,scorecardSha256:$scorecardSha256,scorecardBytes:$scorecardBytes,
    model:{tag:$tag,ollamaDigest:$digest,sourceRepository:$sourceRepository,
      sourceRevision:$sourceRevision,ggufFile:$ggufFile,ggufSha256:$ggufSha256,
      quantization:$quantization,contextLength:8192,templateSha256:$templateSha256,
      modelfileSha256:$modelfileSha256,licenseId:$licenseId,licenseSha256:$licenseSha256},
    runtime:{ollamaVersion:$ollamaVersion,macosVersion:$macosVersion,macosBuild:$macosBuild,
      architecture:$architecture,hardwareModel:$hardwareModel,chip:$chip,
      physicalUnifiedMemoryBytes:$memory},
    protocol:{profile:$profile,contextLength:8192,maxOutputTokens:2048,temperature:0.2,
      seed:null,coldStarts:1,warmupLoads:1,measuredWarmTrials:$trials},
    residency:{coldBefore:{selectedModelResident:false},warmBefore:{selectedModelResident:true,
      model:$tag,ollamaDigest:$digest,ramBytes:$ram,vramBytes:$vram},after:{selectedModelResident:true,
      model:$tag,ollamaDigest:$digest,ramBytes:$ram,vramBytes:$vram}},evidence:$evidence}' >"$CAPTURE"
```

Finally publish with fixed paths and a post-run time:

```sh
LOOMARR_PLANNER_REFERENCE_SCORECARD="$SCORECARD" \
LOOMARR_PLANNER_REFERENCE_CAPTURE="$CAPTURE" \
LOOMARR_PLANNER_REFERENCE_EVIDENCE_DIR="$EVIDENCE_DIR" \
LOOMARR_PLANNER_REFERENCE_GENERATED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
LOOMARR_PLANNER_REFERENCE_OUT="$OUT" \
make planner-reference-host
```

Publication is immutable: an existing output is never overwritten. The deep module re-hashes every
raw input; parses the source, GGUF, Ollama, residency, and macOS evidence; rejects
unknown/duplicate/trailing capture JSON; cross-checks every normalized artifact/runtime/host fact plus
model/profile/trials and resident memory against the scorecard; sorts the evidence set; and emits only
normalized facts plus digests and byte counts. Local paths and unrelated resident-model details remain
in neither the capture nor the manifest. The result is necessary provenance, not certification or
authority to train, deploy, or distribute. External compute and API work still shares the current $20
aggregate ceiling; remaining headroom is not pre-allocated to GPU work.

### Channel recommendation certification

`make channel-recommend-cert` is the independent certification lane for the recommendation pillar.
It sends only a closed, synthetic Library/preference snapshot to the configured model and asks for
inert Channel Concepts. It never exposes an operator identity or viewing history, supplies tools, or
executes a Channel, Proposal, approval, acquisition, or spend effect. The digest-pinned
`channel-recommendation-v2` holdout covers sparse, broad, repetitive, family, seasonal, era-heavy,
conflicting, and adversarial contexts; its `certification` split is excluded from the training
allowlist. Its case identities and normalized snapshot contents are mechanically disjoint from the
retained v1 no-ship holdout and the prompt-development corpus. The v2 contract keeps JSON mode and
binds every comparable call to exactly 1,024 maximum output tokens.

Run `make channel-recommend-cert-dry-run` first with the same model/profile and suite ceilings. It
emits a machine-readable contract with `inferenceAuthorized=false` and constructs no provider, so it
requires no API credential and spends nothing. The live target below remains a separate explicit
action.

The command is explicit, serial, and outside CI. Before it constructs a provider, set an exact
model/profile plus positive whole-suite ceilings:

```sh
LLM_PROVIDER=ollama \
LLM_URL=http://127.0.0.1:11434 \
LLM_MODEL=qwen3.5:9b \
LOOMARR_RECOMMEND_PROFILE=qwen35-local \
LOOMARR_RECOMMEND_MODEL_DIGEST=6488c96fa5fa \
LOOMARR_RECOMMEND_MAX_CALLS=8 \
LOOMARR_RECOMMEND_MAX_TOKENS=50000 \
LOOMARR_RECOMMEND_MAX_SPEND_NANOUSD=1 \
make channel-recommend-cert
```

For OpenRouter, use its exact canonical base, a concrete namespaced model, one pinned upstream
provider in `LOOMARR_EVAL_GENERATOR_UPSTREAM_PROVIDER`, and `OPENROUTER_API_KEY` (or `LLM_API_KEY`).
The route disables fallback and data collection and requests ZDR through the shared strict adapter.
Local certification also requires the hexadecimal Ollama artifact ID from `ollama list`; a tag alone
is mutable and is not certification identity. The scorecard records provider/model/profile, local
artifact digest or hosted upstream, prompt/schema/scorer versions, fixture digest, calls,
prompt/completion tokens, exact nanodollar charges, attempts, latency, hard failures, quality metrics,
floors, and the pre-registered selection margin. It excludes credentials, endpoint URLs, prompts,
generation IDs, reasoning text, and raw provider payloads. Missing hosted token or charge accounting
stops the suite; local Ollama is explicitly treated as unbilled. Crossing any ceiling records the
consumed call and stops before another case. Machine JSON and Markdown are written under
`$LOOMARR_ARTIFACT_DIR` unless the two `LOOMARR_RECOMMEND_*_OUT` variables override them.

Run at least the shared planner profile and one alternative against that identical frozen contract,
then compare their scorecards without inference:

```sh
LOOMARR_RECOMMEND_SHARED_PROFILE=qwen35-shared \
LOOMARR_RECOMMEND_SCORECARDS="artifacts/qwen.json artifacts/gemma.json" \
make channel-recommend-compare
```

The comparator rejects different fixture, prompt, schema, scorer, threshold, metric, or margin
identities. A scorecard with incomplete accounting, an interrupted suite, a hard failure, or a missed
floor is ineligible even if its serialized `certified` field says otherwise. `mean_quality` is the
equal mean of the seven frozen quality metrics. The shared planner model remains selected unless a
certified alternative exceeds it by at least `0.02`; if the shared model fails and an alternative
certifies, the result justifies a distinct recommendation route. This is a routing/fine-tuning gate,
not evidence that an adapter has already been trained or that certification transfers to another
pillar.

`make planner-tool-diagnostic` isolates the production turn immediately after one non-empty
synthetic catalog result. It requires explicit `LLM_URL` and `LLM_MODEL`; `LLM_PROVIDER` defaults to
`ollama`. An OpenRouter run also requires `LLM_API_KEY` and exactly one
`LOOMARR_EVAL_GENERATOR_UPSTREAM_PROVIDER`. The command uses the production system/user renderer,
catalog tool schema, assistant tool-call correlation, tool-result shape, sampling controls, and
post-result JSON mode. Its JSON report contains versioned prompt/tool/message-template identities,
SHA-256 digests, message roles, option flags, provider attribution, JSON validity, and whether the
model repeated a tool call. It never emits prompt text, tool-result content, model output,
credentials, or reasoning content. This is focused adapter evidence, not model certification.

`make eval-matrix` prevents tuning to one local model. It requires the ordinary exported `LLM_*`
configuration plus `OPENROUTER_API_KEY`, `OPENROUTER_MODEL`,
`OPENROUTER_GENERATOR_PROVIDER`, and `OPENROUTER_JUDGE_PROVIDER`;
`OPENROUTER_JUDGE_MODEL` may select a different hosted judge. Each role requires an exact immutable
namespaced model slug and exactly one concrete OpenRouter upstream provider; router/latest aliases and
comma-separated fallback orders fail preflight. It writes separate `local` and `openrouter` scorecards from the
exact same corpus. Both hosted roles use branded `openrouter` identity. Their actual chat requests
pin that singleton route, set `allow_fallbacks=false`, `require_parameters=true`,
`data_collection=deny`, and `zdr=true`; required certification fails before clients if either
applicable route is absent or invalid. Branded required certification also fails preflight unless
both generator and judge URLs are exactly OpenRouter's canonical OpenAI-compatible
`https://openrouter.ai/api/v1` endpoint; blank, alternate, and trailing-slash variants are rejected.
Generic/custom OpenAI-compatible evaluation remains flexible outside this branded lane. Keys are passed
only in request authorization and are never scorecard metadata. The command also requires
`LOOMARR_EVAL_ALLOW_LOCAL=1`: set it only after confirming the machine is idle and the configured
local runtime fits available RAM/VRAM without spilling. The two legs are sequential, and the target
does not provision or start Ollama. On a shared media server, run this manual certification during a
maintenance window; playback and transcode capacity take precedence over evaluation evidence.

The filler-admission bakeoff is a separate capture-then-replay workflow. Follow the
[OpenRouter filler bakeoff runbook](../engineering/filler-bakeoff-openrouter.md); never place blind
labels in its packet JSONL or run paid inference before the manifest, packet hashes, route profile,
request ceiling, and spend ceiling are locked.

### Rust dependency review

Renovate opens one weekly grouped PR for Cargo minor and patch updates across the production and
fuzz workspaces. Major updates require explicit Dependency Dashboard approval: update one direct
crate at a time, amend the §14 rationale when its capability or cost changes, and compare
`Cargo.lock` rather than accepting a generated diff blindly. Every Cargo update runs `make rust-audit`, `make rust-check`,
`make image-cert`, and the amd64/arm64 release build. An advisory ignore requires a reason, an owner,
and a removal issue in the same PR; an unannotated permanent ignore is not policy.

Loomarr-owned shipping Rust crates use `#![forbid(unsafe_code)]`. Transitive codecs may contain
unsafe or native code, which is why they remain behind the one-shot worker boundary. The fuzz harness
is non-shipping test infrastructure built around libFuzzer; its own source contains no unsafe block.

Auth tests need the negative cases: members get 403 on titles, approve and admin routes;
sessions die on disable.

## Green that proves nothing

Each of these has happened here:

- **A test that passes first try is suspect.** Sabotage the code and confirm it goes red.
- **`make ci-lint` needs `shellcheck` on `PATH`** — without it, actionlint skips the shell half
  and exits 0 locally while CI fails.
- **A fake that ignores its context** can't catch write-through-dead-context bugs.
- **A story on an unregistered route** snapshots "Not Found" as its baseline and stays green.
- **A pnpm config only fails on a cold install** — with `node_modules` present, pnpm doesn't
  re-evaluate build scripts.

## Determinism

Pod assembly and shuffling take an explicit seed. The visual suite pins the timezone to UTC.
Baselines are Linux-only, generated in the pinned Docker image; local macOS or Windows runs
write differently-suffixed files that are gitignored.

## Fixtures

Fixtures in `internal/testkit/fixtures/` are captures with source-version comments. Write
parsers against them, not against remembered field names — and never let a fixture equate two
distinct identifiers, which has hidden shipped bugs by making a wrong lookup return the right
answer.
