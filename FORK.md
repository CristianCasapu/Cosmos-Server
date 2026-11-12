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
| Updates come from this fork's releases instead of the original's; fork releases are ordered by build number, and the newest release is chosen by version instead of by position in GitHub's list | modified: `src/update.go` (+ `src/update_fork_test.go`), `src/CRON.go` |
| The version shown in the menu includes the fork build | modified: `client/src/layout/MainLayout/Drawer/index.jsx` |
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
