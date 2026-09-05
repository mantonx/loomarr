# Docker install

You'll need Docker Engine on Linux or Docker Desktop on macOS, plus your Emby or Jellyfin URL and
an admin API key. TMDB and an LLM are also required to create a channel from a sentence; requesters
and filler can be added later from Settings.

Choose an exact version from [GitHub Releases](https://github.com/loomarr/loomarr/releases). This
example uses `0.1.0-beta.8`; keep the version pinned for reproducible installs and rollbacks.

## Start it

```bash
VERSION=0.1.0-beta.8
git clone --branch "v${VERSION}" --depth 1 https://github.com/loomarr/loomarr && cd loomarr
cp .env.example .env
# Edit .env: SERVER_PUBLIC_URL must be a URL this host and your media server can reach.
LOOMARR_VERSION="$VERSION" docker compose -f docker/compose.yaml --profile sqlite up -d
```

This pulls the pinned `ghcr.io/loomarr/loomarr:0.1.0-beta.8` image. Linux hosts use the native
amd64 or arm64 manifest. Docker Desktop does the same on Intel or Apple Silicon Macs.

Profiles combine:

| Profile | Adds |
| --- | --- |
| `sqlite` | The default database — one file on the `/data` volume |
| `postgres` | A Postgres container, selected with `docker/compose.postgres.yaml` |
| `ai` | A local Ollama. Omit it if you use a hosted provider or run Ollama already |

The SQLite deployment intentionally leaves `DATABASE_URL` out of the container environment.
Loomarr's built-in default still selects `/data/loomarr.db`, while leaving the key unpinned lets the
in-app database migration write its PostgreSQL target to `/data/bootstrap.json` and restart onto it.
Set `DATABASE_URL` yourself only when you want launch configuration—not Loomarr—to remain the
authority for database selection.

Open `SERVER_PUBLIC_URL` and follow the wizard — see [Quickstart](../help/quickstart.md). The
supported Compose topology publishes Traefik on host port 8080; Loomarr's port 8080 is private.
Set `LOOMARR_HTTP_PORT` in `.env` if the host must publish a different port, and include that port
in `SERVER_PUBLIC_URL`.

The Compose stack also publishes UDP port `51029` so Android TV clients can find Loomarr across
Docker Desktop's network boundary. Allow inbound UDP `51029` from the trusted LAN in the Docker
host firewall. Discovery announces `SERVER_PUBLIC_URL`, so that URL must use an address the TV can
reach; do not set it to `localhost` or a container-only hostname.

The default Traefik entrypoint is plain HTTP for the trusted-LAN deployment model in
[`SECURITY.md`](../../SECURITY.md). It is not an internet-facing TLS configuration. Keep the Docker
host trusted: Traefik's Docker socket bind is marked read-only, but the Docker API itself is a
host-control boundary.

For Postgres, use the explicit database override; a Compose profile can start Postgres but cannot
change Loomarr's SQLite default by itself:

```bash
LOOMARR_VERSION="$VERSION" docker compose \
  -f docker/compose.yaml -f docker/compose.postgres.yaml \
  --profile postgres up -d
```

## Settings

Most application settings can be entered in the UI. Release Compose requires the canonical public
URL before it starts; the media-server values are also useful to set up front:

```bash
SERVER_PUBLIC_URL=http://192.168.1.10:8080   # how your media server reaches Traefik → Loomarr
LOOMARR_HTTP_PORT=8080                       # host port owned by Traefik
LIBRARY_URL=http://192.168.1.10:8096         # your Emby/Jellyfin
LIBRARY_TOKEN=…                              # an admin API key
```

Two things that cause most first-run problems:

- **`localhost` inside a container means the container.** Use service names
  (`http://emby:8096`) if they share a Docker network, or your host's LAN IP if not.
- **An environment variable wins over the UI and locks that field.** If a setting won't edit,
  something in your compose file is setting it.

Full list: [configuration reference](../configuration.md).

## Data and backups

Runtime state defaults under `/data`, but an application backup is a **database snapshot**, not a
copy of that volume:

| Path | Contents |
| --- | --- |
| `/data/loomarr.db` | Database — accounts, channels, settings, encrypted secrets |
| `/data/encryption.key` | Installation key — required to restore encrypted database secrets |
| `/data/filler/` | Commercial and bumper clips |
| `/data/images/` | Cached artwork |
| `/data/prepared/` | Reusable prepared programme media for instant channel changes |

The database backup contains accounts, channels, settings, encrypted secrets, and wrapped data
keys. It deliberately does not contain `/data/encryption.key`. Preserve that key separately: losing
it makes stored credentials unrecoverable. The backup also omits filler files, prepared media,
cached artwork, and operator-uploaded images. Copy the `/data` volume as part of host-level backup
if those files matter; cached and prepared derivatives can be regenerated.

Prepared media is bounded by the hot-applied `PLAYOUT_PREPARED_BUDGET_GB` soft cap (512 GiB by
default). Keep enough free space for one programme beyond the cap because packaging commits before
the retention pass; recently played programmes remain protected even if that temporarily exceeds
the cap.

If you write your own compose file, **mount `/data`**. Without it the database goes into the
container's writable layer and is lost on the next `up --force-recreate` or image pull.

The easiest backup is the admin UI: **Settings → System → Backup** downloads a snapshot with your
signed-in session, no token required. To script it, use `/v1/backup` (admin-only). Reveal a usable
`API_TOKEN` under **Settings → Secrets** (eye toggle + copy), or set your own
(`API_TOKEN=<something-long>` in `.env`, then restart); export it as `$API_TOKEN` before the curl,
or the request is `401`.

Back up SQLite:

```bash
backup="loomarr-$(date +%F).db"
umask 077
curl -fsS -H "Authorization: Bearer $API_TOKEN" \
  http://localhost:8080/v1/backup > "$backup.partial" &&
  chmod 600 "$backup.partial" &&
  mv "$backup.partial" "$backup"
```

On Postgres, create a custom archive so the documented restore command can consume it. Loomarr's
image ships no Postgres client and the supported Compose stack keeps the database off the host
network, so run the client inside the Postgres service and stream the archive to the trusted host;
`/v1/backup` returns 501 there.

```bash
compose=(docker compose -f docker/compose.yaml -f docker/compose.postgres.yaml --profile postgres)
archive="loomarr-$(date +%F).dump"
umask 077
"${compose[@]}" exec -T postgres sh -ceu \
  'pg_dump --format=custom --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' \
  > "$archive.partial" &&
  chmod 600 "$archive.partial" &&
  mv "$archive.partial" "$archive"
```

Backups contain secrets. Keep the SQLite file or Postgres archive at mode `0600`.

Scheduled backups default to `/data/backups`, which protects against a bad migration or application
change but not loss of the host disk or the whole volume. Copy them to another disk or host.

Restore only while Loomarr is stopped. For SQLite, replace `/data/loomarr.db`, then set mode `0600`
and ownership to `65532:65532` before starting the container. For Postgres, use the same Compose
files as the running stack, stop Loomarr, and replace only its database:

```bash
compose=(docker compose -f docker/compose.yaml -f docker/compose.postgres.yaml --profile postgres)
"${compose[@]}" stop loomarr
"${compose[@]}" exec -T postgres sh -ceu '
  dropdb --username="$POSTGRES_USER" --force "$POSTGRES_DB"
  createdb --username="$POSTGRES_USER" --owner="$POSTGRES_USER" "$POSTGRES_DB"
  pg_restore --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" --exit-on-error
' < loomarr-YYYY-MM-DD.dump
"${compose[@]}" up -d loomarr traefik
```

After either restore, wait for `/v1/readyz`, sign in, and verify a channel before deleting the
newer backup. Repository maintainers can exercise the same isolated recovery contract with
`make backup-restore-verify` (SQLite, no Docker) or `make backup-restore-drill` (SQLite and
Docker-backed Postgres).

## Checking it's up

```bash
curl -fsS http://localhost:8080/v1/readyz && echo ready
```

The Loomarr HTTP listener starts only after the database opens and migrations finish. Traefik waits
for Loomarr's container healthcheck, then independently probes `/v1/readyz` before routing traffic.
If startup fails, inspect `docker compose logs loomarr traefik`; there is no readiness response from
a process that never started listening.

Traefik is a real load-balancing edge, but the first beta supports **one Loomarr replica**. Do not
run `docker compose up --scale loomarr=…` in production yet: recurring jobs, playout ownership, and
file-backed state still need the multi-replica investigation recorded in the beta-readiness plan.

## Filler clips

Filler works without extra Compose configuration: Loomarr stores clips under `/data/filler` on
the same persistent `loomarr-data` volume as the rest of `/data`.

To use a different host folder, add an explicit bind mount whose container target is an absolute
path, then set `FILLER_DIR` to that target. Saving `filler.dir` in Settings selects the desired
library but the running generation keeps its current library until Loomarr restarts; Loomarr does
not copy clips between the old and new folders.

On the Tunarr backend, mount the same host folder or named volume into Tunarr at the same absolute
container path so it can scan the clips. Internal playout reads Loomarr's own `/data/filler`
directly and needs no second mount.

## When something's wrong

The wizard tests every connection and each failure links to its fix in
[Troubleshooting](../help/troubleshooting.md). The two most common:

- **Channels appear in the guide but won't play** — usually `SERVER_PUBLIC_URL`. ffmpeg fetches
  the playlist as a separate process, so a wrong value fails only at tune time.
- **The URL is unavailable** — run `docker compose ps`, then check `docker compose logs loomarr
  traefik`. Database and migration failures happen before Loomarr's private listener starts;
  Traefik does not route to an unhealthy backend.
