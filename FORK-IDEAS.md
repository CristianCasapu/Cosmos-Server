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

## Usability gaps found in the code

| # | Gap | Where |
| --- | --- | --- |
| 1 | No choice of where an app's data goes at install (config vs bulk data, per disk); named volumes land in Docker's root | install wizard, `DefaultDataPath` only |
| 3 | No system-health overview (certificate expiry, failed jobs, stopped containers, full disks); the container-updates banner never shows (`.length` on a map) | `servapps.jsx:296` |
| 10 | "Provided" certificate mode and certificate status | check again on 0.24.0, the Domains tab changed this |
| 12 | A permanent mount is written to `/etc/fstab` by device path (`/dev/sdb1`, not a UUID) and without `nofail`: a missing or renamed disk can stop the boot. The server does not check that the mount point is under `/mnt` (only the form does) | `src/storage/mounts.go` |

(2 data disks, 4 time zone, 5 port conflicts, 6 image defaults, 7 help texts, 8 maintenance time, 9 schedules, 11 file picker shortcuts, 13 volume usage, 14 volume paths and 15 alerts are done; see `FORK.md`.)

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
