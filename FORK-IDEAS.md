# Ideas and known defects

The working list for this fork: what the owner asked for, what an audit of the
code found, and what other forks have that is worth taking. Newest requests
first. Move an item to `FORK.md` when it ships. Line references are to
upstream 0.23.4 unless noted and may have moved.

## Asked for by the owner, not done yet

1. **Forms that fill themselves in.** Done: the "New URL" dialog and the URL
   Setup tab (simple view), the new-container form and the market installer
   (addresses), users, scheduled jobs, the email settings, the Cloudflare
   set-up (see `FORK.md`; the principles are kept with the owner's plans).
   The list is done (HTTP settings, URL protection, disk format, alerts,
   API tokens, OpenID clients, the storage dialogs, the domain dialog, the
   small forms; see `FORK.md`). Left from it: `api-docs/` does not know the
   fork's new routes, fields and codes; no "test the provider credentials"
   check for a domain added by hand; a folder a failed mount created stays
   behind; creating a SnapRAID setup still waits for running jobs; a mount
   under /mnt that is not a data disk can no longer be unmounted from the
   panel; `github.com/robfig/cron/v3` is imported directly but marked
   indirect in `go.mod`. The owner's to change if wanted: a save that would
   lock the admin out is refused with no override; format and unmount need
   the typed name (outside scripts using the API without it are refused);
   the Strict level's 500 requests a minute; merges combine local data disks
   only; OpenID accepts a phone app's own scheme besides https; the parity
   proposal is the smallest disk that is large enough; whether Cloudflare's
   ranges are offered under trusted proxies at all.
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
6., 7., 9. and 10. Done on the owner's decision of 2026-11-14: labels an
   image carries are not obeyed, commands take POST only, the panel has a
   script policy, request events are sampled (see `FORK.md`).
8. A name for the panel instead of the bare IP address (cookies on an IP are
   shared with every HTTPS service on another port of that address).

The latent permission findings, the bot filter, `noopener` and the record of
refused cross-site requests are fixed (see `FORK.md`). Not fixed, with the
reason: a session obtained before two-factor is switched on survives the
switch ("Logout" ends every session); a container's command, entrypoint and
healthcheck are not masked for read-only callers (the edit form would save
the mask back); a new SQLite file is world-readable until the next start;
Tailscale's range counts as local; configuration still controls the network
edge (URLs, hostnames, trusted proxies, the admin address list), resources
is root through Docker and admin through the terminal, by upstream's
design; tokens still pass the plain "logged in" checks as their owner; an
image that bakes `cosmos-auto-update=true` cannot be switched on from the
panel; `/oauth2/*` and the OpenID discovery document carry no security
headers; upstream's per-URL hardening sends a year-long HSTS with
includeSubDomains to proxied apps; the country picker loads flags from an
outside host; in paid code (not touched): three routes that act on GET and
a registry sign-in that accepts tokens without the owner check.

Turnstile and the edge blocks were never tried against the real Cloudflare:
a widget with real keys, "create the widget for me", the permission name for
zone IP Access Rules, the answer to a test rule for a documentation address,
paging. Until Authenticated Origin Pulls with the owner's own certificate
are enforced, another Cloudflare customer could make Cosmos believe a chosen
visitor address (bounded: shielded names, two events at least, challenges
first, the exemptions and caps). The owner's to change if wanted: Turnstile
on the invitation page, failing closed when Cloudflare is down, a challenge
rather than a block for a first offence, seven days of exemption for an
administrator's address, the caps of 500 rules and 60 blocks an hour.

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

## Cloudflare and email: left for later (2026-11-14)

- Authenticated Origin Pulls are built (off until switched on). To confirm on
  the real setup: that Cloudflare accepts the certificate Cosmos makes, that
  its proxy presents it on port 2053, and how long a change takes; the
  owner's choices: shared or own certificate (own is the one that proves the
  request passed the owner's zone), one-time token or manual upload.
- A SmartShield strike has no Fail2ban line when a proxy reported that
  address within the last day (accepted trade-off); `access.origin.denied`
  does not count towards a ban.
- An Origin CA certificate on a shielded name shows as "not trusted" in the
  reachability check.
- Unverified against the real Cloudflare: the "may this token edit?" check,
  the public-address lookup used by the preview, and whether the "Edit zone
  DNS" template alone is enough.
- Email: Thunderbird's database is not asked; the less common provider
  entries are unverified (each is probed live at first use); Microsoft's
  retirement date for password sign-in may need updating in the hint.
- The panel on the domain's own name shares its session cookie with every
  port and subdomain of that name (the owner chose it knowingly).

## Router ports for apps: left for later (2026-11-14)

- Not yet run against the owner's router: whether it lists forwards made by
  hand (80, 443, mail), what it answers for a taken port, how it treats
  leases (`/storage/dev/plans/14-upnp-apps.md`, section 8, has the list).
- Chips outside the app card (URL list, route editor, home tiles) carry the
  port the panel is browsed on, which is wrong only when the router forwards
  another external port for Cosmos itself.
- The switch is on the container page's "Internet" section, not on its
  Network tab; IPv6 pinholes and NAT-PMP/PCP are not done; a renamed
  container loses its choice; nothing is tested from outside.
- The owner's choices to review: the reserved list holds 443 by default; a
  stopped container's port is closed and reopened; a choice is forgotten 15
  minutes after its container is gone; at most 32 app ports.

## Router ports: after the outage of 2026-11-15

- A router that gives no remaining time for a forward (it answers 0) is
  taken to keep it until removed. If such a router does end forwards by
  itself, the forward is missing for up to five minutes (the interval of
  the look) each time; a shorter look for Cosmos' own ports would narrow it.
- Nothing outside the server watches whether it can be reached from the
  internet: a watch from the owner's other machine or a small external
  service would tell them sooner than a visitor does.

## Look of the panel: left for later (2026-11-14)

- Seen when the security release was checked, left as it is: on a phone the
  security dashboard's icon-only buttons (Incidents, Block at Cloudflare,
  Unban) are 36x30 px; Storage > Mounts at 820 px squeezes the mount options
  to a word per line; the merge dialog's chip flashes briefly; the container
  page's action icons are cut off at 390 px; WebKit does not draw the greyed
  look of locked controls; the audit line of a manual block at Cloudflare
  shows its end time raw; "Block at Cloudflare" is offered on a Cloudflare
  address (trying it is refused with the reason).

