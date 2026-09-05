package releaseverify

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type workflowTopologyAuthority struct {
	jobs map[string]int
}

func workflowTopologyAuthorityEntries() map[string]workflowTopologyAuthority {
	return map[string]workflowTopologyAuthority{
		"android-beta.yml":            {jobs: map[string]int{"release": 9}},
		"apple-compilation-cache.yml": {jobs: map[string]int{"publish": 13}},
		"cache-cleanup.yml":           {jobs: map[string]int{"cleanup": 1}},
		"ci-agent.yml":                {jobs: map[string]int{"run": 3}},
		"ci-android.yml":              {jobs: map[string]int{"run": 11}},
		"ci-apple-mobile.yml":         {jobs: map[string]int{"run": 9}},
		"ci-apple-tv.yml":             {jobs: map[string]int{"run": 9}},

		"ci-apple-cache-validation.yml": {jobs: map[string]int{"producer": 7, "consumer": 7}},

		"ci-clients.yml":             {jobs: map[string]int{"run": 7}},
		"ci-docs.yml":                {jobs: map[string]int{"run": 6}},
		"ci-frontend.yml":            {jobs: map[string]int{"run": 7}},
		"ci-go-contracts.yml":        {jobs: map[string]int{"run": 14}},
		"ci-go.yml":                  {jobs: map[string]int{"run": 12}},
		"ci-image-certification.yml": {jobs: map[string]int{"run": 5}},
		"ci-image.yml":               {jobs: map[string]int{"run": 4}},
		"ci-playwright.yml":          {jobs: map[string]int{"run": 9}},
		"ci-postgres.yml":            {jobs: map[string]int{"run": 6}},
		"ci-rust-contracts.yml":      {jobs: map[string]int{"run": 3}},
		"ci-tuner.yml":               {jobs: map[string]int{"run": 7}},
		"ci.yml": {jobs: map[string]int{
			"changes": 3, "agent-harness-macos": 0, "release-candidate-scope": 1, "full-manual-scope": 1,
			"ci-policy": 6, "rust-contracts": 0, "go-contracts": 0, "image-certification": 0, "go": 0,
			"store-postgres": 0, "frontend": 0, "clients": 0, "apple-mobile": 0, "apple-tv": 0, "apple-cache-validation": 0,
			"playwright": 0, "tuner": 0, "image": 0, "docs": 0, "android": 0, "ci-ok": 3,
		}},
		"image-benchmark.yml":  {jobs: map[string]int{"benchmark": 6}},
		"pages.yml":            {jobs: map[string]int{"build": 5, "deploy": 1}},
		"release-notes.yml":    {jobs: map[string]int{"publish-notes": 4}},
		"release.yml":          {jobs: map[string]int{"build": 7, "publish": 9}},
		"rust-maintenance.yml": {jobs: map[string]int{"supply-chain": 5, "fuzz": 5}},
	}
}

type reusableWorkflowCallerAuthority struct {
	name      string
	condition string
}

