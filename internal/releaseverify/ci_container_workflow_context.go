package releaseverify

import (
	"fmt"
	"slices"
	"strconv"

	"gopkg.in/yaml.v3"
)

type workflowJobContextKey struct {
	workflow string
	job      string
}

type workflowJobContextAuthority struct {
	name            string
	runsOn          string
	needs           string
	needsList       []string
	timeoutMinutes  int
	environmentName string
	environmentURL  string
	strategy        *workflowStrategyAuthority
}

type workflowStrategyAuthority struct {
	shards  []int
	include []workflowMatrixEntryAuthority
}

type workflowMatrixEntryAuthority struct {
	platform string
	runner   string
	arch     string
}

func workflowJobContextAuthorityEntries() map[workflowJobContextKey]workflowJobContextAuthority {
	return map[workflowJobContextKey]workflowJobContextAuthority{
		{workflow: "android-beta.yml", job: "release"}:            {name: "Verify, sign, and optionally publish Android TV beta", runsOn: "ubuntu-latest", environmentName: "android-beta"},
		{workflow: "apple-compilation-cache.yml", job: "publish"}: {name: "Publish validated Apple compilation cache", runsOn: "xcode-27", timeoutMinutes: 75},
		{workflow: "cache-cleanup.yml", job: "cleanup"}:           {name: "Drop the closed PR's caches", runsOn: "ubuntu-latest"},
		{workflow: "ci-agent.yml", job: "run"}:                    {name: "Agent harness (macOS)", runsOn: "macos-latest"},
		{workflow: "ci-android.yml", job: "run"}:                  {name: "Android TV — React Native Play bundle", runsOn: "ubuntu-latest"},
		{workflow: "ci-apple-mobile.yml", job: "run"}:             {name: "Apple mobile — native build + launch", runsOn: "xcode-27", timeoutMinutes: 75},
		{workflow: "ci-apple-tv.yml", job: "run"}:                 {name: "Apple TV — native build + launch", runsOn: "xcode-27", timeoutMinutes: 60},

		{workflow: "ci-apple-cache-validation.yml", job: "producer"}: {name: "Apple compilation cache — produce on Xcode 27", runsOn: "xcode-27", timeoutMinutes: 75},
		{workflow: "ci-apple-cache-validation.yml", job: "consumer"}: {name: "Apple compilation cache — consume on distinct Xcode 27 runner", runsOn: "xcode-27", needsList: []string{"producer"}, timeoutMinutes: 75},

		{workflow: "ci-clients.yml", job: "run"}:             {name: "Shared clients — lint + test + browser/iOS/Android/TV bundles", runsOn: "ubuntu-latest"},
		{workflow: "ci-docs.yml", job: "run"}:                {name: "Docs — links + structure + prose", runsOn: "ubuntu-latest"},
		{workflow: "ci-frontend.yml", job: "run"}:            {name: "Frontend — biome + typecheck + unit + build (${{ matrix.shard }}/${{ strategy.job-total }})", runsOn: "ubuntu-latest", strategy: &workflowStrategyAuthority{shards: []int{1, 2}}},
		{workflow: "ci-go-contracts.yml", job: "run"}:        {name: "Go — repository contracts", runsOn: "ubuntu-latest"},
		{workflow: "ci-go.yml", job: "run"}:                  {name: "Go — race-policy tests (${{ matrix.shard }}/${{ strategy.job-total }})", runsOn: "ubuntu-latest", strategy: &workflowStrategyAuthority{shards: []int{1, 2, 3}}},
		{workflow: "ci-image-certification.yml", job: "run"}: {name: "Rust image — runtime certification", runsOn: "ubuntu-latest"},
		{workflow: "ci-image.yml", job: "run"}: {
			name: "Image — release build (${{ matrix.platform }})", runsOn: "${{ matrix.runner }}", timeoutMinutes: 45,
			strategy: &workflowStrategyAuthority{include: []workflowMatrixEntryAuthority{{platform: "linux/amd64", runner: "ubuntu-24.04"}, {platform: "linux/arm64", runner: "ubuntu-24.04-arm"}}},
		},
		{workflow: "ci-playwright.yml", job: "run"}:     {name: "Playwright — visual + a11y + e2e (${{ matrix.shard }}/${{ strategy.job-total }})", runsOn: "ubuntu-latest", strategy: &workflowStrategyAuthority{shards: []int{1, 2, 3, 4}}},
		{workflow: "ci-postgres.yml", job: "run"}:       {name: "Store conformance (Postgres)", runsOn: "ubuntu-latest"},
		{workflow: "ci-rust-contracts.yml", job: "run"}: {name: "Rust — repository contracts", runsOn: "ubuntu-latest"},
		{workflow: "ci-tuner.yml", job: "run"}:          {name: "Tuner — Chromium + Firefox + WebKit", runsOn: "macos-latest"},
		{workflow: "ci.yml", job: "ci-policy"}:          {name: "CI policy — workflow, harness, and docs contracts", runsOn: "ubuntu-latest", needs: "changes"},
		{workflow: "ci.yml", job: "changes"}:            {name: "What changed", runsOn: "ubuntu-latest"},
		{workflow: "ci.yml", job: "ci-ok"}: {
			name: "CI", runsOn: "ubuntu-latest",
			needsList: []string{"changes", "release-candidate-scope", "full-manual-scope", "ci-policy", "agent-harness-macos", "rust-contracts", "go-contracts", "image-certification", "go", "store-postgres", "frontend", "clients", "apple-mobile", "apple-tv", "apple-cache-validation", "playwright", "tuner", "image", "docs", "android"},
		},
		{workflow: "image-benchmark.yml", job: "benchmark"}: {
			name: "AVIF ladders (${{ matrix.platform }})", runsOn: "${{ matrix.runner }}", timeoutMinutes: 30,
			strategy: &workflowStrategyAuthority{include: []workflowMatrixEntryAuthority{{platform: "linux-amd64", runner: "ubuntu-24.04"}, {platform: "linux-arm64", runner: "ubuntu-24.04-arm"}}},
		},
		{workflow: "pages.yml", job: "build"}:                 {name: "Build", runsOn: "ubuntu-latest"},
		{workflow: "pages.yml", job: "deploy"}:                {name: "Deploy", runsOn: "ubuntu-latest", needs: "build", environmentName: "github-pages", environmentURL: "${{ steps.deployment.outputs.page_url }}"},
		{workflow: "release-notes.yml", job: "publish-notes"}: {name: "Publish GitHub Release notes", runsOn: "ubuntu-latest"},
		{workflow: "release.yml", job: "build"}: {
			name: "Build image (${{ matrix.platform }})", runsOn: "${{ matrix.runner }}",
			strategy: &workflowStrategyAuthority{include: []workflowMatrixEntryAuthority{{platform: "linux/amd64", runner: "ubuntu-24.04", arch: "amd64"}, {platform: "linux/arm64", runner: "ubuntu-24.04-arm", arch: "arm64"}}},
		},
		{workflow: "release.yml", job: "publish"}:               {name: "Sign and publish image", runsOn: "ubuntu-latest", needs: "build"},
		{workflow: "rust-maintenance.yml", job: "supply-chain"}: {name: "Advisories, licences, and sources", runsOn: "ubuntu-24.04", timeoutMinutes: 20},
		{workflow: "rust-maintenance.yml", job: "fuzz"}:         {name: "Bounded protocol and decoder fuzz", runsOn: "ubuntu-24.04", timeoutMinutes: 20},
	}
}