- Users: whether two-factor is on is shown nowhere (the server does not send
  it; a yes/no field in `/api/users` would do). The seats meter stays
  visible without a licence (the five-user limit is enforced by the free
  version); a sentence when the limit is reached would help.
- Storage: the tree's expand icon is under 44 px (tapping the card does the
  same); on a desktop the SnapRAID report is cut inside its box; the
  fixtures follow the server's code, not a real controller's answers.

- Phone targets still under 44 px, on purpose: the chips on the app cards
  (24 to 32 px with 8 px between them; 44 px would double the card), table
  row icon buttons (30 px), date-picker icons, the security audit's filter
  selects, the sign-in logo link.
- Five heading jumps remain (two raw `<h4>` on the HTTPS tab and the
  scheduler, three small titles under a page title). On a phone the Market
  showcase's description is cut after three lines.
- A signed-in non-admin opening an admin-only app still gets upstream's
  JSON "User not Authorized". The guards' 403 pages stay plain text (fixed
  English sentences; styling them would mean HTML from the proxy on every
  app's hostname).
- Not tried on a real phone, real Safari or a screen reader; the setup
  wizard and the two-factor pages were seen with mocked answers only; log
  lines were never seen with real output.
- "Apps that should run but are stopped" was decided against for the health
  lines: a stopped container with a restart policy was stopped by someone,
  and Docker does not say who.
- The owner's to change if wanted: how many lines show before a notice area
  folds (2 on ServApps, 3 on home); what the phone's "More" holds; the
  out-of-memory health line; the health thresholds (disks 90 % and 95 %);
  Theme colours on the sign-in pages of a device that never opened the
  panel; the icon timings (2 s wait, 5 minutes for a failure).

## What a stranger can still learn (2026-11-14)

- A publicly trusted certificate that lists other names is served as it is
  to visitors of a name served directly: issue one certificate per direct
  name, or wildcards only. The hourly count of refused handshakes is shown
  nowhere in the panel.