func reusableWorkflowCallerAuthorityEntries() map[string]reusableWorkflowCallerAuthority {
	return map[string]reusableWorkflowCallerAuthority{
		"agent-harness-macos": {name: "Agent harness (macOS)", condition: "needs.changes.outputs.lane != 'pr-fast' && needs.changes.outputs.impact_agent == 'true'"},
		"rust-contracts":      {name: "Rust — repository contracts", condition: "needs.changes.outputs.impact_rust == 'true' || needs.changes.outputs.release_candidate == 'true'"},
		"go-contracts":        {name: "Go — repository contracts", condition: "needs.changes.outputs.impact_contracts == 'true' || needs.changes.outputs.release_candidate == 'true'"},
		"image-certification": {name: "Rust image — runtime certification", condition: "needs.changes.outputs.lane != 'pr-fast' && (needs.changes.outputs.impact_rust == 'true' || needs.changes.outputs.release_candidate == 'true')"},
		"go":                  {name: "Go — race-policy tests", condition: "needs.changes.outputs.lane != 'pr-fast' && needs.changes.outputs.impact_go == 'true'"},
		"store-postgres":      {name: "Store conformance (Postgres)", condition: "needs.changes.outputs.lane != 'pr-fast' && needs.changes.outputs.impact_postgres == 'true'"},
		"frontend":            {name: "Frontend — biome + typecheck + unit + build", condition: "needs.changes.outputs.impact_web == 'true'"},
		"clients":             {name: "Shared clients — lint + test + browser/iOS/Android/TV bundles", condition: "needs.changes.outputs.impact_clients == 'true'"},
		"apple-mobile":        {name: "Apple mobile — native build + launch", condition: "needs.changes.outputs.lane != 'pr-fast' && needs.changes.outputs.impact_apple_mobile == 'true'"},
		"apple-tv":            {name: "Apple TV — native build + launch", condition: "needs.changes.outputs.lane != 'pr-fast' && needs.changes.outputs.impact_apple_tv == 'true'"},

		"apple-cache-validation": {name: "Apple compilation cache — supported-toolchain validation", condition: "github.event_name == 'workflow_dispatch' && inputs.scope == 'apple-cache-validation'"},

		"playwright": {name: "Playwright — visual + a11y + e2e", condition: "needs.changes.outputs.lane != 'pr-fast' && (needs.changes.outputs.impact_visual == 'true' || needs.changes.outputs.impact_e2e == 'true')"},
		"tuner":      {name: "Tuner — Chromium + Firefox + WebKit", condition: "needs.changes.outputs.lane != 'pr-fast' && needs.changes.outputs.impact_tuner == 'true'"},
		"image":      {name: "Image — release build", condition: "needs.changes.outputs.lane != 'pr-fast' && needs.changes.outputs.impact_image == 'true'"},
		"docs":       {name: "Docs — links + structure + prose", condition: "needs.changes.outputs.impact_docs == 'true'"},
		"android":    {name: "Android TV — React Native Play bundle", condition: "needs.changes.outputs.impact_android == 'true'"},
	}
}

func verifyWorkflowTopology(workflowName string, workflow *yaml.Node) error {
	authority, registered := workflowAuthorityCatalog().topology[workflowName]
	if !registered {
		return fmt.Errorf("workflow %s has no registered topology authority", workflowName)
	}
	jobs, ok := mappingValue(workflow, "jobs")
	if !ok || jobs.Kind != yaml.MappingNode || len(jobs.Content)/2 != len(authority.jobs) {
		return fmt.Errorf("workflow %s job topology differs from its authority", workflowName)
	}
	for index := 0; index < len(jobs.Content); index += 2 {
		jobName, job := jobs.Content[index].Value, jobs.Content[index+1]
		stepCount, expected := authority.jobs[jobName]
		if !expected || job.Kind != yaml.MappingNode {
			return fmt.Errorf("workflow %s contains unregistered job %s", workflowName, jobName)
		}
		if _, reusable := mappingValue(job, "uses"); reusable {
			if stepCount != 0 {
				return fmt.Errorf("workflow %s job %s changed from a step job to a reusable caller", workflowName, jobName)
			}
			if err := verifyReusableWorkflowCaller(jobName, job); err != nil {
				return fmt.Errorf("workflow %s job %s: %w", workflowName, jobName, err)
			}
			continue
		}
		steps, hasSteps := mappingValue(job, "steps")
		if !hasSteps || steps.Kind != yaml.SequenceNode || len(steps.Content) != stepCount {
			return fmt.Errorf("workflow %s job %s step topology differs from its authority", workflowName, jobName)
		}
		for stepIndex, step := range steps.Content {
			if step.Kind != yaml.MappingNode {
				return fmt.Errorf("workflow %s job %s step %d must be a mapping", workflowName, jobName, stepIndex+1)
			}
			_, hasRun := mappingValue(step, "run")
			_, hasAction := mappingValue(step, "uses")
			if hasRun == hasAction {
				return fmt.Errorf("workflow %s job %s step %d must contain exactly one of run or uses", workflowName, jobName, stepIndex+1)
			}
		}
	}
	return nil
}

func verifyReusableWorkflowCaller(jobName string, job *yaml.Node) error {
	authority, ok := workflowAuthorityCatalog().reusableCallers[jobName]
	if !ok || !mappingHasOnlyKeys(job, "name", "needs", "if", "uses") {
		return fmt.Errorf("reusable-workflow caller keys differ from their exact authority")
	}
	for key, want := range map[string]string{
		"name":  authority.name,
		"needs": "changes",
		"if":    authority.condition,
		"uses":  "./" + workflowAuthorityCatalog().familyWorkflows[jobName],
	} {
		value, present := mappingValue(job, key)
		if !present || value.Kind != yaml.ScalarNode || value.Tag != "!!str" || value.Value != want {
			return fmt.Errorf("reusable-workflow caller %s differs from its exact authority", key)
		}
	}
	return nil
}
