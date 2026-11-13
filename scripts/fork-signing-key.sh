#!/bin/sh
# Fork addition: makes the key releases of this fork are signed with.
#
# The server installs a release only when its signature file verifies under a
# public key compiled into it (src/forkrelease/keys.go, two slots). This
# script makes an Ed25519 key pair, stores the PRIVATE half as a secret of
# the GitHub Actions environment "release" (which only the branch
# `production` may use) and prints the PUBLIC half as the line to paste into
# keys.go. The private key is never printed, never passed as an argument, and
# exists as a file only in a private temporary folder that is removed when
# the script ends.
#
#   scripts/fork-signing-key.sh                 first key, slot 1
#   scripts/fork-signing-key.sh --slot 2        a second key (to replace the
#                                               first one later, or as a spare)
#   --replace                 overwrite the secret of that slot (the old key
#                             is gone for good: releases signed with it stay
#                             valid, new ones can no longer be made with it)
#   --offline-copy FILE       also write the private key to FILE (mode 600),
#                             for a copy kept away from GitHub
#   --repo OWNER/NAME         default: the repository of this checkout
#
# Needs: openssl (3.x), gh (logged in as the owner of the repository).
set -eu

slot=1
replace=no
offline=""
repo=""

while [ $# -gt 0 ]; do
  case "$1" in
    --slot) slot="${2:-}"; shift 2 ;;
    --replace) replace=yes; shift ;;
    --offline-copy) offline="${2:-}"; shift 2 ;;
    --repo) repo="${2:-}"; shift 2 ;;
    -h|--help) sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "Unknown argument: $1 (see --help)" >&2; exit 2 ;;
  esac
done

case "$slot" in 1|2) ;; *) echo "--slot is 1 or 2" >&2; exit 2 ;; esac
secret="FORK_RELEASE_SIGNING_KEY_$slot"
environment="release"
branch="production"

command -v openssl >/dev/null || { echo "openssl is not installed" >&2; exit 1; }
command -v gh >/dev/null || { echo "gh is not installed" >&2; exit 1; }

if [ -z "$repo" ]; then
  cd "$(dirname "$0")/.."
  repo=$(git remote get-url origin | sed -E 's#^(https://github\.com/|git@github\.com:)##; s#\.git$##')
fi
case "$repo" in
  */*) ;;
  *) echo "Cannot tell the repository; give --repo OWNER/NAME" >&2; exit 1 ;;
esac

if [ -n "$offline" ] && [ -e "$offline" ]; then
  echo "$offline exists already; choose another name for the offline copy" >&2; exit 1
fi

gh auth status >/dev/null 2>&1 || { echo "gh is not logged in (gh auth login)" >&2; exit 1; }

# The environment that holds the key: only workflow runs on `production` may
# use it, so a workflow started on any other branch cannot read the key.
gh api --silent -X PUT "repos/$repo/environments/$environment" --input - <<EOF
{"deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true}}
EOF
policies=$(gh api "repos/$repo/environments/$environment/deployment-branch-policies" --jq '.branch_policies[].name')
if ! printf '%s\n' "$policies" | grep -qx "$branch"; then
  gh api --silent -X POST "repos/$repo/environments/$environment/deployment-branch-policies" -f name="$branch" -f type=branch
fi
policies=$(gh api "repos/$repo/environments/$environment/deployment-branch-policies" --jq '.branch_policies[].name')
if [ "$policies" != "$branch" ]; then
  echo "The environment \"$environment\" of $repo may be used by:" >&2
  printf '%s\n' "$policies" | sed 's/^/  /' >&2
  echo "It must be usable by the branch \"$branch\" only. Correct it under Settings > Environments > $environment, then run this again." >&2
  exit 1
fi

if gh secret list --repo "$repo" --env "$environment" --json name --jq '.[].name' | grep -qx "$secret"; then
  if [ "$replace" != yes ]; then
    echo "The secret $secret exists already in the environment \"$environment\"." >&2
    echo "Nothing was changed. To make a second key use --slot $((3 - slot)); to throw this one away and make a new one add --replace." >&2
    exit 1
  fi
  echo "Replacing the secret $secret: the key it held can no longer sign."
fi

# The private key lives in memory-backed storage where there is some, in a
# folder only this user can enter, and is removed however the script ends.
umask 077
base=/dev/shm
[ -d "$base" ] && [ -w "$base" ] || base="${TMPDIR:-/tmp}"
work=$(mktemp -d "$base/fork-signing-key.XXXXXXXX")
cleanup() { rm -rf "$work"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

openssl genpkey -algorithm ed25519 -out "$work/key.pem" 2>/dev/null

# The public key as keys.go holds it: base64 of the 32 raw bytes.
public=$(openssl pkey -in "$work/key.pem" -pubout -outform DER | tail -c 32 | base64 | tr -d '\n')
if [ "${#public}" -ne 44 ]; then
  echo "openssl did not produce an Ed25519 key in the expected form" >&2; exit 1
fi

# Prove the pair works the way the release workflow and the server use it.
printf 'cosmos-fork-release-v1\nversion 0.0.0-fork.1\n' > "$work/message"
openssl pkeyutl -sign -inkey "$work/key.pem" -rawin -in "$work/message" -out "$work/signature"
{ printf '\060\052\060\005\006\003\053\145\160\003\041\000'; printf '%s' "$public" | base64 -d; } > "$work/public.der"
openssl pkey -pubin -inform DER -in "$work/public.der" -out "$work/public.pem"
openssl pkeyutl -verify -pubin -inkey "$work/public.pem" -rawin -in "$work/message" -sigfile "$work/signature" >/dev/null \
  || { echo "The new key does not verify its own signature; nothing was stored" >&2; exit 1; }

# From standard input: the key is in no argument list and no shell history.
gh secret set "$secret" --repo "$repo" --env "$environment" < "$work/key.pem"

if [ -n "$offline" ]; then
  cp "$work/key.pem" "$offline"
  chmod 600 "$offline"
fi

cat <<EOF

The private key is stored as the secret $secret of the environment
"$environment" of $repo (usable by the branch "$branch" only).

Paste this line into src/forkrelease/keys.go, in place of slot $slot:

	"$public", // slot $slot, made $(date -u +%Y-%m-%d)

Then commit, let the checks pass and promote: the next release is signed with
this key, and servers that installed it accept only releases signed with a
key in keys.go.
EOF

if [ -n "$offline" ]; then
  cat <<EOF

A copy of the private key is in $offline (mode 600). Whoever has that file can
sign releases your server installs as root: move it to storage that is not on
this server (a USB stick in a drawer), and delete it here.
EOF
else
  cat <<EOF

No copy of the private key exists outside GitHub (GitHub never shows a secret
again). If the secret is deleted, or you lose the GitHub account, releases can
only be signed again with a key the installed server already trusts: a second
key (--slot 2, with --offline-copy FILE if you want one kept away from
GitHub). Without one the server has to be updated by hand once.
EOF
fi
