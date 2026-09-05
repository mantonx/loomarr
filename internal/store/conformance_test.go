package store

import (
	"testing"
)

// The store conformance suite (AGENTS.md: ONE suite, TWO backends — never forked per dialect).
//
// ⚠ One SUITE, not one FILE. This was a single 2,650-line file whose function order was
// historical — clip tests scattered across four non-contiguous regions, `ScheduledJobPaused`
// ~900 lines from its two siblings, and a 300-line source-registry test wedged between them.
// The rule the directives protect is that both dialects run the SAME assertions; nothing about
// it requires one file, and splitting by domain costs the rule nothing:
//
//	conformance_titles.go    titles + provisioning
//	conformance_channels.go  channels + the scheduling caches (episodes, airing history)
//	conformance_jobs.go      suggester jobs, scheduled jobs, proposals
//	conformance_filler.go    clips, sources, pulls, splits
//	conformance_ops.go       settings, sessions, counts, activity, retention
//
// ⚠ These are NOT `_test.go` files, deliberately — the same reason they never were. Both
// backend test files (sqlite in-package, Postgres behind a build tag) call `RunConformance`,
// so the assertions have to be ordinary package code they can import.
//
// The groups below also make failure output navigable: a red run now reads
// `TestPostgresConformance/Filler/ClipCounts` rather than one flat list of 41 names.

// NewStoreFunc builds a fresh, migrated, empty Store for one test. The SQLite
// and Postgres test files each supply one; RunConformance runs the SAME
// assertions against both (AGENTS.md: one suite, two backends — never forked).
type NewStoreFunc func(t *testing.T) Store

