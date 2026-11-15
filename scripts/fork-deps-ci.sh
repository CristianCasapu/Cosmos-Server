#!/usr/bin/env bash
# Fork addition: the steps of .github/workflows/fork-deps-update.yml that act
# on the repository (git, gh, jq only; nothing of a dependency runs here):
#
#   guard                  may the update run now? (kill switch, a sync, a rollback or a release under way)
#   receive <bundle> <base> <day>   validate the update's commits (with THIS checkout's validator) and push deps/<day>
#   promote <sha> <base>   move custom and production to the checked candidate, in one atomic, non-forced push
#   drop <sha> <base> <reason>      take the newest major out of the candidate; prints the new tip
#   majors <sha> <base>    how many major commits the candidate has
#   wait-release <sha>     wait until fork-release.yml published a release of <sha>; prints its version
#   status <outcome> [failed-step] [failure-file]   write status.json on the branch fork-deps-status
#   issue <failure-file>   open or update the issue "Automatic dependency update needs a session"; prints its number
#   resolved <text>        close that issue, if open, with a comment
#
# Environment: GITHUB_REPOSITORY, GITHUB_RUN_ID, GITHUB_SERVER_URL, GH_TOKEN
# (gh), REPORT (the update's report.json, when there is one), CANDIDATE
# (deps/<day>), BASE, SHA, RELEASE. Every value that comes from a commit or
# the report is treated as text: bounded, and never evaluated.
set -euo pipefail

repo=${GITHUB_REPOSITORY:?}
run=${GITHUB_RUN_ID:-0}
server=${GITHUB_SERVER_URL:-https://github.com}
here=$(cd "$(dirname "$0")" && pwd)
issue_title="Automatic dependency update needs a session"
status_branch=fork-deps-status

# commits made here are the machine's
export GIT_AUTHOR_NAME=${GIT_AUTHOR_NAME:-github-actions[bot]} GIT_COMMITTER_NAME=${GIT_COMMITTER_NAME:-github-actions[bot]}
export GIT_AUTHOR_EMAIL=${GIT_AUTHOR_EMAIL:-41898282+github-actions[bot]@users.noreply.github.com}
export GIT_COMMITTER_EMAIL=${GIT_COMMITTER_EMAIL:-41898282+github-actions[bot]@users.noreply.github.com}

hex40() { [[ "$1" =~ ^[0-9a-f]{40}$ ]]; }
day_ok() { [[ "$1" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; }
# text for an issue: no mentions, no code fences, no control characters, bounded
clean() { LC_ALL=C sed -e 's/\x1b\[[0-9;]*[A-Za-z]//g' -e 's/[@`]/_/g' -e 's/[^[:print:]\t]//g' | head -c "${1:-20000}"; }

cmd=${1:-}; shift || true
case "$cmd" in

guard)
  if [ "${FORK_DEPS_UPDATE:-}" = "off" ] || [ "${FORK_DEPS_UPDATE:-}" = "OFF" ]; then
    echo "paused"; exit 0
  fi
  for wf in fork-sync.yml fork-rollback.yml fork-release.yml; do
    busy=$(gh run list --repo "$repo" --workflow "$wf" --limit 20 --json status \
      --jq '[.[] | select(.status != "completed")] | length' 2>/dev/null || echo 0)
    if [ "${busy:-0}" != "0" ]; then echo "busy $wf"; exit 0; fi
  done
  echo "go"
  ;;

receive)
  bundle=$1 base=$2 day=$3
  hex40 "$base" && day_ok "$day" || { echo "receive: bad arguments" >&2; exit 2; }
  git bundle verify -q "$bundle" >/dev/null 2>&1 || { echo "receive: the bundle is not usable on top of $base" >&2; exit 1; }
  git fetch -q "$bundle" "+HEAD:refs/fork-deps/tip"
  tip=$(git rev-parse refs/fork-deps/tip)
  # the validator of this checkout (the base: trusted), never the candidate's
  node "$here/fork-deps-validate.mjs" "$base" "$tip" >&2
  # the candidate branch is the machine's own: replaced by a later run of the same day
  git push -q --force origin "$tip:refs/heads/deps/$day"
  echo "$tip"
  ;;

majors)
  sha=$1 base=$2
  git log --format=%B "$base..$sha" | grep -c '^Fork-Dependency: [a-z]* [^ ]* [^ ]* [^ ]* major ' || true
  ;;

