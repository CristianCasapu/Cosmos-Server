#!/bin/sh
# Fork addition: a stand-in for a protection piece (fork/<piece>/install.sh
# or uninstall.sh) that follows the interface install.sh calls
# (/storage/dev/plans/47-install-interface.md, kept in the plan): the test
# bed puts it in place of the real pieces in its test release only, so the
# installer's side of the interface is tested without the pieces of
# branch work/guard. It writes /etc/cosmos-e2e-stub-<piece> and records it;
# /etc/cosmos-e2e-stub-<piece>.exit, when present, holds the exit code to
# give instead (to try the installer's handling of 1, 3, 4);
# /etc/cosmos-e2e-stub-<piece>.packages, the packages --print-packages names.
set -eu
piece=$(basename "$(dirname "$0")")
mode=install
case $(basename "$0") in uninstall.sh) mode=uninstall ;; esac
record="" yes=0
all="$*"
while [ $# -gt 0 ]; do
	case $1 in
	--print-packages) cat "/etc/cosmos-e2e-stub-$piece.packages" 2>/dev/null || true; exit 0 ;;
	--yes) yes=1 ;;
	--dry-run) exit 0 ;;
	--record) record=$2; shift ;;
	--cosmos-config | --https-port | --http-port | --log | --port | --home | --ipv4 | --confirm-timeout | --ignore | --ssh | --ssh-allowance-hours) shift ;;
	--allow-ssh-from-outside | --accept-nftables-service | --sshd | --no-sshd | --no-cosmos-jails | --exposed) ;;
	*) echo "unknown flag $1" >&2; exit 2 ;;
	esac
	shift
done
[ "$yes" = 1 ] || { echo "the stub only runs with --yes" >&2; exit 2; }
echo "args ok" >"/run/cosmos-e2e-stub-$piece.$mode"
printf '%s\n' "$all" >"/run/cosmos-e2e-stub-$piece.$mode.args"
if [ "$mode" = uninstall ]; then
	rm -f "/etc/cosmos-e2e-stub-$piece"
	exit 0
fi
if [ -f "/etc/cosmos-e2e-stub-$piece.exit" ]; then
	code=$(cat "/etc/cosmos-e2e-stub-$piece.exit")
	echo "stub $piece: refused on purpose" >&2
	exit "$code"
fi
echo "installed by the stub" >"/etc/cosmos-e2e-stub-$piece"
[ -z "$record" ] || printf '%s\tfile\t%s\n%s\tstatus\tinstalled\n' "$piece" "/etc/cosmos-e2e-stub-$piece" "$piece" >>"$record"
echo "stub $piece installed"
