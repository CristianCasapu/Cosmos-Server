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
a widget with real keys, "create the widget for me", the answer to a test
rule for a documentation address, paging, and whether an account-owned token
can be given the permission for a domain's IP Access Rules at all (the custom
rules are the way that is documented to work with such a token). Until Authenticated Origin Pulls with the owner's own certificate
are enforced, another Cloudflare customer could make Cosmos believe a chosen
visitor address (bounded: shielded names, two events at least, challenges
first, the exemptions and caps). The owner's to change if wanted: Turnstile
on the invitation page, failing closed when Cloudflare is down, a challenge
rather than a block for a first offence, seven days of exemption for an
administrator's address, the caps of 500 rules and 60 blocks an hour.

## Automatic updates: left for later (2026-11-15)

The weekly schedule of `fork-deps-update.yml` is commented out until a first
run by hand went well; only that run can prove that the workflow's token may
push action pins, the queued retry without the newest major, reading job
logs while a run is going, the status branch served by
`raw.githubusercontent.com`, the smoke on a real runner and the release
build without caches. Pins inside `.github/workflows/` are never changed by
the machine (that needs a token with the `workflows` permission: not given).
A package compromised at its source and not pulled within 7 days would ship
and run as root: the risk the owner accepted. The rollback: a signal in the
first milliseconds of a start still counts as a failed start; a hang costs
up to three times 15 minutes; a Docker daemon restart during a trial counts
unless the launcher cannot reach Docker either; data a new version migrated
is not restored; the proof does not cover Docker, the proxy routes or a
remote monitoring database; an older version cannot be put back by hand
(upstream's "not newer" rule); a restore that is stuck sends no
notification (the server is down: the journal and `cosmos-update.README`
say it); the hand reinstall of the README needs curl, unzip, openssl and
GitHub; a notice told before the first administrator exists goes to
nobody. Known and left, each needing several faults in a row: a state
read that fails for a moment just before a restore begins, while the state
could still be written, leaves no sign that the restore is owed; a rollback
notice can repeat at every start when the state can be neither removed nor
synced; after an unzip that failed, one start may run the half-unpacked
folder when the state cannot be read at that moment (the next start
restores it); a version installed by hand is watched by nothing; a start
the server refuses with its own 400 is reported as "did not answer in
time". The owner's to change if wanted: majors per run (6), the 7-day
cool-down, the 72 hours the previous files are kept, the 15-minute window.

## Dependencies: left for later (2026-11-15)

- Docker client library v26 is out of support (two daemon-side advisories
  recorded as known); its supported successor (moby/moby/client v29) is a
  new module that would change about 40 of upstream's files: left for
  upstream. Docker 29 daemons accept the v26 client.
- Nebula stays on 1.9 (from 1.10 `print -json` prints a list, v2
  certificates are the default and `default_local_cidr_any` is gone, all of
  which upstream's paid code relies on) and restic on 0.18 (0.19 exits when
  HOME is not set, as under systemd). Not proven for them: the rclone
  backend, the arm64 binaries beyond their checksums, the real systemd
  environment, NAT and relays.
- The owner's to decide: removing lint and test tooling nothing runs
  (eslint 8, prettier 2, babel 7, testing-library, jest-dom) and packages
  nothing imports (react-number-format, react-perfect-scrollbar,
  react-window, react-draggable, history, date-fns, xterm-addon-fit),
  express 5 for `test-server.js`, the hold on the sponsors action, and the
  browsers Vite 8 builds for by default (Chrome 111, Firefox 114, Safari
  16.4 and newer).
- Two defects seen in upstream's paid backup code, on old and new restic
  alike (not touched): the password change runs `restic key change`, which
  does not exist (the command is `key passwd`), and the restore-size answer
  is not pure JSON and lacks `total_file_count`.
- Checks to repair: `fork-ui-check-sensitive-edit.mjs` waits a fixed 3 s
  after saving the Storage tab and can read a container that is still
  being recreated (it should wait for the container, and tolerate an empty
  inspect); the gallery's guard on `any-image` in Firefox trips on the app
  probe's CORS line when the seeded app addresses do not resolve. The five
  end-to-end checks (creation, settings-forms, users, sudo, pwa) passed in
  three engines on the dependency majors before the last console fixes
  (the permission guard, the tab panels, two hidden checkboxes); after
  them the any-image and sensitive-edit checks, the gallery views and the
  console look were run, not those five again.
- An upstream merge will conflict in many frontend files (Grid `size`,
  `sx`, the button's `loading`, the tree view), `package.json`, the lock
  file, `vite.config.js`, the build action and the check scripts: take
  upstream's file, then make the same one-line edits again (the transforms
  used are kept outside the repository, with the plan).
- Held: the service library (1.3.0 cannot render upstream's systemd unit
  template), modules only paid code uses, majors of upstream's own code
  behind a new import path.
- The Pebble proof's two renewal-window checks fail on `custom` too (the
  check or Pebble 2.10's answers: to find out); the installable app's check
  in Firefox needs the port refused rather than the container stopped.

## Security features: left for later (2026-11-13)

- Dashboard: thresholds of the status line are constants; "Under attack" does
  not say whether the attacking addresses are already blocked.
- Audit: where "Recent activity on your account" lives (home page for now);
  the 2000-line export cap; the own-activity view hides why a login failed.
- Telegram: a "find my chat ID" button; the bell for "update available" and
  "container keeps stopping"; alerts
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
- Names served through Cloudflare are now watched from the internet by the
  server itself (see `FORK.md`). Names served directly cannot be tested from
  inside the house: a small scheduled request from a machine of the owner's
  outside it (to `/_health`, alerting after two failures) would cover them.
  The owner's to change if wanted: the interval (5 minutes), whether an
  outage rings through Telegram's quiet hours (it does not), whether app
  names are watched or only the panel's. For a shielded name the certificate
  line still says "not trusted" with a self-signed or Origin CA certificate
  although the verdict is "reachable".

## Notifications: left for later (2026-11-15)

- The Postgres branch of the schema upgrade is written but not tested (the
  server runs SQLite); rotated log files are not read; notifications from
  paid code get the generic sentence only.
- An open router notification for a port that is no longer wanted stays
  open until its 24-hour window ends; the retry time stored in "What Cosmos
  did" is the one at the moment of writing (the live section has the
  current one); times in sentences are the server's local time; the link
  buttons of the details all read "Open the page it is about".
