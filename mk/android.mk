## ---- Android TV React Native release ------------------------------------

.PHONY: android
android: android-release-test ## React Native Android TV — verified unsigned four-ABI Play artifact

.PHONY: android-release-test
android-release-test: ## compile once, strip the ephemeral CI signature, and retain promotion evidence
	@./scripts/test-android-release.sh
