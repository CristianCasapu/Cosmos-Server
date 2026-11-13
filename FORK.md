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
| Incremental snapshots, a backup module of the fork's own (standard library only, nothing shared with upstream's paid backup): dated folders per source under a destination, unchanged files hard-linked to the previous snapshot, schedule and daily/weekly/monthly retention, a list of snapshots and restore from the panel with the current folder kept beside it | `src/snapshots/` (+ tests), `client/src/pages/config/snapshots.jsx`; modified: `src/utils/types.go` (`Config.Snapshots`), `src/CRON.go`, `src/httpServer.go`, `src/configapi/paths.go`, `client/src/pages/config/configGeneral.jsx`, `client/src/pages/config/index.jsx`, `client/src/api/config.ts`, `client/src/api/config.demo.ts`, `client/src/utils/activity.js` |
| Defects fixed: the configuration file is written atomically (upstream #591); the setup wizard keeps the administrator's email; "Update ports" keeps the host IP of a binding (#611) and the Volumes tab keeps read-only mounts read-only; the container-updates banner shows; wildcard alerts fire and "reset to default" writes working alerts; two page crashes (market, installer); Docker volume paths use the real location instead of `/var/lib/docker` (#478) | `src/utils/fork_atomic_write.go`, `src/metrics/fork_alerts.go`, `src/docker/fork_volume_path.go`, `client/src/pages/servapps/containers/forkContainerFixes.js` (+ tests); modified: `src/utils/utils.go`, `src/newInstall.go`, `src/metrics/alerts.go`, `src/docker/backups.go`, `client/src/pages/servapps/containers/network.jsx`, `volumes.jsx`, `docker-compose.jsx`, `client/src/pages/servapps/servapps.jsx`, `client/src/pages/dashboard/AlertPage.jsx`, `client/src/pages/market/listing.jsx` |
| Data disks mounted outside `/mnt` (here `/storage`, `/archive`) are listed in Storage, measured, and get a "disk nearly full" alert once | `src/storage/fork_datamounts.go`, `src/metrics/fork_disk_alerts.go` (+ tests); modified: `src/storage/mounts.go`, `src/metrics/system.go`, `src/utils/types.go` (`DiskAlertsSeeded`), `client/src/pages/storage/mounts.jsx`, `merges.jsx`, `client/src/pages/dashboard/AlertPage.jsx` |
| Time zone setting for containers that ask for the host's zone (`TZ=auto`): the setting, else the server's own zone | `src/utils/fork_timezone.go` (+ test), `client/src/pages/config/timezoneInput.jsx`; modified: `src/docker/api_blueprint.go`, `src/configapi/set.go`, `src/system.go`, `src/utils/types.go`, `client/src/pages/config/configGeneral.jsx`, `client/src/pages/config/index.jsx` |
| Forms that fill themselves in: the local-plus-domains address model in the new-container form and the market installer; help texts that were passed but never rendered; required fields marked and errors shown under their field; "use the image's defaults" for volumes and variables of an image already on the server; file picker shortcuts to the data path and the data disks | `client/src/components/filePickerShortcuts.jsx`, `client/src/pages/servapps/containers/imageDefaults.jsx`; modified: `client/src/pages/config/users/formShortcuts.jsx`, `containerPicker.jsx`, `client/src/components/filePicker.jsx`, `hostnameSuggestions.jsx`, `client/src/pages/servapps/containers/newServiceForm.jsx`, `docker-compose.jsx`, `setup.jsx`, `client/src/pages/config/routes/routeman.jsx`, `newRoute.jsx`, `configHTTP.jsx`, `configExternal.jsx`, `client/src/api/docker.tsx`, `storage.tsx` |
| App links are grouped on the app card and on the container's page (local addresses under a house, external ones under a globe), and a "Domains" action attaches or detaches a configured domain later: ticking one copies the app's URL under that domain, unticking deletes that copy, never the app's own URL; a line says when the router's port has to be forwarded by hand | `client/src/components/appLinks.jsx`, `client/src/utils/appLinks.js`; modified: `client/src/pages/servapps/servapps.jsx`, `client/src/pages/servapps/containers/overview.jsx`, `client/src/components/hostnameSuggestions.jsx`, `client/src/pages/servapps/exposeModal.jsx` |
| Snapshots: patterns of files and folders to leave out; a folder that had files in the last snapshot and is now empty (a disk that is not mounted) is not snapshotted and its old snapshots are not thinned out; the folders a restore keeps (`<name>.before-restore-…`) are listed and can be deleted from the panel, one at a time, after a confirmation (`POST /api/snapshots/cleanup`, admin) | `src/snapshots/kept.go` (+ tests `guards_test.go`, `kept_test.go`); modified: `src/snapshots/`, `src/utils/types.go`, `client/src/pages/config/snapshots.jsx`, `client/src/api/config.ts` |
| Two-factor codes: five wrong codes within ten minutes lock the account's second step for fifteen minutes, administrators included (upstream's limit let admins through and accepted the sixth code); and `GET /api/mfa` no longer replaces a verified secret with an unverified one (it answered any logged-in request and so switched 2FA off), a reset still goes through an administrator | `src/user/fork_mfa_limit.go`, `src/user/fork_mfa_keep.go` (+ tests); modified: `src/user/2fa_check.go`, `src/user/2fa_new.go`, `client/src/pages/authentication/newMFA.jsx` |
| Saving the settings keeps the certificate authority and the self-signed certificate with their keys (the save dropped them, so a new certificate was made and the browser asked again); the plain-HTTP port answers with a redirect to the HTTPS port for the hostnames Cosmos serves instead of 404 | `src/configapi/fork_certificates.go`, `src/fork_plain_http.go` (+ tests); modified: `src/configapi/set.go`, `src/httpServer.go` |
| Pages that reloaded too early after a save: the settings and the alerts are read again once the server answers (they kept the old values, the alerts with their controls disabled); the folder button stays level with its field in the backup settings; button rows and the alert editor fit a phone; the new URL form and a refused notification poll no longer leave script errors | modified: `client/src/pages/config/index.jsx`, `configGeneral.jsx`, `snapshots.jsx`, `client/src/pages/dashboard/AlertPage.jsx`, `client/src/pages/config/users/containerPicker.jsx`, `client/src/layout/MainLayout/Header/HeaderContent/Notification.jsx`, `scripts/fork-ui-check*.mjs` |
| Editing, updating or re-deploying a container keeps the old one (stopped, renamed `<name>-cosmos-old-<id>`) until its replacement has been created and started; on failure the old container itself is put back, running if it ran, and the reason reaches the caller (upstream removed it first and tried to create it again). A running `--rm` container is refused instead of lost | `src/docker/fork_safe_edit.go` (+ tests; live test `fork_safe_edit_live_test.go`, run only with `COSMOS_FORK_LIVE_DOCKER=1` and a `DOCKER_HOST` of its own); modified: `src/docker/docker.go`, `api_blueprint.go`, `api_managecont.go` |
| A container update changes only the memory and CPU limits it carries (the Network, Volumes, URL and terminal forms removed them) | `src/docker/fork_limits.go` (+ test); modified: `src/docker/api_updateContainer.go` |
| Terminal websockets: writes and write deadlines go through one lock per connection (a busy host terminal with pings ended the whole server) | `src/utils/fork_ws_writer.go` (+ tests `src/fork_terminal_test.go`, `src/docker/fork_terminal_test.go`); modified: `src/terminal.go`, `src/docker/api_terminal.go` |
| Mails that fail are reported: the invite answer carries `emailWasSent: false` and `emailError`; the "forgot password" form gives one answer whether or not the account exists (it told a stranger which nicknames exist), tells the admins when the mail fails, and sends one link a minute per account; the notice of a failed Let's Encrypt order says why | `src/user/fork_mail_report.go`, `src/fork_letsencrypt_reason.go` (+ tests); modified: `src/user/resend.go`, `password_reset.go`, `src/httpServer.go`, `client/src/pages/config/users/usermanagement.jsx`, `client/src/pages/authentication/forgotPassword.jsx` |
| Host ports: the port list says when a host port is already taken and by whom (another app, Cosmos itself, another row); a certain conflict or a value that is not a port holds "Next" / "Update Ports"; the review step, the market installer and the compose import warn. Ports of stopped containers are not known | `client/src/utils/portConflicts.js` (+ test), `client/src/components/portConflicts.jsx`; modified: `client/src/pages/servapps/containers/network.jsx`, `newServiceForm.jsx`, `docker-compose.jsx` |
| Schedules: the job and SnapRAID dialogs say a crontab in words with its next run, refuse what the server would not schedule (six fields, seconds first) with the reason, and offer common schedules; the lists use the same words | `client/src/utils/cronExpression.js` (+ test), `client/src/components/cronInput.jsx`; modified: `client/src/pages/cron/newJob.jsx`, `jobsManage.jsx`, `client/src/pages/storage/snapRaidDialog.jsx`, `parity.jsx` |
| Volumes page: where a volume's data is and which apps use it, or that none does | `client/src/utils/volumeUsage.js` (+ test), `client/src/components/volumeUsage.jsx`; modified: `client/src/pages/servapps/volumes.jsx` |
| "New mount" dialog: the partitions that are not mounted are offered to click | `client/src/utils/mountDevices.js` (+ test), `client/src/components/mountDeviceSuggestions.jsx`; modified: `client/src/pages/storage/mountDialog.jsx` |
| Each app card and container page says whether the app can be reached from the internet: a chip (checked / not verified / problem / if forwarded / restricted / no) that is the link when the address may work, and on the container page every check (who may open it, DNS against the server's public address from UPnP or DynDNS, router forwarding as UPnP knows it, certificate), what to do about each problem, published host ports, "Check again" and an explicit "Look up my public address". Nothing is tested from outside; only configured names are looked up, bounded and cached; addresses only for admins | `src/reach/` (+ tests), `src/fork_reachability.go`, `client/src/components/reachability.jsx`, `client/src/utils/reachability.js` (+ test); modified: `src/httpServer.go`, `client/src/pages/servapps/servapps.jsx`, `client/src/pages/servapps/containers/overview.jsx`, `client/src/api/config.ts`, `client/src/utils/activity.js` |
| Security record: every security-relevant event (logins and failures, two-factor, sessions, permissions refused, API tokens, shield strikes and bans, scans, account, settings, URL and container changes, terminals, updates) is kept with who, from where, what and outcome, in a table of the fork's own (`fork_security_events`), bounded against floods (limits per kind and address, summary rows, retention per kind). Readable by administrators through `GET /cosmos/api/security/events`, `/counts`, `/top-ips`, `/kinds`; other code can subscribe to events | `src/security/` (+ tests), `src/utils/fork_security.go`, `src/fork_security_hooks.go`, `fork_security.go` in `src/user`, `src/configapi`, `src/docker`, `src/proxy` (+ tests); one-line hooks in `src/httpServer.go`, `index.go`, `system.go`, `terminal.go`, `src/utils/loggedIn.go`, `middleware.go`, and in the login, account, settings, route, container, shield and snapshot handlers |
| `cosmos.security.log` in the configuration folder: one fixed line per incident with a client address (time, kind, address, severity, count; nothing from the request), mode 0600, rotated at 5 MB, made for Fail2ban | `src/security/logfile.go` (+ test) |
| The API answers the panel's own pages and clients only (Fetch Metadata / Origin; a request another site, another application or the address bar started gets 403 `HTTP009`), only on the server's own port, and on an application's hostname only the sign-in calls | `src/fork_same_origin.go`, `src/utils/fork_hostname_paths.go` (+ tests); modified: `src/httpServer.go`, `src/utils/middleware.go` |
| Sessions: SameSite=Lax, a malformed cookie is refused like any invalid one, a token is bound to its hostname, no path is exempt from the second factor, logout ends the session on the server | `src/user/fork_session.go`, `fork_logout_revoke.go` (+ tests); modified: `src/user/token.go`, `logout.go` |
| Accounts: a pending account answers like an unknown one at login; an administrator cannot delete or demote the own account | `src/user/fork_accounts.go` (+ test); modified: `src/user/login.go`, `delete.go`, `edit.go` |
| Icons of applications are passed on as pictures only; the icon search stays off the local network | `src/fork_icons.go` (+ test); modified: `src/icons.go` |
| Proxy: applications get the visitor's address and the proxy's headers from the server, not from the visitor; behind a trusted proxy the client address is the proxy's own entry | `src/proxy/fork_forwarded.go`, `src/utils/fork_forwarded_for.go` (+ tests); modified: `src/proxy/routeTo.go`, `shield.go`, `src/utils/utils.go` |
| Secrets carried in an address are kept out of the log and the events; `?token=` is accepted on websockets only; sign-in bodies are bounded; one log message per line | `src/utils/fork_redact_url.go`, `fork_log_safe.go`, `src/fork_api_inputs.go` (+ tests); modified: `src/utils/logger.go`, `log.go`, `middleware.go`, `src/proxy/shield.go`, `src/httpServer.go` |
| Accounts that may not read the configuration get a reduced one from `/api/config` | `src/configapi/fork_config_view.go` (+ test); modified: `src/configapi/get.go` |
| The configuration backup is written 0600 and never through a link; reset-metrics checks the method first and records the reset | `src/fork_backup_file.go`, `src/metrics/fork_reset.go` (+ tests); modified: `src/backup.go`, `src/metrics/api.go` |
| Snapshots refuse to restore or delete a folder with a disk mounted inside it | `src/snapshots/mounts_below.go` (+ test); modified: `src/snapshots/restore.go`, `kept.go` |
| The self-signed certificate lists IP addresses as addresses (valid for the server's IP once the device trusts Cosmos' authority); an application's sign-in ends on a local path | `src/utils/fork_certificate_names.go`, `src/authorizationserver/fork_redirect.go` (+ tests); modified: `src/utils/certificates.go`, `src/httpServer.go`, `src/authorizationserver/oauth2.go` |
| SmartShield never bans an address of the local network permanently (another 72-hour ban instead) | `src/proxy/fork_shield_local.go` (+ test); modified: `src/proxy/shield_bans.go`, `fork_local.go` |
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
- Upstream's test `TestEveryRouteIsDocumented` fails for the fork's routes:
  the generated API documentation (`api-docs/`) is not regenerated here, to
  keep upstream merges free of conflicts in generated files. The fork's checks
  do not run that test.
