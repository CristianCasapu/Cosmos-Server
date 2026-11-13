# Ideas and known defects

The working list for this fork: what the owner asked for, what an audit of the
code found, and what other forks have that is worth taking. Newest requests
first. Move an item to `FORK.md` when it ships. Line references are to
upstream 0.23.4 unless noted and may have moved.

## Asked for by the owner, not done yet

1. **Forms that fill themselves in.** Done for the ServApps "New URL" dialog,
   the new-container form and the market installer (see `FORK.md`). Still to
   go through the same way: the route editor, mounts, cron jobs and alerts.
2. **App links and reachability: done** (a local address always, an external
   one per attached domain, grouped on the cards, "Domains", and a chip that
   says whether the app can be reached from the internet). Left, the owner's
   to decide: looking the public address up automatically (a setting, off by
   default) instead of on a click; a real test from outside, which needs a
   machine outside the network (a small echo service on a VPS of the owner's
   rather than a public service).
3. **Incremental backups: done** as the fork's own snapshots module, with an
   exclude list, the guard against an unmounted (empty) source and removal of
   `.before-restore-*` folders. Ideas left: optional stop/start of the apps
   around a run, encryption.
4. **"Allow HTTP on local IP" cannot work**: the session cookie is always
   marked "HTTPS only", so a login over plain HTTP does not hold. Making it
   work means weakening that cookie setting for local addresses; left as it
   is until the owner decides (HTTPS on the local address works).

5. **Security record: settings for its limits and retention** (today
   constants: 180 days, 20 000 rows per kind, 5 000 for the kinds a stranger
   can produce); container actions that upstream does over GET (`secure`,
   `auto-update`, `lazy`) leave no audit entry; streamed operations are
   recorded as accepted, not by their final outcome.

## Security audit of 2026-11-13: decisions open and findings not fixed

The findings, with where and how, are kept outside this public repository
(the owner has them). Decisions that are the owner's:

1. to 5. Done: signed releases, approval of upstream releases, the release
   workflow in two jobs, the two-hour password confirmation, logout that
   ends every session with a 30-day maximum (see `FORK.md`). Left from
   these: a spare release key in slot 2 with an offline copy (without one,
   losing the secret means one release installed by hand); a ruleset on
   `production` and two-factor on the GitHub account, since signing does not
   protect against someone who can push to `production`; repeating a
   refused command by itself after the password; "skip this upstream
   release for good".
6. Labels that come from an image (`cosmos-force-network-mode` and the like)
   are obeyed: ignore them unless set by the admin.
7. Routes that act on GET: POST only, with the UI changed to match.
8. A name for the panel instead of the bare IP address (cookies on an IP are
   shared with every HTTPS service on another port of that address).
9. A script policy (CSP) for the panel.
10. Upstream writes one event row per proxied request: stop or sample.

Not fixed, latent today (they need a limited API token or a custom role, and
none exists): permission checks on invitations and two-factor resets by a
users-only role, on configuration writes by a limited token. Until fixed,
treat any token with "Configuration" or "Users" as an admin token. The bot
filter only ever blocks an empty User-Agent. Links to apps open without
`noopener`. A refused cross-site request is logged but not yet written to the
security record.

## Dependencies: left for later (2026-11-13)

- Docker client library v26 is out of support; v28 needs changes in ten
  upstream files and v29 moves to another module: wait for upstream.
- react-router 7, vite 6 or later (build tool, end of life on 4), MUI 9,
  React 19: large migrations of upstream's frontend, not started.
- The bundled `nebula` 1.8.2 and `restic` 0.17.3 are old builds with known
  advisories, taken from upstream's release zip and checked by MD5 only:
  download current ones from their own releases with a pinned SHA-256
  (check Constellation's and existing repositories' compatibility first).
- GitHub Actions are pinned by commit but to old majors (Node 20 runtime).

## Security features: left for later (2026-11-13)

- Dashboard: thresholds of the status line are constants; "Under attack" does
  not say whether the attacking addresses are already blocked; a refused
  cross-site request is logged but not in the record.
- Audit: where "Recent activity on your account" lives (home page for now);
  the 2000-line export cap; the own-activity view hides why a login failed.
- Telegram: a "find my chat ID" button; the bell for "update available" and
  "container keeps stopping"; SMART warnings (nothing raises them); alerts
  are sorted by name, not metric; no proxy setting.