- A name served directly ties the address to the domain by design; the
  Certificate Transparency logs list every name of every certificate
  ordered; another Cloudflare account steering a connection to the server
  with the right name is closed only by Authenticated Origin Pulls with the
  owner's own certificate; the refusal itself says a TLS server listens.
- The ownership markers Cosmos writes in DNS say "managed by cosmos";
  `/cosmos/api/can-send-email` answers anyone (the sign-in page needs it).

## Checks not run on the combined tree (2026-11-14)

- The installable app's script phases, and the certificates, home-names,
  Cloudflare, e-mail, Telegram, Fail2ban and UPnP checks, were each run on
  their own branch only (each needs a set-up of its own).

## Creating apps: left for later (2026-11-14)

- An image's declared volumes and ports are known only when the image is
  already on the server (nothing is pulled to read them): "Review and
  create" proposes no volumes for a new image. `env_file` cannot be read
  (the review says so). PUID/PGID are proposed only for linuxserver.io
  images. The review's sentences are not shown when editing.
- One WebKit page error ("Load failed") while the server reloaded after a
  creation, probably upstream's list refresh; unproven. The New URL dialog
  was not seen in a browser as a non-admin. `DomainPicker` in
  `hostnameSuggestions.jsx` is unused now.
- The owner's to change if wanted: the list of sensitive access and the
  tick per item; which data counts as configuration and which as bulk; bulk
  data in a Docker volume proposed on the data disk with the most free
  space; a lone compose port published on the same server port;
  `unless-stopped` when a file sets no restart policy; PUID/PGID from the
  owner of the data path, else 1000; "put the previous one back" has no
  confirmation and the container it sets aside becomes a leftover itself.

## Installable app: left for later (2026-11-14)

- Never tried on real devices (iPhone notch and status-bar colours, the
  Android install banner, the desktop install dialog were simulated); the
  update flow was not run in WebKit across a browser restart.
- With a certificate the device does not trust, browsers refuse the service
  worker (seen in Chromium): the panel still works as a page. The owner's
  public name has a recognised certificate; `.local` names need Cosmos'
  authority installed on the device.
- Left out on purpose: push notifications, background sync, offline viewing
  of data, manifest screenshots, the window-controls-overlay title bar.
- The owner's to change if wanted: "Not now" lasting 30 days, the four
  shortcuts.

## Exposing apps: left for later (2026-11-14)

- The "New URL" dialog still has the old domain picker (direct copies
  without the shield choice); the URL tab's port proposal does not see the
  Network tab.
