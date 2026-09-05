package releaseverify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type renovateConfig struct {
	EnabledManagers            []string              `json:"enabledManagers"`
	AutomergeType              string                `json:"automergeType"`
	PlatformAutomerge          bool                  `json:"platformAutomerge"`
	IgnoreTests                bool                  `json:"ignoreTests"`
	InternalChecksFilter       string                `json:"internalChecksFilter"`
	MinimumReleaseAgeBehaviour string                `json:"minimumReleaseAgeBehaviour"`
	RangeStrategy              string                `json:"rangeStrategy"`
	LockFileMaintenance        renovateEnabled       `json:"lockFileMaintenance"`
	CustomManagers             []renovateCustom      `json:"customManagers"`
	PackageRules               []renovatePackageRule `json:"packageRules"`
}

type renovateEnabled struct {
	Enabled bool `json:"enabled"`
}

type renovateCustom struct {
	Description         string   `json:"description"`
	ManagerFilePatterns []string `json:"managerFilePatterns"`
	DatasourceTemplate  string   `json:"datasourceTemplate"`
	VersioningTemplate  string   `json:"versioningTemplate"`
}

type renovatePackageRule struct {
	Description                 string   `json:"description"`
	MatchManagers               []string `json:"matchManagers"`
	MatchDatasources            []string `json:"matchDatasources"`
	MatchDepTypes               []string `json:"matchDepTypes"`
	MatchFileNames              []string `json:"matchFileNames"`
	MatchPackageNames           []string `json:"matchPackageNames"`
	MatchUpdateTypes            []string `json:"matchUpdateTypes"`
	MatchCurrentVersion         string   `json:"matchCurrentVersion"`
	GroupName                   string   `json:"groupName"`
	MinimumReleaseAge           string   `json:"minimumReleaseAge"`
	Automerge                   bool     `json:"automerge"`
	DependencyDashboardApproval bool     `json:"dependencyDashboardApproval"`
	Enabled                     *bool    `json:"enabled"`
}

func TestRenovateOwnsEveryDependencyEcosystem(t *testing.T) {
	config := readRenovateConfig(t)
	requireExactSet(t, "enabled managers", config.EnabledManagers, []string{
		"gomod", "npm", "cargo", "github-actions", "dockerfile", "docker-compose", "custom.regex",
	})
	if config.AutomergeType != "pr" || !config.PlatformAutomerge || config.IgnoreTests {
		t.Fatalf("native checked PR automerge is required: %+v", config)
	}
	if config.MinimumReleaseAgeBehaviour != "timestamp-required" {
		t.Fatalf("minimumReleaseAgeBehaviour = %q", config.MinimumReleaseAgeBehaviour)
	}
	if config.InternalChecksFilter != "strict" {
		t.Fatalf("internalChecksFilter = %q, want strict", config.InternalChecksFilter)
	}
	if config.RangeStrategy != "pin" {
		t.Fatalf("rangeStrategy = %q, want pin", config.RangeStrategy)
	}
	if config.LockFileMaintenance.Enabled {
		t.Fatal("unscoped lockfile maintenance must remain disabled")
	}
	if len(config.CustomManagers) != 5 {
		t.Fatalf("custom managers = %d, want all repository-owned pin managers", len(config.CustomManagers))
	}
	manager := customManagerByDescription(t, config.CustomManagers, "Update repository-owned test container image authorities")
	requireExactSet(t, "test image authority paths", manager.ManagerFilePatterns, []string{
		"/^internal/testkit/(?:postgres|ryuk)image/image\\.txt$/",
	})
	if manager.DatasourceTemplate != "docker" || manager.VersioningTemplate != "docker" {
		t.Fatalf("test image manager = %+v", manager)
	}
	for _, description := range []string{
		"Update pinned Go development tools",
		"Update pinned npm command-line tools",
		"Update documentation tool containers",
		"Keep the Playwright package and browser image discoverable together",
	} {
		_ = customManagerByDescription(t, config.CustomManagers, description)
	}
	if _, err := os.Stat(filepath.Join("..", "..", ".github", "dependabot.yml")); !os.IsNotExist(err) {
		t.Fatalf("Dependabot config must be retired, stat err = %v", err)
	}
}

