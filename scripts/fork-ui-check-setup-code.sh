#!/bin/sh
# Fork addition (plan 47): runs scripts/fork-ui-check-setup-code.mjs on a fresh
# throwaway instance for each engine and size (every run sets its instance up).
#
#   BIN=<cosmos built with CGO_ENABLED=0> STATIC=<a built ./static> sh scripts/fork-ui-check-setup-code.sh
#
# ENGINES (default chromium,firefox,webkit), SIZES (d,m), PREFIX (cosmos-uisetup),
# SCRATCH (/storage/dev/.cache/$PREFIX), PLAYWRIGHT_IMAGE. Everything it
# starts is on a Docker network of its own and removed at the end. The test
# instance has no Docker (the wizard's Docker step is skipped).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
PREFIX=${PREFIX:-cosmos-uisetup}
SCRATCH=${SCRATCH:-/storage/dev/.cache/$PREFIX}
ENGINES=${ENGINES:-chromium,firefox,webkit}
SIZES=${SIZES:-d,m}
PLAYWRIGHT_IMAGE=${PLAYWRIGHT_IMAGE:-mcr.microsoft.com/playwright:v1.65.1-noble}
BASE_IMAGE=${BASE_IMAGE:-debian:13}
: "${BIN:?BIN: the cosmos binary}" "${STATIC:?STATIC: a built panel (./static)}"

clean() {
	docker rm -f -v "$PREFIX-srv" "$PREFIX-pw" >/dev/null 2>&1 || true
	docker network rm "$PREFIX-net" >/dev/null 2>&1 || true
	docker run --rm -v "$(dirname "$SCRATCH"):/w" "$BASE_IMAGE" rm -rf "/w/$(basename "$SCRATCH")" >/dev/null 2>&1 || true
}
trap clean EXIT
trap 'exit 130' INT TERM
clean
mkdir -p "$SCRATCH/pw/out"
docker network create "$PREFIX-net" >/dev/null
cp "$here/fork-ui-check-setup-code.mjs" "$SCRATCH/pw/"
docker run --rm --name "$PREFIX-pw" -v "$SCRATCH/pw:/work" -w /work "$PLAYWRIGHT_IMAGE" sh -c 'npm i --silent playwright@1.65.1 >/dev/null 2>&1'

status=0
for engine in $(echo "$ENGINES" | tr ',' ' '); do
	for size in $(echo "$SIZES" | tr ',' ' '); do
		docker rm -f -v "$PREFIX-srv" >/dev/null 2>&1 || true
		docker run --rm -v "$SCRATCH:/s" "$BASE_IMAGE" rm -rf /s/srv >/dev/null
		mkdir -p "$SCRATCH/srv/config"
		cp "$BIN" "$SCRATCH/srv/cosmos"
		cp -r "$STATIC" "$SCRATCH/srv/static"
		echo '{"version":"0.24.0-fork.999"}' >"$SCRATCH/srv/meta.json"
		docker run -d --name "$PREFIX-srv" --hostname cosmos-setup --network "$PREFIX-net" --network-alias cosmos-setup.test \
			-v "$SCRATCH/srv:/srv" -w /srv -e COSMOS_CONFIG_FOLDER=/srv/config/ -e COSMOS_HTTP_PORT=2052 -e COSMOS_HTTPS_PORT=2053 \
			-e DOCKER_HOST=unix:///srv/no.sock "$BASE_IMAGE" sh -c 'while true; do ./cosmos; sleep 1; done' >/dev/null
		for _ in $(seq 1 60); do [ -s "$SCRATCH/srv/config/setup-code" ] && break; sleep 1; done
		code=$(cat "$SCRATCH/srv/config/setup-code")
		docker run --rm --name "$PREFIX-pw" --network "$PREFIX-net" --ipc=host -v "$SCRATCH/pw:/work" -w /work \
			-e BASE=http://cosmos-setup.test:2052 -e SETUP_CODE="$code" -e ENGINE="$engine" -e SIZE="$size" -e OUT=/work/out \
			"$PLAYWRIGHT_IMAGE" node fork-ui-check-setup-code.mjs || status=1
	done
done
[ -z "${KEEP_SHOTS:-}" ] || cp -r "$SCRATCH/pw/out" "$KEEP_SHOTS"
exit $status