- Fail2ban: enabling single jails or banning by hand from the panel; bans of
  an IPv6 /64; SmartShield's whitelist as never-ban list; events for bans
  that expire; its never-ban list holds the LAN's IPv6 prefix as it was at
  install time (the host guard refreshes its own copy every ten minutes).
- Upstream asks `/api/me` about 47 times per page load, which eats into the
  500-requests-a-minute limit.

## Usability gaps found in the code

| # | Gap | Where |
| --- | --- | --- |
| 1 | No choice of where an app's data goes at install (config vs bulk data, per disk); named volumes land in Docker's root | install wizard, `DefaultDataPath` only |
| 3 | No system-health overview (certificate expiry, failed jobs, stopped containers, full disks); the container-updates banner never shows (`.length` on a map) | `servapps.jsx:296` |
| 10 | "Provided" certificate mode and certificate status | check again on 0.24.0, the Domains tab changed this |

(2 data disks, 4 time zone, 5 port conflicts, 6 image defaults, 7 help texts, 8 maintenance time, 9 schedules, 12 mounts, 11 file picker shortcuts, 13 volume usage, 14 volume paths and 15 alerts are done; see `FORK.md`.)

Limits of the port check (5): ports of stopped containers are not known, Cosmos' own ports are read from the saved settings (not from `COSMOS_HTTP_PORT`/`COSMOS_HTTPS_PORT`), host-network containers are not covered.

## Defects found by reading the code (upstream, not fixed here yet)

Confirmed by reading only, on 0.23.4; re-check on 0.24.0 before fixing. (The certificate wipe on save, the two-factor limit and reset, and the HTTP port's 404 are fixed; see `FORK.md`.)

- 30 minutes after any network disconnect Cosmos prunes all unused images (unless `SkipPruneImages`), which also removes this fork's build images.
- **Let's Encrypt, leftover corners** (the plain-HTTP start is gone on 0.24.0, the self-signed certificate serves when an order fails): if no self-signed certificate can be generated and none is stored, the server still starts on HTTP only; a stored certificate that does not parse ends the process; an order through a DNS provider gives a notice without a reason.
- **SmartShield and the local network** (re-checked on 0.24.0, nothing changed): a private address is treated like any other; three temporary bans in seven days give a permanent one (in memory: a restart clears it). A logged-in admin is never banned, but a device whose address is banned and whose session has expired cannot log in until the ban ends. Options, the owner's to choose: add the LAN range to the Shield whitelist (a setting), never make a ban permanent for local addresses, or switch permanent bans off.
- **A container edit interrupted by a stop of Cosmos** leaves the old container stopped as `<name>-cosmos-old-<id>`; nothing offers to put it back or removes it at the next start. A failed network reconnection during an edit is only logged.
- Route Setup/Security tabs can save stale config.

## From other forks and open upstream pull requests

Survey of 254 forks on 2026-11-12. Nearly everything useful is by one author
(madejackson: `aseracorp/Cosmos-Server`, `madejackson/Cosmos-Server`, about 35
open upstream PRs); his commits are AI-assisted, so review each. Not yet
verified by us beyond the survey.

- Bug fixes: `779d90db` atomic config write; `7f7bdca7` limits wiped on update; PR #594 streaming endpoints cut by the 45 s API timeout; PR #576 HTTP→HTTPS redirect; PR #606 admin email at setup; PR #615 installer crash on tmpfs volumes; PR #597 market page crash; `b33d811d` fatal on empty certificate pairs; PR #585 then #595 healthcheck formats; PR #627 OIDC issuer with `host:port` (auth code: review carefully); PR #577, #587 installer fixes.
- Small UI fixes: PR #598 terminal ghost keys, #603 log timestamps, #600 breadcrumbs, #575 login autofill, #570 consent page.
- Features to consider: `shm_size` (#567), exec-form command (#495), generated secrets in market templates (#586), volume options (#573, #599), memory swap limits (`0d1620ff`).
- **Never take:** `Fyve-Labs/Cosmos-Server` (bypasses the licence check), PR #602 (licence field), anything under `src/pro`, `src/constellation`, `src/backups`, `src/storage/rclone.go`, `src/utils/cloud.go`.
