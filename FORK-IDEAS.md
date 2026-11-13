# Ideas and known defects

The working list for this fork: what the owner asked for, what an audit of the
code found, and what other forks have that is worth taking. Newest requests
first. Move an item to `FORK.md` when it ships. Line references are to
upstream 0.23.4 unless noted and may have moved.

## Asked for by the owner, not done yet

1. **Forms that fill themselves in.** Done for the ServApps "New URL" dialog,
   the new-container form and the market installer (see `FORK.md`). Still to
   go through the same way: the route editor, mounts, cron jobs and alerts.
2. **App links: done** (a local address always, an external one per attached
   domain, grouped on the cards, attached or detached later through
   "Domains"). Left: the hint about the router port only knows whether UPnP
   is switched on, not whether the port is really reachable from outside.
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
| 5 | Host ports: no conflict check before a container is created, no overview | container forms |
| 9 | Cron expressions are typed by hand in three forms (no presets, no validation) | cron, backups, SnapRAID forms; PR #596 upstream adds 5-field crontabs |
| 10 | "Provided" certificate mode and certificate status | check again on 0.24.0, the Domains tab changed this |
| 12 | Mounting a disk: device typed by hand, fstab entry by `/dev/sdX` without `nofail` | storage mounts dialog |
| 13 | Docker volumes: no location, size or "used by" | volumes page |

(2 data disks, 4 time zone, 6 image defaults, 7 help texts, 8 maintenance time, 11 file picker shortcuts, 14 volume paths and 15 alerts are done; see `FORK.md`.)

## Defects found by reading the code (upstream, not fixed here yet)

Confirmed by reading only, on 0.23.4; re-check on 0.24.0 before fixing. (The certificate wipe on save, the two-factor limit and reset, and the HTTP port's 404 are fixed; see `FORK.md`.)

- **SmartShield can ban a LAN device permanently**, including from the Cosmos UI (`src/proxy/shield.go`; 0.24.0 rewrote the shield and added a whitelist: re-check).
- **Editing a container removes it before creating the new one**, with no way back if creation fails; memory and CPU limits are reset by any partial update (fix in `aseracorp/unstable` commit `7f7bdca7`).
- **If Let's Encrypt fails the server starts on plain HTTP only**, although it announces a self-signed fallback.
- 30 minutes after any network disconnect Cosmos prunes all unused images (unless `SkipPruneImages`), which also removes this fork's build images.
- Host terminal: concurrent websocket writes can crash the process; route Setup/Security tabs can save stale config; setup wizard loses the admin email (upstream PR #606, one line); invite and reset mails report "sent" on failure.

## From other forks and open upstream pull requests

Survey of 254 forks on 2026-11-12. Nearly everything useful is by one author
(madejackson: `aseracorp/Cosmos-Server`, `madejackson/Cosmos-Server`, about 35
open upstream PRs); his commits are AI-assisted, so review each. Not yet
verified by us beyond the survey.

- Bug fixes: `779d90db` atomic config write; `7f7bdca7` limits wiped on update; PR #594 streaming endpoints cut by the 45 s API timeout; PR #576 HTTP→HTTPS redirect; PR #606 admin email at setup; PR #615 installer crash on tmpfs volumes; PR #597 market page crash; `b33d811d` fatal on empty certificate pairs; PR #585 then #595 healthcheck formats; PR #627 OIDC issuer with `host:port` (auth code: review carefully); PR #577, #587 installer fixes.
- Small UI fixes: PR #598 terminal ghost keys, #603 log timestamps, #600 breadcrumbs, #575 login autofill, #570 consent page.
- Features to consider: `shm_size` (#567), exec-form command (#495), generated secrets in market templates (#586), volume options (#573, #599), memory swap limits (`0d1620ff`).
- **Never take:** `Fyve-Labs/Cosmos-Server` (bypasses the licence check), PR #602 (licence field), anything under `src/pro`, `src/constellation`, `src/backups`, `src/storage/rclone.go`, `src/utils/cloud.go`.
