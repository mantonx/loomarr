package releaseverify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIImpactClassifier(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", ".."))
	cmd := exec.Command("bash", filepath.Join("scripts", "ci-impact-test.sh"))
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ci impact classifier contract: %v\n%s", err, output)
	}
}

func TestCILaneSelector(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", ".."))
	cmd := exec.Command("bash", filepath.Join("scripts", "ci-lane-test.sh"))
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CI lane selector contract: %v\n%s", err, output)
	}
}

func TestCIRunMetrics(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", ".."))
	cmd := exec.Command("bash", filepath.Join("scripts", "ci-run-metrics-test.sh"))
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CI run metrics contract: %v\n%s", err, output)
	}
}

func TestGoImpactSelector(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", ".."))
	cmd := exec.Command("bash", filepath.Join("scripts", "go-impact-test.sh"))
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Go impact selector contract: %v\n%s", err, output)
	}
}

func TestCIDispatchScope(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", ".."))
	cmd := exec.Command("bash", filepath.Join("scripts", "ci-dispatch-scope-test.sh"))
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CI dispatch scope contract: %v\n%s", err, output)
	}
}

func TestReleaseSourceEvidence(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", ".."))
	cmd := exec.Command("bash", filepath.Join("scripts", "validate-release-source-test.sh"))
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("release source evidence contract: %v\n%s", err, output)
	}
}

func TestAndroidReleaseSourceEvidence(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", ".."))
	cmd := exec.Command("bash", filepath.Join("scripts", "validate-android-release-source-test.sh"))
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Android release source evidence contract: %v\n%s", err, output)
	}
}

func TestLegacyAndroidImpactIncludesEvidenceAuthorities(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", ".."))
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflow), "ci-impact|validate-android-release-source") {
		t.Fatal("legacy Android impact filter must invalidate evidence when its classifier or source validator changes")
	}
}
