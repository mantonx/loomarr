package releaseverify

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

var androidReleaseRuns = []string{
	`./scripts/download-android-ci-artifact.sh "${{ inputs.ci_run_id }}" .artifacts/android-ci`,
	`set -euo pipefail
umask 077
test -n "$ANDROID_UPLOAD_KEYSTORE_BASE64"
keystore="$RUNNER_TEMP/loomarr-upload.p12"
printf '%s' "$ANDROID_UPLOAD_KEYSTORE_BASE64" | base64 --decode > "$keystore"
echo "LOOMARR_ANDROID_KEYSTORE_PATH=$keystore" >> "$GITHUB_ENV"`,
	`set -euo pipefail
unsigned=$(find .artifacts/android-ci -maxdepth 1 -type f -name '*-unsigned.aab' -print -quit)
manifest=${unsigned%.aab}.json
./scripts/sign-android-ci-artifact.sh "$unsigned" "$manifest" "$ANDROID_RELEASE_OUTPUT_DIR"`,
	`set -euo pipefail
umask 077
service_account="$RUNNER_TEMP/google-play-service-account.json"
test -n "$GOOGLE_PLAY_SERVICE_ACCOUNT_JSON_BASE64"
printf '%s' "$GOOGLE_PLAY_SERVICE_ACCOUNT_JSON_BASE64" | base64 --decode > "$service_account"
aab=$(find "$ANDROID_RELEASE_OUTPUT_DIR" -maxdepth 1 -type f -name '*.aab' -print -quit)
manifest=${aab%.aab}.json
GOOGLE_PLAY_SERVICE_ACCOUNT_JSON_PATH="$service_account" \
  ./scripts/publish-android-beta.sh "$aab" "$manifest"`,
	`rm -f -- "${LOOMARR_ANDROID_KEYSTORE_PATH:-}" "$RUNNER_TEMP/google-play-service-account.json"`,
}

// VerifyAndroidReleaseWorkflow keeps upload signing and Play publication behind one manual,
// serialized, main-only environment. The first release may stop after artifact retention; every
// later API publication still goes through the same audited job and cannot select Production.
func VerifyAndroidReleaseWorkflow(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	root, err := parseYAML(data)
	if err != nil {
		return err
	}
	if err := verifyUses(root); err != nil {
		return err
	}
	if err := verifyOnlyKeys(root, "Android release workflow", "name", "on", "concurrency", "permissions", "jobs"); err != nil {
		return err
	}
	if scalarValue(root, "name") != "Android TV beta" {
		return errors.New("android release workflow name must be Android TV beta")
	}
	if err := verifyAndroidTrigger(root); err != nil {
		return err
	}
	if err := verifyAndroidPermissions(root); err != nil {
		return err
	}
	if err := verifyAndroidConcurrency(root); err != nil {
		return err
	}

	jobs, err := requiredMap(root, "jobs")
	if err != nil {
		return err
	}
	if len(jobs.Content) != 2 || jobs.Content[0].Value != "release" {
		return errors.New("android release workflow must contain only the audited release job")
	}
	job, err := requiredMap(jobs, "release")
	if err != nil {
		return err
	}
	if err := verifyOnlyKeys(job, "Android release job", "name", "if", "runs-on", "environment", "env", "steps"); err != nil {
		return err
	}
	if scalarValue(job, "if") != "github.ref == 'refs/heads/main'" {
		return errors.New("android release job must run only from refs/heads/main")
	}
	if scalarValue(job, "runs-on") != "ubuntu-latest" || scalarValue(job, "environment") != "android-beta" {
		return errors.New("android release job must run on ubuntu-latest in the android-beta environment")
	}
	if err := verifyAndroidEnvironment(job); err != nil {
		return err
	}
	return verifyAndroidSteps(job)
}

func verifyAndroidTrigger(root *yaml.Node) error {
	on, err := requiredMap(root, "on")
	if err != nil {
		return err
	}
	if err := verifyOnlyKeys(on, "Android release trigger", "workflow_dispatch"); err != nil {
		return err
	}
	dispatch, err := requiredMap(on, "workflow_dispatch")
	if err != nil {
		return err
	}
	if err := verifyOnlyKeys(dispatch, "Android workflow dispatch", "inputs"); err != nil {
		return err
	}
	inputs, err := requiredMap(dispatch, "inputs")
	if err != nil {
		return err
	}
	if len(inputs.Content) != 4 {
		return errors.New("android release workflow must expose exactly ci_run_id and publish_to_play")
	}
	for _, name := range []string{"ci_run_id", "publish_to_play"} {
		if _, err := requiredMap(inputs, name); err != nil {
			return err
		}
	}
	publish, _ := requiredMap(inputs, "publish_to_play")
	if scalarValue(publish, "type") != "boolean" || scalarValue(publish, "default") != "false" || scalarValue(publish, "required") != "true" {
		return errors.New("play publication must be an explicit, default-false boolean input")
	}
	return nil
}