- The owner's to change if wanted: the grouping window (24 h) and the
  re-alert interval (4 h); that anyone may delete their own copies; that
  "keeps stopping" and "Fail2ban not answering" ring the bell; that opening
  the bell no longer marks everything read.

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

## The monitoring database: left for later (2026-11-25)

- Before the fallback takes over (two failed looks, about 20 to 35 seconds), the rows that cannot be written are kept in memory and written again, but only up to 5 000 events and 500 notifications; beyond that they are dropped (counted and logged). A long write burst during the first half-minute of an outage can still lose rows. Metric buckets and the security record keep theirs as before.
- When the settings change to another database, the raw metric bucket open at that moment (30 s of data, an hour for hourly metrics) is written whole into the new database's hour and day totals. It is noted there and never merged twice, but the new database's first day total holds up to one bucket recorded just before it became the store.
- Max and min totals in a catch-up take the local file's value: when two servers' outages fall in the same hour or day, the value may be the other server's (Cosmos records no max/min totals today).
- "Mark resolved" during an outage resolves the problem in the local file only; rows of the same problem written to PostgreSQL before the outage stay open there after the return (a watch that later sees the problem over resolves them; otherwise they stay until they are pruned).
- The tests cut PostgreSQL through proxies and stopped containers on one host, not on a virtual machine or a real network: a server that loses power mid-transaction, packets dropped by a router, and the kernel's retransmission wait on systems without the TCP user timeout (it is Linux-only) were not exercised.

## The host guard: left for later (2026-11-26)

- The guard's test bed on other kernels: a VM mode (a qemu/KVM guest per distribution from its cloud image, two NICs on the Docker in Docker's bridges) so that Debian 12's 6.1 and Ubuntu's 6.8 kernels (nft_fib, set timeouts, hooks) are exercised, not only the host's.
- fwmark/connmark policy routing against the guard's strict reverse-path check (an uplink chosen by a mark).
- A real cloud image (EC2/GCE/Hetzner) for the cloud detection and the exposed default.
- Probes the reviews listed as missing: tinysshd at all; journald rate limiting and a journald restart during a dropbear connection; other kernels; a real Tailscale client with an exit node; a router swap with DHCPv6 and a new delegated prefix at once.
- Wire `sh scripts/fork-guard-e2e.sh fast` into the checks of every push (it is not in `.github/` yet).
- The check "the session survived (ticks without a gap)" of the `ssh` group failed 2 times in 15 runs on debian12-ce (2026-11-26). Both times the install's exit code and its output came back through the same session, so the session was not cut; whether it was a pause of the ticks over 3 s or the client's exit code is not known. The check now prints both when it fails: look at the next failure.

## Telegram messages: left for later (2026-11-25)

