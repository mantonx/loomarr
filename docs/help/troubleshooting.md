# Troubleshooting

<!--
EDITOR NOTE: the headings below are an API contract, not a style choice. internal/api/setup.go
emits anchors like "troubleshooting#tunarr-library" on every failed setup check, and
docs/embed_test.go proves each one resolves. Renaming a heading breaks a link the running
server is already sending. Add sections freely; rename with care.
-->

Every red check in the setup wizard links to a section here.

## Android TV cannot find Loomarr

The TV app searches with DNS-SD and Loomarr's LAN discovery fallback. For the supported Docker
Compose deployment, confirm that UDP port `51029` is published and allowed inbound from the trusted
LAN. `SERVER_PUBLIC_URL` must contain the Docker host's LAN address and the HTTP port published by
Traefik—for example, `http://192.168.1.10:8080`—because that is the address discovery returns to the
TV. Client and server must be on the same LAN; guest Wi-Fi and access-point client isolation commonly
block discovery traffic. Manual URL entry remains available when local network policy blocks it.

## Database secret encryption

**Settings:** Security → Database secret encryption

Loomarr encrypts recoverable credentials before writing them to SQLite or PostgreSQL. The database
contains wrapped data-encryption keys; the installation key stays outside it.

- **Installation key does not match database** — restore the `encryption.key` file that belonged to
  this database, or supply the same key through `LOOMARR_ENCRYPTION_KEY` or
  `LOOMARR_ENCRYPTION_KEY_FILE`. Do not delete the database or key file and retry: Loomarr fails
  closed because a different key cannot recover the credentials.
- **Restored database cannot start** — a database-only backup deliberately omits the installation
  key. Restore that key separately. Losing it makes the stored credentials unrecoverable; replace
  the database or re-enter credentials only through a deliberate recovery process.
- **A database was exposed before this upgrade** — rewriting live rows cannot erase plaintext from
  old backups, replicas, filesystem snapshots, SQLite free pages, or WAL history. Rotate upstream
  credentials if those older copies may have been exposed.

A full host or volume copy containing both the database and installation key is outside this
protection boundary. Encryption here protects a database or database-only backup by itself.

## Notification providers

**Settings:** Notifications

- **A test stays queued** — the notification worker runs about every 15 seconds. Refresh the
  provider row; its safe health summary will show acceptance or a bounded failure category.
- **Configuration invalid** — edit the provider and check every required field. Sensitive fields
  are write-only: a blank field preserves a configured value unless you explicitly clear it.
- **Transport unavailable** — verify that the provider host is reachable from Loomarr's container.
  For MQTT, `localhost` is the container itself and `mqtts://` requires a certificate valid for the
  broker hostname.
- **Recipient rejected** — replace the upstream token, webhook, room/chat identifier, SMTP
  credentials, or Push permission. Loomarr never returns the rejected credential in its error.
- **Browser Push is unavailable** — Push requires browser support and a secure context (HTTPS, or
  localhost for development). Permission is requested only after **Enable this browser**. If it was
  denied, change the site's notification permission in the browser; ordinary Loomarr use continues.
- **Browser Push stopped after working** — browsers and Push services can expire a subscription.
  Loomarr disables a subscription reported gone instead of retrying it; delete that provider and
  add Browser Push again from the affected browser.
- **A webhook or token was revoked** — create or rotate the credential at Slack, Discord,
  Mattermost, Gotify, Telegram, or Matrix; update the provider and send a test before revoking the
  old credential. A write-only URL or token cannot be recovered from Loomarr. Matrix also requires
  an unencrypted room and a token whose user has joined it.
- **Apprise accepted only some fan-out targets** — the Loomarr test confirms the Apprise handoff,
  not each downstream receipt. Check the Apprise service logs and configuration; downstream
  credentials remain in that operator-managed service.
- **ntfy or MQTT behaves unexpectedly** — verify the ntfy topic and authentication together; a
  public hosted topic is not private merely because its name is obscure. For MQTT, confirm the
  broker scheme/port, TLS trust, credentials, base topic, QoS, and retain setting. A retained test
  remains on the broker until replaced or cleared.

## Media server

**Check:** `media_server` · **Settings:** Connections → Media server

Loomarr reads your library from Emby or Jellyfin, and can import user accounts from it.

- **Connection refused, or a timeout** — the URL isn't reachable *from Loomarr's container*.
  `localhost` there means the container itself. Use the service name (`http://emby:8096`) if they
  share a Docker network, or your host's LAN IP if not.
- **401 or "invalid token"** — the key is wrong, or belongs to a non-admin user. Loomarr needs an
  **admin** key: it reads the user list and every library.
- **Connects, but suggestions say your library is empty** — the token works, but those libraries
  aren't visible to that user. Check the user's library access in Emby.