func verifyWorkflowJobContextAuthority(workflowName, jobName string, job *yaml.Node) error {
	want, ok := workflowAuthorityCatalog().jobContexts[workflowJobContextKey{workflow: workflowName, job: jobName}]
	if !ok {
		return fmt.Errorf("workflow %s job %s has no declarative execution-context authority", workflowName, jobName)
	}
	label := fmt.Sprintf("workflow %s job %s", workflowName, jobName)
	for key, value := range map[string]string{"name": want.name, "runs-on": want.runsOn} {
		if err := verifyExactOptionalScalar(job, key, value, label); err != nil {
			return err
		}
	}
	if err := verifyWorkflowNeeds(job, want, label); err != nil {
		return err
	}
	if err := verifyExactOptionalInteger(job, "timeout-minutes", want.timeoutMinutes, label); err != nil {
		return err
	}
	if err := verifyWorkflowEnvironment(job, want, label); err != nil {
		return err
	}
	return verifyWorkflowStrategy(job, want.strategy, label)
}

func verifyExactOptionalScalar(scope *yaml.Node, key, want, label string) error {
	got, present := mappingValue(scope, key)
	if want == "" {
		if present {
			return fmt.Errorf("%s must not set %s", label, key)
		}
		return nil
	}
	if !present || got.Kind != yaml.ScalarNode || got.Tag != "!!str" || got.Value != want {
		return fmt.Errorf("%s %s differs from its source-bound authority", label, key)
	}
	return nil
}

