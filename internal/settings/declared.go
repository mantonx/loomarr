package settings

import (
	"fmt"
	"net/mail"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

func recipientHTTPOrigin(value any) error {
	raw, ok := value.(string)
	if !ok {
		return fmt.Errorf("want an HTTP or HTTPS origin")
	}
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("want an HTTP or HTTPS origin without credentials, path, query, or fragment")
	}
	return nil
}

func smtpPort(value any) error {
	port, ok := value.(int)
	if !ok || port < 1 || port > 65535 {
		return fmt.Errorf("want a port from 1 through 65535")
	}
	return nil
}

func mailboxAddress(value any) error {
	raw, ok := value.(string)
	if !ok {
		return fmt.Errorf("want one mailbox address")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parsed, err := mail.ParseAddress(raw)
	if err != nil || parsed.Name != "" || parsed.Address != raw {
		return fmt.Errorf("want one address such as loomarr@example.com, without a display name")
	}
	return nil
}

// declared is the canonical registry content: every app-managed setting, in the
// order it appears in design.md §15. This list IS the contract — design.md §15
// is its human mirror and `make config-docs` its generated reference. A key added
// here without a matching §15 row (or vice versa) is the drift AGENTS.md forbids.
//
// Env-only bootstrap keys (DATABASE_URL, AUTO_MIGRATE, LISTEN_ADDR, LOG_LEVEL, TZ)
// are NOT here — they stay in config.Config (config-design §1 classification).
// Generated tokens (API_TOKEN, PLAYOUT_TOKEN) live in secrets.go
// (minted, not demanded — §4), not the app-managed registry.

// autoFileConfidenceRange bounds `filler.autofile.min_confidence` to 50–95 (§10 V38).
//
// ⚠ **The upper bound is load-bearing, not cosmetic.** `filler.MaxAutoFileConfidence` is 95 and
// an ungrounded era is capped strictly BELOW it, which is what guarantees no settable threshold
// can auto-file a fabricated era. Raising this ceiling without raising that cap silently breaks
// the guarantee §10 makes.
//
// ⚠ The number is repeated here rather than imported: `settings` must not depend on `filler`
// (the dependency runs the other way — the tagger reads settings). `filler`'s own test pins the
// relationship, so a divergence fails there rather than going unnoticed.
//
// The lower bound is a usability floor: below 50 the threshold admits clips whose tags did not
// fully verify, which makes Incoming an empty room and the catalog a surprise.
func autoFileConfidenceRange(v any) error {
	n, ok := v.(int)
	if !ok {
		return fmt.Errorf("want a whole number")
	}
	if n < 50 || n > 95 {
		return fmt.Errorf("want 50-95 (got %d) — below 50 files clips whose tags didn't verify; above 95 nothing would ever file", n)
	}
	return nil
}

// positiveLimit rejects a zero or negative cap.
//
// ⚠ Zero is refused rather than treated as "unlimited". A limit key silently meaning its own
// opposite is how an operator sets 0 expecting "no fetching" and gets an uncapped crawler.
// Turning auto-fetch OFF is `filler.fetch.every = 0`, which says what it does.
func positiveLimit(v any) error {
	n, ok := v.(int)
	if !ok {
		return fmt.Errorf("want a whole number")
	}
	if n < 1 {
		return fmt.Errorf("want 1 or more (got %d) — to stop fetching automatically, set filler.fetch.every to 0", n)
	}
	return nil
}

func positiveWholeNumber(v any) error {
	n, ok := v.(int)
	if !ok {
		return fmt.Errorf("want a whole number")
	}
	if n < 1 {
		return fmt.Errorf("want 1 or more (got %d)", n)
	}
	return nil
}

func positiveDuration(v any) error {
	d, ok := v.(time.Duration)
	if !ok {
		return fmt.Errorf("want a duration")
	}
	if d <= 0 {
		return fmt.Errorf("want a duration greater than zero (got %s)", d)
	}
	return nil
}

func nonNegativeWholeNumber(v any) error {
	n, ok := v.(int)
	if !ok {
		return fmt.Errorf("want a whole number")
	}
	if n < 0 {
		return fmt.Errorf("want 0 or more (got %d)", n)
	}
	return nil
}

func breakDuration(v any) error {
	d, ok := v.(time.Duration)
	if !ok {
		return fmt.Errorf("want a duration")
	}
	if d < 30*time.Second {
		return fmt.Errorf("want at least 30s (got %s) — shorter values are clamped by Tunarr and would make playout backends disagree", d)
	}
	return nil
}

func optionalCountryCode(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("want a country code")
	}
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return nil
	}
	if len(s) != 2 || s[0] < 'A' || s[0] > 'Z' || s[1] < 'A' || s[1] > 'Z' {
		return fmt.Errorf("want an ISO 3166-1 alpha-2 country code (for example US or CA)")
	}
	return nil
}

// storagePath validates generation-scoped storage roots without importing their owning modules
// back into settings. Absolute paths keep one generation anchored to an unambiguous root.
// Refusing the filesystem root prevents a bad edit from turning a bounded subsystem into a walk
// or output sink over the whole mounted host/container filesystem.
func storagePath(optional bool) ValidateFunc {
	return func(v any) error {
		path, ok := v.(string)
		if !ok {
			return fmt.Errorf("want a filesystem path")
		}
		path = strings.TrimSpace(path)
		if path == "" {
			if optional {
				return nil
			}
			return fmt.Errorf("want a non-empty absolute path")
		}
		clean := filepath.Clean(path)
		if !filepath.IsAbs(clean) {
			return fmt.Errorf("want an absolute path (got %q)", path)
		}
		root := filepath.VolumeName(clean) + string(filepath.Separator)
		if clean == root {
			return fmt.Errorf("the filesystem root cannot be used as a storage directory")
		}
		return nil
	}
}

