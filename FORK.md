# About this fork

This is a personal fork of [Cosmos-Server](https://github.com/azukaar/Cosmos-Server)
by Yann Stepienik, kept for private use on one server. It is not affiliated
with or endorsed by the original project. The original licence (`LICENCE`:
Apache 2.0 with Commons Clause and Anti Tampering Clause) applies unchanged,
and nothing here alters any paid feature or licensing mechanism.

## Branches

- `master` mirrors the original project.
- `custom` (default) is where the fork is developed: the original plus the
  changes below. Every push is built, tested and started once
  (`fork-ci.yml`), but nothing is released from it.
- `production` is what the server runs. Releases are built from it and from
  nothing else, and the server installs only releases. It moves forward in
  two ways: `custom` is promoted to it, or the daily sync brings in an
  upstream release that passed the checks.
- `sync/<tag>` exists only while an upstream release is being checked, or when
  it failed its checks.

## What is changed

| Change | Files |
| --- | --- |
| "Send test email" in the SMTP settings, usable before saving; reports each step of the attempt | `src/configapi/emailcheck.go`, `src/utils/emails_deliver.go` (+ test), `client/src/pages/config/emailTest.jsx`; modified: `src/httpServer.go`, `client/src/pages/config/configGeneral.jsx`, `client/src/api/config.ts`, `client/src/api/config.demo.ts`, `client/src/utils/locales/en/translation.json` |
| Email delivery: connection timeouts, and implicit TLS when the port is 465 | `src/utils/emails_deliver.go`; modified: `src/utils/emails.go` |
| Skin (font, colours, surfaces) and a light/dark switch | `client/public/skin/`; modified: `client/index.html`, `vite.config.js` |
| "Internet access" in the HTTP settings: shows which hostname Cosmos accepts and can ask the router, over UPnP, to forward Cosmos' own HTTPS port (and, only if asked, the HTTP port). Off by default; talks to the default gateway only; mappings are leased for one hour and renewed | `src/upnp/` (+ tests), `client/src/pages/config/internetAccess.jsx`; modified: `src/utils/types.go` (`HTTPConfig.UPnP`), `src/httpServer.go`, `client/src/pages/config/configHTTP.jsx`, `client/src/pages/config/index.jsx`, `client/src/api/config.ts`, `client/src/api/config.demo.ts`, `client/src/utils/locales/en/translation.json` |
| `.local` names are published again when Avahi loses them (daemon restart, machine renamed after a name conflict, Avahi not yet running when Cosmos started); upstream publishes them once and they stay dead until Cosmos restarts (upstream issue #507) | `src/proxy/avahi_watch.go`, `src/proxy/avahi_live_test.go`; modified: `src/proxy/avahi.go` |
| Containers created by Cosmos keep Docker's default stop timeout instead of 0 (they were killed on stop, upstream issue #626), and one created with 0 is healed the next time it is edited; changing a container that was removed meanwhile reports that instead of an internal error | `src/docker/fork_fixes.go` (+ test); modified: `src/docker/api_blueprint.go`, `src/docker/docker.go`, `src/docker/api_autoupdate.go` |
| Updates come from this fork's releases instead of the original's; fork releases are ordered by build number, and the newest release is chosen by version instead of by position in GitHub's list | modified: `src/update.go` (+ `src/update_fork_test.go`), `src/CRON.go` |
| Server updates: the "new version" notice follows this fork's releases (checked after start and every six hours) and names the version; the settings show running and latest version with "check" (installs nothing) and "update now" (asks first); the nightly maintenance and automatic update run at a configurable time (`MaintenanceTime`, default 02:00); a restart no longer installs an update by itself; the running version is no longer sent to upstream's version service | `src/fork_updates.go` (+ test), `client/src/components/serverUpdate.jsx`; modified: `src/CRON.go`, `src/index.go`, `src/httpServer.go`, `src/utils/types.go`, `client/src/pages/config/configGeneral.jsx`, `client/src/pages/config/index.jsx`, `client/src/pages/home/index.jsx`, `client/src/api/config.ts`, `client/src/api/config.demo.ts`, `client/src/utils/locales/en/translation.json` |
| Feedback for commands: a message with the outcome (done or failed, with the server's reason) for every command the UI sends, and for commands that take longer than a second a floating line saying what runs, for how long, how many calls are done ("5/6") and the step in progress. Done once, where the API client sends requests; upstream's own error bar now shows in the same place | `client/src/utils/activity.js`, `client/src/components/activityIndicator.jsx`; modified: `client/src/api/client.ts`, `client/src/App.jsx`, `client/src/utils/locales/en/translation.json` |
| Tooltips on icon-only buttons everywhere (they were missing, and on the ServApps action bar present but never shown); a button the user may not use can no longer be triggered from the keyboard | modified: `client/src/components/` (`permissionGuard`, `delete`, `MenuButton`, `back`, `filePicker`, `responseiveButton`), `client/src/pages/servapps/actionBar.jsx`, `deleteModal.jsx` and some twenty screens, each with a `Fork addition` note |
| Addresses to pick from when an app gets a URL: one per domain configured under Configuration > Domains, a `.local` name, and upstream's default; the ServApps "New URL" dialog starts on the first | `client/src/components/hostnameSuggestions.jsx`; modified: `client/src/pages/config/routes/routeman.jsx`, `client/src/pages/servapps/exposeModal.jsx` |
| UPnP: after a failed attempt Cosmos stops asking the router until an admin presses "Check now" (or the settings change), says so in the card and notifies the admins once; the status can be read without confirming the password | modified: `src/upnp/manager.go` (+ test), `client/src/pages/config/internetAccess.jsx` |
| Settings: the backup output directory and the default data path are checked in the form and again by the server on save; a backup directory that does not exist is created (with its parents, owner-only), and the save is refused with the reason when it cannot be used | `src/configapi/paths.go` (+ test); modified: `src/configapi/set.go`, `client/src/pages/config/index.jsx` |
| App links: every app gets a local address (a `.local` name when mDNS publishing is on, else upstream's default) and, for each domain the admin attaches in the ServApps "New URL" dialog, an external address as a second route. Local addresses (`.local` names, private IPs) are refused to visitors from outside the server's own networks, so a forwarded port does not expose them to someone who guesses the name | `src/proxy/fork_local.go` (+ test); modified: `src/proxy/routerGen.go`, `client/src/components/hostnameSuggestions.jsx`, `client/src/pages/servapps/exposeModal.jsx` |
| The configuration backup runs on a schedule set in the settings (every 2, 6 or 12 hours, daily or weekly, from a chosen time) instead of always every two hours | `src/fork_backup_schedule.go` (+ test); modified: `src/CRON.go`, `src/utils/types.go`, `client/src/pages/config/configGeneral.jsx`, `client/src/pages/config/index.jsx` |
| The version shown in the menu includes the fork build | modified: `client/src/layout/MainLayout/Drawer/index.jsx` |
| The Support menu links to this fork | modified: `client/src/menu-items/support.jsx`, `client/src/utils/locales/en/translation.json` |
| Checks, release, upstream sync and rollback automation | `.github/actions/fork-build/`, `.github/workflows/fork-ci.yml`, `fork-release.yml`, `fork-sync.yml`, `fork-rollback.yml`, `scripts/fork-promote.sh` |

## Versions and updates

A fork release is named `<upstream version>-fork.<build>`: `0.24.0-fork.7` is
the seventh fork release and is based on upstream `0.24.0`. The build number
counts every fork release and never restarts. The server orders fork releases
by build number alone, so a higher build always replaces a lower one, whatever
upstream version each is based on. (Builds 1 to 3 were published under an
older scheme, as `0.23.4001` to `0.23.4003`.)

The UI and `package.json` keep upstream's plain version, because that is what
"requires Cosmos x.y.z" in the app market refers to; the menu shows it with the
fork build appended.

### From a change to the server

1. Work lands on `custom`. Each push runs the checks: UI build, the fork's
   tests, server and launcher build, and one start of the built server.
2. `scripts/fork-promote.sh` moves `production` forward to the head of
   `custom`. It refuses unless the checks passed for exactly that commit and
   `custom` already contains everything on `production`.
3. A push to `production` builds and publishes a release (`fork-release.yml`;
   pushes that only change Markdown are skipped). The same checks run again on
   the release build, so nothing is published that has not started once.
4. The server looks for a newer release during the night and installs it.

### Upstream releases

Once a day `fork-sync.yml` takes upstream's latest stable release, once it has
been out for three days (`MIN_AGE_DAYS`; "Run workflow" with `ignore_age`
skips the wait). It merges the release on top of `production` as a candidate
branch `sync/<tag>` and runs the checks on it. If they pass, `production`
moves to the candidate, is released, and the merge is brought into `custom`.
If the merge conflicts, cannot be pushed (the workflow token may not push
changes under `.github/workflows`) or fails the checks, `production` stays as
it is and an issue is opened; merge the release on `custom` by hand and
promote it.

### Going back

"Roll production back" (`fork-rollback.yml`, run from the Actions tab with a
release tag such as `v0.24.0-fork.7`) puts the files of that release back on
`production` as a new commit and releases it under the next build number,
which the server installs like any update. Going back across an upstream
version is at your own risk: settings already migrated by the newer version
are not migrated back. Afterwards `custom` must merge `production` (which
undoes the change there too) before it can be promoted again.

### Good to know

- GitHub pauses scheduled workflows in a repository with no activity for 60
  days; if that happens, re-enable "Sync with upstream" in the Actions tab.
- A fork install must never be pointed back at upstream's releases: a fork
  version is not comparable with upstream's numbering.
