package releaseverify

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVerifyAndroidReleaseWorkflow(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	workflowPath := filepath.Join(filepath.Dir(source), "..", "..", ".github", "workflows", "android-beta.yml")
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	good := string(data)
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr bool
	}{
		{name: "protected signed testing release", mutate: func(value string) string { return value }},
		{name: "cannot run on pull requests", mutate: replaceOnce("workflow_dispatch:", "pull_request:", t), wantErr: true},
		{name: "cannot expose production", mutate: replaceOnce("          ANDROID_RELEASE_TRACK: internal", "          ANDROID_RELEASE_TRACK: production", t), wantErr: true},
		{name: "publication requires an explicit choice", mutate: replaceOnce("        default: false", "        default: true", t), wantErr: true},
		{name: "protected environment is fixed", mutate: replaceOnce("    environment: android-beta", "    environment: unprotected", t), wantErr: true},
		{name: "non-main dispatch is rejected", mutate: replaceOnce("    if: github.ref == 'refs/heads/main'", "    if: github.ref != 'refs/heads/main'", t), wantErr: true},
		{name: "workflow permissions stay read only", mutate: replaceOnce("  contents: read", "  contents: write", t), wantErr: true},
		{name: "artifact validation cannot be removed", mutate: replaceOnce(`        run: ./scripts/download-android-ci-artifact.sh "${{ inputs.ci_run_id }}" .artifacts/android-ci`, "        run: true", t), wantErr: true},
		{name: "sign-only handoff cannot be replaced", mutate: replaceOnce("          ./scripts/sign-android-ci-artifact.sh \"$unsigned\" \"$manifest\" \"$ANDROID_RELEASE_OUTPUT_DIR\"", "          ./gradlew bundleRelease", t), wantErr: true},
		{name: "publisher cannot be bypassed", mutate: replaceOnce("            ./scripts/publish-android-beta.sh \"$aab\" \"$manifest\"", "            curl https://example.invalid/publish", t), wantErr: true},
		{name: "publisher cannot run unconditionally", mutate: replaceOnce("        if: inputs.publish_to_play", "        if: always()", t), wantErr: true},
		{name: "credential cleanup cannot be conditional", mutate: replaceOnce("        if: always()", "        if: success()", t), wantErr: true},
		{name: "mutable actions are rejected", mutate: replaceOnce("actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", "actions/checkout@v7", t), wantErr: true},
		{name: "second job is rejected", mutate: replaceOnce("jobs:\n", "jobs:\n  bypass:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo bypass\n", t), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "android-beta.yml")
			if err := os.WriteFile(path, []byte(tc.mutate(good)), 0o600); err != nil {
				t.Fatal(err)
			}
			err := VerifyAndroidReleaseWorkflow(path)
			if tc.wantErr && err == nil {
				t.Fatal("VerifyAndroidReleaseWorkflow accepted an unsafe workflow")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("VerifyAndroidReleaseWorkflow: %v", err)
			}
		})
	}
}

func TestCIAndroidGradleCachePolicy(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	workflowPath := filepath.Join(filepath.Dir(source), "..", "..", ".github", "workflows", "ci-android.yml")
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)

	for _, want := range []string{
		"id: gradle-cache",
		"path: |\n            ~/.gradle/caches\n            ~/.gradle/wrapper",
		"key: android-tv-react-native-v1-${{ runner.os }}-temurin-21-node-${{ env.NODE_VERSION }}-${{ hashFiles('web/apps/tv/**', 'web/packages/**', 'web/pnpm-lock.yaml', 'web/scripts/**') }}-${{ github.sha }}-${{ github.run_id }}",
		"restore-keys: android-tv-react-native-v1-${{ runner.os }}-temurin-21-node-${{ env.NODE_VERSION }}-${{ hashFiles('web/apps/tv/**', 'web/packages/**', 'web/pnpm-lock.yaml', 'web/scripts/**') }}-${{ github.sha }}-",
		"- name: Record Gradle cache provenance",
		"gradle-cache-primary-key=android-tv-react-native-v1-${{ runner.os }}-temurin-21-node-${{ env.NODE_VERSION }}-${{ hashFiles('web/apps/tv/**', 'web/packages/**', 'web/pnpm-lock.yaml', 'web/scripts/**') }}-${{ github.sha }}-${{ github.run_id }}",
		"gradle-cache-hit=${{ steps.gradle-cache.outputs.cache-hit == 'true' }}",
		"gradle-cache-source-sha=${GITHUB_SHA}",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("ci-android Gradle cache policy missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"steps.gradle-cache.outputs.cache-primary-key",
		"steps.gradle-cache.outputs.cache-matched-key",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("ci-android Gradle cache policy claims unavailable %q output", forbidden)
		}
	}
}

func replaceOnce(oldValue, newValue string, t *testing.T) func(string) string {
	t.Helper()
	return func(value string) string {
		if strings.Count(value, oldValue) != 1 {
			t.Fatalf("fixture contains %d instances of %q, want 1", strings.Count(value, oldValue), oldValue)
		}
		return strings.Replace(value, oldValue, newValue, 1)
	}
}