## Seerr

**Check:** `requester` · **Settings:** Connections → Requester

Seerr (Jellyseerr or Overseerr) downloads titles you don't have. Without it, channels still build
from what you already own.

- **401** — wrong API key. It's in Seerr under Settings → General → API Key.
- **Requests are accepted but nothing downloads** — that's downstream of Loomarr. Seerr forwards
  to Sonarr/Radarr; check Seerr's own request log first.
- **Re-requesting a title you already have succeeds with nothing queued** — expected. Seerr
  returns the existing media.

## Tunarr

**Check:** `tunarr` · **Settings:** Connections → Tunarr

Only applies if you chose Tunarr as your playout backend. On a default install this check blocks
nothing — you don't need it.

- **Can't reach Tunarr** — same container-networking rule as the media server. Tunarr's default
  port is 8000.
- **Channels exist in Loomarr but not in Tunarr** — the channel hasn't rebuilt yet, or the rebuild
  is failing. Press **Rebuild now** on the channel page and read the error.
- **A channel exists but plays nothing** — see below.

### Tunarr library

**Check:** `tunarr_library`

Tunarr needs your Emby or Jellyfin server as *its own* media source, with the movie and show
libraries enabled **and scanned**. Settings → Connections → "Connect Tunarr" does the wiring; the
scan is what fills Tunarr's program table.

Skip the scan and there's nothing to see: channels build, the rebuild reports success, and every
slot plays dead air.

## LLM

**Check:** `llm` · **Settings:** AI

Suggestions need a model that supports **tool calling** — that's how Loomarr grounds it in your
real library.

- **"No tool-calling support"** — the model is reachable but can't be grounded. Pick one the model
  picker marks as recommended.
- **Ollama unreachable** — check the URL. On Docker Desktop, Ollama running on the host is
  `http://host.docker.internal:11434`, not `localhost`.
- **A downloaded model isn't selected** — downloading and selecting are separate steps. Selecting
  hot-swaps the running suggester, so a download never changes what's running.

## TMDB

**Check:** `tmdb` · **Settings:** AI → Grounding

TMDB supplies the metadata behind titles you don't own yet. Without it, Loomarr can only suggest
from your existing library.

- **401** — wrong key. Use the **API Read Access Token** or the v3 key from your TMDB account
  settings.

## Filler

**Check:** `filler` · **Settings:** Filler

Appears once you've configured a drop-folder.

- **No clips found** — the folder has to be readable *by Loomarr's container*. Loomarr scans it
  automatically; on the Tunarr backend it also exposes the same folder as a `local` source for
  playout.
- **Clips exist but channels play no commercials** — check the channel's pod preview. If it shows
  only bumpers, your commercials are missing the tags used for matching.
- **"Ingest unavailable"** — yt-dlp and ffmpeg ship in the Loomarr image, so this means the
  running install can't execute them: usually a custom image built without the vendored binaries,
  or `INGEST_YTDLP_PATH` / `INGEST_FFMPEG_PATH` pointing somewhere wrong. Clips you copy in by
  hand still work.

## LiveTV

**Check:** `livetv`

This is what puts Loomarr's channels in your media server's Live TV guide: a tuner (M3U) plus a
guide provider (XMLTV). It's **one-time** setup, not per channel — after it, channels you create,
rename or delete propagate on their own.

- **Channels exist but aren't in the guide** — the tuner is wired, the guide hasn't refreshed.
  Media servers refresh on their own schedule, often nightly. Loomarr asks for a refresh after
  each rebuild, but the media server decides when to honour it.
- **Duplicate channels in the guide** — a tuner got registered twice. Delete the extras in your
  media server; Loomarr's connect is idempotent and won't add more.
- **Channels show but won't play** — not a guide problem. On the Tunarr backend, re-run **Wire
  Tunarr to your library** ([above](#tunarr-library)).
- **Playback stops after about 4 seconds in Firefox** — a Firefox quirk, not a Loomarr fault; the
  stream is fine. Use a Chrome-based browser or the Emby/Jellyfin app.

Registering a tuner needs the same admin key as the media-server connection.

## Downloads not appearing

Loomarr finds out a title finished by **polling** — a scheduled library scan, plus a
download-queue poll if you use Sonarr/Radarr directly. A title flips *downloading* → *available*
once it shows up in your media-server library.

- **Stuck in *requested* or *downloading*** — check the requester is reachable (Settings →
  Connections → Test) and that Sonarr/Radarr actually grabbed a release. Settings → Tasks shows
  each poll's last run and has a **Run now**.
- **Downloaded but still not showing** — confirm it landed in the same library Loomarr reads, and
  that the library has been scanned. Availability follows the library, so a title your media
  server hasn't indexed won't flip to *available*.
