# Fork addition: the install scripts' test bed must never be able to change
# the HOST it runs on. scripts/fork-install-e2e.sh (the host side) may hold no
# call of apt, dpkg, systemctl, install.sh or uninstall.sh and no recursive
# removal of a system path; those exist only in machine-lib.sh and
# machine-cases.sh, which run in the test machines and refuse anywhere else.
# This prints every line of the host side that breaks the rule ("<line>:
# <text>"); nothing printed means none. Comments and here-documents (the
# Dockerfiles built inside the Docker in Docker) are left out.
#
#   awk -f scripts/fork-install-e2e/host-lint.awk scripts/fork-install-e2e.sh
{
	line = $0
	if (heredoc != "") {
		t = line
		sub(/^\t+/, "", t)
		if (t == heredoc) heredoc = ""
		next
	}
	if (match(line, /<<-?[ ]*['"]?[A-Za-z_][A-Za-z0-9_]*['"]?/)) {
		h = substr(line, RSTART, RLENGTH)
		sub(/^<<-?[ ]*['"]?/, "", h)
		sub(/['"]$/, "", h)
		heredoc = h
	}
	# a comment: a # at the start of the line, or after a blank outside quotes
	code = line
	sq = 0; dq = 0
	for (i = 1; i <= length(code); i++) {
		c = substr(code, i, 1)
		if (c == "'" && !dq) sq = !sq
		else if (c == "\"" && !sq) dq = !dq
		else if (c == "#" && !sq && !dq && (i == 1 || substr(code, i - 1, 1) ~ /[ \t]/)) { code = substr(code, 1, i - 1); break }
	}
	if (code ~ /(^|[^A-Za-z0-9_.\/-])(apt-get|apt-cache|apt-mark|apt|dpkg|dpkg-query|systemctl|update-ca-certificates|unattended-upgrade)([^A-Za-z0-9_'-]|$)/ ||
	    code ~ /(^|[^A-Za-z0-9_.\/-])(bash|sh|exec|source)[ \t]+[^|;&]*install\.sh/ ||
	    code ~ /(^|[ \t;&|])(\.\/|\/)[^ \t]*install\.sh/ ||
	    code ~ /install\.sh["']?[ \t]+(--?[A-Za-z]|<)/ ||
	    code ~ /rm[ \t]+-[A-Za-z]*[rR][A-Za-z]*[ \t]+(--[ \t]+)?["']?\/(etc|opt|var|usr|lib|lib64|boot|root|home|srv|bin|sbin|run|\*|["' ]|$)/)
		printf "%d: %s\n", NR, line
}