func declared() []Setting {
	return []Setting{
		// --- Connections: media server (§15, Phase 5) ---
		{
			Key: "library.flavor", EnvVar: "LIBRARY_FLAVOR", Group: GroupMediaServer,
			Kind: KindEnum, Enum: []EnumOption{opt("emby", "Emby"), opt("jellyfin", "Jellyfin")}, Default: "",
			Doc: "Emby or Jellyfin. They sign in differently, so Loomarr needs to know which one you run.",
		},
		{
			Key: "library.url", EnvVar: "LIBRARY_URL", Group: GroupMediaServer,
			Kind: KindURL, Default: "",
			Doc: "Media server base URL, e.g. http://emby:8096.",
		},
		{
			Key: "library.token", EnvVar: "LIBRARY_TOKEN", Group: GroupMediaServer,
			Kind: KindSecret, Default: "",
			Doc: "An API key from your media server. Lets Loomarr read your library and set up the TV guide.",
		},
		{
			// Direct play (§9.1 V47): translate the media server's file paths to where Loomarr can
			// read them, so playout reads the FILE and copies it instead of transcoding the media
			// server's HTTP stream. Empty = no mapping, so playout falls back to the HTTP stream —
			// which is what a media server on another host with no shared mount needs.
			Key: "library.path_map", EnvVar: "LIBRARY_PATH_MAP", Group: GroupMediaServer,
			Kind: KindString, Default: "", Advanced: true,
			Doc: "Path mapping so Loomarr can read your media files directly (much faster, no transcoding when the file already plays). Your media server reports each file by its OWN path (e.g. /data/tv); if that same file is mounted somewhere else on the machine running Loomarr (e.g. /mnt/media/tv), map one to the other as \"/data=>/mnt/media\". Multiple rules are separated by commas or newlines. Leave empty if Loomarr and your media server don't share the files — playout will stream from the media server instead.",
		},
		// --- Connections: requester (§15, Phase 6) ---
		// How Loomarr acquires missing titles: through Seerr (default), or Sonarr + Radarr
		// directly. The provider gates which fields show (ShowWhen), mirroring llm.provider.
		{
			Key: "requester.provider", EnvVar: "REQUESTER_PROVIDER", Group: GroupRequester,
			Kind: KindEnum, Enum: []EnumOption{opt("seerr", "Seerr / Jellyseerr"), opt("arr", "Sonarr + Radarr (direct)")}, Default: "seerr",
			Doc: "How Loomarr downloads missing titles: through Seerr, or Sonarr and Radarr directly.",
		},
		{
			Key: "seerr.url", EnvVar: "SEERR_URL", Group: GroupRequester,
			Kind: KindURL, Default: "", Required: FeatureAcquisition,
			Doc:      "Your Seerr address, e.g. http://seerr:5055. This is how Loomarr downloads missing titles.",
			ShowWhen: map[string][]string{"requester.provider": {"seerr"}},
		},
		{
			Key: "seerr.api_key", EnvVar: "SEERR_API_KEY", Group: GroupRequester,
			Kind: KindSecret, Default: "",
			Doc:      "Your Seerr API key.",
			ShowWhen: map[string][]string{"requester.provider": {"seerr"}},
		},
		{
			Key: "sonarr.url", EnvVar: "SONARR_URL", Group: GroupRequester,
			Kind: KindURL, Default: "",
			Doc:      "Sonarr address (for TV), e.g. http://sonarr:8989.",
			ShowWhen: map[string][]string{"requester.provider": {"arr"}},
		},
		{
			Key: "sonarr.api_key", EnvVar: "SONARR_API_KEY", Group: GroupRequester,
			Kind: KindSecret, Default: "",
			Doc:      "Your Sonarr API key (Settings → General in Sonarr).",
			ShowWhen: map[string][]string{"requester.provider": {"arr"}},
		},
		{
			Key: "sonarr.quality_profile", Label: "Sonarr quality profile override", EnvVar: "SONARR_QUALITY_PROFILE", Group: GroupRequester,
			Kind: KindString, Default: "", Advanced: true,
			Doc:      "Optional Sonarr quality profile (name or id). Blank = Sonarr's first profile.",
			ShowWhen: map[string][]string{"requester.provider": {"arr"}},
		},
		{
			Key: "sonarr.root_folder", Label: "Sonarr root folder override", EnvVar: "SONARR_ROOT_FOLDER", Group: GroupRequester,
			Kind: KindString, Default: "", Advanced: true,
			Doc:      "Optional Sonarr root folder path. Blank = Sonarr's first root folder.",
			ShowWhen: map[string][]string{"requester.provider": {"arr"}},
		},
		{
			Key: "radarr.url", EnvVar: "RADARR_URL", Group: GroupRequester,
			Kind: KindURL, Default: "",
			Doc:      "Radarr address (for movies), e.g. http://radarr:7878.",
			ShowWhen: map[string][]string{"requester.provider": {"arr"}},
		},
		{
			Key: "radarr.api_key", EnvVar: "RADARR_API_KEY", Group: GroupRequester,
			Kind: KindSecret, Default: "",
			Doc:      "Your Radarr API key (Settings → General in Radarr).",
			ShowWhen: map[string][]string{"requester.provider": {"arr"}},
		},
		{
			Key: "radarr.quality_profile", Label: "Radarr quality profile override", EnvVar: "RADARR_QUALITY_PROFILE", Group: GroupRequester,
			Kind: KindString, Default: "", Advanced: true,
			Doc:      "Optional Radarr quality profile (name or id). Blank = Radarr's first profile.",
			ShowWhen: map[string][]string{"requester.provider": {"arr"}},
		},
		{
			Key: "radarr.root_folder", Label: "Radarr root folder override", EnvVar: "RADARR_ROOT_FOLDER", Group: GroupRequester,
			Kind: KindString, Default: "", Advanced: true,
			Doc:      "Optional Radarr root folder path. Blank = Radarr's first root folder.",
			ShowWhen: map[string][]string{"requester.provider": {"arr"}},
		},

		// --- Connections: Tunarr (§15, Phase 10) ---
		{
			Key: "tunarr.url", EnvVar: "TUNARR_URL", Group: GroupTunarr,
			Kind: KindURL, Default: "",
			Doc: "Your Tunarr address, e.g. http://tunarr:8000. This is where Loomarr builds your channels.",
		},
		{
			Key: "tunarr.transcode_config_id", Label: "Transcode profile override", EnvVar: "TUNARR_TRANSCODE_CONFIG_ID", Group: GroupTunarr,
			Kind: KindString, Default: "", Advanced: true,
			Doc: "Which Tunarr transcode profile new channels use. Leave empty to use Tunarr's default.",
		},
		{
			// RE-SCOPED by V4 (§9.1), not duplicated. This was a Tunarr-group, Advanced
			// knob documented as "only needed for uploaded channel icons". Internal
			// playout makes it the base every SEGMENT request resolves against, so it
			// moves to Playout and stops being Advanced — "get it wrong and channels
			// appear in the guide but never play" is not an advanced failure mode.
			//
			// Deliberately ONE key, not a second `playout.public_url`: it is genuinely
			// the server's own public address, and both callers (icon fetch, segment
			// fetch) need the same value. Two keys could drift, and an operator would
			// have to know which one Live TV reads.
			Key: "server.public_url", Label: "Loomarr address", EnvVar: "SERVER_PUBLIC_URL", Group: GroupPlayout,
			Kind: KindURL, Default: "",
			Doc: "Loomarr's own address as your media server and Tunarr can reach it, e.g. http://loomarr:8080. Internal playout serves every stream segment from this base, so a wrong value means channels appear in the guide and never play. Also used for uploaded channel icons.",
		},

		{
			Key: "access.public_url", Label: "Recipient-facing Loomarr address", EnvVar: "ACCESS_PUBLIC_URL", Group: GroupGeneral,
			Kind: KindURL, Default: "", Validate: recipientHTTPOrigin,
			Doc: "The absolute browser address invitation and recovery recipients can reach, e.g. https://loomarr.example.com. General suggests the current browser origin when this is empty; save a different address when recipients reach Loomarr elsewhere. The server never infers it from request headers.",
		},
		{
			Key: "notifications.email.enabled", Label: "Send email notifications", EnvVar: "NOTIFICATIONS_EMAIL_ENABLED", Group: GroupNotifications,
			Kind: KindBool, Presentation: PresentationSwitch, Default: false, MigrationOnly: true,
			Doc: "Deliver account invitations and recovery messages by email. An incomplete setup suppresses email without affecting copied links, QR codes, or direct account creation.",
		},
		{
			Key: "notifications.smtp.host", Label: "SMTP host", EnvVar: "NOTIFICATIONS_SMTP_HOST", Group: GroupNotifications,
			Kind: KindString, Default: "", MigrationOnly: true, ShowWhen: map[string][]string{"notifications.email.enabled": {"true"}},
			Doc: "Hostname of the SMTP submission server. Required when email delivery is enabled.",
		},
		{
			Key: "notifications.smtp.port", Label: "SMTP port", EnvVar: "NOTIFICATIONS_SMTP_PORT", Group: GroupNotifications,
			Kind: KindInt, Default: 587, MigrationOnly: true, Validate: smtpPort, ShowWhen: map[string][]string{"notifications.email.enabled": {"true"}},
			Doc: "Port of the SMTP submission server, from 1 through 65535.",
		},
		{
			Key: "notifications.smtp.security", Label: "SMTP security", EnvVar: "NOTIFICATIONS_SMTP_SECURITY", Group: GroupNotifications,
			Kind: KindEnum, Enum: []EnumOption{
				opt("starttls", "STARTTLS (required)"),
				opt("tls", "TLS"),
				opt("none", "None (insecure local relay)"),
			},
			Default: "starttls", MigrationOnly: true, ShowWhen: map[string][]string{"notifications.email.enabled": {"true"}},
			Doc: "STARTTLS requires encryption and never downgrades; TLS connects encrypted immediately. None is only for an explicitly trusted local relay. Certificate verification is always enabled.",
		},
		{
			Key: "notifications.smtp.username", Label: "SMTP username", EnvVar: "NOTIFICATIONS_SMTP_USERNAME", Group: GroupNotifications,
			Kind: KindString, Default: "", MigrationOnly: true, ShowWhen: map[string][]string{"notifications.email.enabled": {"true"}},
			Doc: "Username for SMTP authentication. Leave empty only for an unauthenticated relay.",
		},
		{
			Key: "notifications.smtp.password", Label: "SMTP password", EnvVar: "NOTIFICATIONS_SMTP_PASSWORD", Group: GroupNotifications,
			Kind: KindSecret, Default: "", MigrationOnly: true, ShowWhen: map[string][]string{"notifications.email.enabled": {"true"}},
			Doc: "Password for SMTP authentication. It is write-only, masked on read, and must remain empty for an unauthenticated relay.",
		},
		{
			Key: "notifications.email.from_address", Label: "Sender address", EnvVar: "NOTIFICATIONS_EMAIL_FROM_ADDRESS", Group: GroupNotifications,
			Kind: KindString, Default: "", MigrationOnly: true, Validate: mailboxAddress, ShowWhen: map[string][]string{"notifications.email.enabled": {"true"}},
			Doc: "Mailbox Loomarr sends from, such as loomarr@example.com. Required when email delivery is enabled.",
		},
		{
			Key: "notifications.email.from_name", Label: "Sender name", EnvVar: "NOTIFICATIONS_EMAIL_FROM_NAME", Group: GroupNotifications,
			Kind: KindString, Default: "Loomarr", MigrationOnly: true, ShowWhen: map[string][]string{"notifications.email.enabled": {"true"}},
			Doc: "Display name shown beside the sender address.",
		},

		// --- Playout (§9.1, §15 — added by V4) ---
		// Loomarr serves its own channels. `playout.backend` is the ONLY key here with a
		// per-channel override: it rides `policy_json` as `policy.playout` (nil = inherit
		// this global), the same shape `rules`/`filler`/`window`/`autoCurate` already use,
		// so there is no migration. Inheriting channels resolve this value live, so changing
		// the default moves that fleet; a channel with an explicit policy remains pinned to
		// its selected backend.
		{
			Key: "playout.backend", Label: "Playback engine", EnvVar: "PLAYOUT_BACKEND", Group: GroupPlayout,
			Kind: KindEnum, Enum: []EnumOption{
				opt("internal", "Loomarr (internal)"),
				opt("tunarr", "Tunarr"),
			},
			Default: "internal",
			Doc:     "Who streams a channel. Internal playout is required for mid-roll breaks (§10) and reports real transcode telemetry. Tunarr remains fully supported — the right answer for hardware that cannot transcode, or an install that already works. Overridable per channel.",
		},
		{
			Key: "playout.encoder", Label: "Encoder override", EnvVar: "PLAYOUT_ENCODER", Group: GroupPlayout,
			Kind: KindString, Default: "", Advanced: true,
			Doc: "ffmpeg encoder for internal playout (e.g. libx264, h264_vaapi, h264_nvenc). Empty = pick the best one the transcode check found. Set it only to override that choice.",
		},
		{
			Key: "playout.audio_language", Label: "Preferred audio language", EnvVar: "PLAYOUT_AUDIO_LANGUAGE", Group: GroupPlayout,
			Kind: KindString, Presentation: PresentationLanguage, Default: "eng",
			Doc: "Preferred audio language for internal playout, as an ISO 639-2 code (eng, fra, spa, jpn). A preference, not a requirement: a film with no track in this language plays its first track rather than failing. Empty = play whichever track comes first in the file, which is how a foreign-language dub ends up playing instead of the original. A channel can override this on its Watch tab (§9.1).",
		},
		{
			Key: "playout.quality_tier", Label: "Maximum playback quality", EnvVar: "PLAYOUT_QUALITY_TIER", Group: GroupPlayout,
			Kind: KindEnum, Enum: []EnumOption{
				opt("efficient", "Efficient — 720p, lowest bandwidth"),
				opt("balanced", "Balanced — 1080p"),
				opt("quality", "Quality — 1080p, best picture"),
			},
			Default: "balanced",
			Doc:     "The picture-versus-bandwidth target. Efficient is 720p and roughly half the bitrate — the right answer for a NAS running several channels, or for watching away from home. Balanced is 1080p and the default. Quality is 1080p at a higher frame rate and bitrate, which on grainy or dark film can be visibly cleaner but costs noticeably more bandwidth per channel. Whichever you pick, playout still steps down automatically as more channels start, so the choice is a ceiling rather than a promise.",
		},
		{
			// ⚠ Still separate from ingest.ffmpeg_path, but NOT for the reason this comment
			// used to give ("the filler sidecar bundles its own ffmpeg in a different image").
			// There is one image now (§16), so that rationale died with the sidecar and the
			// two-tag split.
			//
			// The live reason: these two callers fail differently. Playout's ffmpeg is a
			// runtime dependency of a channel that is ON AIR, while ingest's is a build
			// dependency of a download nobody is watching — so an operator pointing ingest at
			// a newer yt-dlp-compatible ffmpeg must not be able to break playout by doing it.
			Key: "playout.ffmpeg_path", Label: "FFmpeg path", EnvVar: "PLAYOUT_FFMPEG_PATH", Group: GroupPlayout,
			Kind: KindString, Presentation: PresentationPath, Default: "ffmpeg", Advanced: true,
			Doc: "Where the ffmpeg program lives. The default works whenever ffmpeg is on the system PATH; set it only if yours is somewhere unusual.",
		},
		{
			// Where the in-app HLS player's segments are written (§9.1 Watch, V46). Empty =
			// the OS temp dir, which is the right default for most installs. An operator points
			// it at a fast disk or a tmpfs when playing several channels to browsers, or away
			// from a small root filesystem — the footprint is a rolling window of a few segments
			// per watched channel, cleaned up when the last viewer leaves. Advanced: a wrong
			// value degrades in-app playback, never the media-server streams (those never touch
			// this dir).
			Key: "playout.hls_dir", Label: "Browser playback cache", EnvVar: "PLAYOUT_HLS_DIR", Group: GroupPlayout,
			Kind: KindString, Presentation: PresentationPath, Default: "", Advanced: true,
			Doc: "Directory where in-app browser playback writes its temporary HLS segments (§9.1). Empty uses the system temp directory. Point it at a fast disk (SSD or a RAM-backed tmpfs like /dev/shm) if you watch several channels in the browser at once, or away from a small root filesystem. Only affects in-app playback; your media server's streams never use it. The space used is a few short segments per channel being watched, deleted when you stop watching.",
		},
		{
			// Persistent and deliberately separate from playout.hls_dir: those live fragments are
			// scratch bytes deleted when viewing stops; these publications are reusable across channels
			// and restarts. Read once at composition because moving an active publication library while
			// clients hold keyed asset URLs would split one origin across two roots.
			Key: "playout.prepared_dir", Label: "Prepared media library", EnvVar: "PLAYOUT_PREPARED_DIR", Group: GroupPlayout,
			Kind: KindString, Presentation: PresentationPath, Default: "/data/prepared", Advanced: true,
			Doc: "Where Loomarr stores reusable prepared programmes for instant channel changes. Defaults inside /data so the documented volume carries it across restarts. This can grow with the unique programmes scheduled across channels; put it on persistent fast storage, not a RAM disk. Changing it takes effect after restart.",
		},
		{
			// A soft cap rather than a quota: active HLS publications win when their protected
			// bytes exceed it. Hot-applied because it changes only the next retention decision;
			// publication identity and keyed asset paths do not move.
			Key: "playout.prepared_budget_gb", Label: "Prepared media budget", EnvVar: "PLAYOUT_PREPARED_BUDGET_GB", Group: GroupPlayout,
			Kind: KindInt, Default: 512, Advanced: true, Validate: positiveWholeNumber,
			Doc: "Soft storage cap in GiB for reusable prepared programmes. Loomarr evicts the least recently used whole programmes after preparation runs, while anything played in the last fifteen minutes stays protected. The 512 GiB default holds roughly 220 hours at Balanced quality. Changes apply to the next pass without restart.",
		},
		{
			Key: "playout.max_channels", Label: "Live transcode safety cap", EnvVar: "PLAYOUT_MAX_CHANNELS", Group: GroupPlayout,
			Kind: KindInt, Default: "0", Validate: nonNegativeWholeNumber,
			Doc: "Optional safety cap for simultaneous internal transcodes. Leave at 0 for Loomarr to use measured capacity automatically. A positive value can lower that measurement but cannot raise it.",
		},

		{
			// The guide's display timezone (§12, V13b gap 7). The API always speaks absolute
			// epoch ms — a timezone is a RENDERING choice, and putting it on the wire would
			// invite a client to reinterpret instants it should merely format.
			//
			// Empty = the viewer's own browser timezone, which is right for the household
			// case. An operator sets it when the server and its viewers are elsewhere, or
			// when they want the guide to read in the channels' "broadcast" timezone.
			Key: "guide.timezone", Label: "Timezone", EnvVar: "GUIDE_TIMEZONE", Group: GroupPlayout,
			Kind: KindString, Default: "",
			Doc: "Which timezone the TV guide's times are shown in, as an IANA name like America/New_York. Leave empty to use each viewer's own device timezone.",
		},
		{
			// How far back the guide will look (§12, V13b gap 8).
			//
			// A real bound, not a cosmetic one: the past is recomputed from the channel's
			// CURRENT lineup, so a distant "as aired" view would be fiction — the lineup has
			// been reconciled since. A day is honest; a month would be invention presented as
			// history.
			Key: "guide.retention_hours", Label: "Past listings (hours)", EnvVar: "GUIDE_RETENTION_HOURS", Group: GroupPlayout,
			Kind: KindInt, Default: "24",
			Doc: "How far back the TV guide lets you scroll, in hours. Past listings are recomputed from each channel's current lineup, so going too far back would show a schedule that never actually aired.",
		},

		// --- Backup (§16, §15 — added by V4) ---
		{
			Key: "backup.schedule", Label: "Automatic backup schedule", EnvVar: "BACKUP_SCHEDULE", Group: GroupBackup,
			Kind: KindCron, Default: "0 30 3 * * *",
			Doc: "When to write the nightly database backup. It contains settings, channels, people, encrypted secrets, and wrapped data keys, but not the external installation key. It does not contain filler, prepared media, cached artwork, or operator image uploads.",
		},
		{
			Key: "backup.retain", Label: "Backups to keep", EnvVar: "BACKUP_RETAIN", Group: GroupBackup,
			Kind: KindInt, Default: "7",
			Doc: "How many backups to keep before pruning the oldest.",
		},
		{
			Key: "backup.dir", Label: "Backup location", EnvVar: "BACKUP_DIR", Group: GroupBackup,
			Kind: KindString, Presentation: PresentationPath, Default: "/data/backups",
			Doc: "Where backups are written. Defaults inside /data so the documented volume carries them; point it elsewhere to keep backups off the same disk as the database.",
		},

		// --- Images (§15, §22, V52) ---
		{
			// ⚠ Defaults inside /data for the same reason `filler.dir` and `backup.dir` do — the
			// documented volume carries it — but with a consequence neither of those has: the
			// application backup is a DATABASE backup, and no image bytes are in the database
			// (§22). Everything here is regenerable or re-fetchable EXCEPT operator uploads, which
			// is why image maintenance counts unrecoverable-missing rows as a warning rather than
			// pretending it can repair them.
			Key: "images.dir", Label: "Image library location", EnvVar: "IMAGES_DIR", Group: GroupImages,
			Kind: KindString, Presentation: PresentationPath, Default: "/data/images",
			Doc: "Where Loomarr stores images — originals and the resized copies it serves. Defaults inside /data so the documented volume carries it. Not covered by the database backup: back up the volume.",
		},
		{
			Key: "images.max_upload_bytes", Label: "Maximum image upload", EnvVar: "IMAGES_MAX_UPLOAD_BYTES", Group: GroupImages,
			Kind: KindInt, Presentation: PresentationBytes, Default: "8388608",
			Doc: "The largest image someone may upload, in bytes (8 MiB by default). Enforced while reading the upload, not from the size the client declares.",
		},
		{
			Key: "images.remote_fetch_enabled", Label: "Download remote artwork", EnvVar: "IMAGES_REMOTE_FETCH_ENABLED", Group: GroupImages,
			Kind: KindBool, Default: true,
			Doc: "Whether Loomarr may download artwork from TMDB and your media server. Turn this off to keep to locally-produced images only — no outbound image requests are made.",
		},
		{
			Key: "images.cache_budget_mb", Label: "Resized-image cache", EnvVar: "IMAGES_CACHE_BUDGET_MB", Group: GroupImages,
			Kind: KindInt, Default: "2048", Advanced: true,
			Doc: "How much disk the resized copies may use before Loomarr starts removing the least recently used ones. They are always regenerable, so this costs a little latency, never an image.",
		},

		// --- Connections: TMDB (§15, Phase 11) ---
		{
			Key: "tmdb.api_key", EnvVar: "TMDB_API_KEY", Group: GroupTMDB,
			Kind: KindSecret, Default: "", Required: FeatureSuggestions,
			Doc: "A free TMDB API key. Enables TMDB title search, channel icon suggestions, and grounding for AI channel suggestions.",
		},

		// --- AI (§15, §8.1; in-app selection persists to llm.* and overrides these env pins) ---
		{
			Key: "llm.provider", Label: "Lineup AI provider", EnvVar: "LLM_PROVIDER", Group: GroupAI,
			Kind: KindEnum, Enum: []EnumOption{opt("ollama", "Ollama"), opt("openai", "OpenAI-compatible")}, Default: "ollama", Required: FeatureSuggestions,
			Doc: "Which AI to use: a local Ollama, or an OpenAI-compatible service. You can also pick a model in the AI settings.",
		},
		{
			// Both providers need an endpoint: the Ollama host for local AI, or the
			// OpenAI-compatible base URL for hosted AI. The default host is conventional, not
			// universal, so hiding this for Ollama made remote Ollama impossible to configure.
			Key: "llm.url", Label: "AI service address", EnvVar: "LLM_URL", Group: GroupAI,
			Kind: KindURL, Default: "",
			Doc: "For Ollama, its host such as http://ollama:11434. For a hosted provider, the exact OpenAI-compatible API base; Loomarr fills this for OpenRouter, while Custom remains editable.",
		},
		{
			// The normal AI page uses the ranked picker for both provider kinds so two
			// controls never compete. This declaration remains for env compatibility and
			// the explicit All Settings escape hatch.
			Key: "llm.model", Label: "Lineup model", EnvVar: "LLM_MODEL", Group: GroupAI,
			Kind: KindString, Default: "", Required: FeatureSuggestions,
			Doc:      "The active model used to build channel lineups. Prefer the guided picker on the AI page; OpenRouter ids use provider/model (for example openai/gpt-4o-mini).",
			ShowWhen: map[string][]string{"llm.provider": {"openai"}},
		},
		{
			// Ollama is local and needs no key — this only applies to a hosted service.
			Key: "llm.api_key", Label: "Hosted AI API key", EnvVar: "LLM_API_KEY", Group: GroupAI,
			Kind: KindSecret, Default: "",
			Doc:      "API key for your hosted AI service. Never shown again after saving.",
			ShowWhen: map[string][]string{"llm.provider": {"openai"}},
		},
		{
			// Local-only (§8.2): a hosted service has no model to hold in memory, so this
			// is hidden for the openai provider rather than shown as an inert control.
			Key: "llm.keep_alive", Label: "Keep local model loaded", EnvVar: "LLM_KEEP_ALIVE", Group: GroupAI,
			Kind: KindDuration, Default: "2m", Advanced: true,
			Doc:      "How long to keep the local AI model loaded in memory between requests. Loading it takes several seconds, so keeping it ready makes suggestions much faster — but the model shares GPU memory with channel playback, so the default is short (2m) to free that memory for streaming. Raise it if you rarely stream and want faster suggestions; set 0 to free memory as soon as each request finishes.",
			ShowWhen: map[string][]string{"llm.provider": {"ollama"}},
		},
		{
			Key: "suggest.max_acquisitions", Label: "Pending-download limit per person", EnvVar: "SUGGEST_MAX_ACQUISITIONS", Group: GroupAI,
			Kind: KindInt, Default: 10,
			Doc: "The most titles a single suggestion may download.",
		},

		// --- Self-updating channels / re-curation (programming-design §8.2) ---
		{
			Key: "job.recurate.schedule", EnvVar: "JOB_RECURATE_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 0 4 * * 0", Advanced: true,
			Doc: "How often auto-curate channels re-evaluate their intent against the library (cron). Weekly by default — this runs the AI, so keep it infrequent.",
		},
		{
			Key: "recurate.min_score_pct", Label: "Auto-curation request threshold", EnvVar: "RECURATE_MIN_SCORE_PCT", Group: GroupAI,
			Kind: KindInt, Default: 60, Advanced: true,
			Doc: "Quality bar (0–100) a not-in-library title must clear for auto-curate to REQUEST it. In-library matches are added regardless. A per-channel override may be stricter or looser.",
		},
		{
			Key: "recurate.max_titles", Label: "Auto-curation channel limit", EnvVar: "RECURATE_MAX_TITLES", Group: GroupAI,
			Kind: KindInt, Default: 40, Advanced: true,
			Doc: "The most titles an auto-curate channel may grow to. Re-curation won't request net-new titles past this cap. A per-channel override may be stricter or looser.",
		},

		// --- Channels & playback (§15, Phase 10; policy defaults = programming-design §2) ---
		{
			Key: "channel.reconcile_every", Label: "Channel rebuild cooldown", EnvVar: "CHANNEL_RECONCILE_EVERY", Group: GroupChannels,
			Kind: KindDuration, Default: "10m", Advanced: true,
			Doc: "Minimum delay after a successful rebuild before that channel is eligible for another scheduled sweep. Change the sweep cadence under System → Tasks.",
		},
		{
			Key: "sched.window_hours", Label: "Schedule ahead", EnvVar: "SCHED_WINDOW_HOURS", Group: GroupChannels,
			Kind: KindDuration, Default: "24h",
			Doc: "How far ahead each channel schedules — the rolling window it materializes and rolls forward, instead of the whole series run (per-channel/-rule overridable; 0 = schedule everything).",
		},

		// --- Filler / commercials (§15, Phase 12; §10 redesign — Tunarr-owned) ---
		{
			Key: "filler.home_country", Label: "Home country", EnvVar: "FILLER_HOME_COUNTRY", Group: GroupFiller,
			Kind: KindString, Default: "", Validate: optionalCountryCode,
			Doc: "Optional ISO two-letter country that constrains automatic filler use. Unknown and foreign clips remain reviewable but do not air. Leave blank to preserve the legacy unrestricted pool until geography is configured.",
		},
		{
			Key: "filler.home_market", Label: "Home local market", EnvVar: "FILLER_HOME_MARKET", Group: GroupFiller,
			Kind: KindString, Default: "",
			Doc: "Optional local broadcast market inside the home country, such as New York or Seattle. Local clips must match it exactly; Loomarr never infers it from the guide timezone.",
		},
		{
			// ⚠ Defaults inside /data, like database.url and backup.dir — the documented
			// volume carries it, so a zero-env `docker run` has a working drop-folder
			// instead of a Filler page that is one empty state telling the operator to go
			// and configure something. It was "" for no recorded reason while its two
			// neighbours both defaulted; that asymmetry made the whole feature opt-in by
			// accident. Still overridable to point at an existing library on another disk.
			Key: "filler.dir", Label: "Clip library", EnvVar: "FILLER_DIR", Group: GroupFiller,
			Kind: KindString, Presentation: PresentationPath, Apply: ApplyRestart, Default: "/data/filler", Required: FeatureFiller, Validate: storagePath(false),
			Doc: "Where Loomarr stores clips. Each is filed under its content hash with its metadata beside it. Defaults inside /data so the documented volume carries it; point it elsewhere to use an existing clip library.",
		},
		{
			// The watch folder (§10 V38c, "Two folders, one pipeline"). Clips ARRIVE here —
			// downloads land here, operators drop files here — and every sync drains it into the
			// clip folder above.
			//
			// ⚠ **The default is EMPTY, resolved to `<filler.dir>/_watch` by the reader**, not a
			// literal `/data/filler/_watch`. A literal would silently stop tracking the moment an
			// operator pointed `filler.dir` at an existing library on another disk: arrivals would
			// keep landing under `/data` while the catalog looked elsewhere, and the drop-folder
			// would appear broken with both settings looking correct.
			//
			// ⚠ **Inside the clip folder rather than a sibling.** A sibling needs its own mounted
			// volume, and an unmounted watch folder loses anything not yet filed on the next
			// restart — silently, because an empty folder is also what success looks like. The
			// scan skips `_watch` by name so a waiting file is never catalogued from its arrival
			// path (which would then be pruned the moment intake moved it).
			Key: "filler.watch_dir", Label: "Drop folder", EnvVar: "FILLER_WATCH_DIR", Group: GroupFiller,
			Kind: KindString, Presentation: PresentationPath, Apply: ApplyRestart, Default: "", Validate: storagePath(true),
			Doc: "Folder Loomarr watches for new clips. Anything dropped here is filed into your clip folder and then removed. Leave blank to use a '_watch' folder inside the clip folder.",
		},
		{
			Key: "filler.sync_every", Label: "Scan for dropped clips every", EnvVar: "FILLER_SYNC_EVERY", Group: GroupFiller,
			Kind: KindDuration, Default: "15m", Advanced: true,
			Doc: "How often Loomarr drains the drop folder and reconciles its own clip library.",
		},
		{
			// The drop-folder's on/off switch on the Sources tab (§10 V35). A setting rather
			// than a row because the folder is DERIVED from `filler.dir` — a remote
			// collection's switch is a column on its own row, so a row and a setting never
			// describe the same source.
			//
			// ⚠ Default TRUE, and it must stay that way: an install that has never seen this
			// key has a working drop-folder, and defaulting to false would silently stop the
			// scan on upgrade.
			//
			// ⚠ There is deliberately no `filler.source.library.enabled`. Nothing scans a
			// media-server library for clips (§10 took the media server out of the filler
			// path), so that key would gate no work — a control that dims a row and changes
			// nothing. The Sources tab renders that row as provenance, without a switch.
			Key: "filler.source.folder.enabled", Label: "Watch the drop folder", EnvVar: "FILLER_SOURCE_FOLDER_ENABLED", Group: GroupFiller,
			Kind: KindBool, Default: true,
			Doc: "Scan the drop-folder for clips. Switching it off stops the catalog sync; clips already in the catalog stay.",
		},
		{
			Key: "filler.ai_tagging", Label: "Tag clips with AI", EnvVar: "FILLER_AI_TAGGING", Group: GroupFiller,
			Kind: KindBool, Default: false,
			Doc: "Classify untagged commercials against the grounded era, audience, brand, and taxonomy vocabulary.",
		},
		// Auto-filing (§10 V38). ⚠ These two keys were REMOVED from §15 in V35's review as
		// declared-but-unconsumed — §15's own rule is that a setting not in the registry does not
		// exist, and the corollary is that one nothing READS does not either. They return here
		// with their consumer in the same PR: the tag job reads them, and a test proves a clip
		// below the threshold reaches Incoming instead of the catalog.
		//
		// ⚠ **ON by default** (maintainer, 2026-08-02), which means an existing install begins
		// filing clips without a human on its first tagging run after upgrade. What makes that
		// safe is not this number but §10's grounding CAP: an ungrounded era scores below every
		// reachable threshold, so the fabrication class stays with a person regardless of what
		// this is set to. `filler.Score` owns that property and a sabotage test pins it.
		{
			Key: "filler.autofile.enabled", Label: "File confident clips automatically", EnvVar: "FILLER_AUTOFILE_ENABLED", Group: GroupFiller,
			Kind: KindBool, Default: true,
			Doc: "File confidently-tagged clips into the catalog automatically. Anything Loomarr is unsure about waits for you under Filler → Incoming.",
		},
		{
			// On-demand transcription (§10 V44). ⚠ OFF by default: it shares the whisper seam with
			// the language gate on the local path (~341s per clip under QEMU), and spends provider
			// credit on the hosted path, so it is a deliberate opt-in either way. The job is
			// SELECTIVE even when on — it only transcribes clips whose source described them thinly,
			// never the whole catalog.
			Key: "filler.transcribe.enabled", Label: "Transcribe unclear clips", EnvVar: "FILLER_TRANSCRIBE_ENABLED", Group: GroupFiller,
			Kind: KindBool, Default: false, Advanced: true,
			Doc: "Listen to clips whose source told us almost nothing and write down what they say, so Loomarr can work out the brand and era. Uses the transcription provider selected below.",
		},
		{
			Key: "filler.transcribe.provider", Label: "Transcription service", EnvVar: "FILLER_TRANSCRIBE_PROVIDER", Group: GroupFiller,
			Kind: KindEnum, Enum: []EnumOption{
				opt("whisper", "Local (whisper)"), opt("hosted", "Hosted AI service"),
			},
			Default: "whisper", Advanced: true,
			Doc: "Where timed transcripts come from: the bundled local Whisper engine, or the hosted AI provider configured under AI. OpenRouter supports this with the same key used for text and vision.",
		},
		{
			Key: "filler.transcribe.model", Label: "Transcription model", EnvVar: "FILLER_TRANSCRIBE_MODEL", Group: GroupFiller,
			Kind: KindString, Default: "openai/whisper-large-v3", Advanced: true,
			Doc: "Speech-to-text model used for hosted transcription. This is separate from the chat and vision models because it must return timed transcript segments.",
		},
		{
			// Vision tagging (§10 V44). ⚠ OFF by default AND gated on a vision-capable LLM: the
			// hosted path spends multimodal tokens per clip and sends frames off the box, the local
			// path needs an Ollama vision model. Off, or with no vision model, the job is inert.
			Key: "filler.vision.enabled", Label: "Inspect unclear clips with vision AI", EnvVar: "FILLER_VISION_ENABLED", Group: GroupFiller,
			Kind: KindBool, Default: false, Advanced: true,
			Doc: "Look at a few frames of clips Loomarr still can't identify — reading on-screen logos and text — to work out the brand, even for clips with no speech. Needs a vision-capable AI model.",
		},
		{
			// ⚠ Its OWN model knob, exactly like filler.language_model — and the live test that
			// added it found why: the tagging model (`llm.model`) is often a TEXT model with no
			// vision path (qwen3 in dev), while the box has a separate vision-capable one (gemma-4).
			// Tying vision to `llm.model` would force an operator to switch their whole LLM to a
			// vision model just to tag clips. Empty ⇒ fall back to `llm.model`, so an install whose
			// main model already sees images needs no second setting.
			Key: "filler.vision.model", Label: "Vision model", EnvVar: "FILLER_VISION_MODEL", Group: GroupFiller,
			Kind: KindString, Default: "", Advanced: true,
			Doc: "Which AI model reads clip frames (must be vision-capable). Leave empty to reuse your main model — set it only when that model can't see images.",
		},
		{
			// ⚠ **The model knob above was only half a separation, and the half it was missing is
			// the one that fails silently.** `filler.vision.model` let an operator name a vision
			// model, but the provider was still built from `llm.provider`/`llm.url`/`llm.api_key`
			// — so naming a LOCAL model while the main LLM was hosted sent an Ollama tag to the
			// hosted endpoint. Measured: `llava:7b` → `https://openrouter.ai/api/v1` → 401 on every
			// segment, which `ground` reports as zero looks and the gate refuses as "a segment
			// could not be classified". The model name is useless without the host that serves it.
			//
			// Empty ⇒ inherit the main provider exactly as before, so no existing install changes.
			// ⚠ `inherit` is a REAL value, not an empty string: the registry invariant requires
			// every enum option to carry a value and a label, and an explicit word reads better in
			// the picker than a blank row. The resolver treats "" the same way, so an env var set
			// to empty means inherit rather than "no provider".
			Key: "filler.vision.provider", Label: "Vision service", EnvVar: "FILLER_VISION_PROVIDER", Group: GroupFiller,
			Kind: KindEnum, Default: "inherit", Advanced: true,
			Enum: []EnumOption{
				opt("inherit", "Same as your main AI"),
				opt("ollama", "Ollama"),
				opt("openai", "OpenAI-compatible"),
			},
			Doc: "Which service reads clip frames. Leave as “same as your main AI” unless your vision model lives somewhere else — a local Ollama, say, while your main AI is a hosted service.",
		},
		{
			// Empty + `ollama` resolves to the conventional local host, the same rule `ollamaBase`
			// already applies to probes and pulls — so the common case (hosted text, local vision)
			// needs the provider knob alone.
			Key: "filler.vision.url", Label: "Vision service address", EnvVar: "FILLER_VISION_URL", Group: GroupFiller,
			Kind: KindURL, Default: "", Advanced: true,
			Doc:      "Where that service lives. Leave empty for a local Ollama on this machine.",
			ShowWhen: map[string][]string{"filler.vision.provider": {"ollama", "openai"}},
		},
		{
			// ⚠ **Never falls back to `llm.api_key`, and that is the point rather than an
			// omission.** Declaring a separate vision service means declaring its own credentials:
			// inheriting would send the operator's hosted key to whatever host they just named,
			// including `localhost`. A local Ollama needs no key, so the common case leaves this
			// empty and nothing is sent.
			Key: "filler.vision.api_key", Label: "Vision service API key", EnvVar: "FILLER_VISION_API_KEY", Group: GroupFiller,
			Kind: KindSecret, Default: "", Advanced: true,
			Doc:      "API key for that service, if it needs one. Your main AI's key is never reused here. Never shown again after saving.",
			ShowWhen: map[string][]string{"filler.vision.provider": {"openai"}},
		},
		{
			// ⚠ Max is filler.MaxAutoFileConfidence (95), and the ceiling is load-bearing rather
			// than cosmetic: an ungrounded era is capped BELOW it, so no settable value can admit
			// a fabricated era. Raising this bound without raising that cap breaks the guarantee.
			Key: "filler.autofile.min_confidence", Label: "Confidence required to file", EnvVar: "FILLER_AUTOFILE_MIN_CONFIDENCE", Group: GroupFiller,
			Kind: KindInt, Default: 85, Validate: autoFileConfidenceRange,
			Doc:      "How sure Loomarr must be before filing a clip without asking (50–95). Lower files more automatically; higher sends more to Incoming for you to check.",
			ShowWhen: map[string][]string{"filler.autofile.enabled": {"true"}},
		},
		{
			// On-file loudness normalisation (§10 V42, wired for real in V51b).
			//
			// ⚠ **This key spent three phases gating a function nothing called, and the note that
			// used to sit here claimed the opposite.** It said the setting "lands with its
			// consumer (`filler.NormalizeInPlace`, called from the auto-file step)" (retired-ok) — invoking
			// §15's own rule that a setting nothing READS does not exist. The consumer was
			// deleted or never wired; V51b found the function with no production caller at all,
			// so the toggle has been inert since it shipped. It is now read by the TRANSCODE rung,
			// which applies the loudness filter in the pass that is already re-encoding the clip.
			//
			// The lesson is the one §15's rule was written for, arriving from the other side: a
			// COMMENT asserting a consumer exists is not the same as a consumer existing, and
			// nothing failed when it stopped being true.
			//
			// ⚠ DEFAULT OFF, and the default is the safety property rather than a preference.
			// This REWRITES the operator's file in FILLER_DIR: the original is unrecoverable.
			// V40 chose playout-only normalisation for exactly that reason and it remains the
			// default path; this is an explicit opt-in for operators who want the correction
			// baked in.
			//
			// ⚠ There is deliberately NO separate target here. The pass reuses
			// `filler.target_lufs` (−23), because two targets in one system means a clip
			// normalised on file gets corrected again at playout toward a different number —
			// double processing, and quieter than either setting asks for.
			//
			// ⚠ Idempotency is NOT optional for this one. A re-scan cannot tell by looking that
			// a file was already normalised, so without the sidecar's `normalizedLufs` marker
			// every pass would normalise an already-normalised file and walk the loudness down
			// run after run. The transcode rung writes that marker after the encode lands, and
			// its own `mezzanine` marker stops the re-encode independently.
			Key: "filler.autofile.normalize_loudness", Label: "Rewrite files to normalize loudness", EnvVar: "FILLER_AUTOFILE_NORMALIZE_LOUDNESS",
			Group: GroupFiller, Kind: KindBool, Default: false,
			Doc:      "Rewrite each clip's audio to a consistent loudness as it is filed. ⚠ This changes the file itself and cannot be undone — the original is replaced. Leave off to have Loomarr even out the volume during playback instead, which changes nothing on disk.",
			ShowWhen: map[string][]string{"filler.autofile.enabled": {"true"}},
		},

		// Automatic compilation splitting (§10 V43). Detection ran only on a button press and
		// its result always required a human, which made compilations the most manual part of a
		// system whose claim is that it maintains itself — while the tagger beside it files
		// clips unattended above a threshold.
		{
			// (`filler.split.every` was retired here in V51b. Splitting is no longer a sweep with
			// its own cadence — it is a rung every long recording reaches as it is ingested, so
			// "how often do we go looking" stopped being a question with an answer. Detection is
			// still bounded, by `filler.pipeline.max_splits` per pass.)
			//
			// ⚠ **ON by default as of V51b, reversing the note this comment used to carry.** It
			// said: off, because cutting is destructive in a way tagging is not — a mis-cut clip
			// plays HALF AN ADVERT and the source is consumed either way. That reasoning was
			// sound and the risk has not changed; what changed is the evidence. The gate is
			// strict — after known duplicates and short fragments are removed, the remaining reel
			// qualifies as a whole or none of it does; an ungrounded era disqualifies at every
			// threshold, and any segment the detector admits it could not resolve sends the
			// remaining reel to a human — and the measured failure mode is the gate REFUSING good
			// reels, not admitting bad ones. Off by default meant every compilation waited for a
			// click that the design says should be unnecessary.
			Key: "filler.autosplit.enabled", Label: "Accept confident cuts automatically", EnvVar: "FILLER_AUTOSPLIT_ENABLED", Group: GroupFiller,
			Kind: KindBool, Default: true,
			Doc: "Accept the cuts automatically when Loomarr is confident about every one of them. Anything less certain still waits for you under Filler → Incoming.",
		},
		{
			// ⚠ A SEPARATE number from `filler.autofile.min_confidence`, and the separation is
			// the point: one dial would force the stricter of two different failure modes to
			// govern both. Bounded by the same range for the same reason — an ungrounded era is
			// capped below 95, so no settable value can auto-confirm a fabricated one.
			Key: "filler.autosplit.min_confidence", Label: "Confidence required to accept cuts", EnvVar: "FILLER_AUTOSPLIT_MIN_CONFIDENCE",
			Group: GroupFiller, Kind: KindInt, Default: 85, Validate: autoFileConfidenceRange,
			Doc:      "How sure Loomarr must be about every advert it found inside a recording before cutting it up without asking (50–95).",
			ShowWhen: map[string][]string{"filler.autosplit.enabled": {"true"}},
		},
		{
			// ⚠ ONE key doing two jobs on purpose. It selects which clips the split job even
			// looks at (longer than this ⇒ a compilation worth detecting) AND it is the ceiling
			// every segment must clear to auto-confirm. Two keys could disagree — a clip the job
			// considers too long to be an advert must not then auto-confirm as one.
			Key: "filler.autosplit.max_duration", Label: "Longest expected single clip", EnvVar: "FILLER_AUTOSPLIT_MAX_DURATION",
			Group: GroupFiller, Kind: KindDuration, Default: "120s",
			Doc: "The longest a single advert is expected to be. Recordings longer than this are treated as compilations worth splitting, and any piece longer than this is one Loomarr will ask you about.",
		},
		{
			Key: "filler.structure_window_authority_path", Label: "Long-reel authority file", EnvVar: "FILLER_STRUCTURE_WINDOW_AUTHORITY_PATH", Group: GroupFiller,
			Kind: KindString, Presentation: PresentationPath, Apply: ApplyRestart, Default: "", Validate: storagePath(true), Advanced: true,
			Doc:      "Optional absolute path to a separately reviewed long-reel materialization authority. Empty or invalid evidence enables no certified slice; a valid authority may create held children but cannot make them airable.",
			ShowWhen: map[string][]string{"filler.autosplit.enabled": {"true"}},
		},
		{
			Key: "filler.structure_window_deployment_path", Label: "Long-reel deployment file", EnvVar: "FILLER_STRUCTURE_WINDOW_DEPLOYMENT_PATH", Group: GroupFiller,
			Kind: KindString, Presentation: PresentationPath, Apply: ApplyRestart, Default: "", Validate: storagePath(true), Advanced: true,
			Doc:      "Optional absolute path to the reviewed authority's OpenRouter route and spend deployment. Empty, invalid, or mismatched evidence performs no structure inference and enables no certified slice.",
			ShowWhen: map[string][]string{"filler.autosplit.enabled": {"true"}},
		},
		// The ingest pipeline's per-run budget (§10 V51b). Every one of these bounds ONE PASS, not
		// the catalog: a backlog drains over cycles, which is the property the per-job batch
		// constants they replace (LanguageBatch 25, TranscribeBatch 10, VisionBatch 5,
		// defaultSplitsPerRun 3) were chosen to defend. The numbers are carried forward unchanged.
		//
		// ⚠ **Zero means NONE, and that is a distinct state from the default.** It is the only way
		// an operator can say "never do this kind of work on this box" — the same three-state
		// encoding `filler.fetch.every` uses, and the reason these are integers rather than
		// booleans-plus-a-rate.
		{
			Key: "filler.pipeline.max_clips", Label: "Clips prepared per pass", EnvVar: "FILLER_PIPELINE_MAX_CLIPS", Group: GroupFiller,
			Kind: KindInt, Default: 25, Advanced: true,
			Doc: "How many clips Loomarr advances through preparation in one pass. A large import drains over several passes rather than occupying the machine in one.",
		},
		{
			// ⚠ THREE, the tightest budget here, because a transcode competes with playout for
			// the GPU and this is what makes the existing catalog backfill converge over a day
			// instead of pinning the box. Zero switches re-encoding off entirely — an escape
			// hatch that matters, because this is the one rung that rewrites the operator's file.
			Key: "filler.transcode.max_per_run", Label: "Clips re-encoded per pass", EnvVar: "FILLER_TRANSCODE_MAX_PER_RUN", Group: GroupFiller,
			Kind: KindInt, Default: 3, Advanced: true,
			Doc: "How many clips Loomarr re-encodes to its standard format in one pass. Set to 0 to never re-encode — clips then play in whatever format they arrived in.",
		},
		{
			Key: "filler.pipeline.max_whisper", Label: "Clips transcribed per pass", EnvVar: "FILLER_PIPELINE_MAX_WHISPER", Group: GroupFiller,
			Kind: KindInt, Default: 10, Advanced: true,
			Doc: "How many clips Loomarr listens to in one pass, for language and transcription together. Listening is slow — minutes per clip on some machines — so this keeps a pass from running away.",
		},
		{
			Key: "filler.pipeline.max_vision", Label: "Clips inspected visually per pass", EnvVar: "FILLER_PIPELINE_MAX_VISION", Group: GroupFiller,
			Kind: KindInt, Default: 5, Advanced: true,
			Doc: "How many clips Loomarr looks at with a vision model in one pass. The smallest budget, because on a hosted model each one is a charge.",
		},
		{
			// ⚠ Counted in SEGMENTS, not clips — the only budget here that is, and §10 V51g is
			// why it must exist. A rung's cost may not scale unboundedly with a clip's content:
			// the live catalog holds proposals of 235, 222, 142 and 133 segments, so an unbounded
			// per-segment pass is exactly the shape that burned 377s against a 120s budget.
			//
			// A reel with more segments than this continues on the next pass. The proposal persists
			// Looked per segment, so this is a resource budget rather than a review threshold.
			Key: "filler.pipeline.max_split_vision", Label: "Compilation segments inspected per pass", EnvVar: "FILLER_PIPELINE_MAX_SPLIT_VISION", Group: GroupFiller,
			Kind: KindInt, Default: 60, Advanced: true,
			Doc: "How many segments of one recording Loomarr looks at in a single pass. A longer recording is judged over several passes rather than made to wait for you — this bounds how much looking happens at once, not how big a recording can be.",
		},
		{
			Key: "filler.pipeline.max_splits", Label: "Compilations split per pass", EnvVar: "FILLER_PIPELINE_MAX_SPLITS", Group: GroupFiller,
			Kind: KindInt, Default: 3, Advanced: true,
			Doc: "How many long recordings Loomarr looks inside in one pass. Finding the adverts in one recording takes minutes.",
		},
		{
			// ⚠ **ON by default, and it is the only reject an operator can turn off** — because
			// "we could not identify it" is not the same claim as "it is not a commercial". A
			// wordless station ident is exactly that case, and §10 calls a silent advert some of
			// the best filler there is.
			//
			// ⚠ It is also why the rejected list is NOT optional: an operator has to be able to
			// see what this caught and put it back. The reject is recorded with its reason and is
			// reversible in one click; a silent tombstone would not be acceptable at this default.
			//
			// The guard that makes it safe lives in the score rung: a clip is only "unidentified"
			// if something actually LOOKED and found nothing. A clip the tagger never reached —
			// an install with no LLM, a catalog imported before tagging existed — falls through
			// to review, never to a reject.
			Key: "filler.reject.unidentified", Label: "Set unidentified clips aside", EnvVar: "FILLER_REJECT_UNIDENTIFIED", Group: GroupFiller,
			Kind: KindBool, Default: true,
			Doc: "Set aside clips that nothing could identify — no era, brand, speech or on-screen text. They're listed under Filler → Incoming with a reason, and you can put any of them back.",
		},
		// Auto-fetch and its limits (§10 V38b). A registered source is polled on a schedule, which
		// supersedes §15's "there is no unattended crawler" — the superseded rule's concern
		// survives as these bounds rather than as a prohibition.
		//
		// ⚠ Every one of them fails toward doing LESS. An operator who never opens this page gets
		// a trickle they can live with; the failure mode being designed against is "add a source,
		// wake up to 8,000 files".
		{
			Key: "filler.fetch.every", Label: "Check sources every", EnvVar: "FILLER_FETCH_EVERY", Group: GroupFiller,
			Kind: KindDuration, Default: "6h",
			Doc: "How often Loomarr checks your sources for new clips. Set to 0 to stop fetching automatically — you can still queue clips yourself.",
		},
		{
			Key: "filler.fetch.max_per_run", Label: "Downloads per source check", EnvVar: "FILLER_FETCH_MAX_PER_RUN", Group: GroupFiller,
			Kind: KindInt, Default: 10, Advanced: true, Validate: positiveLimit,
			Doc: "How many clips one source may download each time it's checked. Keeps a big collection trickling in instead of arriving all at once.",
		},
		{
			// ⚠ Bounds the UNATTENDED path only. An admin queueing a clip or approving a pull is
			// a deliberate act and is not stopped by this — a ceiling on what happens while
			// nobody is looking is not a ceiling on what someone chooses to do.
			Key: "filler.fetch.max_catalog_clips", Label: "Automatic-download catalog limit", EnvVar: "FILLER_FETCH_MAX_CATALOG_CLIPS", Group: GroupFiller,
			Kind: KindInt, Default: 2000, Advanced: true, Validate: positiveLimit,
			Doc: "Stop fetching automatically once your catalog reaches this many clips. You can still add more by hand.",
		},
		{
			Key: "filler.fetch.max_disk_gb", Label: "Automatic-download storage limit (GB)", EnvVar: "FILLER_FETCH_MAX_DISK_GB", Group: GroupFiller,
			Kind: KindInt, Default: 20, Advanced: true, Validate: positiveLimit,
			Doc: "Stop fetching automatically once the filler folder reaches this size in GB.",
		},
		{
			Key: "filler.breaks_per_hour", Label: "Breaks per program hour", EnvVar: "FILLER_BREAKS_PER_HOUR", Group: GroupFiller,
			Kind: KindInt, Default: 4, Validate: nonNegativeWholeNumber,
			Doc: "Default commercial-break frequency for channels that follow it. Set 0 to disable breaks by default; each channel can choose its own frequency.",
		},
		{
			Key: "filler.break_duration", Label: "Length of each break", EnvVar: "FILLER_BREAK_DURATION", Group: GroupFiller,
			Kind: KindDuration, Default: "5m", Validate: breakDuration,
			Doc: "How long each commercial break lasts by default. Channels can choose their own length. Use breaks per program hour to turn breaks off.",
		},
		{
			Key: "filler.pod_max", Label: "Clips per break", EnvVar: "FILLER_POD_MAX", Group: GroupFiller,
			Kind: KindInt, Default: 4, Validate: positiveWholeNumber,
			Doc: "Preferred clip count per break. Loomarr automatically exceeds it when shorter clips need more slots to fill the requested break length.",
		},
		{
			Key: "filler.cooldown_seconds", Label: "Repeat cooldown (seconds)", EnvVar: "FILLER_COOLDOWN_SECONDS", Group: GroupFiller,
			Kind: KindInt, Default: 1800, Advanced: true, Validate: nonNegativeWholeNumber,
			Doc: "Preferred seconds before the same commercial airs again. Loomarr relaxes this predictably when a small pool would otherwise leave a break empty.",
		},
		// ⚠ Default 0 = OFF, and that is the whole safety property of this knob (V17c).
		// `00014_clips_quality` shipped with quality as display-only and warned that a
		// well-meaning "prefer HD" would quietly starve the era-accurate 4:3 commercials the
		// feature exists to play. That warning still holds — which is why an install that sets
		// nothing behaves exactly as it did before this key existed, pinned by a test.
		//
		// Advanced: an operator who does not know what 240p looks like in a break should never
		// meet this, and one who does will go looking.
		{
			Key: "filler.min_quality", Label: "Minimum picture height (px)", EnvVar: "FILLER_MIN_QUALITY", Group: GroupFiller,
			Kind: KindInt, Default: 0, Advanced: true,
			Doc: "Minimum clip height in pixels for a commercial to be eligible (480 excludes 240p rips). 0 disables the floor, which is the default — era accuracy beats resolution.",
		},
		{
			Key: "filler.weight", Label: "Selection weight", EnvVar: "FILLER_WEIGHT", Group: GroupFiller,
			Kind: KindInt, Default: 1, Advanced: true,
			Doc: "How heavily this commercial set is drawn from, relative to others.",
		},
		{
			// ⚠ A REJECT, not a filter, and its default is ON — unlike filler.min_quality above,
			// which is opt-in eligibility over clips that already exist. A file shorter than this
			// never becomes a catalog row at all (§10 V40).
			//
			// It exists because `DurationMs <= 0` was the only guard, and a 2.9KB / 33ms truncated
			// download passed it and sat filed-and-airable in the dev catalog.
			Key: "filler.min_duration", Label: "Reject files shorter than", EnvVar: "FILLER_MIN_DURATION", Group: GroupFiller,
			Kind: KindDuration, Default: "10s", Advanced: true,
			Doc: "Clips shorter than this are rejected on sight and never enter the catalog — a truncated download is not a short commercial. Set to 0s to accept anything with a readable duration.",
		},
		{
			// ⚠ **The only setting in Loomarr that deletes an operator's media**, which is why its
			// doc says so in the operator's own words rather than in ours. Everything else here
			// tombstones: "remove from catalog" keeps the file, disabling a source keeps its clips.
			//
			// It exists because partial confirm (§10 V54) leaves a residue BY DESIGN — every reel
			// files its confident cuts and keeps the doubtful ones back, so small proposals
			// accumulate and each one pins a 1–2 GB recording. Without an expiry the feature that
			// shrinks the operator's work grows their storage instead.
			//
			// ⚠ 0s is OFF, and that is the three-state encoding the rest of §10 uses: an operator
			// who has not chosen an expiry has not agreed to have recordings deleted. A reel that
			// produced NO clips is never eligible at any window — it is the only copy of that
			// content, and reaping it would destroy material Loomarr never managed to use.
			Key: "filler.split.review_window", Label: "Keep unreviewed cuts for", EnvVar: "FILLER_SPLIT_REVIEW_WINDOW", Group: GroupFiller,
			Kind: KindDuration, Default: "720h", Advanced: true,
			Doc: "How long cuts you haven't reviewed wait before Loomarr gives up on them. When the time is up it drops the leftover cuts and DELETES the original recording to reclaim the space — but only for recordings that already produced clips, so nothing is lost that was never used. The clips themselves are never touched. Set to 0s to keep everything forever.",
		},
		{
			// ⚠ **ELIGIBILITY, not a reject — the pair to `filler.min_duration` above, and the
			// distinction is which side of the catalog boundary they sit on.** `min_duration`
			// refuses a file entry; these two decide whether a clip already IN the catalog may
			// fill a break. A clip outside them stays filed, searchable and pinnable; it simply
			// is not drawn automatically.
			//
			// ⚠ **`Policy.MinClipMs`/`MaxClipMs` existed for phases with no way to set them.**
			// They were assigned in tests and nowhere else — no key, no env var, no policy field —
			// so `durationEligible` always returned true and `PoolReport.Eligible`, which
			// `coverage.go` headlines as "the number that surprises operators", was arithmetically
			// identical to `Commercials` on every install. The pool strip showed one number twice
			// and called the pair a diagnosis. Wiring them is what makes that strip mean something.
			//
			// Both default to 0 = OFF, so no existing install changes behaviour on upgrade.
			Key: "filler.min_clip_duration", Label: "Shortest clip used automatically", EnvVar: "FILLER_MIN_CLIP_DURATION", Group: GroupFiller,
			Kind: KindDuration, Default: "0s", Advanced: true,
			Doc: "Commercials shorter than this are not drawn into breaks automatically. 0s disables the floor. Unlike the minimum clip length above, this never rejects a clip — it stays in the catalog and can still be pinned to a channel.",
		},
		{
			Key: "filler.max_clip_duration", Label: "Longest clip used automatically", EnvVar: "FILLER_MAX_CLIP_DURATION", Group: GroupFiller,
			Kind: KindDuration, Default: "0s", Advanced: true,
			Doc: "Commercials longer than this are not drawn into breaks automatically — the guard against a three-minute infomercial filling a thirty-second gap. 0s disables the ceiling. Never rejects a clip; it stays in the catalog and can still be pinned.",
		},
		{
			// ⚠ Applied in the PLAYOUT chain, never written back to the file. The drop-folder
			// holds the operator's own files; in-place normalisation is destructive, unrepeatable,
			// and a re-scan cannot tell it already happened (§10 V40, §9.1).
			//
			// Measured spread across real fetched clips: -21.8 to -32.6 LUFS, ~11 dB of
			// clip-to-clip jump. -23 is the broadcast target.
			Key: "filler.target_lufs", Label: "Playback loudness target (LUFS)", EnvVar: "FILLER_TARGET_LUFS", Group: GroupFiller,
			Kind: KindString, Default: "-23", Advanced: true,
			Doc: "Loudness every filler clip is normalised to at playout, in LUFS (-23 is the broadcast standard). Empty disables normalisation and clips play at whatever level they were recorded.",
		},
		{
			// ⚠ A clip with NO speech is always kept — a wordless visual spot has no language, and
			// those are often the best filler. Only confident non-target speech rejects (§10 V40).
			Key: "filler.language", Label: "Expected spoken language", EnvVar: "FILLER_LANGUAGE", Group: GroupFiller,
			Kind: KindString, Default: "en", Advanced: true,
			Doc: "The language filler is expected to be in. A clip whose speech is confidently something else is rejected; a clip with no speech at all is always kept. Empty turns the language check off.",
		},
		{
			// Mirrors `llm.provider`'s local-vs-hosted split (§8.1), and for the same reason:
			// local is free and offline, hosted costs money and leaves the box.
			//
			// ⚠ **NOT Ollama.** "We already run a local LLM so we do not need whisper" is the
			// reasonable inference and it is wrong — Ollama has no audio input path at all
			// (probed 2026-08-03: completion/vision/tools/thinking, no `audio`; vision is images).
			// Local audio means whisper; hosted is what Ollama cannot be.
			//
			// ⚠ whisper is ~3s per clip natively but **~341s under QEMU**, which is why the job
			// runs in the background and why an arm64 install effectively needs the hosted path.
			Key: "filler.language_provider", Label: "Language detection service", EnvVar: "FILLER_LANGUAGE_PROVIDER", Group: GroupFiller,
			Kind: KindEnum, Enum: []EnumOption{
				opt("whisper", "Local (whisper)"), opt("hosted", "Hosted AI service"),
			},
			Default: "whisper", Advanced: true,
			Doc: "What works out a clip's language: the built-in local engine (free and offline, but slow on low-power hardware), or a hosted AI service (fast anywhere, costs a fraction of a cent per clip and sends a few seconds of audio off this machine).",
		},

		// --- Filler ingest (§10, §15) ---
		// ⚠ The vendored binaries ship in the SINGLE image (§16). This block used to be
		// labelled "loomarr:filler image variant only" — that variant no longer exists, so
		// these paths are overrides for an unusual layout, not an opt-in.
		// The two tool paths are what the `ingest` feature gate probes. They are
		// settings rather than hardcoded so an operator can point at a NEWER yt-dlp
		// than the image ships — yt-dlp releases fixes far faster than we cut images,
		// and a stale one silently stops extracting from YouTube.
		{
			Key: "ingest.ytdlp_path", Label: "yt-dlp executable", EnvVar: "INGEST_YTDLP_PATH", Group: GroupFiller,
			Kind: KindString, Presentation: PresentationPath, Default: "", Advanced: true,
			Doc: "Where the yt-dlp program lives. The Loomarr image sets this; empty means clip downloading is off.",
		},
		{
			Key: "ingest.ffmpeg_path", Label: "ffmpeg executable", EnvVar: "INGEST_FFMPEG_PATH", Group: GroupFiller,
			Kind: KindString, Presentation: PresentationPath, Default: "", Advanced: true,
			Doc: "Where the ffmpeg program lives (yt-dlp needs it to combine video and audio).",
		},
		{
			Key: "ingest.timeout", Label: "Download timeout", EnvVar: "INGEST_TIMEOUT", Group: GroupFiller,
			Kind: KindDuration, Default: "30m", Advanced: true,
			Doc: "How long one download may run before it's stopped, so a stuck fetch can't block others.",
		},
		// Compilation splitting (§10, V34). Empty/unrunnable ⇒ the transcript-rescue
		// step is unavailable and over-long segments surface as UNSPLITTABLE in the
		// review rather than being guessed at — coarse splitting needs only ffmpeg.
		{
			Key: "ingest.whisper_path", Label: "whisper executable", EnvVar: "INGEST_WHISPER_PATH", Group: GroupFiller,
			Kind: KindString, Presentation: PresentationPath, Default: "", Advanced: true,
			Doc: "Where the whisper-cli program lives. The image sets this; empty means over-long compilation segments can't be transcribed for hidden ad breaks.",
		},
		{
			Key: "ingest.whisper_model", Label: "Transcription model file", EnvVar: "INGEST_WHISPER_MODEL", Group: GroupFiller,
			Kind: KindString, Presentation: PresentationPath, Default: "", Advanced: true,
			Doc: "The whisper model file whisper-cli transcribes with. Size is a correctness property, not a quality preference — too small drops audio and the boundary detector then invents breaks.",
		},
		{
			// ⚠ A SECOND model, and the reason is the `.en` suffix on the one above. An
			// English-only whisper build does NOT identify languages — it assumes English and
			// transcribes accordingly, so asked about a Spanish advert it answers "en" and the
			// language gate silently never rejects anything (§10 V40).
			//
			// `tiny` (multilingual, ~74MB) is adequate here in a way it was not for splitting:
			// language ID is CLASSIFICATION over the first seconds, not transcription, so the
			// "does it drop audible speech" gate that ruled out tiny.en never applies.
			//
			// Empty ⇒ local language detection is unavailable and the gate stays inert; the image
			// sets it, a source build does not.
			Key: "filler.language_model", Label: "Language detection model file", EnvVar: "FILLER_LANGUAGE_MODEL", Group: GroupFiller,
			Kind: KindString, Presentation: PresentationPath, Default: "", Advanced: true,
			Doc: "The model file used to work out what language a clip is in. Must be a MULTILINGUAL whisper model — an English-only one reports every clip as English, so the check would never reject anything. The image ships one; leave empty to turn local detection off.",
		},
		// --- Users & security (§15, Phase 9) ---
		{
			Key: "session.ttl", Label: "Sign-in lifetime", EnvVar: "SESSION_TTL", Group: GroupUsersSecurity,
			Kind: KindDuration, Default: "720h",
			Doc: "How long you stay signed in before needing to log in again.",
		},
		{
			Key: "cookie.secure", Label: "Secure cookies", EnvVar: "COOKIE_SECURE", Group: GroupUsersSecurity,
			Kind: KindEnum, Enum: []EnumOption{opt("auto", "Auto (match the request)"), opt("always", "Always"), opt("never", "Never (local dev only)")}, Default: "auto", Advanced: true,
			Doc: "When to mark the login cookie secure: auto (match the request), always, or never (for local dev only).",
		},
		{
			Key: "security.trust_proxy", Label: "Trust reverse proxy", EnvVar: "TRUST_PROXY", Group: GroupUsersSecurity,
			Kind: KindBool, Default: "false", Advanced: true,
			Doc: "Trust the X-Forwarded-For and X-Forwarded-Proto headers. Turn this on only if a reverse proxy sits in front of Loomarr and sets them. Off by default so a direct client can't forge them to bypass the login rate limit or downgrade its cookie.",
		},

		// --- SSO: a third CREDENTIAL path, never a provisioning one (§11, D-F, V8) ---
		//
		// ⚠ There is deliberately NO `auth.sso.auto_create` and NO `auth.sso.admin_group`,
		// though the v2 mock draws both. Auto-create is lazy self-provision, which is exactly
		// what §11's allowlist exists to prevent; group-derived roles would move a Loomarr
		// decision to someone else's directory. Adding either key later is a §11 conversation,
		// not a settings change.
		{
			Key: "auth.sso.enabled", Label: "Enable single sign-on", EnvVar: "AUTH_SSO_ENABLED", Group: GroupSSO,
			Kind: KindBool, Default: "false",
			Doc: "Let people sign in with your identity provider. They still need an account here — signing in with your provider does not create one.",
		},
		{
			Key: "auth.sso.issuer", Label: "SSO issuer URL", EnvVar: "AUTH_SSO_ISSUER", Group: GroupSSO,
			Kind: KindURL, Default: "",
			Doc:      "Your identity provider's address, e.g. https://auth.example.home. Loomarr reads its published configuration from there.",
			ShowWhen: map[string][]string{"auth.sso.enabled": {"true"}},
		},
		{
			Key: "auth.sso.client_id", Label: "SSO client ID", EnvVar: "AUTH_SSO_CLIENT_ID", Group: GroupSSO,
			Kind: KindString, Default: "",
			Doc:      "The client ID your provider issued for Loomarr.",
			ShowWhen: map[string][]string{"auth.sso.enabled": {"true"}},
		},
		{
			Key: "auth.sso.client_secret", Label: "SSO client secret", EnvVar: "AUTH_SSO_CLIENT_SECRET", Group: GroupSSO,
			Kind: KindSecret, Default: "",
			Doc:      "The client secret your provider issued for Loomarr.",
			ShowWhen: map[string][]string{"auth.sso.enabled": {"true"}},
		},

		// --- Advanced: TTLs, retention, workers, event webhook (§15) ---
		{
			Key: "request.ttl", EnvVar: "REQUEST_TTL", Group: GroupAdvanced,
			Kind: KindDuration, Default: "48h",
			Doc: "How long Loomarr keeps trying to request a title before giving up.",
		},
		{
			Key: "downloading.ttl", EnvVar: "DOWNLOADING_TTL", Group: GroupAdvanced,
			Kind: KindDuration, Default: "12h",
			Doc: "How long a downloading title waits to finish before Loomarr gives up on it.",
		},
		// The background-job scheduler's per-job CRON schedules (§18.1). Sonarr/Overseerr-style
		// 6-field seconds-leading cron; edited via the Tasks page's Modify Job modal (presets
		// + an advanced raw-cron field). These OVERRIDE each job's built-in default cron.
		{
			Key: "job.system_health.schedule", EnvVar: "JOB_SYSTEM_HEALTH_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "*/30 * * * * *",
			Doc: "How often Loomarr checks its database and configured connections for Current Health (cron).",
		},
		{
			Key: "job.notification_delivery.schedule", EnvVar: "JOB_NOTIFICATION_DELIVERY_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "*/15 * * * * *",
			Doc: "How often Loomarr claims queued invitation and recovery messages for delivery (cron).",
		},
		{
			Key: "job.reconcile.schedule", EnvVar: "JOB_RECONCILE_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 */5 * * * *",
			Doc: "How often Loomarr checks on in-progress downloads (cron).",
		},
		{
			Key: "job.channel_maintenance.schedule", EnvVar: "JOB_CHANNEL_MAINTENANCE_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 */10 * * * *",
			Doc: "How often Loomarr refreshes series episodes and reconciles live channels with Tunarr (cron).",
		},
		{
			Key: "job.playout_prepare.schedule", EnvVar: "JOB_PLAYOUT_PREPARE_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 * * * * *", Advanced: true,
			Doc: "How often Loomarr looks ahead in accepted channel schedules and prepares the nearest programmes while spare hardware is available.",
		},
		{
			Key: "job.filler_sync.schedule", EnvVar: "JOB_FILLER_SYNC_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 */15 * * * *",
			Doc: "How often Loomarr syncs the filler catalog (cron).",
		},
		{
			// ⚠ Daily and off-peak on purpose. The window this job enforces is measured in WEEKS,
			// so a faster cadence buys nothing and only widens the chance of a pass landing while
			// an operator is mid-review on a reel one hour past its expiry.
			Key: "job.filler_split_sweep.schedule", EnvVar: "JOB_FILLER_SPLIT_SWEEP_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 45 4 * * *",
			Doc: "How often Loomarr checks for split suggestions you never reviewed (cron). What it does when it finds them is set by `filler.split.review_window`.",
		},
		{
			// ⚠ A scheduler Job's `ScheduleKey` MUST be declared here — `Resolve` panics on an
			// undeclared key, so a job registered without its row takes the whole app down at
			// boot. Caught by comprehensive verification, which is the right place, but the coupling is easy
			// to miss when adding a job.
			//
			// Distinct from `filler.fetch.every`, which is the operator-facing "how often" in the
			// Filler group; this is the cron the scheduler actually runs on, in Advanced beside
			// its siblings. The two agree by default (6h).
			Key: "job.filler_fetch.schedule", EnvVar: "JOB_FILLER_FETCH_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 0 */6 * * *",
			Doc: "How often Loomarr checks your filler sources for new clips (cron).",
		},
		{
			// ⚠ **Every scheduled job needs its ScheduleKey declared here** — `Resolve` PANICS on
			// an undeclared key, so a job registered without one takes the whole app down at
			// startup rather than degrading. Caught by the boot test, which is exactly its job.
			//
			// Hourly rather than the 15-minute sync or the 6-hourly fetch, because this is the
			// expensive one: on the local backend a batch of 25 clips is minutes natively and
			// hours under QEMU (~341s per clip). Hourly drains a catalog steadily without a pass
			// overlapping the next.
			// ⚠ **This one key replaced FOUR** (§10 V51b), all now retired-ok:
			// `job.filler_language.schedule`, `job.filler_split.schedule` (retired-ok),
			// `job.filler_transcribe.schedule` and `job.filler_vision.schedule` (retired-ok).
			// Those four existed to keep expensive sweeps off each
			// other's toes by PHASE-OFFSETTING them (:15, :30, :45, :50) — a scheduling discipline
			// that only works while nobody adds a fifth, and which the comments they carried
			// spelled out at length.
			//
			// The pipeline makes the whole arrangement unnecessary rather than tidier: it runs
			// ONE clip at a time through all the rungs in order, so two expensive stages cannot
			// share the runner by construction. There is nothing left to offset.
			//
			// ⚠ Every two minutes, far tighter than the hourly sweeps, and affordable for the
			// reason the sweeps were not: a pass is bounded by the per-run budget rather than by
			// the catalog size, so an idle install costs one indexed query. It has to be tight,
			// because this is now the ONLY thing that advances a freshly downloaded clip.
			//
			// ⚠ Every job needs its schedule key declared or the settings service PANICS at
			// startup on `Resolve` of an undeclared key — so a new job and its key always land in
			// the same change.
			Key: "job.filler_pipeline.schedule", EnvVar: "JOB_FILLER_PIPELINE_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 */2 * * * *",
			Doc: "How often Loomarr advances new filler clips through preparation — measuring, re-encoding, splitting, listening and identifying them (cron).",
		},
		// --- The image service jobs (§22, V52) ---
		//
		// ⚠ Every job needs its schedule key declared or the settings service PANICS at startup on
		// `Resolve` of an undeclared key, so all four land with the jobs themselves.
		{
			// Every minute, because this is what stands between an adopted row and a visible
			// image: `Adopt` deliberately does NOT fetch (a page adopting fifty posters would put
			// TMDB's latency on Loomarr's own page load), so until this runs the surface shows a
			// placeholder. A pass is bounded by the concurrency cap and an indexed work-list
			// query, so an idle install costs one cheap SELECT.
			Key: "job.images_fetch.schedule", EnvVar: "JOB_IMAGES_FETCH_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 * * * * *",
			Doc: "How often Loomarr downloads artwork it has recorded but not yet fetched (cron). Until this runs, those images show as placeholders.",
		},
		{
			// ⚠ Every five minutes: this is the step between a clip's artwork being RENDERED and
			// that artwork being visible through the image service, so a slow cadence reads as the
			// feature not working while an operator watches an import. Cheap to run often — the
			// work list selects only clips with artwork on disk and no image identity yet, so a
			// healthy install pays one indexed query.
			Key: "job.images_adopt_artwork.schedule", EnvVar: "JOB_IMAGES_ADOPT_ARTWORK_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 */5 * * * *",
			Doc: "How often Loomarr copies clip thumbnails and hover previews into the shared image library (cron). Until a clip has been copied over, its older thumbnail is still what you see.",
		},
		{
			// At :20, clear of the filler media cluster (:15/:30/:45/:50) and of the two 04:xx
			// backup/retention jobs. AVIF encoding is CPU-intensive, so this is the image job that
			// genuinely contends for the box — §22 keeps it off request latency so a cold grid of
			// 50 posters cannot launch 50 encodes at once.
			Key: "job.images_avif.schedule", EnvVar: "JOB_IMAGES_AVIF_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 20 * * * *",
			Doc: "How often Loomarr encodes the AVIF copies of images that don't have them yet (cron). AVIF is the smallest format and the most expensive to produce, so it is made in the background; until it exists browsers take WebP.",
		},
		{
			Key: "job.images_maintenance.schedule", EnvVar: "JOB_IMAGES_MAINTENANCE_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 0 5 * * *",
			Doc: "When Loomarr restores recoverable artwork, enforces retention, and cleans up image storage.",
		},
		{
			Key: "job.library_scan.schedule", EnvVar: "JOB_LIBRARY_SCAN_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 */5 * * * *",
			Doc: "How often Loomarr scans the media server for newly-added titles to mark requested items available (cron).",
		},
		{
			Key: "job.library_full_scan.schedule", EnvVar: "JOB_LIBRARY_FULL_SCAN_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 0 3 * * *",
			Doc: "How often Loomarr does a full media-server sweep to catch anything the incremental scan missed (cron).",
		},
		{
			Key: "job.library_scan.lookback", EnvVar: "JOB_LIBRARY_SCAN_LOOKBACK", Group: GroupAdvanced,
			Kind: KindDuration, Default: "1h",
			Doc: "How far back the incremental library scan looks for newly-added titles (should exceed the scan interval).",
		},
		{
			Key: "episodes.max_age", EnvVar: "EPISODES_MAX_AGE", Group: GroupAdvanced,
			Kind: KindDuration, Default: "24h",
			Doc: "How stale a cached series episode list may be before it is re-read from the media server. A missing or expired entry still falls back to a live read, so this bounds freshness, never correctness.",
		},
		{
			Key: "job.arr_queue_poll.schedule", EnvVar: "JOB_ARR_QUEUE_POLL_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 * * * * *",
			Doc: "How often Loomarr polls Sonarr/Radarr download progress (cron; direct requester only).",
		},
		{
			Key: "job.seerr_queue_poll.schedule", EnvVar: "JOB_SEERR_QUEUE_POLL_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 * * * * *",
			Doc: "How often Loomarr polls Seerr for coarse acquisition status (cron; Seerr requester only).",
		},
		{
			Key: "job.workers", EnvVar: "JOB_WORKERS", Group: GroupAdvanced,
			Kind: KindInt, Default: 1,
			Doc: "How many channel suggestions can be worked on at once. One is the appliance-safe default; raise it deliberately for larger or hosted-model deployments.",
		},
		{
			Key: "job.timeout", EnvVar: "JOB_TIMEOUT", Group: GroupAdvanced,
			Kind: KindDuration, Default: "10m",
			Doc: "How long one channel suggestion may run before it's stopped.",
		},
		{
			Key: "jobs.retention", EnvVar: "JOBS_RETENTION", Group: GroupAdvanced,
			Kind: KindDuration, Default: "720h",
			Doc: "How long finished suggestion jobs are kept before they're cleaned up.",
		},
		{
			Key: "proposals.retention", EnvVar: "PROPOSALS_RETENTION", Group: GroupAdvanced,
			Kind: KindDuration, Default: "2160h",
			Doc: "How long suggested lineups are kept before they're cleaned up.",
		},
		{
			Key: "activity.retention", EnvVar: "ACTIVITY_RETENTION", Group: GroupAdvanced,
			Kind: KindDuration, Default: "720h",
			Doc: "How long the Dashboard's recent-activity entries are kept before they're cleaned up.",
		},
		{
			Key: "diagnostics.dir", EnvVar: "DIAGNOSTICS_DIR", Group: GroupAdvanced,
			Kind: KindString, Presentation: PresentationPath, Apply: ApplyRestart,
			Default: "/data/diagnostics", Validate: storagePath(false), Advanced: true,
			Doc: "Where Loomarr keeps bounded ffmpeg and streaming-process output. The directory must be persistent if logs should survive a restart.",
		},
		{
			Key: "diagnostics.retention", EnvVar: "DIAGNOSTICS_RETENTION", Group: GroupAdvanced,
			Kind: KindDuration, Default: "168h", Validate: positiveDuration,
			Doc: "How long Diagnostic events and completed Process runs are kept. Active Process runs are never removed by age.",
		},
		{
			Key: "diagnostics.max_storage_mb", EnvVar: "DIAGNOSTICS_MAX_STORAGE_MB", Group: GroupAdvanced,
			Kind: KindInt, Default: 512, Validate: positiveWholeNumber,
			Doc: "Soft global storage budget in MiB for retained Diagnostic events and bounded Process output. Active Process runs remain protected.",
		},
		{
			Key: "job.housekeeping.schedule", EnvVar: "JOB_HOUSEKEEPING_SCHEDULE", Group: GroupAdvanced,
			Kind: KindCron, Default: "0 30 4 * * *",
			Doc: "When Loomarr removes expired sessions and operational records beyond their retention periods.",
		},
		{
			Key: "setup.completed", EnvVar: "SETUP_COMPLETED", Group: GroupAdvanced,
			Kind: KindBool, Default: false, Advanced: true,
			Doc: "Whether first-run setup is done. Until it is, Loomarr opens the setup wizard.",
		},
	}
}
