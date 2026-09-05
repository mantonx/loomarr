package releaseverify

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type workflowActionKey struct {
	workflow string
	job      string
	step     int
}

// workflowActionAuthorityEntries returns the reviewed action-step authority.
// Each JSON object is also YAML and binds the complete mapping at its absolute
// steps[] index: name/id, exact pinned uses, inputs, condition, and environment.
func workflowActionAuthorityEntries() map[workflowActionKey]string {
	return map[workflowActionKey]string{
		{workflow: "android-beta.yml", job: "release", step: 0}:             `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "android-beta.yml", job: "release", step: 1}:             `{"uses":"actions/setup-java@dd06d9cba3e5552c54d9f8ea23572deb30010f7c","with":{"distribution":"temurin","java-version":"21"}}`,
		{workflow: "android-beta.yml", job: "release", step: 2}:             `{"uses":"android-actions/setup-android@40fd30fb8d7440372e1316f5d1809ec01dcd3699"}`,
		{workflow: "android-beta.yml", job: "release", step: 6}:             `{"name":"Retain the signed AAB and release evidence","uses":"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a","with":{"name":"loomarr-android-tv-${{ github.sha }}","path":".artifacts/android-release/*","if-no-files-found":"error","retention-days":30}}`,
		{workflow: "apple-compilation-cache.yml", job: "publish", step: 0}:  `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "apple-compilation-cache.yml", job: "publish", step: 2}:  `{"uses":"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020","with":{"node-version":"${{ env.NODE_VERSION }}"}}`,
		{workflow: "apple-compilation-cache.yml", job: "publish", step: 4}:  `{"name":"Restore pnpm and CocoaPods work","uses":"actions/cache/restore@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/.local/share/pnpm/store\n~/Library/Caches/CocoaPods\n","key":"apple-cache-publisher-${{ runner.os }}-${{ hashFiles('web/pnpm-lock.yaml') }}-${{ github.run_id }}","restore-keys":"apple-clients-mobile-${{ runner.os }}-${{ hashFiles('web/pnpm-lock.yaml') }}-"}}`,
		{workflow: "apple-compilation-cache.yml", job: "publish", step: 7}:  `{"name":"Restore the latest validated generation","uses":"actions/cache/restore@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"${{ runner.temp }}/apple-compilation-cache.tar.zst","key":"${{ steps.apple-toolchain.outputs.fingerprint }}-${{ github.run_id }}","restore-keys":"${{ steps.apple-toolchain.outputs.fingerprint }}-"}}`,
		{workflow: "apple-compilation-cache.yml", job: "publish", step: 11}: `{"name":"Publish the validated generation","uses":"actions/cache/save@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"${{ runner.temp }}/apple-compilation-cache.tar.zst","key":"${{ steps.apple-toolchain.outputs.fingerprint }}-${{ github.run_id }}"}}`,
		{workflow: "ci-agent.yml", job: "run", step: 0}:                     `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-agent.yml", job: "run", step: 1}:                     `{"uses":"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e","with":{"go-version":"${{ env.GO_VERSION }}","cache":false}}`,
		{workflow: "ci-android.yml", job: "run", step: 0}:                   `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-android.yml", job: "run", step: 1}:                   `{"uses":"actions/setup-java@dd06d9cba3e5552c54d9f8ea23572deb30010f7c","with":{"distribution":"temurin","java-version":"21"}}`,
		{workflow: "ci-android.yml", job: "run", step: 2}:                   `{"uses":"android-actions/setup-android@40fd30fb8d7440372e1316f5d1809ec01dcd3699"}`,
		{workflow: "ci-android.yml", job: "run", step: 3}:                   `{"uses":"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020","with":{"node-version":"22"}}`,
		{workflow: "ci-android.yml", job: "run", step: 7}:                   `{"name":"Cache Gradle","id":"gradle-cache","uses":"actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/.gradle/caches\n~/.gradle/wrapper\n","key":"android-tv-react-native-v1-${{ runner.os }}-temurin-21-node-${{ env.NODE_VERSION }}-${{ hashFiles('web/apps/tv/**', 'web/packages/**', 'web/pnpm-lock.yaml', 'web/scripts/**') }}-${{ github.sha }}-${{ github.run_id }}","restore-keys":"android-tv-react-native-v1-${{ runner.os }}-temurin-21-node-${{ env.NODE_VERSION }}-${{ hashFiles('web/apps/tv/**', 'web/packages/**', 'web/pnpm-lock.yaml', 'web/scripts/**') }}-${{ github.sha }}-"}}`,
		{workflow: "ci-android.yml", job: "run", step: 10}:                  `{"name":"Retain the exact unsigned merge-result bundle","uses":"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a","with":{"name":"loomarr-android-unsigned-${{ github.sha }}","path":".artifacts/android-ci/*","if-no-files-found":"error","retention-days":30}}`,
		{workflow: "ci-apple-mobile.yml", job: "run", step: 0}:              `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-apple-mobile.yml", job: "run", step: 1}:              `{"uses":"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020","with":{"node-version":"${{ env.NODE_VERSION }}"}}`,
		{workflow: "ci-apple-mobile.yml", job: "run", step: 5}:              `{"name":"Restore the validated Apple compilation cache","uses":"actions/cache/restore@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"${{ runner.temp }}/apple-compilation-cache.tar.zst","key":"${{ steps.apple-toolchain.outputs.fingerprint }}-${{ github.run_id }}","restore-keys":"${{ steps.apple-toolchain.outputs.fingerprint }}-"}}`,
		{workflow: "ci-apple-mobile.yml", job: "run", step: 8}:              `{"name":"Keep simulator screenshot","if":"always()","uses":"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a","with":{"name":"apple-client-mobile","path":"${{ runner.temp }}/apple-client-mobile/","if-no-files-found":"warn","retention-days":7}}`,
		{workflow: "ci-apple-tv.yml", job: "run", step: 0}:                  `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-apple-tv.yml", job: "run", step: 1}:                  `{"uses":"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020","with":{"node-version":"${{ env.NODE_VERSION }}"}}`,
		{workflow: "ci-apple-tv.yml", job: "run", step: 5}:                  `{"name":"Restore the validated Apple compilation cache","uses":"actions/cache/restore@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"${{ runner.temp }}/apple-compilation-cache.tar.zst","key":"${{ steps.apple-toolchain.outputs.fingerprint }}-${{ github.run_id }}","restore-keys":"${{ steps.apple-toolchain.outputs.fingerprint }}-"}}`,
		{workflow: "ci-apple-tv.yml", job: "run", step: 8}:                  `{"name":"Keep simulator screenshot","if":"always()","uses":"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a","with":{"name":"apple-client-tv","path":"${{ runner.temp }}/apple-client-tv/","if-no-files-found":"warn","retention-days":7}}`,

		{workflow: "ci-apple-cache-validation.yml", job: "producer", step: 0}: `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-apple-cache-validation.yml", job: "producer", step: 1}: `{"uses":"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020","with":{"node-version":"${{ env.NODE_VERSION }}"}}`,
		{workflow: "ci-apple-cache-validation.yml", job: "producer", step: 3}: `{"name":"Restore pnpm and CocoaPods work","uses":"actions/cache/restore@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/.local/share/pnpm/store\n~/Library/Caches/CocoaPods\n","key":"apple-cache-validation-${{ runner.os }}-${{ hashFiles('web/pnpm-lock.yaml') }}-${{ github.run_id }}","restore-keys":"apple-cache-validation-${{ runner.os }}-${{ hashFiles('web/pnpm-lock.yaml') }}-"}}`,
		{workflow: "ci-apple-cache-validation.yml", job: "producer", step: 6}: `{"name":"Transfer the validated cache to a distinct runner","uses":"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a","with":{"name":"apple-compilation-cache-portability","path":"${{ runner.temp }}/apple-compilation-cache.tar.zst","if-no-files-found":"error","retention-days":1}}`,
		{workflow: "ci-apple-cache-validation.yml", job: "consumer", step: 0}: `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-apple-cache-validation.yml", job: "consumer", step: 1}: `{"uses":"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020","with":{"node-version":"${{ env.NODE_VERSION }}"}}`,
		{workflow: "ci-apple-cache-validation.yml", job: "consumer", step: 3}: `{"name":"Restore pnpm and CocoaPods work","uses":"actions/cache/restore@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/.local/share/pnpm/store\n~/Library/Caches/CocoaPods\n","key":"apple-cache-validation-${{ runner.os }}-${{ hashFiles('web/pnpm-lock.yaml') }}-${{ github.run_id }}","restore-keys":"apple-cache-validation-${{ runner.os }}-${{ hashFiles('web/pnpm-lock.yaml') }}-"}}`,
		{workflow: "ci-apple-cache-validation.yml", job: "consumer", step: 5}: `{"name":"Receive the producer cache","uses":"actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c","with":{"name":"apple-compilation-cache-portability","path":"${{ runner.temp }}"}}`,

		{workflow: "ci-clients.yml", job: "run", step: 0}:                `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-clients.yml", job: "run", step: 1}:                `{"uses":"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020","with":{"node-version":"${{ env.NODE_VERSION }}"}}`,
		{workflow: "ci-clients.yml", job: "run", step: 3}:                `{"name":"Cache pnpm, Metro, and Turbo work","uses":"actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/.local/share/pnpm/store\n${{ runner.temp }}/metro-cache\nweb/.turbo\n","key":"clients-${{ runner.os }}-${{ hashFiles('web/pnpm-lock.yaml') }}-${{ github.sha }}","restore-keys":"clients-${{ runner.os }}-${{ hashFiles('web/pnpm-lock.yaml') }}-\nclients-${{ runner.os }}-\n"}}`,
		{workflow: "ci-docs.yml", job: "run", step: 0}:                   `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-docs.yml", job: "run", step: 1}:                   `{"uses":"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020","with":{"node-version":"${{ env.NODE_VERSION }}"}}`,
		{workflow: "ci-docs.yml", job: "run", step: 3}:                   `{"name":"Links (offline)","uses":"lycheeverse/lychee-action@e7477775783ea5526144ba13e8db5eec57747ce8","with":{"args":"--offline --no-progress README.md CONTRIBUTING.md CLAUDE.md AGENTS.md CONTEXT.md PROGRESS.md docs 'design/*.md' .agents .claude","fail":true}}`,
		{workflow: "ci-docs.yml", job: "run", step: 4}:                   `{"name":"Prose","uses":"vale-cli/vale-action@518a9136acc6e6668ce7c00d367051e0941e87ff","with":{"files":"[\"README.md\", \"CONTRIBUTING.md\", \"docs/help\", \"docs/install\", \"docs/dev\"]","fail_on_error":true,"version":"3.17.1"}}`,
		{workflow: "ci-frontend.yml", job: "run", step: 0}:               `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-frontend.yml", job: "run", step: 1}:               `{"uses":"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020","with":{"node-version":"${{ env.NODE_VERSION }}"}}`,
		{workflow: "ci-frontend.yml", job: "run", step: 3}:               `{"name":"Cache pnpm store","uses":"actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/.local/share/pnpm/store","key":"pnpm-${{ runner.os }}-${{ hashFiles('web/pnpm-lock.yaml') }}","restore-keys":"pnpm-${{ runner.os }}-"}}`,
		{workflow: "ci-go-contracts.yml", job: "run", step: 0}:           `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-go-contracts.yml", job: "run", step: 1}:           `{"uses":"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e","with":{"go-version":"${{ env.GO_VERSION }}","cache":false}}`,
		{workflow: "ci-go-contracts.yml", job: "run", step: 3}:           `{"name":"Restore contract caches","id":"cache","uses":"actions/cache/restore@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/go/pkg/mod\n~/.cache/go-build\n~/.cache/golangci-lint\n","key":"go-contracts-${{ runner.os }}-${{ steps.epoch.outputs.week }}-v2.13.2-${{ hashFiles('go.sum', 'Cargo.lock') }}-${{ github.run_id }}","restore-keys":"go-contracts-${{ runner.os }}-${{ steps.epoch.outputs.week }}-v2.13.2-${{ hashFiles('go.sum', 'Cargo.lock') }}-\ngo-contracts-${{ runner.os }}-${{ steps.epoch.outputs.week }}-v2.13.2-\n"}}`,
		{workflow: "ci-go-contracts.yml", job: "run", step: 13}:          `{"name":"Save contract caches (main only)","if":"always() && github.ref == 'refs/heads/main'","uses":"actions/cache/save@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/go/pkg/mod\n~/.cache/go-build\n~/.cache/golangci-lint\n","key":"${{ steps.cache.outputs.cache-primary-key }}"}}`,
		{workflow: "ci-go.yml", job: "run", step: 0}:                     `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-go.yml", job: "run", step: 1}:                     `{"uses":"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e","with":{"go-version":"${{ env.GO_VERSION }}","cache":false}}`,
		{workflow: "ci-go.yml", job: "run", step: 4}:                     `{"name":"Restore Go test caches (modules + build)","id":"gocache","uses":"actions/cache/restore@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/go/pkg/mod\n~/.cache/go-build\n","key":"go-test-${{ runner.os }}-${{ steps.epoch.outputs.week }}-s${{ matrix.shard }}-${{ hashFiles('go.sum') }}-${{ github.run_id }}","restore-keys":"go-test-${{ runner.os }}-${{ steps.epoch.outputs.week }}-s${{ matrix.shard }}-${{ hashFiles('go.sum') }}-\ngo-test-${{ runner.os }}-${{ steps.epoch.outputs.week }}-s${{ matrix.shard }}-\n"}}`,
		{workflow: "ci-go.yml", job: "run", step: 6}:                     `{"name":"Cache ffmpeg packages","id":"ffmpeg-cache","uses":"actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/ffmpeg-debs","key":"ffmpeg-debs-${{ runner.os }}-${{ steps.os.outputs.release }}-v1"}}`,
		{workflow: "ci-go.yml", job: "run", step: 11}:                    `{"name":"Save Go caches (main only)","if":"always() && github.ref == 'refs/heads/main'","uses":"actions/cache/save@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/go/pkg/mod\n~/.cache/go-build\n","key":"${{ steps.gocache.outputs.cache-primary-key }}"}}`,
		{workflow: "ci-image-certification.yml", job: "run", step: 0}:    `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-image-certification.yml", job: "run", step: 1}:    `{"uses":"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e","with":{"go-version":"${{ env.GO_VERSION }}","cache":false}}`,
		{workflow: "ci-image-certification.yml", job: "run", step: 4}:    `{"name":"Keep the Rust image certification report","if":"always()","uses":"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a","with":{"name":"rust-image-certification","path":"${{ runner.temp }}/image-certification.json","if-no-files-found":"ignore","retention-days":14}}`,
		{workflow: "ci-image.yml", job: "run", step: 0}:                  `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-image.yml", job: "run", step: 1}:                  `{"uses":"docker/setup-buildx-action@37fe631027851001ddb9b187196cc803df7f5f0e"}`,
		{workflow: "ci-image.yml", job: "run", step: 2}:                  `{"name":"Build (${{ matrix.platform }})","uses":"docker/build-push-action@53b7df96c91f9c12dcc8a07bcb9ccacbed38856a","with":{"context":".","platforms":"${{ matrix.platform }}","push":false,"load":true,"tags":"loomarr-ci:verify","cache-from":"type=gha,scope=release"}}`,
		{workflow: "ci-playwright.yml", job: "run", step: 0}:             `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-playwright.yml", job: "run", step: 1}:             `{"uses":"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020","with":{"node-version":"${{ env.NODE_VERSION }}"}}`,
		{workflow: "ci-playwright.yml", job: "run", step: 3}:             `{"name":"Cache pnpm store","uses":"actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/.local/share/pnpm/store","key":"pnpm-${{ runner.os }}-${{ hashFiles('web/pnpm-lock.yaml') }}","restore-keys":"pnpm-${{ runner.os }}-"}}`,
		{workflow: "ci-playwright.yml", job: "run", step: 8}:             `{"name":"Keep the failure images (visual + e2e)","if":"failure()","uses":"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a","with":{"name":"playwright-failures-shard-${{ matrix.shard }}","path":"web/apps/web/test-results/","if-no-files-found":"ignore","retention-days":7}}`,
		{workflow: "ci-postgres.yml", job: "run", step: 0}:               `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-postgres.yml", job: "run", step: 1}:               `{"uses":"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e","with":{"cache":false,"go-version":"${{ env.GO_VERSION }}"}}`,
		{workflow: "ci-postgres.yml", job: "run", step: 3}:               `{"name":"Restore Go caches (store conformance)","id":"gocache","uses":"actions/cache/restore@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/go/pkg/mod\n~/.cache/go-build\n","key":"gobuild-pg-${{ runner.os }}-${{ steps.epoch.outputs.week }}-${{ hashFiles('go.sum') }}-${{ github.run_id }}","restore-keys":"gobuild-pg-${{ runner.os }}-${{ steps.epoch.outputs.week }}-${{ hashFiles('go.sum') }}-\ngobuild-pg-${{ runner.os }}-${{ steps.epoch.outputs.week }}-\n"}}`,
		{workflow: "ci-postgres.yml", job: "run", step: 5}:               `{"name":"Save Go caches (main only)","if":"always() && github.ref == 'refs/heads/main'","uses":"actions/cache/save@55cc8345863c7cc4c66a329aec7e433d2d1c52a9","with":{"path":"~/go/pkg/mod\n~/.cache/go-build\n","key":"${{ steps.gocache.outputs.cache-primary-key }}"}}`,
		{workflow: "ci-rust-contracts.yml", job: "run", step: 0}:         `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-tuner.yml", job: "run", step: 0}:                  `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci-tuner.yml", job: "run", step: 1}:                  `{"uses":"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020","with":{"node-version":"${{ env.NODE_VERSION }}"}}`,
		{workflow: "ci-tuner.yml", job: "run", step: 6}:                  `{"name":"Keep tuner failure evidence","if":"failure()","uses":"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a","with":{"name":"tuner-failures","path":"web/apps/web/test-results/","if-no-files-found":"ignore","retention-days":7}}`,
		{workflow: "ci.yml", job: "ci-policy", step: 0}:                  `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "ci.yml", job: "ci-policy", step: 1}:                  `{"uses":"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e","with":{"go-version":"${{ env.GO_VERSION }}","cache":false}}`,
		{workflow: "ci.yml", job: "changes", step: 0}:                    `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1","with":{"fetch-depth":0}}`,
		{workflow: "ci.yml", job: "ci-ok", step: 1}:                      `{"name":"Check out the timing reporter","if":"always()","continue-on-error":true,"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "image-benchmark.yml", job: "benchmark", step: 0}:     `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "image-benchmark.yml", job: "benchmark", step: 1}:     `{"uses":"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e","with":{"go-version":"1.27","cache":true}}`,
		{workflow: "image-benchmark.yml", job: "benchmark", step: 5}:     `{"name":"Keep the machine-readable report","if":"always()","uses":"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a","with":{"name":"rust-image-benchmark-${{ matrix.platform }}","path":"${{ runner.temp }}/image-benchmark-${{ matrix.platform }}.json\n${{ runner.temp }}/image-parallelism-${{ matrix.platform }}/*.json\n","if-no-files-found":"ignore","retention-days":30}}`,
		{workflow: "pages.yml", job: "build", step: 0}:                   `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "pages.yml", job: "build", step: 1}:                   `{"uses":"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020","with":{"node-version":"22"}}`,
		{workflow: "pages.yml", job: "build", step: 3}:                   `{"uses":"actions/configure-pages@45bfe0192ca1faeb007ade9deae92b16b8254a0d"}`,
		{workflow: "pages.yml", job: "build", step: 4}:                   `{"uses":"actions/upload-pages-artifact@fc324d3547104276b827a68afc52ff2a11cc49c9","with":{"path":"docs-site/dist"}}`,
		{workflow: "pages.yml", job: "deploy", step: 0}:                  `{"id":"deployment","uses":"actions/deploy-pages@cd2ce8fcbc39b97be8ca5fce6e763baed58fa128"}`,
		{workflow: "release-notes.yml", job: "publish-notes", step: 0}:   `{"name":"Check out the tagged commit","uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "release-notes.yml", job: "publish-notes", step: 1}:   `{"name":"Set up Go","uses":"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e","with":{"go-version-file":"go.mod","cache":false}}`,
		{workflow: "release.yml", job: "build", step: 0}:                 `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "release.yml", job: "build", step: 2}:                 `{"uses":"docker/setup-buildx-action@37fe631027851001ddb9b187196cc803df7f5f0e"}`,
		{workflow: "release.yml", job: "build", step: 3}:                 `{"name":"Log in to GHCR","uses":"docker/login-action@dbcb813823bdd20940b903addbd779551569679f","with":{"registry":"${{ env.REGISTRY }}","username":"${{ github.actor }}","password":"${{ secrets.GITHUB_TOKEN }}"}}`,
		{workflow: "release.yml", job: "build", step: 4}:                 `{"name":"Build and push digest only","id":"build","uses":"docker/build-push-action@53b7df96c91f9c12dcc8a07bcb9ccacbed38856a","with":{"context":".","target":"runtime","platforms":"${{ matrix.platform }}","push":true,"outputs":"type=image,name=${{ env.IMAGE }},push-by-digest=true,name-canonical=true,push=true","build-args":"VERSION=${{ github.ref_name }}\nCOMMIT=${{ github.sha }}\n","provenance":"mode=max","sbom":true,"cache-from":"type=gha,scope=release-${{ matrix.arch }}","cache-to":"type=gha,scope=release-${{ matrix.arch }},mode=max"}}`,
		{workflow: "release.yml", job: "build", step: 6}:                 `{"name":"Upload the digest","uses":"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a","with":{"name":"release-digest-${{ matrix.arch }}","path":"/tmp/digests/${{ matrix.arch }}","if-no-files-found":"error","retention-days":1}}`,
		{workflow: "release.yml", job: "publish", step: 0}:               `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "release.yml", job: "publish", step: 2}:               `{"uses":"docker/setup-buildx-action@37fe631027851001ddb9b187196cc803df7f5f0e"}`,
		{workflow: "release.yml", job: "publish", step: 3}:               `{"name":"Log in to GHCR","uses":"docker/login-action@dbcb813823bdd20940b903addbd779551569679f","with":{"registry":"${{ env.REGISTRY }}","username":"${{ github.actor }}","password":"${{ secrets.GITHUB_TOKEN }}"}}`,
		{workflow: "release.yml", job: "publish", step: 4}:               `{"name":"Download the per-arch digests","uses":"actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c","with":{"path":"/tmp/digests","pattern":"release-digest-*","merge-multiple":true}}`,
		{workflow: "release.yml", job: "publish", step: 7}:               `{"name":"Install cosign","uses":"sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6","with":{"cosign-release":"v2.6.5"}}`,
		{workflow: "rust-maintenance.yml", job: "supply-chain", step: 0}: `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "rust-maintenance.yml", job: "fuzz", step: 0}:         `{"uses":"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"}`,
		{workflow: "rust-maintenance.yml", job: "fuzz", step: 4}:         `{"name":"Keep crash and timeout artifacts","if":"failure()","uses":"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a","with":{"name":"rust-image-fuzz-artifacts","path":"rust/loomarr-image/fuzz/artifacts/protocol_decoder","if-no-files-found":"ignore","retention-days":30}}`,
	}
}

func verifyRegisteredWorkflowActionCardinality(workflowName string, jobs *yaml.Node) error {
	for key := range workflowAuthorityCatalog().actions {
		if key.workflow != workflowName {
			continue
		}
		job, ok := mappingValue(jobs, key.job)
		if !ok || job.Kind != yaml.MappingNode {
			return fmt.Errorf("workflow %s is missing job %s required by pinned action authority", workflowName, key.job)
		}
		steps, ok := mappingValue(job, "steps")
		if !ok || steps.Kind != yaml.SequenceNode {
			return fmt.Errorf("workflow %s job %s is missing steps required by pinned action authority", workflowName, key.job)
		}
		if key.step >= len(steps.Content) {
			return fmt.Errorf("workflow %s job %s is missing pinned action at absolute step %d", workflowName, key.job, key.step+1)
		}
	}
	return nil
}

func verifySourceBoundWorkflowActions(workflowName, jobName string, steps *yaml.Node) error {
	actions := workflowAuthorityCatalog().actions
	for key, source := range actions {
		if key.workflow != workflowName || key.job != jobName {
			continue
		}
		if key.step >= len(steps.Content) {
			return fmt.Errorf("workflow %s job %s is missing pinned action at absolute step %d", workflowName, jobName, key.step+1)
		}
		want, err := parseActionAuthority(source)
		if err != nil {
			return fmt.Errorf("parse pinned action authority for %s job %s step %d: %w", workflowName, jobName, key.step+1, err)
		}
		if !equalYAMLAuthority(steps.Content[key.step], want) {
			return fmt.Errorf("workflow %s job %s pinned action at absolute step %d differs from its exact authority", workflowName, jobName, key.step+1)
		}
	}
	for stepIndex, step := range steps.Content {
		if _, hasAction := mappingValue(step, "uses"); !hasAction {
			continue
		}
		key := workflowActionKey{workflow: workflowName, job: jobName, step: stepIndex}
		if _, expected := actions[key]; !expected {
			return fmt.Errorf("workflow %s job %s has an unapproved action at absolute step %d", workflowName, jobName, stepIndex+1)
		}
	}
	return nil
}

func parseActionAuthority(source string) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.NewDecoder(strings.NewReader(source)).Decode(&document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("action authority must be one mapping")
	}
	if err := validateNode(document.Content[0]); err != nil {
		return nil, err
	}
	uses, ok := mappingValue(document.Content[0], "uses")
	if !ok || uses.Kind != yaml.ScalarNode || !actionPin.MatchString(strings.TrimSpace(uses.Value)) {
		return nil, fmt.Errorf("action authority must contain one exact full-SHA uses scalar")
	}
	return document.Content[0], nil
}

func equalYAMLAuthority(left, right *yaml.Node) bool {
	if left == nil || right == nil || left.Kind != right.Kind || left.Tag != right.Tag || left.Value != right.Value {
		return false
	}
	if left.Kind != yaml.MappingNode {
		if len(left.Content) != len(right.Content) {
			return false
		}
		for index := range left.Content {
			if !equalYAMLAuthority(left.Content[index], right.Content[index]) {
				return false
			}
		}
		return true
	}
	if len(left.Content) != len(right.Content) {
		return false
	}
	for index := 0; index < len(left.Content); index += 2 {
		value, ok := mappingValue(right, left.Content[index].Value)
		if !ok || !equalYAMLAuthority(left.Content[index+1], value) {
			return false
		}
	}
	return true
}