func verifyExactOptionalInteger(scope *yaml.Node, key string, want int, label string) error {
	got, present := mappingValue(scope, key)
	if want == 0 {
		if present {
			return fmt.Errorf("%s must not set %s", label, key)
		}
		return nil
	}
	if !present || got.Kind != yaml.ScalarNode || got.Tag != "!!int" {
		return fmt.Errorf("%s %s differs from its source-bound authority", label, key)
	}
	value, err := strconv.Atoi(got.Value)
	if err != nil || value != want {
		return fmt.Errorf("%s %s differs from its source-bound authority", label, key)
	}
	return nil
}

func verifyWorkflowNeeds(job *yaml.Node, want workflowJobContextAuthority, label string) error {
	got, present := mappingValue(job, "needs")
	if want.needs == "" && len(want.needsList) == 0 {
		if present {
			return fmt.Errorf("%s must not set needs", label)
		}
		return nil
	}
	if want.needs != "" {
		if !present || got.Kind != yaml.ScalarNode || got.Tag != "!!str" || got.Value != want.needs {
			return fmt.Errorf("%s needs differs from its source-bound authority", label)
		}
		return nil
	}
	if !present || got.Kind != yaml.SequenceNode || len(got.Content) != len(want.needsList) {
		return fmt.Errorf("%s needs differs from its source-bound authority", label)
	}
	for index, dependency := range got.Content {
		if dependency.Kind != yaml.ScalarNode || dependency.Tag != "!!str" || dependency.Value != want.needsList[index] {
			return fmt.Errorf("%s needs differs from its source-bound authority", label)
		}
	}
	return nil
}

func verifyWorkflowEnvironment(job *yaml.Node, want workflowJobContextAuthority, label string) error {
	got, present := mappingValue(job, "environment")
	if want.environmentName == "" {
		if present {
			return fmt.Errorf("%s must not set environment", label)
		}
		return nil
	}
	if want.environmentURL == "" {
		if !present || got.Kind != yaml.ScalarNode || got.Tag != "!!str" || got.Value != want.environmentName {
			return fmt.Errorf("%s environment differs from its source-bound authority", label)
		}
		return nil
	}
	if !present || got.Kind != yaml.MappingNode || !mappingHasOnlyKeys(got, "name", "url") {
		return fmt.Errorf("%s environment differs from its source-bound authority", label)
	}
	for key, value := range map[string]string{"name": want.environmentName, "url": want.environmentURL} {
		entry, ok := mappingValue(got, key)
		if !ok || entry.Kind != yaml.ScalarNode || entry.Tag != "!!str" || entry.Value != value {
			return fmt.Errorf("%s environment differs from its source-bound authority", label)
		}
	}
	return nil
}

