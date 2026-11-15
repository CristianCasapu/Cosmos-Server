package utils

// Fork addition: the kinds and the English of the notifications about the
// automatic dependency updates (src/fork_deps_status.go). Kept in a file of
// their own and added to the catalogue when the package loads, so that the
// catalogue's file is not edited for them; the same keys are in
// translation.json (TestNotificationTextsAreInThePanel covers them).

var depsNotificationKinds = map[string]notificationKind{
	"deps.updated": {"updates", "notif.deps.updated.title"},
	"deps.failed":  {"updates", "notif.deps.failed.title"},
	"deps.paused":  {"updates", "notif.deps.paused.title"},
	"deps.quiet":   {"updates", "notif.deps.quiet.title"},
	"deps.stale":   {"updates", "notif.deps.stale.title"},
}

var depsNotificationTexts = map[string]string{
	"notif.deps.updated.title":           "Dependencies were updated automatically",
	"notif.deps.updated.what.tonight":    "Dependencies were updated automatically: {{summary}}, in release {{release}}, which this server installs tonight at {{at}}.",
	"notif.deps.updated.what.at":         "Dependencies were updated automatically: {{summary}}, in release {{release}}, which this server installs at {{at}}.",
	"notif.deps.updated.what.manual":     "Dependencies were updated automatically: {{summary}}, in release {{release}}: install it from Configuration > General.",
	"notif.deps.updated.what.installed":  "Dependencies were updated automatically: {{summary}}, in release {{release}}, which is already installed.",
	"notif.deps.updated.means":           "Only versions of dependencies changed. The release passed the build, every test and a check in a browser before it was published, and it is signed like every release.",
	"notif.deps.updated.doing.auto":      "Cosmos installs it during the nightly maintenance, after checking its signature.",
	"notif.deps.updated.doing.manual":    "Automatic updates are off on this server: nothing is installed until you do it.",
	"notif.deps.failed.title":            "The automatic dependency update failed",
	"notif.deps.failed.what":             "The automatic dependency update failed its checks: nothing was published; it needs a session.",
	"notif.deps.failed.what.release":     "The automatic dependency update passed its checks, but its release could not be published: nothing reached the servers; it needs a session.",
	"notif.deps.failed.what.customMoved": "The automatic dependency update stopped because the custom branch changed while it worked: nothing was published; the next run starts again.",
	"notif.deps.failed.means":            "This server keeps running its current version. The changes that were tried are kept on GitHub for the session.",
	"notif.deps.failed.means.customMoved": "Nothing is broken: this server keeps running its current version.",
	"notif.deps.failed.doing":            "The next weekly run starts again from the current code.",
	"notif.deps.paused.title":            "The automatic dependency updates are paused",
	"notif.deps.paused.what":             "The automatic dependency updates are paused on GitHub: dependencies are not updated until they are switched on again.",
	"notif.deps.paused.means":            "While they are paused, Cosmos does not warn that the weekly run is missing.",
	"notif.deps.quiet.title":             "No dependency needed updating",
	"notif.deps.quiet.what":              "Dependencies are checked every week: nothing needed updating since {{date}}.",
	"notif.deps.quiet.recovered":         "The automatic dependency update ran again on {{date}} without a problem: nothing needed updating.",
	"notif.deps.stale.title":             "The automatic dependency update has not run",
	"notif.deps.stale.what":              "The automatic dependency update has not run since {{date}}: the workflow may be disabled on GitHub.",
	"notif.deps.stale.means":             "GitHub switches off the scheduled workflows of a repository without activity for 60 days. Without the weekly run, dependencies are neither updated nor checked for known vulnerabilities.",
	"notif.step.deps.general":            "Open Configuration > General (server updates)",
	"notif.step.deps.run":                "Look at the run on GitHub: {{link}}",
	"notif.step.deps.issue":              "Open the issue on GitHub and start a session: {{link}}",
	"notif.step.deps.actions":            "Open the repository's Actions on GitHub and enable the workflow fork-deps-update: {{link}}",
	"notif.fact.deps.minor":              "Minor and patch updates",
	"notif.fact.deps.major":              "Major updates",
	"notif.fact.deps.ranAt":              "Last run",
	"notif.fact.deps.run":                "Run",
	"notif.fact.deps.issue":              "Issue",
	"notif.fact.deps.step":               "Failed step",
	"notif.fact.deps.change":             "Updated",
	"notif.fact.deps.more":               "Not listed here",
}

func init() {
	for kind, k := range depsNotificationKinds {
		notificationKinds[kind] = k
	}
	for key, text := range depsNotificationTexts {
		notificationTexts[key] = text
	}
}