drop)
  sha=$1 base=$2 reason=$3
  hex40 "$sha" && hex40 "$base" || { echo "drop: bad arguments" >&2; exit 2; }
  # the newest commit that carries a major (majors come after the minors, one per commit)
  newest=""
  for c in $(git rev-list "$base..$sha"); do
    if git log -1 --format=%B "$c" | grep -q '^Fork-Dependency: [a-z]* [^ ]* [^ ]* [^ ]* major '; then newest=$c; break; fi
  done
  [ -n "$newest" ] || { echo "drop: no major in the candidate" >&2; exit 1; }
  dropped=$(git log -1 --format=%B "$newest" | grep '^Fork-Dependency: ' | sed -E "s/^Fork-Dependency: ([a-z]+) (\S+) (\S+) (\S+) major \S+$/Fork-Dependency-Dropped: \1 \2 \3 \4 $(printf '%s' "$reason" | clean 150 | tr -d '\n\\&/')/")
  # what came after it: only commits without a major (the record of the run)
  after=$(git rev-list --reverse "$newest..$sha")
  git checkout -q --detach "$newest^"
  date=$(git log -1 --format=%cI "$sha")
  for c in $after; do
    msg=$(git log -1 --format=%B "$c")
    if git log -1 --format=%s "$c" | grep -q '^Record the automatic dependency update'; then
      msg=$(printf '%s\n%s\n' "$msg" "$dropped")
    fi
    # the record of the run is empty; anything else after a major is replayed as it was
    if [ "$(git rev-parse "$c^{tree}")" != "$(git rev-parse "$c^^{tree}")" ]; then
      git cherry-pick -n "$c" >/dev/null
    fi
    GIT_AUTHOR_DATE=$date GIT_COMMITTER_DATE=$date git commit -q --allow-empty -m "$msg"
  done
  if [ -z "$after" ]; then
    GIT_AUTHOR_DATE=$date GIT_COMMITTER_DATE=$date git commit -q --allow-empty -m "Record the automatic dependency update without $(git log -1 --format=%s "$newest" | clean 120)" -m "$dropped"
  fi
  git rev-parse HEAD
  ;;

promote)
  sha=$1 base=$2
  hex40 "$sha" && hex40 "$base" || { echo "promote: bad arguments" >&2; exit 2; }
  git fetch -q origin custom production
  if [ "$(git rev-parse origin/custom)" != "$base" ]; then echo "custom-moved"; exit 0; fi
  if ! git merge-base --is-ancestor origin/production "$sha"; then echo "production-ahead"; exit 0; fi
  # one push for both branches, not forced: if either moved meanwhile, neither changes
  if ! git push -q --atomic origin "$sha:refs/heads/custom" "$sha:refs/heads/production"; then
    git fetch -q origin custom
    if [ "$(git rev-parse origin/custom)" != "$base" ]; then echo "custom-moved"; else echo "push-refused"; fi
    exit 0
  fi
  echo "promoted"
  ;;

wait-release)
  sha=$1 tries=${WAIT_TRIES:-90}
  for _ in $(seq 1 "$tries"); do
    git fetch -q --tags origin 2>/dev/null || true
    tag=$(git tag --points-at "$sha" --list 'v*-fork.*' | head -n 1)
    if [ -n "$tag" ] && gh release view "$tag" --repo "$repo" --json isDraft --jq '.isDraft' 2>/dev/null | grep -qx false; then
      echo "${tag#v}"; exit 0
    fi
    sleep "${WAIT_SECONDS:-60}"
  done
  echo "wait-release: no release of $sha appeared" >&2
  exit 1
  ;;

