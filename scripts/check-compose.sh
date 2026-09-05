#!/usr/bin/env sh

set -eu

SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd -P)"
ROOT="$(CDPATH='' cd -- "$SCRIPT_DIR/.." && pwd -P)"
VERSION=0.1.0-beta.1
PUBLIC_URL=http://192.0.2.10

if LOOMARR_VERSION='' SERVER_PUBLIC_URL="$PUBLIC_URL" docker compose \
	-f "$ROOT/docker/compose.yaml" --profile sqlite config >/dev/null 2>&1; then
	echo 'compose-verify: release Compose accepts an unpinned Loomarr image' >&2
	exit 1
fi
if LOOMARR_VERSION="$VERSION" SERVER_PUBLIC_URL='' docker compose \
	-f "$ROOT/docker/compose.yaml" --profile sqlite config >/dev/null 2>&1; then
	echo 'compose-verify: release Compose accepts an empty public URL' >&2
	exit 1
fi

sqlite="$(
	LOOMARR_VERSION="$VERSION" SERVER_PUBLIC_URL="$PUBLIC_URL" docker compose \
		-f "$ROOT/docker/compose.yaml" \
		--profile sqlite config 2>/dev/null
)"
printf '%s\n' "$sqlite" | grep -q "image: ghcr.io/loomarr/loomarr:$VERSION"
if printf '%s\n' "$sqlite" | grep -q 'DATABASE_URL:'; then
	echo 'compose-verify: SQLite deployment pins DATABASE_URL and disables in-app migration' >&2
	exit 1
fi
printf '%s\n' "$sqlite" | grep -q 'image: traefik:v3.7.1@sha256:6b9cbca6fac42ab0075f5437d8dc1685cfd188626d8d515839ea94f8b6271c42'
printf '%s\n' "$sqlite" | grep -q 'image: busybox:1.36@sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662'
# shellcheck disable=SC2016 # the backticks are literal Traefik rule syntax
printf '%s\n' "$sqlite" | grep -q 'providers.docker.constraints=Label(`media.loomarr.edge`,`true`)'
printf '%s\n' "$sqlite" | grep -q -- '--ping.entrypoint=health'
printf '%s\n' "$sqlite" | grep -q 'http://127.0.0.1:8082/ping'
printf '%s\n' "$sqlite" | grep -q 'traefik.http.routers.loomarr.service: loomarr'
printf '%s\n' "$sqlite" | grep -q 'traefik.http.services.loomarr.loadbalancer.server.port: "8080"'
printf '%s\n' "$sqlite" | grep -q 'SERVER_PUBLIC_URL: http://192.0.2.10'
printf '%s\n' "$sqlite" | grep -q -- '--image-tag'
printf '%s\n' "$sqlite" | grep -q 'condition: service_completed_successfully'

if "$ROOT/scripts/check-release-tag.sh" --image-tag latest >/dev/null 2>&1; then
	echo 'compose-verify: release Compose validator accepts the mutable latest alias' >&2
	exit 1
fi
"$ROOT/scripts/check-release-tag.sh" --image-tag "$VERSION"

loomarr="$(printf '%s\n' "$sqlite" | sed -n '/^  loomarr:$/,/^  [a-zA-Z0-9_-][a-zA-Z0-9_-]*:$/p')"
loomarr_port_targets="$(printf '%s\n' "$loomarr" | awk '
	/^    ports:$/ { ports = 1; next }
	ports && /^    [a-zA-Z_]/ { exit }
	ports && /target:/ { print $2 }
')"
if [ "$loomarr_port_targets" != 51029 ]; then
	echo 'compose-verify: Loomarr publishes a host port other than its single LAN-discovery UDP port' >&2
	exit 1
fi
printf '%s\n' "$loomarr" | grep -q 'published: "51029"'
printf '%s\n' "$loomarr" | grep -q 'protocol: udp'
if printf '%s\n' "$loomarr" | grep -q '^    build:'; then
	echo 'compose-verify: release Compose can fall back to an unsigned local build' >&2
	exit 1
fi
if printf '%s\n' "$sqlite" | grep -q 'target: 8082'; then
	echo 'compose-verify: Traefik admin/ping entrypoint is published' >&2
	exit 1
fi

postgres="$(
	LOOMARR_VERSION="$VERSION" SERVER_PUBLIC_URL="$PUBLIC_URL" docker compose \
		-f "$ROOT/docker/compose.yaml" \
		-f "$ROOT/docker/compose.postgres.yaml" \
		--profile postgres config 2>/dev/null
)"
printf '%s\n' "$postgres" | grep -q "image: ghcr.io/loomarr/loomarr:$VERSION"
printf '%s\n' "$postgres" | grep -q 'image: postgres:16@sha256:f1c3376c26f2609ab9f29f71f824103fe2fcd8ee0346485cb6122a4f93df6f94'
printf '%s\n' "$postgres" | grep -q 'DATABASE_URL: postgres://loomarr:loomarr@postgres:5432/loomarr?sslmode=disable'
if printf '%s\n' "$postgres" | grep -q 'DATABASE_URL: sqlite:'; then
	echo 'compose-verify: postgres deployment resolved Loomarr to SQLite' >&2
	exit 1
fi

echo 'compose-verify: Traefik, SQLite, and Postgres deployments are wired'