- Ports forwarded by hand (without UPnP) cannot be given a name; no AAAA
  records for direct names; the records use the public-IP lookup services,
  not the address the router reports (the owner's to decide).
- The Market does not check the image's architecture before pulling; compose
  import turns number ports (`- 8080`) into a broken port string.
- Two "Failed to fetch" page errors were seen in the install check while the
  server reloaded after creation (probably upstream's list refresh; unproven).
- The "give apps a local address" banner on ServApps is squeezed on a phone.
- The owner's to change if wanted: the `<app>-direct.<domain>` naming rule,
  the dangerous-port and sign-in default lists (`src/cloudflare/expose.go`,
  `client/src/utils/exposure.js`), `expose.opened` being high severity (one
  Telegram message per exposure), the Internet dialog needing administrator
  rights (it can open router ports).

## Certificates: left for later (2026-11-14)

- A second authority as a fallback (ZeroSSL or Google Trust Services need an
  account with EAB); small once the owner has one.
- DNS-PERSIST-01 when Let's Encrypt offers it; a guided HTTP-01 forward for
  servers whose ports 80/443 are reachable; certificate profiles.
- An order through a DNS provider other than Cloudflare was never run end to
  end (no stand-in exists); the dialog was checked up to choosing the provider.
- Staging is a process variable (`ACME_STAGING=true`), not a panel switch.
- The owner's to change if wanted: the limits (10 a day, 30 a week, 3 for the
  same names a week, "Renew now" 4 an hour) and which failures count as
  lasting on the home page.

## Local names: left for later (2026-11-14)

- Names for use at home under the owner's domain: done (see `FORK.md`).
  Left: AAAA (ULA) records; a per-app opt-out (every app gets a home name
  once the domain is switched on); home names for URLs with a port of their
  own; domains whose DNS is not at Cloudflare; the app cards were not seen
  in a browser (logic covered by `node --test`). The owner's to change if
  wanted: the panel's home name (`cosmos.<label>.<domain>`), sign-in going
  through the panel's home name rather than its public one, home names in
  app links shown only to accounts that may read the configuration.
- `.local` does not resolve on Android 11 and older, most smart TVs, Linux
  with only systemd-resolved, or over Tailscale; names with a dot before
  `.local` fail on Windows and most Linux machines.
- The Cosmos sign-in in front of an app uses the server's configured HTTPS
  port for the return address: if the router forwards another external port
  for Cosmos itself, the return address is wrong.

## Usability gaps found in the code

| # | Gap | Where |
| --- | --- | --- |
| 10 | "Provided" certificate mode and certificate status | check again on 0.24.0, the Domains tab changed this |

(1 where an app's data goes, 3 system health, 2 data disks, 4 time zone, 5 port conflicts, 6 image defaults, 7 help texts, 8 maintenance time, 9 schedules, 12 mounts, 11 file picker shortcuts, 13 volume usage, 14 volume paths and 15 alerts are done; see `FORK.md`.)

Limits of the port check (5): ports of stopped containers are not known, Cosmos' own ports are read from the saved settings (not from `COSMOS_HTTP_PORT`/`COSMOS_HTTPS_PORT`), host-network containers are not covered.

## Defects found by reading the code (upstream, not fixed here yet)

Confirmed by reading only, on 0.23.4; re-check on 0.24.0 before fixing. (The certificate wipe on save, the two-factor limit and reset, and the HTTP port's 404 are fixed; see `FORK.md`.)

- 30 minutes after any network disconnect Cosmos prunes all unused images (unless `SkipPruneImages`), which also removes this fork's build images.
- **Let's Encrypt, leftover corners** (the plain-HTTP start is gone on 0.24.0, the self-signed certificate serves when an order fails): if no self-signed certificate can be generated and none is stored, the server still starts on HTTP only; a stored certificate that does not parse ends the process; an order through a DNS provider gives a notice without a reason.
- **SmartShield and the local network** (re-checked on 0.24.0, nothing changed): a private address is treated like any other; three temporary bans in seven days give a permanent one (in memory: a restart clears it). A logged-in admin is never banned, but a device whose address is banned and whose session has expired cannot log in until the ban ends. Options, the owner's to choose: add the LAN range to the Shield whitelist (a setting), never make a ban permanent for local addresses, or switch permanent bans off.
- **A container edit interrupted by a stop of Cosmos** leaves the old container stopped as `<name>-cosmos-old-<id>`; nothing offers to put it back or removes it at the next start. A failed network reconnection during an edit is only logged.

## From other forks and open upstream pull requests

Survey of 254 forks on 2026-11-12. Nearly everything useful is by one author
(madejackson: `aseracorp/Cosmos-Server`, `madejackson/Cosmos-Server`, about 35
open upstream PRs); his commits are AI-assisted, so review each. Not yet
verified by us beyond the survey.

- Bug fixes: `779d90db` atomic config write; `7f7bdca7` limits wiped on update; PR #594 streaming endpoints cut by the 45 s API timeout; PR #576 HTTP→HTTPS redirect; PR #606 admin email at setup; PR #615 installer crash on tmpfs volumes; PR #597 market page crash; `b33d811d` fatal on empty certificate pairs; PR #585 then #595 healthcheck formats; PR #627 OIDC issuer with `host:port` (auth code: review carefully); PR #577, #587 installer fixes.
- Small UI fixes: PR #598 terminal ghost keys, #603 log timestamps, #600 breadcrumbs, #575 login autofill, #570 consent page.
- Features to consider: `shm_size` (#567), exec-form command (#495), generated secrets in market templates (#586), volume options (#573, #599), memory swap limits (`0d1620ff`).
- **Never take:** `Fyve-Labs/Cosmos-Server` (bypasses the licence check), PR #602 (licence field), anything under `src/pro`, `src/constellation`, `src/backups`, `src/storage/rclone.go`, `src/utils/cloud.go`.