status)
  outcome=$1 step=${2:-} failure_file=${3:-}
  case "$outcome" in updated|nothing|failed|paused) ;; *) echo "status: unknown outcome" >&2; exit 2 ;; esac
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT
  if git fetch -q origin "refs/heads/$status_branch" 2>/dev/null; then
    git show FETCH_HEAD:status.json > "$work/previous.json" 2>/dev/null || echo '{}' > "$work/previous.json"
    parent=$(git rev-parse FETCH_HEAD)
  else
    echo '{}' > "$work/previous.json"; parent=""
  fi
  now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  today=${now%%T*}
  # the list: the commits from the base to the candidate (what fork-deps-notes.mjs reads for a release)
  if [ -n "${BASE:-}" ] && [ -n "${SHA:-}" ] && hex40 "$BASE" && hex40 "$SHA" && git cat-file -e "$SHA" 2>/dev/null; then
    git log --reverse --no-merges --format=%B%x00 "$BASE..$SHA" > "$work/log"
    node --input-type=module -e '
      import * as lib from "'"$here"'/fork-deps-lib.mjs";
      import { readFileSync, writeFileSync } from "node:fs";
      const s = lib.summarise(readFileSync(process.argv[1], "utf8").split("\0").map(lib.parseTrailers), process.argv[2]);
      writeFileSync(process.argv[3], JSON.stringify({ changes: s.changes.slice(0, 300), dropped: s.dropped.slice(0, 50), tried: s.tried.slice(0, 50), left: s.left.slice(0, 50) }));
    ' "$work/log" "$today" "$work/list.json"
  else
    echo '{"changes":[],"dropped":[],"tried":[],"left":[]}' > "$work/list.json"
  fi
  failure=""
  if [ -n "$failure_file" ] && [ -f "$failure_file" ]; then failure=$(clean 1500 < "$failure_file"); fi
  quiet=$(jq -r 'if .outcome == "nothing" and (.quietSince | type) == "string" then .quietSince else "" end' "$work/previous.json" 2>/dev/null || true)
  [ "$outcome" = "nothing" ] && [ -z "$quiet" ] && quiet=$today
  [ "$outcome" != "nothing" ] && quiet=""
  issue=${ISSUE:-}
  [[ "$issue" =~ ^[1-9][0-9]{0,8}$ ]] || issue=""
  release=${RELEASE:-}
  [[ "$release" =~ ^[0-9]+\.[0-9]+\.[0-9]+-fork\.[1-9][0-9]{0,8}$ ]] || release=""
  [[ "$step" =~ ^(update|advisories|checks|push|custom-moved|release)$ ]] || step=""
  jq -n --argjson run "${run:-0}" --arg at "$now" --arg outcome "$outcome" --arg release "$release" \
    --arg quiet "$quiet" --arg step "$step" --arg issue "$issue" --arg failure "$failure" --slurpfile list "$work/list.json" '
    {schema: 1, run: $run, at: $at, outcome: $outcome}
    + (if $release != "" then {release: $release} else {} end)
    + (if $quiet != "" then {quietSince: $quiet} else {} end)
    + (if $step != "" then {failedStep: $step} else {} end)
    + (if $issue != "" then {issue: ($issue | tonumber)} else {} end)
    + (if $failure != "" then {failure: $failure} else {} end)
    + $list[0]' > "$work/status.json"
  if [ "$(stat -c %s "$work/status.json")" -gt 60000 ]; then
    jq '.changes |= .[:100] | .left |= .[:20] | .tried |= .[:20]' "$work/status.json" > "$work/s2" && mv "$work/s2" "$work/status.json"
  fi
  # one file on a branch of its own; each run adds a commit (never forced)
  blob=$(git hash-object -w "$work/status.json")
  tree=$(printf '100644 blob %s\tstatus.json\n' "$blob" | git mktree)
  if [ -n "$parent" ]; then
    commit=$(git commit-tree "$tree" -p "$parent" -m "Status of the automatic dependency update: $outcome")
  else
    commit=$(git commit-tree "$tree" -m "Status of the automatic dependency update: $outcome")
  fi
  git push -q origin "$commit:refs/heads/$status_branch"
  cat "$work/status.json"
  ;;

issue)
  failure_file=$1
  body=$(mktemp)
  {
    echo "The automatic dependency update of $(date -u +%F) did not publish anything: it needs a session."
    echo
    echo "- Run: $server/$repo/actions/runs/$run"
    [ -n "${CANDIDATE:-}" ] && echo "- Candidate branch: \`${CANDIDATE}\` (left in place: start the repair from it, or from \`custom\`)"
    echo
    if [ -n "${REPORT:-}" ] && [ -f "$REPORT" ]; then
      echo "What it changed:"; echo
      jq -r '.changes[]? | "- \(.eco) \(.name) \(.from) -> \(.to) (\(.kind))"' "$REPORT" | clean 8000
      echo
      echo "Tried and failed:"; echo
      jq -r '.tried[]? | "- \(.eco) \(.name) \(.from) -> \(.to): \(.reason)"' "$REPORT" | clean 4000
      echo
    fi
    echo "What failed:"
    echo
    echo '```'
    clean 12000 < "$failure_file"
    echo
    echo '```'
    echo
    echo "Production and custom were not changed. A session can check out the candidate branch, find the cause with the run's log, fix it on custom, and let the next weekly run (or \"Run workflow\") take it from there."
  } > "$body"
  number=$(gh issue list --repo "$repo" --state open --search "\"$issue_title\" in:title" --json number,title \
    --jq "[.[] | select(.title == \"$issue_title\")][0].number // empty")
  if [ -n "$number" ]; then
    gh issue comment "$number" --repo "$repo" --body-file "$body" >/dev/null
  else
    url=$(gh issue create --repo "$repo" --title "$issue_title" --body-file "$body")
    number=${url##*/}
  fi
  rm -f "$body"
  [[ "$number" =~ ^[0-9]+$ ]] && echo "$number"
  ;;

resolved)
  text=$1
  for number in $(gh issue list --repo "$repo" --state open --search "\"$issue_title\" in:title" --json number,title \
      --jq "[.[] | select(.title == \"$issue_title\")][].number"); do
    gh issue close "$number" --repo "$repo" --reason completed --comment "$(printf '%s' "$text" | clean 2000)" >/dev/null || true
  done
  ;;

*)
  sed -n '2,20p' "$0" >&2; exit 2 ;;
esac
