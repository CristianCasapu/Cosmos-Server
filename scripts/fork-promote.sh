#!/bin/sh
# Fork addition: promotes the head of `custom` to `production`, which releases
# it and, through the server's own updater, deploys it. Refuses unless the
# checks passed for exactly that commit and production can simply be moved
# forward to it.
set -eu

# Work in the repository this script belongs to, wherever it is called from.
cd "$(dirname "$0")/.."

repo=$(git remote get-url origin | sed -E 's#^(https://github\.com/|git@github\.com:)##; s#\.git$##')

git fetch --quiet origin custom production
sha=$(git rev-parse origin/custom)

if [ "$(git rev-parse origin/production)" = "$sha" ]; then
  echo "production is already at the head of custom ($sha)."
  exit 0
fi

if ! git merge-base --is-ancestor origin/production "$sha"; then
  echo "production has commits that custom lacks (an upstream sync or a rollback)." >&2
  echo "Merge origin/production into custom, push, wait for the checks, then promote." >&2
  exit 1
fi

result=$(gh run list --repo "$repo" --workflow fork-ci.yml --commit "$sha" \
  --json status,conclusion --jq '.[0] | (.status // "") + " " + (.conclusion // "")')
if [ "$result" != "completed success" ]; then
  echo "The checks have not passed for $sha (state: ${result:-no run})." >&2
  echo "See: gh run list --repo $repo --workflow fork-ci.yml" >&2
  exit 1
fi

echo "Promoting to production:"
git log --oneline origin/production.."$sha"
git push origin "$sha:refs/heads/production"
echo "Pushed. The release is building: gh run watch --repo $repo"
