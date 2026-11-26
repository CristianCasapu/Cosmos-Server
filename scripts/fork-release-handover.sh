#!/bin/bash
# Fork addition (plan 47): what the publish job signs must be exactly what
# the build job made.
#
#   bash scripts/fork-release-handover.sh <release folder> <file with the build's sums>
#
# The build job writes the SHA-256 of every file it hands over
# ("<64 hex>  <name>" lines, sha256sum's own form) into a job output; the
# publish job downloads the artifact by its ID (not by its name, which a
# later job of the run could overwrite) and runs this before signing. It
# ends 0 only when the folder holds exactly the files listed (names and
# count), each a plain file with the SHA-256 listed. Any difference: exit 1
# with an "::error::" line, and nothing may be signed.
set -euo pipefail
export LC_ALL=C

fail() { echo "::error::$*" >&2; exit 1; }
[ $# -eq 2 ] || fail "usage: fork-release-handover.sh <release folder> <sums file>"
dir=$1 sums=$2
[ -d "$dir" ] || fail "$dir is not a folder"
[ -f "$sums" ] || fail "$sums is missing"

listed=$(mktemp)
found=$(mktemp)
trap 'rm -f "$listed" "$found"' EXIT

n=0
while IFS= read -r line || [ -n "$line" ]; do
	[ -n "$line" ] || continue
	[[ $line =~ ^([0-9a-f]{64})\ \ ([A-Za-z0-9._-]{1,200})$ ]] || fail "the build's list of sums holds a line that is not '<sha256>  <name>'"
	n=$((n + 1))
	[ "$n" -le 64 ] || fail "the build's list of sums is longer than a release"
	printf '%s\n' "${BASH_REMATCH[2]}" >>"$listed"
	file=$dir/${BASH_REMATCH[2]}
	if [ ! -f "$file" ] || [ -L "$file" ]; then fail "${BASH_REMATCH[2]} is not a plain file in what was downloaded"; fi
	have=$(sha256sum -- "$file" | cut -d' ' -f1)
	[ "$have" = "${BASH_REMATCH[1]}" ] || fail "${BASH_REMATCH[2]} is not the file the build made (its SHA-256 differs): it was replaced after the build"
done <"$sums"
[ "$n" -gt 0 ] || fail "the build's list of sums is empty"

sort -o "$listed" "$listed"
[ -z "$(uniq -d "$listed")" ] || fail "the build's list of sums names a file twice"
find "$dir" -mindepth 1 -printf '%P\n' | sort >"$found"
if ! cmp -s "$listed" "$found"; then
	echo "listed by the build / downloaded:" >&2
	diff "$listed" "$found" >&2 || true
	fail "what was downloaded holds other files than the build made"
fi
echo "The $n files are those the build made."