func TestRenovateAutomergeIsAnExplicitAgedAllowlist(t *testing.T) {
	config := readRenovateConfig(t)
	var automerge []renovatePackageRule
	for _, rule := range config.PackageRules {
		if rule.Automerge {
			automerge = append(automerge, rule)
		}
	}
	if len(automerge) != 2 {
		t.Fatalf("automerge rules = %d, want exactly two explicit low-risk classes", len(automerge))
	}

	goPatches := ruleByDescription(t, automerge, "Automerge aged stable Go patch releases")
	requireExactSet(t, "Go automerge managers", goPatches.MatchManagers, []string{"gomod"})
	requireExactSet(t, "Go automerge update types", goPatches.MatchUpdateTypes, []string{"patch"})
	if goPatches.MatchCurrentVersion != ">=1.0.0" || goPatches.MinimumReleaseAge != "14 days" {
		t.Fatalf("Go automerge boundary = %+v", goPatches)
	}

	types := ruleByDescription(t, automerge, "Automerge aged patches of non-shipping type declarations")
	requireExactSet(t, "type declaration allowlist", types.MatchPackageNames, []string{"@types/node", "@types/qrcode"})
	requireExactSet(t, "type declaration update types", types.MatchUpdateTypes, []string{"patch"})
	if types.MinimumReleaseAge != "14 days" {
		t.Fatalf("type declaration minimum age = %q", types.MinimumReleaseAge)
	}

	major := ruleByDescription(t, config.PackageRules, "Hold every major for explicit dashboard approval")
	if major.Automerge || !major.DependencyDashboardApproval {
		t.Fatalf("major update policy = %+v", major)
	}
	actions := ruleByDescription(t, config.PackageRules, "GitHub Actions execute privileged CI code and always require review")
	containers := ruleByDescription(t, config.PackageRules, "Container changes always require review and image gates")
	if actions.Automerge || containers.Automerge {
		t.Fatal("Actions and container updates must never auto-merge")
	}
}

func TestRenovatePreservesCompatibilityGroupsAndToolchainHolds(t *testing.T) {
	config := readRenovateConfig(t)
	native := ruleByDescription(t, config.PackageRules, "Keep Expo and native compatibility updates atomic")
	requireExactSet(t, "Expo/native compatibility set", native.MatchPackageNames, []string{
		"expo", "expo-*", "@expo/config-plugins", "expo-doctor", "react", "react-dom", "react-native",
		"react-native-reanimated", "react-native-worklets", "react-native-safe-area-context",
		"react-native-screens", "react-native-svg", "react-native-web",
	})
	if native.GroupName != "Expo SDK 58 native" {
		t.Fatalf("native group = %q", native.GroupName)
	}

	toolchains := ruleByDescription(t, config.PackageRules, "Curate coupled Dockerfile toolchain and snapshot upgrades manually")
	requireExactSet(t, "Dockerfile toolchain holds", toolchains.MatchPackageNames, []string{"node", "golang", "rust", "debian"})
	if toolchains.Enabled == nil || *toolchains.Enabled {
		t.Fatalf("Dockerfile toolchain rule must be disabled: %+v", toolchains)
	}
	compose := ruleByDescription(t, config.PackageRules, "Curate fixture-coupled and self Compose images manually")
	requireExactSet(t, "Compose holds", compose.MatchPackageNames, []string{"chrisbenincasa/tunarr", "ghcr.io/loomarr/loomarr"})
	if compose.Enabled == nil || *compose.Enabled {
		t.Fatalf("Compose hold rule must be disabled: %+v", compose)
	}
}

func readRenovateConfig(t *testing.T) renovateConfig {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config renovateConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func ruleByDescription(t *testing.T, rules []renovatePackageRule, description string) renovatePackageRule {
	t.Helper()
	for _, rule := range rules {
		if rule.Description == description {
			return rule
		}
	}
	t.Fatalf("missing Renovate rule %q", description)
	return renovatePackageRule{}
}

func customManagerByDescription(t *testing.T, managers []renovateCustom, description string) renovateCustom {
	t.Helper()
	for _, manager := range managers {
		if manager.Description == description {
			return manager
		}
	}
	t.Fatalf("missing Renovate custom manager %q", description)
	return renovateCustom{}
}

func requireExactSet(t *testing.T, name string, got, want []string) {
	t.Helper()
	got = slices.Clone(got)
	want = slices.Clone(want)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want exactly %v", name, got, want)
	}
}