// RunConformance is the whole contract. Every backend must pass all of it.
func RunConformance(t *testing.T, newStore NewStoreFunc) {
	t.Run("Inventory", func(t *testing.T) {
		t.Run("RoundTripAndIdempotency", func(t *testing.T) { testInventoryRoundTrip(t, newStore) })
		t.Run("GroundedIdentityOnly", func(t *testing.T) { testInventoryGroundedIdentity(t, newStore) })
		t.Run("MeasurementRevision", func(t *testing.T) { testInventoryMeasurementRevision(t, newStore) })
		t.Run("ExplicitMissing", func(t *testing.T) { testInventoryExplicitMissing(t, newStore) })
		t.Run("MalformedRejected", func(t *testing.T) { testInventoryMalformedRejected(t, newStore) })
		t.Run("BoundsRejected", func(t *testing.T) { testInventoryBoundsRejected(t, newStore) })
	})

	t.Run("Titles", func(t *testing.T) {
		t.Run("TitleRoundTrip", func(t *testing.T) { testTitleRoundTrip(t, newStore) })
		t.Run("UpdateTitleProgress", func(t *testing.T) { testUpdateTitleProgress(t, newStore) })
		t.Run("UpsertIsIdempotent", func(t *testing.T) { testUpsertIdempotent(t, newStore) })
		t.Run("GetMissingIsNotFound", func(t *testing.T) { testGetMissing(t, newStore) })
		t.Run("ListByState", func(t *testing.T) { testListByState(t, newStore) })
		t.Run("ClaimDueTitles", func(t *testing.T) { testClaimDue(t, newStore) })
		t.Run("ClaimDueConcurrent", func(t *testing.T) { testClaimConcurrent(t, newStore) })
	})

	t.Run("Channels", func(t *testing.T) {
		t.Run("ChannelRoundTrip", func(t *testing.T) { testChannelRoundTrip(t, newStore) })
		t.Run("ChannelRevisionCAS", func(t *testing.T) { testChannelRevisionCAS(t, newStore) })
		t.Run("ChannelTargetedRevisionWrites", func(t *testing.T) { testChannelTargetedRevisionWrites(t, newStore) })
		t.Run("ChannelListAndDelete", func(t *testing.T) { testChannelListDelete(t, newStore) })
		t.Run("ChannelDeleteDropsImageRefs", func(t *testing.T) { testChannelDeleteDropsImageRefs(t, newStore) })
		t.Run("ClaimDueChannels", func(t *testing.T) { testClaimDueChannels(t, newStore) })
		t.Run("ClaimDueChannelsConcurrent", func(t *testing.T) { testClaimChannelsConcurrent(t, newStore) })
		t.Run("SeriesEpisodesCache", func(t *testing.T) { testSeriesEpisodes(t, newStore) })
		t.Run("AiringHistory", func(t *testing.T) { testAiringHistory(t, newStore) })
	})

	t.Run("Jobs", func(t *testing.T) {
		t.Run("DiscoveryFeedback", func(t *testing.T) { testDiscoveryFeedback(t, newStore) })
		t.Run("DiscoveryFeedbackMissingChannel", func(t *testing.T) { testDiscoveryFeedbackMissingChannel(t, newStore) })
		t.Run("DiscoveryFeedbackHardDelete", func(t *testing.T) { testDiscoveryFeedbackHardDelete(t, newStore) })
		t.Run("DiscoveryFeedbackDeleteRace", func(t *testing.T) { testDiscoveryFeedbackDeleteRace(t, newStore) })
		t.Run("DiscoveryFeedbackDetachedChannel", func(t *testing.T) { testDiscoveryFeedbackDetachedChannel(t, newStore) })
		t.Run("JobRoundTrip", func(t *testing.T) { testJobRoundTrip(t, newStore) })
		t.Run("ProposalJobListScope", func(t *testing.T) { testProposalJobListScope(t, newStore) })
		t.Run("ClaimDueJobs", func(t *testing.T) { testClaimDueJobs(t, newStore) })
		t.Run("ClaimDueJobsConcurrent", func(t *testing.T) { testClaimJobsConcurrent(t, newStore) })
		t.Run("JobCacheByIntentHash", func(t *testing.T) { testJobCacheByHash(t, newStore) })
		t.Run("SuggestionSuccessAtomic", func(t *testing.T) { testSuggestionSuccessAtomic(t, newStore) })
		t.Run("SuggestionRequeueCAS", func(t *testing.T) { testSuggestionRequeueCAS(t, newStore) })
		t.Run("CloneSuggestionSuccess", func(t *testing.T) { testCloneSuggestionSuccess(t, newStore) })
		t.Run("ProposalJobSnapshot", func(t *testing.T) { testProposalJobSnapshot(t, newStore) })
		t.Run("ProposalJobFirstLiveMonotonic", func(t *testing.T) { testProposalJobFirstLiveMonotonic(t, newStore) })
		t.Run("ScheduledJobRoundTrip", func(t *testing.T) { testScheduledJobRoundTrip(t, newStore) })
		t.Run("ClaimDueScheduledJobs", func(t *testing.T) { testClaimDueScheduledJobs(t, newStore) })
		t.Run("ScheduledJobPaused", func(t *testing.T) { testScheduledJobPaused(t, newStore) })
		t.Run("ProposalRoundTripAndQueues", func(t *testing.T) { testProposalQueues(t, newStore) })
		t.Run("ProposalApprovalAtomic", func(t *testing.T) { testProposalApprovalAtomic(t, newStore) })
		t.Run("ProposalApprovalStaleChannel", func(t *testing.T) { testProposalApprovalStaleChannel(t, newStore) })
		t.Run("ProposalApprovalSuperseded", func(t *testing.T) { testProposalApprovalSuperseded(t, newStore) })
		t.Run("ProposalApprovalSameIntentConflict", func(t *testing.T) { testProposalApprovalSameIntentConflict(t, newStore) })
		t.Run("ProposalDecisionConcurrent", func(t *testing.T) { testProposalDecisionConcurrent(t, newStore) })
		t.Run("ProposalApprovalOverlappingTitles", func(t *testing.T) { testProposalApprovalOverlappingTitles(t, newStore) })
		t.Run("ProposalAutoApprovalQuotaConcurrent", func(t *testing.T) { testProposalAutoApprovalQuotaConcurrent(t, newStore) })
		t.Run("ProposalApprovalManualOrdering", func(t *testing.T) { testProposalApprovalManualParticipatesInRequesterOrdering(t, newStore) })
		t.Run("ProposalApprovalCanceledWaiter", func(t *testing.T) { testProposalApprovalCanceledWaiterReleasesOrdering(t, newStore) })
		t.Run("LookupByNonID", func(t *testing.T) { testLookupByNonID(t, newStore) })
	})

	t.Run("Notifications", func(t *testing.T) {
		t.Run("LifecycleAndIdempotency", func(t *testing.T) { testNotificationLifecycle(t, newStore) })
		t.Run("ProductVocabulary", func(t *testing.T) { testNotificationProductVocabulary(t, newStore) })
		t.Run("Destinations", func(t *testing.T) { testNotificationDestinations(t, newStore) })
		t.Run("ExpiredLeaseIsAmbiguous", func(t *testing.T) { testNotificationExpiredLease(t, newStore) })
		t.Run("ConcurrentClaim", func(t *testing.T) { testNotificationConcurrentClaim(t, newStore) })
		t.Run("Retention", func(t *testing.T) { testNotificationRetention(t, newStore) })
	})

	t.Run("SecretProtection", func(t *testing.T) {
		t.Run("InstallationKeyFingerprint", func(t *testing.T) {
			testInstallationKeyFingerprint(t, newStore)
		})
		t.Run("InitialDataKeyIsIdempotent", func(t *testing.T) {
			testInitialSecretDataKey(t, newStore)
		})
		t.Run("RotationKeepsPriorKeysReadable", func(t *testing.T) {
			testRotateSecretDataKey(t, newStore)
		})
		t.Run("SettingRewritePreservesAudit", func(t *testing.T) {
			testRewriteSettingValuesPreservesAudit(t, newStore)
		})
		t.Run("InstallationKeyReplacementIsAtomic", func(t *testing.T) {
			testReplaceWrappedDataKeysIsAtomic(t, newStore)
		})
	})

	t.Run("Invitations", func(t *testing.T) {
		t.Run("ReserveAndListIdentity", func(t *testing.T) { testInvitationReserveAndList(t, newStore) })
		t.Run("ReservationBlocksDirectCreation", func(t *testing.T) {
			testInvitationReservationBlocksDirectCreation(t, newStore)
		})
		t.Run("ContactIsAtomicAndGloballyUnique", func(t *testing.T) {
			testInvitationContactIsAtomicAndGloballyUnique(t, newStore)
		})
		t.Run("RegenerateAndRevokeGrants", func(t *testing.T) {
			testInvitationRegenerateAndRevoke(t, newStore)
		})
		t.Run("GrantPreviewDoesNotConsume", func(t *testing.T) {
			testInvitationGrantPreviewDoesNotConsume(t, newStore)
		})
		t.Run("GrantPreviewRejectsEveryUnusableState", func(t *testing.T) {
			testInvitationGrantPreviewRejectsEveryUnusableState(t, newStore)
		})
		t.Run("ConcurrentRedemption", func(t *testing.T) {
			testInvitationConcurrentRedemption(t, newStore)
		})
		t.Run("SiblingGrantLifecycle", func(t *testing.T) {
			testInvitationSiblingGrantLifecycle(t, newStore)
		})
		t.Run("Retention", func(t *testing.T) { testInvitationRetention(t, newStore) })
	})
	t.Run("PasswordRecovery", func(t *testing.T) {
		t.Run("PreviewDoesNotConsume", func(t *testing.T) {
			testPasswordRecoveryPreviewDoesNotConsume(t, newStore)
		})
		t.Run("RedemptionIsAtomic", func(t *testing.T) {
			testPasswordRecoveryRedemptionIsAtomic(t, newStore)
		})
		t.Run("NewRequestSupersedesOld", func(t *testing.T) {
			testPasswordRecoveryNewRequestSupersedesOld(t, newStore)
		})
		t.Run("Retention", func(t *testing.T) {
			testPasswordRecoveryRetention(t, newStore)
		})
		t.Run("DisabledAfterIssueIsUnusable", func(t *testing.T) {
			testPasswordRecoveryDisabledAfterIssueIsUnusable(t, newStore)
		})
		t.Run("ConcurrentRedemption", func(t *testing.T) {
			testPasswordRecoveryConcurrentRedemption(t, newStore)
		})
	})

	t.Run("Filler", func(t *testing.T) {
		t.Run("ClipRoundTripAndFilters", func(t *testing.T) { testClipFilters(t, newStore) })
		t.Run("ClipTagsAndPrune", func(t *testing.T) { testClipTagsAndPrune(t, newStore) })
		t.Run("ClipNameSearch", func(t *testing.T) { testClipNameSearch(t, newStore) })
		t.Run("ClipPlayCounters", func(t *testing.T) { testClipPlayCounters(t, newStore) })
		t.Run("ClipExposureRotation", func(t *testing.T) { testClipExposureRotation(t, newStore) })
		t.Run("ClipKeyIsHashNotPath", func(t *testing.T) { testClipKeyIsHashNotPath(t, newStore) })
		t.Run("ClipIdentityReplacement", func(t *testing.T) { testClipIdentityReplacement(t, newStore) })
		t.Run("ConditioningPublicationCommit", func(t *testing.T) { testConditioningPublicationCommit(t, newStore) })
		t.Run("ConditioningPublicationFailsClosed", func(t *testing.T) {
			testConditioningPublicationFailsClosed(t, newStore)
		})
		t.Run("ClipIdentityReplacementSameHash", func(t *testing.T) { testClipIdentityReplacementSameHash(t, newStore) })
		t.Run("ClipFingerprintCache", func(t *testing.T) { testClipFingerprintCache(t, newStore) })
		t.Run("ClipCounts", func(t *testing.T) { testClipCounts(t, newStore) })
		t.Run("ClipLicense", func(t *testing.T) { testClipLicense(t, newStore) })
		t.Run("ClipHeldLifecycle", func(t *testing.T) { testClipHeld(t, newStore) })
		t.Run("ClipPaging", func(t *testing.T) { testClipPaging(t, newStore) })
		t.Run("ClipSearchWidened", func(t *testing.T) { testClipSearchWidened(t, newStore) })
		t.Run("ClipTopLevelOnly", func(t *testing.T) { testClipTopLevelOnly(t, newStore) })
		t.Run("ClipCreatedAt", func(t *testing.T) { testClipCreatedAt(t, newStore) })
		t.Run("CompositeLineage", func(t *testing.T) { testCompositeLineage(t, newStore) })
		t.Run("SplitConfirmationAtomic", func(t *testing.T) { testSplitConfirmationAtomic(t, newStore) })
		t.Run("SplitConfirmationRequiresReviewParent", func(t *testing.T) {
			testSplitConfirmationRequiresReviewParent(t, newStore)
		})
		t.Run("SplitProposalClaimFencesConfirmers", func(t *testing.T) {
			testSplitProposalClaimFencesConfirmers(t, newStore)
		})
		t.Run("FillerSourceRegistry", func(t *testing.T) { testFillerSources(t, newStore) })
		t.Run("SeededDefaultSources", func(t *testing.T) { testSeededDefaultSources(t, newStore) })
		t.Run("FillerPulls", func(t *testing.T) { testFillerPulls(t, newStore) })
		t.Run("FillerAcquisitionRuns", func(t *testing.T) { testFillerAcquisitionRuns(t, newStore) })
		t.Run("InteractiveOperations", func(t *testing.T) { testInteractiveOperations(t, newStore) })
		t.Run("FillerInferenceAccountingAndBudgets", func(t *testing.T) { testFillerInferenceAccountingAndBudgets(t, newStore) })
		t.Run("FillerAdmissionDecisionAudit", func(t *testing.T) { testFillerAdmissionDecisionAudit(t, newStore) })
		t.Run("SplitProposals", func(t *testing.T) { testSplitProposals(t, newStore) })
		t.Run("ClipPipelineState", func(t *testing.T) { testClipPipeline(t, newStore) })
		t.Run("ClipPipelineOverview", func(t *testing.T) { testClipPipelineOverview(t, newStore) })
		t.Run("ClipPipelineRetry", func(t *testing.T) { testClipPipelineRetry(t, newStore) })
		t.Run("IncomingConveyorCount", func(t *testing.T) { testIncomingConveyorCount(t, newStore) })
		t.Run("Taxonomy", func(t *testing.T) { testTaxonomy(t, newStore) })
	})

	t.Run("Ops", func(t *testing.T) {
		t.Run("DiscoveryQualityLedger", func(t *testing.T) { testDiscoveryQualityLedger(t, newStore) })
		t.Run("SettingsKV", func(t *testing.T) { testSettings(t, newStore) })
		t.Run("SessionLifecycle", func(t *testing.T) { testSessionLifecycle(t, newStore) })
		t.Run("UserCredentialCapabilities", func(t *testing.T) { testUserCredentialCapabilities(t, newStore) })
		t.Run("UserContactAddresses", func(t *testing.T) { testUserContactAddresses(t, newStore) })
		t.Run("ObservabilityCounts", func(t *testing.T) { testCounts(t, newStore) })
		t.Run("ActivityFeed", func(t *testing.T) { testActivityFeed(t, newStore) })
		t.Run("Diagnostics", func(t *testing.T) { testDiagnostics(t, newStore) })
		t.Run("RetentionPurge", func(t *testing.T) { testRetentionPurge(t, newStore) })
	})

	t.Run("Images", func(t *testing.T) {
		t.Run("ImageRoundTrip", func(t *testing.T) { testImageRoundTrip(t, newStore) })
		t.Run("ImageMissingIsNotFound", func(t *testing.T) { testImageMissingIsNotFound(t, newStore) })
		t.Run("ImageUpsertPreservesCreatedAt", func(t *testing.T) { testImageUpsertPreservesCreatedAt(t, newStore) })
		t.Run("ImageRefsAndOwnerLookup", func(t *testing.T) { testImageRefsAndOwnerLookup(t, newStore) })
		t.Run("ImageOrphanDetection", func(t *testing.T) { testImageOrphanDetection(t, newStore) })
		t.Run("ImageDerivatives", func(t *testing.T) { testImageDerivatives(t, newStore) })
		t.Run("ImageDerivativeBatchAtomic", func(t *testing.T) { testImageDerivativeBatchAtomic(t, newStore) })
		t.Run("ImagesMissingFormat", func(t *testing.T) { testImagesMissingFormat(t, newStore) })
		t.Run("ImageFetchQueueAndExpirySweepAreDisjoint", func(t *testing.T) { testImageFetchQueueAndExpirySweepAreDisjoint(t, newStore) })
		t.Run("ImageUnrecoverableSelection", func(t *testing.T) { testImageUnrecoverableSelection(t, newStore) })
		t.Run("ImageDeleteCascades", func(t *testing.T) { testImageDeleteCascades(t, newStore) })
		t.Run("ImageTouch", func(t *testing.T) { testImageTouch(t, newStore) })
		t.Run("ImagesByOrigin", func(t *testing.T) { testImagesByOrigin(t, newStore) })
		t.Run("ImageFetchedBySourceURL", func(t *testing.T) { testImageFetchedBySourceURL(t, newStore) })
		t.Run("ImageRefsRepoint", func(t *testing.T) { testImageRefsRepoint(t, newStore) })
		t.Run("ImageDerivativeBudgetAndEvictionOrder", func(t *testing.T) {
			testImageDerivativeBudgetAndEvictionOrder(t, newStore)
		})
	})
}
