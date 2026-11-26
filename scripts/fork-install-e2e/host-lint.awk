# Fork addition: the install scripts' test bed must never be able to change
# the HOST it runs on. scripts/fork-install-e2e.sh (the host side) may hold:
#   - no call of apt, dpkg, systemctl, install.sh or uninstall.sh (those are
#     only in machine-lib.sh and machine-cases.sh, which run in the test
#     machines and refuse anywhere else);
#   - no command held in a variable: no eval, no "$cmd ...", bash -c "$x",
#     source "$f" or . "$f";
#   - no recursive removal but of a path under the scratch folder ("$SCRATCH/
#     or, in the build steps, "$SCR/), no xargs rm, no find -delete or -exec rm;
#   - no mount of a host path but the worktree read-only ("$HERE...:ro), the
#     scratch ("$SCRATCH...), the Go caches, and the scratch as the Docker in
#     Docker sees it (/e2e/... on a "D run" line); no --volume, no --mount.
# A line ending in "# lint-ok: <why>" is let through, only inside the
# functions in_go, in_builder and remove_marked_folder (where the build steps
# of this same file, which the lint reads too, are run, and where the one
# checked folder is emptied); the END block prints how many, for the log.
# This prints every line that breaks the rule ("<line>: <text>"); nothing
# printed means none. Comments and here-documents (the Dockerfiles built in
# the Docker in Docker) are left out.
#
#   awk -f scripts/fork-install-e2e/host-lint.awk scripts/fork-install-e2e.sh
function bad() { printf "%d: %s\n", NR, line; found++ }
{
	line = $0
	if (heredoc != "") {
		t = line
		sub(/^\t+/, "", t)
		if (t == heredoc) heredoc = ""
		next
	}
	if (match(line, /^[a-z_][a-z0-9_]*\(\) \{/)) { fn = substr(line, 1, index(line, "(") - 1) }
	else if (line ~ /^\}/) fn = ""
	if (match(line, /<<-?[ ]*['"]?[A-Za-z_][A-Za-z0-9_]*['"]?/)) {
		h = substr(line, RSTART, RLENGTH)
		sub(/^<<-?[ ]*['"]?/, "", h)
		sub(/['"]$/, "", h)
		heredoc = h
	}
	# a command continued from the line before (ends in a backslash)
	wascont = cont
	full = (cont ? full " " : "") line
	cont = (line ~ /\\$/)
	# a comment: a # at the start of the line, or after a blank outside quotes
	code = line
	comment = ""
	sq = 0; dq = 0
	for (i = 1; i <= length(code); i++) {
		c = substr(code, i, 1)
		if (c == "'" && !dq) sq = !sq
		else if (c == "\"" && !sq) dq = !dq
		else if (c == "#" && !sq && !dq && (i == 1 || substr(code, i - 1, 1) ~ /[ \t]/)) { comment = substr(code, i); code = substr(code, 1, i - 1); break }
	}
	if (comment ~ /^# lint-ok: /) {
		if (fn == "in_go" || fn == "in_builder" || fn == "remove_marked_folder") { exempt++; next }
		bad(); next
	}
	if (code ~ /(^|[^A-Za-z0-9_.\/-])(apt-get|apt-cache|apt-mark|apt|dpkg|dpkg-query|systemctl|update-ca-certificates|unattended-upgrade)([^A-Za-z0-9_'-]|$)/ ||
	    code ~ /(^|[^A-Za-z0-9_.\/-])(bash|sh|exec|source)[ \t]+[^|;&]*install\.sh/ ||
	    code ~ /(^|[ \t;&|])(\.\/|\/)[^ \t]*install\.sh/ ||
	    code ~ /install\.sh["']?[ \t]+(--?[A-Za-z]|<)/) { bad(); next }
	# a command held in a variable
	if (code ~ /(^|[^A-Za-z0-9_-])eval([ \t]|$)/ ||
	    (!wascont && code ~ /^[ \t]*"?\$\{?[A-Za-z_@*][A-Za-z0-9_]*(\[[@*]\])?\}?"?([ \t;]|$)/) ||
	    code ~ /([;&|({]|\$\(|then|do|else)[ \t]*"?\$\{?[A-Za-z_@*][A-Za-z0-9_]*(\[[@*]\])?\}?"?([ \t;]|$)/ ||
	    code ~ /(^|[^A-Za-z0-9_-])(bash|sh)[ \t]+-c[ \t]+"?\$/ ||
	    code ~ /(^|[;&|({]|then|do|else)[ \t]*(source|\.)[ \t]+"?\$/) { bad(); next }
	# removals
	if (code ~ /xargs[^|;]*[ \t]rm([ \t]|$)/ || code ~ /find[^|;]*[ \t](-delete|-exec[ \t]+rm)/) { bad(); next }
	rest = code
	while (match(rest, /(^|[^A-Za-z0-9_-])rm[ \t]+-[A-Za-z]*[rR][A-Za-z]*[ \t]+(--[ \t]+)?[^ \t;&|]+/)) {
		r = substr(rest, RSTART, RLENGTH)
		sub(/^.*rm[ \t]+-[A-Za-z]*[ \t]+(--[ \t]+)?/, "", r)
		if (r !~ /^"\$(SCRATCH|SCR)\/[^"]/) { bad(); next }
		rest = substr(rest, RSTART + RLENGTH)
	}
	# mounts
	if (code ~ /(^|[ \t])--(volume|mount)([ \t=]|$)/) { bad(); next }
	rest = code
	while (match(rest, /(^|[ \t(])-v[ \t]+("[^"]*"|[^ \t)]+)/)) {
		v = substr(rest, RSTART, RLENGTH)
		sub(/^[ \t(]*-v[ \t]+/, "", v)
		rest = substr(rest, RSTART + RLENGTH)
		if (v ~ /\$\(|`/) { bad(); next } # a path computed on the spot
		if (v !~ /:/) continue # a volume of the container's own, no host path
		if (v ~ /^"\$HERE[^:]*:[^:"]+:ro"$/) continue
		if (v ~ /^"\$SCRATCH[\/:]/) continue
		if (v ~ /^\/storage\/dev\/\.cache\/go-(mod|build):/) continue
		if (v ~ /^"?\/e2e\// && full ~ /(^|[ \t(])D run /) continue
		bad(); next
	}
}
END { printf "lint-ok lines (in in_go, in_builder, remove_marked_folder): %d\n", exempt > "/dev/stderr" }