func verifyAndroidPermissions(root *yaml.Node) error {
	permissions, err := requiredMap(root, "permissions")
	if err != nil {
		return err
	}
	if err := verifyOnlyKeys(permissions, "Android release permissions", "contents", "actions"); err != nil {
		return err
	}
	if scalarValue(permissions, "contents") != "read" || scalarValue(permissions, "actions") != "read" {
		return errors.New("android release workflow may read only contents and CI evidence")
	}
	return nil
}

func verifyAndroidConcurrency(root *yaml.Node) error {
	concurrency, err := requiredMap(root, "concurrency")
	if err != nil {
		return err
	}
	if err := verifyOnlyKeys(concurrency, "Android release concurrency", "group", "cancel-in-progress"); err != nil {
		return err
	}
	if scalarValue(concurrency, "group") != "android-beta-release" || scalarValue(concurrency, "cancel-in-progress") != "false" {
		return errors.New("android release publication must serialize globally without cancellation")
	}
	return nil
}

func verifyAndroidEnvironment(job *yaml.Node) error {
	env, err := requiredMap(job, "env")
	if err != nil {
		return err
	}
	required := map[string]string{
		"LOOMARR_ANDROID_KEYSTORE_PASSWORD":  "${{ secrets.ANDROID_UPLOAD_KEYSTORE_PASSWORD }}",
		"LOOMARR_ANDROID_KEY_ALIAS":          "${{ secrets.ANDROID_UPLOAD_KEY_ALIAS }}",
		"LOOMARR_ANDROID_KEY_PASSWORD":       "${{ secrets.ANDROID_UPLOAD_KEY_PASSWORD }}",
		"LOOMARR_ANDROID_UPLOAD_CERT_SHA256": "${{ vars.ANDROID_UPLOAD_CERT_SHA256 }}",
		"ANDROID_RELEASE_OUTPUT_DIR":         "${{ github.workspace }}/.artifacts/android-release",
	}
	if len(env.Content) != len(required)*2 {
		return errors.New("android release environment contains unaudited keys")
	}
	for name, want := range required {
		if got := scalarValue(env, name); got != want {
			return fmt.Errorf("android release environment %s = %q, want %q", name, got, want)
		}
	}
	return nil
}

func verifyAndroidSteps(job *yaml.Node) error {
	steps, err := requiredSequence(job, "steps")
	if err != nil {
		return err
	}
	if len(steps.Content) != 9 {
		return fmt.Errorf("android release job must contain exactly 9 audited steps, found %d", len(steps.Content))
	}

	actions := map[int]string{
		0: "actions/checkout",
		1: "actions/setup-java",
		2: "android-actions/setup-android",
		6: "actions/upload-artifact",
	}
	runs := map[int]string{
		3: androidReleaseRuns[0],
		4: androidReleaseRuns[1],
		5: androidReleaseRuns[2],
		7: androidReleaseRuns[3],
		8: androidReleaseRuns[4],
	}
	for index, step := range steps.Content {
		if step.Kind != yaml.MappingNode {
			return fmt.Errorf("android release step %d must be a mapping", index+1)
		}
		uses := scalarValue(step, "uses")
		run := strings.TrimSpace(scalarValue(step, "run"))
		if action, ok := actions[index]; ok {
			if strings.SplitN(uses, "@", 2)[0] != action || run != "" {
				return fmt.Errorf("android release step %d must use %s", index+1, action)
			}
		} else if want, ok := runs[index]; ok {
			if uses != "" || run != want {
				return fmt.Errorf("android release step %d is not the audited command", index+1)
			}
		}
	}
	if scalarValue(steps.Content[7], "if") != "inputs.publish_to_play" {
		return errors.New("play publication step must require the explicit publish_to_play input")
	}
	if scalarValue(steps.Content[8], "if") != "always()" {
		return errors.New("android credential cleanup must run always")
	}
	return verifyAndroidStepDetails(steps)
}

func verifyAndroidStepDetails(steps *yaml.Node) error {
	validationEnv, err := requiredMap(steps.Content[3], "env")
	if err != nil || scalarValue(validationEnv, "GH_TOKEN") != "${{ github.token }}" {
		return errors.New("android source validation must receive only the workflow token")
	}
	publishEnv, err := requiredMap(steps.Content[7], "env")
	if err != nil {
		return err
	}
	if len(publishEnv.Content) != 4 || scalarValue(publishEnv, "ANDROID_RELEASE_TRACK") != "internal" ||
		scalarValue(publishEnv, "GOOGLE_PLAY_SERVICE_ACCOUNT_JSON_BASE64") != "${{ secrets.GOOGLE_PLAY_SERVICE_ACCOUNT_JSON_BASE64 }}" {
		return errors.New("play publisher must receive only the selected track and protected service account")
	}
	uploadWith, err := requiredMap(steps.Content[6], "with")
	if err != nil {
		return err
	}
	if scalarValue(uploadWith, "path") != ".artifacts/android-release/*" || scalarValue(uploadWith, "if-no-files-found") != "error" ||
		scalarValue(uploadWith, "retention-days") != "30" {
		return errors.New("signed Android artifacts must be retained for 30 days and fail when missing")
	}
	return nil
}
