#!/bin/sh
# Fork addition: the quick checks the automatic dependency update runs after
# each change it makes (scripts/fork-deps-update.mjs), before it keeps it:
#
#   sh scripts/fork-deps-quick.sh npm   npm ci --ignore-scripts, the UI build, the node tests
#   sh scripts/fork-deps-quick.sh go    the server and launcher build, the fork's Go tests
#   sh scripts/fork-deps-quick.sh all   both
#
# They catch what does not build or breaks a unit test. They do not catch a
# page that renders nothing or throws: the full checks (fork-ci.yml, with the
# browser smoke) do, and the update drops majors one by one when those fail.
# Run from the root of a clone of its own: "npm" replaces node_modules.
set -eu
cd "$(dirname "$0")/.."
what=${1:-all}
# a go.mod whose go line was just raised needs that toolchain (fetched from the module proxy, checksum-verified)
export GOTOOLCHAIN=auto

npm_checks() {
  npm ci --ignore-scripts --no-audit --no-fund --loglevel=error
  npx --no-install vite build --base=/cosmos-ui/ --logLevel error
  node --test "client/src/utils/*.test.mjs" > /tmp/fork-deps-node-tests.log 2>&1 || { tail -n 60 /tmp/fork-deps-node-tests.log; exit 1; }
  node --test scripts/*.test.mjs > /tmp/fork-deps-node-tests.log 2>&1 || { tail -n 60 /tmp/fork-deps-node-tests.log; exit 1; }
  rm -rf static stats.html
}

go_checks() {
  cp src/update.go src/launcher/update.go
  CGO_ENABLED=0 go build -o /tmp/fork-deps-cosmos src/*.go
  CGO_ENABLED=0 go build -o /tmp/fork-deps-launcher ./src/launcher/launcher.go ./src/launcher/update.go
  rm -f /tmp/fork-deps-cosmos /tmp/fork-deps-launcher
  # the list of .github/actions/fork-build/action.yml
  go test ./src/forkrelease/ ./src/launcher/ -count=1
  go test ./src/utils/ ./src/authorizationserver/ -count=1
  go test ./src/upnp/ ./src/configapi/ ./src/snapshots/ ./src/storage/ ./src/metrics/ ./src/user/ ./src/reach/ ./src/security/ ./src/telegram/ ./src/fail2ban/ ./src/maildetect/ ./src/dnsrecords/ ./src/cloudflare/... ./src/certs/ ./src/market/ ./src/turnstile/ ./src/edgeblock/ ./src/reachwatch/ ./src/anyimage/ ./src/behind/ ./src/appsignin/ ./src/cron/ ./src/guides/ ./src/edithints/ ./src/watch/ -count=1
  go test ./src/ -skip TestEveryRouteIsDocumented -count=1
  go test ./src/docker/ ./src/proxy/ -count=1
  rm -f src/launcher/update.go
}

case "$what" in
  npm) npm_checks ;;
  go) go_checks ;;
  all) npm_checks; go_checks ;;
  none) ;;
  *) echo "usage: $0 npm|go|all" >&2; exit 2 ;;
esac