func verifyWorkflowStrategy(job *yaml.Node, want *workflowStrategyAuthority, label string) error {
	strategy, present := mappingValue(job, "strategy")
	if want == nil {
		if present {
			return fmt.Errorf("%s must not set strategy", label)
		}
		return nil
	}
	if !present || strategy.Kind != yaml.MappingNode || !mappingHasOnlyKeys(strategy, "fail-fast", "matrix") {
		return fmt.Errorf("%s strategy differs from its source-bound authority", label)
	}
	failFast, ok := mappingValue(strategy, "fail-fast")
	if !ok || failFast.Kind != yaml.ScalarNode || failFast.Tag != "!!bool" || failFast.Value != "false" {
		return fmt.Errorf("%s strategy fail-fast differs from its source-bound authority", label)
	}
	matrix, ok := mappingValue(strategy, "matrix")
	if !ok || matrix.Kind != yaml.MappingNode {
		return fmt.Errorf("%s matrix differs from its source-bound authority", label)
	}
	if len(want.shards) > 0 {
		if !mappingHasOnlyKeys(matrix, "shard") {
			return fmt.Errorf("%s matrix differs from its source-bound authority", label)
		}
		return verifyWorkflowShards(matrix, want.shards, label)
	}
	if !mappingHasOnlyKeys(matrix, "include") {
		return fmt.Errorf("%s matrix differs from its source-bound authority", label)
	}
	return verifyWorkflowMatrixInclude(matrix, want.include, label)
}

func verifyWorkflowShards(matrix *yaml.Node, want []int, label string) error {
	shards, ok := mappingValue(matrix, "shard")
	if !ok || shards.Kind != yaml.SequenceNode || len(shards.Content) != len(want) {
		return fmt.Errorf("%s matrix shards differ from their source-bound authority", label)
	}
	got := make([]int, len(shards.Content))
	for index, shard := range shards.Content {
		value, err := strconv.Atoi(shard.Value)
		if shard.Kind != yaml.ScalarNode || shard.Tag != "!!int" || err != nil {
			return fmt.Errorf("%s matrix shards must be integers", label)
		}
		got[index] = value
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("%s matrix shards differ from their source-bound authority", label)
	}
	return nil
}

func verifyWorkflowMatrixInclude(matrix *yaml.Node, want []workflowMatrixEntryAuthority, label string) error {
	include, ok := mappingValue(matrix, "include")
	if !ok || include.Kind != yaml.SequenceNode || len(include.Content) != len(want) {
		return fmt.Errorf("%s matrix include differs from its source-bound authority", label)
	}
	for index, entry := range include.Content {
		keys := []string{"platform", "runner"}
		if want[index].arch != "" {
			keys = append(keys, "arch")
		}
		if entry.Kind != yaml.MappingNode || !mappingHasOnlyKeys(entry, keys...) {
			return fmt.Errorf("%s matrix include entry %d differs from its source-bound authority", label, index+1)
		}
		platform, platformOK := mappingValue(entry, "platform")
		runner, runnerOK := mappingValue(entry, "runner")
		if !platformOK || !runnerOK || platform.Kind != yaml.ScalarNode || platform.Tag != "!!str" || runner.Kind != yaml.ScalarNode || runner.Tag != "!!str" ||
			platform.Value != want[index].platform || runner.Value != want[index].runner {
			return fmt.Errorf("%s matrix include entry %d differs from its source-bound authority", label, index+1)
		}
		if want[index].arch != "" {
			arch, ok := mappingValue(entry, "arch")
			if !ok || arch.Kind != yaml.ScalarNode || arch.Tag != "!!str" || arch.Value != want[index].arch {
				return fmt.Errorf("%s matrix include entry %d differs from its source-bound authority", label, index+1)
			}
		}
	}
	return nil
}

func mappingHasOnlyKeys(mapping *yaml.Node, keys ...string) bool {
	if mapping == nil || mapping.Kind != yaml.MappingNode || len(mapping.Content) != len(keys)*2 {
		return false
	}
	want := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		want[key] = struct{}{}
	}
	for index := 0; index < len(mapping.Content); index += 2 {
		if _, ok := want[mapping.Content[index].Value]; !ok {
			return false
		}
	}
	return true
}
