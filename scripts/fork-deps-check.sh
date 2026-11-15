#!/bin/sh
# Fork addition: tells whether a dependency has a known vulnerability that has
# not been looked at yet. Runs govulncheck (Go, symbol level: the vulnerable
# function is reachable from the code) and npm audit (the whole tree, build
# tools included), and compares what they find with
# scripts/fork-deps-known.txt, the findings already explained. Exit code 0:
# nothing new; 1: a new finding (listed); 2: a tool could not run.
# With OUTDATED=1 it also lists the direct dependencies that have a newer
# version (information only, never a failure).
#
# It needs go and npm on the PATH (the checks on GitHub have both). On a
# machine without them, from the repository's root:
#   docker run --rm -v "$PWD":/src -w /src golang:1.27 sh -c \
#     'apt-get -qq update >/dev/null && apt-get -qq install -y nodejs npm >/dev/null && sh scripts/fork-deps-check.sh'
# It reads the repository and writes only src/launcher/update.go (what
# build.sh copies there too, ignored by git) and files under a temporary folder.
set -u
cd "$(dirname "$0")/.."

known=scripts/fork-deps-known.txt
govulncheck_version=v1.9.0
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cp src/update.go src/launcher/update.go

# --- Go: the ids govulncheck reports as reachable ("Your code is affected")
if ! go run "golang.org/x/vuln/cmd/govulncheck@$govulncheck_version" -format json ./... > "$tmp/go.json" 2> "$tmp/go.err"; then
  # govulncheck -format json exits 0 even with findings; anything else is a failure to run
  cat "$tmp/go.err" >&2
  echo "govulncheck could not run" >&2
  exit 2
fi
# a finding with a trace into a function of this module is reachable
node -e '
  const lines = require("fs").readFileSync(process.argv[1], "utf8");
  const ids = new Set();
  // the output is a stream of JSON objects, one after the other
  let depth = 0, start = -1, inString = false, escaped = false;
  for (let i = 0; i < lines.length; i++) {
    const c = lines[i];
    if (inString) { if (escaped) escaped = false; else if (c === "\\") escaped = true; else if (c === "\"") inString = false; continue; }
    if (c === "\"") inString = true;
    else if (c === "{") { if (depth++ === 0) start = i; }
    else if (c === "}" && --depth === 0) {
      const o = JSON.parse(lines.slice(start, i + 1));
      const f = o.finding;
      if (f && f.trace && f.trace.length && f.trace[f.trace.length - 1].function) ids.add(f.osv);
    }
  }
  console.log([...ids].sort().join("\n"));
' "$tmp/go.json" > "$tmp/found"

# --- npm: every advisory npm audit names (GHSA ids), with the package it is in
npm audit --json > "$tmp/npm.json" 2> "$tmp/npm.err"
if ! node -e 'JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).vulnerabilities' "$tmp/npm.json" > /dev/null 2>&1; then
  cat "$tmp/npm.err" >&2
  echo "npm audit could not run" >&2
  exit 2
fi
node -e '
  const d = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
  const ids = new Set();
  for (const v of Object.values(d.vulnerabilities)) {
    for (const via of v.via) if (typeof via === "object" && via.url) ids.add(via.url.split("/").pop());
  }
  console.log([...ids].sort().join("\n"));
' "$tmp/npm.json" >> "$tmp/found"

# --- compare with what is already explained (first word of each line, # starts a comment)
sed -e 's/#.*//' -e 's/[[:space:]].*//' "$known" | grep -v '^$' | sort -u > "$tmp/known"
sort -u "$tmp/found" | grep -v '^$' > "$tmp/found.sorted"
new=$(comm -23 "$tmp/found.sorted" "$tmp/known")
gone=$(comm -13 "$tmp/found.sorted" "$tmp/known")

echo "Findings: $(wc -l < "$tmp/found.sorted"), already explained: $(comm -12 "$tmp/found.sorted" "$tmp/known" | wc -l)"
if [ -n "$gone" ]; then
  echo "Explained in $known but no longer found (the line can go):"
  echo "$gone" | sed 's/^/  /'
fi

if [ "${OUTDATED:-}" = "1" ]; then
  echo "Direct Go modules with a newer version:"
  go list -m -u -f '{{if and (not .Indirect) .Update}}  {{.Path}} {{.Version}} -> {{.Update.Version}}{{end}}' all 2>/dev/null | grep -v '^$' || true
  echo "npm packages with a newer version:"
  npm outdated 2>/dev/null | sed 's/^/  /' || true
fi

if [ -n "$new" ]; then
  echo "New findings, not yet explained in $known:"
  for id in $new; do
    case "$id" in
      GO-*) echo "  $id  https://pkg.go.dev/vuln/$id" ;;
      *)    echo "  $id  https://github.com/advisories/$id" ;;
    esac
  done
  exit 1
fi
echo "Nothing new."