- A message of its own when a data disk goes read-only or disappears from the listing (today its "filling fast" warning only stays open, then reads "No longer followed" after 24 hours).
- "Mark resolved" also in the notifications list, and for several notifications at once.
- Instead of silence beyond 256 open problems, one summary "N problems ended" per period.
- Restic and pre-upgrade job threads: end them on the next InitBackups, which knows the full list.
- The model of the threads (`src/telegram/model_test.go`) does not generate a source that is wrong by its own rules (only one that lags), the cron, UPnP and fill rules themselves (their packages' tests carry them), a restart during a send, events later than 2 minutes, two things on one thread, nor the farewell message (only its unpin).
- Some faults are seen by one named test only, not by the model: a change of chat that keeps the old chat's memory, the brake dropping a message, the raw name instead of the hash, the reconcile marking every open entry. Keep those tests.

## The app's edit tabs: left for later (2026-11-18)

- Docker resolves a bind again at every start: a folder the administrator ticked as "nested inside a folder another app can write" can still be redirected later by that app. The tick says so; nothing watches it afterwards.
- Only the MAC address of the main network is kept across a recreate (Docker picks the one of a network connected after creation).
- Propose a DNS app of the server (Pi-hole, AdGuard Home) as a DNS choice; bring the same proposals into the new-container form's steps.
- The free-port proposal reads the socket tables: a port forwarded only by another program's firewall rule, or used in another network namespace, is not seen.
- A legacy bridge `--link` to a container that is gone fails the recreate (upstream's behaviour, unchanged).
- `fork-ui-check-credentials.sh clean` has the dind entry-point bug (its `rm -rf` becomes `docker rm`): the scratch folder is not removed.

## Guides: left for later (2026-11-18)

- A button that makes a bucket and a limited key for a site from inside Cosmos (an administrator-only, password-confirmed POST that talks to the storage's admin API with its root key, the secret shown once and never stored, recorded; quota and retention asked in the forms standard; tests against a RustFS in a Docker in Docker).
- Guides for databases ("how to connect") and mail relays as new files of `src/guides`.
- The Windows parts of the storage guide (folder rights with icacls, the scheduled task, Windows PowerShell 5.1) are from research and reasoning, not run: run them once on a Windows machine. The same for macOS, phones, NAS appliances and the WordPress plugins' screens.
- MongoDB with `--oplog` on a replica set is not run by the examples test (its test server is a single one).
- The 30-day expiry of replaced versions and the 7-day clean-up of cut uploads are set and accepted by RustFS 1.0.5; nobody waited to see them act.
- A React unit-test runner: the URL form's storage proposal is unit-tested as a pure function and rendered only by the browser check.

## An app's links: left for later (2026-11-17)

- A per-device check whether `.local` names resolve (today: the page having been opened by a `.local` name is the signal; otherwise names for use at home come first).
- The names of ports the router forwards (UPnP) inside an app's "All addresses" menu; they stay in the router-ports view.
- The home's public IPv6 as a signal for "probably home" (the server knows no public IPv6 of the home).
- Through a VPN, names for use at home and home-network addresses are only "maybe": Cosmos cannot know whether the VPN routes the home network.
- The header's "Ctrl + K" box is a dead template leftover (no search exists).

## Forms, part D: left for later (2026-11-17)

- `raidDialog.jsx`/`raid.jsx` and `oldTerminal.jsx` are unreachable upstream files (nothing imports them, they are not bundled): left in place to keep upstream merges clean.
- A Mongo dump proposal for scheduled jobs: mongodump takes its password on the command line, so none is proposed.
- SnapRAID's schedule still needs six fields; the job form takes five or six.
- The shared choice chips are 32 px tall on phones (the gallery does not flag them); the wizard's language button is 24 px (upstream's).
- The installation's progress shows nothing between "Creating network…" and the end while the image is pulled: a slow or failing pull looks like a hang for minutes. Show the pull as a step, and the failure with its reason and "Try again".
- A caller who may change settings can point the Telegram chat ID elsewhere while the token stays masked (the old chat is told): the owner's call whether a chat change should need the token again.
- A whole-settings save refused by the job and alert recheck under the write lock (another save in the microseconds since the early recheck) can follow the replicated parts already published; closing it would mean changing the replication code.

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

## Installing any image: left for later (2026-11-15)

No form for the credentials of a private registry (it would be a new store
of secrets: today the server is logged in with `docker login` on the host;
upstream's registry page is paid). The download size is shown but not
compared with the free space before a pull. A graphics card passed through
`runtime: nvidia` or compose's `deploy` section gets no tick (the compose
import says it drops `deploy`). An app that names its database only in a
configuration file cannot be wired up. Upstream's Market templates of
Miniflux and Nextcloud are not valid JSON (their images install through
"any image"; to report upstream if the owner wants). No recipe for MinIO
(its community image is gone and its repository archived), Garage (needs a
configuration file), SeaweedFS or rest-server. Docker Hub's "verified
publisher" mark is not shown. In Firefox the gallery records one console
error on the ServApps page behind the dialog (the reachability check of a
test app's `.local` address, blocked by CORS; not from this flow). The
owner's to change if wanted: databases pinned to a major version (`lts`
for MariaDB and MySQL), 24-character passwords, Redis, Valkey and Memcached
without a password (not published, reached through link networks), Traefik
with the Docker socket behind a tick, and the limits of image look-ups
(20 a minute for the server and 8 per user; 20 counted Docker Hub reads an
hour, 8 per user; a reserve of 25 pulls).

## Apps without a web interface: left for later (2026-11-16)

An app on the host network is dialled at `127.0.0.1`, not `localhost` (one
that listens only on `::1` is not reached), and a container on a macvlan
and a bridge is dialled at its macvlan address. No test compares the old
and the new way of finding a container's address on real multi-network
containers. A loop through another machine (a router sending the request
back) is bounded only by the shield. A TCP route as a choice for services;
checking a port by itself when a dialog opens; the publisher's page on
GitHub for images of ghcr.io (a new outside host with 60 requests an hour:
not built); hotio and most small projects publish no description to read.
What cannot be known before the first start: settings described only on
linked pages or in entrypoints, "one of these" choices, conditional
settings, folder ownership, names in mixed case. A password of exactly
eight characters that is an ordinary word is hidden wherever that word
appears in the log shown. A URL that chains to another URL of this same
Cosmos is refused like a loop (508). The rule that keeps a value sent as
`***` is on the edit route only: a creation that replaces a container,
from an account that may not read secrets, would still write `***` (the
panel's editors refuse that in the browser). The adapted checks lost a
little: `fork-ui-check-expose.mjs` would not notice a web app shown as a
service, `fork-ui-check-new-url.mjs` ignores "Failed to fetch" for the
whole run. The home-network address is validated in the browser only, as
on the Network tab. A container renamed keeps its old name in the proxy's
cache for up to 10 s.

## Sign-in details and managers: left for later (2026-11-17)

MongoDB gets no web manager (mongo-express has published no image since
2024-11): one once a maintained one exists. A database recognised without
a recipe gets no manager offer. pgAdmin connects without typing only when
it is created with its database (it reads its server list at its first
start) and not for an administrator e-mail that starts with a digit. The
password proposed for an app that makes its first account in its own page
is not kept by Cosmos (keeping it encrypted next to the configuration
would protect nothing: the key lives there too). Redis Insight asks to
accept its terms at first open: Cosmos does not accept them for the owner.
An administrator cannot make a manager's URL public on purpose (the server
turns the protection back on at the next save). That protection goes by
the container's image as its recipe names it: a mirrored or local-registry
copy, an image referenced by id, a PROXY URL or a URL by address to a
manager are not covered; existing URLs are looked at when any setting is
next saved, not at start-up; a tunnelled URL edited from the other node is
not. The container edit can still set the fork's two sign-in labels (such a
container then shows as "its web manager" on a database's page, unticked
in its delete dialog). A repair of a manager that had no network of its
own leaves the previous one behind, empty. "Add a web manager" leaves
an existing database on the networks it has, the default bridge included.
Upstream's delete dialog can remove an app's networks before its
containers are gone (`promises.concat` in deleteModal). The any-image
check's private-registry, arm-only and data-disk steps need a test bed
with those; the services check's story and bad phases need a Cosmos
started without recipes.

## Apps behind the proxy: left for later (2026-11-17)

Connections of port routes that arrive during a restart of the web server
still go to the old, closed handoff (as upstream): to hand them to the
next generation. A refusal that only shows on a
sign-in (POST) is not provoked: for known apps Cosmos reads their variables
instead. Configuration files inside containers are not read, so an advised
line is not confirmed to be in place. No periodic check of all URLs with a
notification when one stops opening. The Market's own review does not show
"tell the app its own address" before an install (its log says it). "Opens now" after a repair is said from the check's own request after a
fixed wait, not from the live proxy. The advice to trust Cosmos as a proxy
(qBittorrent, Jellyfin) names the address Cosmos connects from: without
Docker that is the bridge's gateway, which every process of the server
shares, and the advice does not say so. An open URL form saved after a
one-click repair writes the old value back. Results of URLs that do not
lead to a container are not kept and are checked again at every load. An
app that builds an OpenID redirect address from X-Forwarded-Host now sends
it with the port: the client's redirect addresses may need the form with
the port. The any-image check's "data folder on the data disk" step has
not been seen passing on a test stack with a real second disk.

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
