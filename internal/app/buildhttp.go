package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/config"
	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/playout"
	"github.com/loomarr/loomarr/internal/store"
)

type httpBuild struct {
	rootCtx                  context.Context
	store                    store.Store
	log                      *slog.Logger
	overrides                Overrides
	foundation               foundationBuild
	channels                 channelBuild
	approval                 approvalBuild
	suggestions              suggestionBuild
	fillers                  fillerBuild
	auth                     authBuild
	backups                  api.BackupsService
	restart                  api.RestartService
	bootConfig               *config.Config
	guide                    api.GuideReader
	settings                 api.SettingsService
	notificationDestinations api.NotificationDestinationService
	webPushPublicKey         string
	invitationDelivery       api.InvitationDeliveryService
	passwordRecovery         api.PasswordRecoveryService
	liveConfig               func(string) string
	libraryConfigured        func() bool
	jobs                     api.JobService
	database                 api.DatabaseService
	residentLLM              residentLLMBuild
	healthRefresh            api.HealthRefreshService
}

func buildHTTP(deps httpBuild) http.Handler {
	rootCtx, st, log, ov := deps.rootCtx, deps.store, deps.log, deps.overrides
	set, desiredSet := deps.foundation.set, deps.foundation.desiredSet
	ready, eventBus := deps.foundation.ready, deps.foundation.eventBus
	activityRec, fillerLayout := deps.foundation.activity, deps.foundation.fillerLayout
	appliedRestartSettings := deps.foundation.appliedRestartSettings
	channelSvc, liveTVSvc := deps.channels.channelService, deps.channels.liveTV
	tunerRescanner, tunarrConnectSvc := deps.channels.tunerRescanner, deps.channels.tunarrConnector
	backendController := deps.channels.backendController
	refreshBackendSettings, desiredBackend := deps.channels.refreshBackendSettings, deps.channels.desiredBackend
	checkpointSnapshot := deps.channels.checkpoint
	playoutObserver, preparedObserver := deps.channels.playoutObserver, deps.channels.preparedObserver
	playoutSvc, playoutResolverSvc := deps.channels.playout, deps.channels.playoutResolverService
	playoutGuideSvc, encodePool := deps.channels.playoutGuide, deps.channels.encodePool
	proposalApprover, chBinder := deps.approval.approver, deps.approval.binder
	suggestSvc, proposalWorkflow := deps.suggestions.suggest, deps.suggestions.workflow
	searchSvc, collectionsSvc := deps.suggestions.search, deps.suggestions.collections
	systemLLM, iconSvc, imageSvc := deps.suggestions.systemLLM, deps.suggestions.icons, deps.suggestions.images
	timelineThumbs := deps.suggestions.timelineThumbs
	fillerSvc, podPreview, taxonomyEditor := deps.fillers.service, deps.fillers.preview, deps.fillers.taxonomy
	backup, authorizer := deps.auth.backup, deps.auth.authorizer
	loginSvc, ssoSvc := deps.auth.login, deps.auth.sso
	sessMgr, userSync := deps.auth.sessions, deps.auth.userSync
	provisionSvc, passwordSvc, invitationSvc := deps.auth.provision, deps.auth.password, deps.auth.invitations
	invitationRedemption := deps.auth.invitationRedemption
	deviceMgr, deviceLimiter := deps.auth.deviceManager, deps.auth.deviceLimiter
	playoutSecret, playoutSecretCurrent := deps.auth.playoutSecret, deps.auth.playoutSecretCurrent
	backupsSvc, restartSvc, bootCfg := deps.backups, deps.restart, deps.bootConfig
	guideSvc, settingsSvc := deps.guide, deps.settings
	liveConfig, libraryConfigured := deps.liveConfig, deps.libraryConfigured
	jobsSvc, databaseSvc, residentLLM := deps.jobs, deps.database, deps.residentLLM
	fillerScreening, screeningErr := buildSegmentScreeningSummaryService(fillerLayout)
	if screeningErr != nil {
		log.Error("filler screening summaries were not activated", "err", screeningErr)
	}
	return api.Router(log, api.Options{
		Store:                st,
		Auth:                 authorizer,
		Log:                  log,
		Metrics:              deps.foundation.metrics,
		BackupSQLite:         backup,
		Ready:                ready,
		Login:                loginSvc,
		Sessions:             sessMgr,
		Passwords:            passwordSvc,
		Invitations:          invitationSvc,
		InvitationRedemption: invitationRedemption,
		InvitationDelivery:   deps.invitationDelivery,
		PasswordRecovery:     deps.passwordRecovery,
		AccessPublicURL: func() string {
			if liveConfig == nil {
				return ""
			}
			return liveConfig("access.public_url")
		},
		UserSync:            userSync,
		Devices:             deviceMgr,
		DeviceLimiter:       deviceLimiter,
		CookieSecure:        set.str("cookie.secure"),
		TrustProxy:          set.boolv("security.trust_proxy"),
		DevLogin:            ov.DevLogin,
		Pprof:               ov.Pprof,
		Channels:            channelSvc,
		LiveTV:              liveTVSvc,
		TunerRescanner:      tunerRescanner,
		TunarrConnect:       tunarrConnectSvc,
		Suggest:             suggestSvc,
		ProposalWorkflow:    proposalWorkflow,
		Search:              searchSvc,
		Collections:         collectionsSvc,
		Icons:               iconSvc,
		Images:              imageService(imageSvc),
		Events:              eventBus,
		Shutdown:            rootCtx.Done(),
		Filler:              fillerSvc,
		FillerScreening:     fillerScreening,
		FillerDecisions:     deps.fillers.decisions,
		FillerRights:        deps.fillers.rights,
		Pods:                podPreview,
		Taxonomy:            taxonomyEditor,
		SystemLLM:           systemLLM,
		Database:            databaseSvc,
		Encryption:          buildEncryptionService(st, deps.foundation.protection),
		Backups:             backupsSvc,
		SSO:                 ssoSvc,
		Restart:             restartSvc,
		Activity:            activityRec,
		DiagnosticEvents:    deps.foundation.diagnosticEvents,
		DiagnosticProcesses: deps.foundation.diagnosticProcesses,
		DiagnosticBundles:   deps.foundation.diagnosticBundles,
		DiagnosticCapture:   deps.foundation.diagnostics,
		ClientDiagnostics:   deps.foundation.clientDiagnostics,
		StartupReports:      deps.foundation.startupReports,
		HealthRefresh:       deps.healthRefresh,
		// The baseline for "has a restart-scoped setting changed?" is what THIS
		// generation booted with, captured here rather than per call (config-design §3).
		RestartDrift:             restartDrift(bootCfg, appliedRestartSettings, canonicalRestartCurrent(desiredSet)),
		Jobs:                     jobsSvc,
		Settings:                 settingsSvc,
		NotificationDestinations: deps.notificationDestinations,
		WebPushPublicKey:         deps.webPushPublicKey,
		ProposalNotifications:    deps.foundation.emitter,
		DecisionQuality:          deps.approval.decisionQuality,
		BackendTransition: currentBackendTransition{
			controller: backendController, refresh: refreshBackendSettings, desired: desiredBackend,
		},
		Guide:             guideSvc,
		Provision:         provisionSvc,
		Approver:          proposalApprover,
		Binder:            chBinder,
		FillerLayout:      fillerLayout,
		LiveConfig:        liveConfig,
		LibraryConfigured: libraryConfigured,
		BackendCheckpoint: func(ctx context.Context) (api.BackendCheckpoint, error) {
			snapshot, err := checkpointSnapshot(ctx)
			return api.BackendCheckpoint{
				Applied: snapshot.Applied, Prepared: snapshot.Prepared,
				PublishedInternal: snapshot.PublishedInternal,
			}, err
		},
		LiveConfigInt: set.intv,
		// boolOn (not boolv): the API reads bool keys that are ON by default, where an
		// unanswerable read must fail open — see Options.LiveConfigBoolOn.
		LiveConfigBoolOn: set.boolOn,
		// Internal playout (§9.1). PlayoutSecret is a FUNC so a regenerated token takes
		// effect without a restart (§11 rotation).
		PlayoutObserver:  playoutObserver,
		PreparedObserver: preparedObserver,
		// The in-app HLS repackager for the Watch surface (§9.1, V46). Nil ⇒ /playout/hls 501s.
		Playout:         playoutSvc,
		PlayoutResolver: playoutResolverSvc,
		// The XMLTV guide reads the same resolver, so listings cannot drift from playout.
		PlayoutGuide:         playoutGuideSvc,
		TimelineThumbs:       timelineThumbs,
		PlayoutSecret:        playoutSecret,
		PlayoutSecretCurrent: playoutSecretCurrent,
		// Bound to the ffmpeg path once, like probeAudio above: the answer depends on the
		// BUILD, so it cannot change without the binary changing. Memoised inside, so the
		// `-filters` exec happens on the first offline card and never again.
		PlayoutFont:    playout.CardFontFor(set.str("playout.ffmpeg_path")),
		PlayoutTonemap: playout.TonemapperFor(set.str("playout.ffmpeg_path")),
		// Free GPU memory for the hardware encoders by evicting the resident local LLM (§8.2, §9.1
		// V47). Built on demand and read LIVE, so a provider/model change hot-applies and eviction
		// always targets whatever model is currently resident. Only the local ollama provider holds
		// VRAM on this box — a hosted provider has nothing local to reclaim, so it is a no-op there
		// (the ladder then goes straight to the software fallback).
		ReclaimVRAM: residentLLM.reclaim,
		// The doctor's TRUE resident-VRAM reading (§9.1 V47), extracted to residentLLMVRAMFn above so
		// the admission budget's VRAM shading (V49) shares the exact same source.
		ResidentLLMVRAM: residentLLM.probe,
		// The same pool is consumed by live playout here and by the prepared-media planner. Live work
		// has foreground priority; preparation is cancellable and cannot consume the final slot.
		EncodePool: encodePool,
		PlayoutEncoder: func(
			ctx context.Context, args []string, onProgress func(playout.Progress),
		) (*playout.Process, error) {
			// `onProgress` was nil here since the supervisor was written, so ffmpeg's parsed
			// progress samples were discarded every time. Passing it through is what makes the
			// dashboard's encoder speed measured rather than invented (V16).
			spec, _ := diagnostics.ProcessSpecFromContext(ctx)
			return playout.StartObserved(ctx, set.str("playout.ffmpeg_path"), args, log, onProgress,
				deps.foundation.processDiagnostics, spec)
		},
	})
}
