# Cosmos-Server — a personal fork

> **This is a personal fork of [Cosmos-Server](https://github.com/azukaar/Cosmos-Server)**, the self-hosted home-server gateway and manager written by Yann Stepienik. It is **not affiliated with or endorsed by** the original project. Upstream's own README is at <https://github.com/azukaar/Cosmos-Server#readme>, its documentation at <https://cosmos-cloud.io/doc>.
>
> **Licence:** upstream's code stays under upstream's [`LICENCE`](LICENCE) (Apache 2.0 with the Commons Clause and an Anti Tampering Clause, unchanged); the fork's own additions are offered under [`FORK-LICENCE.md`](FORK-LICENCE.md) — for use with the free community edition only, no commercial use without written consent, and not a contribution to upstream. See [Licence](#licence).

Cosmos (upstream) puts a reverse proxy with automatic HTTPS, an authentication server, an app market, a container manager, storage tools and monitoring behind one web panel. This fork keeps all of that and adds what one owner needed to run it in production on a home server: a security record with a dashboard, Fail2ban and Telegram, signed releases, Cloudflare and certificate handling, forms that fill themselves in, and many fixes. Every change is listed in [`FORK.md`](FORK.md); what each release brought is in [`changelog.md`](changelog.md).

**Upstream's paid features are untouched.** The fork never alters, bypasses or imitates a licence check and copies no code of upstream's paid features. Without a licence the panel *hides* what needs one (a setting, on by default) instead of unlocking it; with a licence nothing is hidden. Where the fork has a module of its own in an area upstream also sells (incremental snapshots), it is written independently, in its own package, and shares nothing with upstream's paid code.

Contents: [What the fork adds](#what-the-fork-adds) · [Status and honesty](#status-and-honesty) · [Install and update](#install-and-update) · [Licence](#licence) · [Contributing](#contributing) · [About Cosmos (from upstream's README)](#about-cosmos-from-upstreams-readme)

---

## What the fork adds

The paragraphs below describe what an administrator sees. The exact files, routes and limits of each change are in [`FORK.md`](FORK.md). There are no pictures here yet: the fork's screenshots come from a gallery script run against a throwaway instance, and none have been taken for publication.

### Security

- **Security record.** Every security-relevant event — sign-ins and failures, two-factor, sessions, refused permissions, API tokens, SmartShield strikes and bans, scans, account, settings, URL and container changes, terminals, updates — is kept with who, from where, what and the outcome, in a table of the fork's own. It is bounded against floods (limits per kind and address, summary rows, retention per kind) and readable by administrators only.
- **Security dashboard** (menu *Security*). A status line in words, counters compared with the period before, a timeline per group, the busiest addresses with SmartShield block and unban, the Fail2ban section, and a searchable list of incidents with plain-word explanations.
- **Audit panel** (*Security > Audit*). What users did and what happened to each account, as sentences, with filters kept in the address, a summary per user and a CSV export (formula cells neutralised). Every user also sees "Recent activity on your account" on the home page — their own rows only.
- **Fail2ban.** Filters, jails and an installer in [`fork/fail2ban/`](fork/fail2ban/) that the administrator runs on the server (Cosmos never runs it). Cosmos writes one fixed line per incident to `cosmos.security.log`, made for Fail2ban; bans are enforced in an nftables table of their own, and local networks are never banned. The panel shows the state, the banned addresses with "Unban" and the jails, and writes the thresholds and the never-ban list.
- **Telegram notifications.** High-severity security events, what Cosmos raises for its administrators, server updates, a restart of Cosmos and a container that keeps stopping, with kinds to switch, a minimum severity, quiet hours, a rate cap with summaries and a test message. The bot token is masked for every reader and kept out of logs.
- **Updates that go back by themselves.** An update keeps the files it replaces; the new version must prove that it started properly, and after three failed starts the previous version is put back and the administrators are told. An update that would not fit on the disk is refused in words.
- **Signed releases, verified before install.** Each release carries a signature file (Ed25519 over the version and the SHA-256 of each zip). The server checks it before it places a download, and the launcher checks it again before it unpacks; a refused update leaves the server on its version and is recorded and notified. Downloads come only from this repository on GitHub's hosts, with size and time limits.
- **Sessions and password confirmation.** Session cookies are SameSite=Lax and bound to their hostname; logging out ends every session of the account on every device; a session lasts at most 30 days; the password confirmation for sensitive actions lasts a fixed two hours and the prompt comes back mid-work without losing what was typed. Five wrong two-factor codes lock the second step for fifteen minutes, administrators included.
- **A TLS listener that names nothing to strangers.** A peer from outside the local network and Cloudflare's ranges gets a certificate only for a name that is served directly, and that certificate names nothing else; for an unknown, shielded, `.local` or home name the handshake is refused. Upstream handed such a peer a certificate listing the panel's and every app's name.
- **Permission and hardening fixes** from a security review: the API answers the panel's own pages only (requests started by another site are refused); secrets carried in addresses are kept out of logs; sign-in bodies are bounded; accounts that may not read the configuration get a reduced view; the configuration backup is owner-only; applications receive the visitor's address from the server, not from the visitor; the "forgot password" form no longer tells a stranger which accounts exist; a local address is never banned permanently; dependencies were brought up to date for security.
- **Further hardening.** Commands that change something are accepted by POST only; labels that come from an image no longer steer how Cosmos runs its container; the panel and its API are sent with a script policy and security headers; request events are sampled instead of one row per request; wrong passwords at the password confirmation are limited per account; several permission checks were completed, so the "Users" permission cannot manage an account stronger than itself and "Configuration" without "Resources" cannot change jobs, storage, backups or folder URLs.
- **Cloudflare Turnstile on the sign-in forms.** Optionally, the sign-in, forgot-password and invitation forms ask Cloudflare's check of visitors from the internet, verified on the server before the form is read; it is never asked on the home network, and a file on the server's console switches it off.
- **Blocks at Cloudflare's edge.** For shielded names, a visitor who abuses through Cloudflare's proxy is challenged or blocked at Cloudflare itself, with the Fail2ban thresholds, lifted when its time is up, through the domain's IP Access Rules or, when the token may not use those, through two custom rules of its own; local addresses, the whitelists and addresses administrators recently used are never sent. The Domains tab shows and lifts every block, and the security dashboard can block an address there.

### Reaching the server

- **Cloudflare zones and the shield.** A guided set-up under *Configuration > Domains*: the API token is checked in plain words, every DNS record is previewed before anything is written, and a record Cosmos did not create is replaced only when ticked. Per address, a switch says whether it is served through Cloudflare's proxy or directly; a shielded name answers only Cloudflare and the local network. Optionally, Cloudflare's *Authenticated Origin Pulls* prove that a request really came through Cloudflare, switched on with a test and an automatic fall-back.
- **Certificates through DNS.** Let's Encrypt certificates that browsers trust without an open port 80/443: through a connected Cloudflare zone (the domain and its wildcard), another DNS provider's token, or one CNAME that hands the proof to a domain Cosmos can write to. Renewal follows each certificate's lifetime and the authority's renewal window, with back-off and limits kept across restarts. A *Certificates* card says per domain who issued it, what it covers, when it ends and why the last attempt failed; the home page warns once per domain with a lasting problem.
- **Local names.** `.local` names are published as address records with the server's LAN address on the interfaces that face the home network (never on Docker bridges or VPNs), checked every minute, and re-published when Avahi loses them; the panel says whether each name is answered and which devices can open one.
- **Names for use at home.** Per Cloudflare domain, an optional label (default `home`) gives every app a name `<app>.home.<domain>` at the server's private address, through one DNS-only wildcard record and one wildcard certificate name — so no app name appears in DNS or in the Certificate Transparency logs. Such a name answers the local network only.
- **Router port forwards.** Over UPnP, Cosmos can ask the router to forward its own HTTPS port and, per app, each published port (off by default). It uses the next free external port when one is taken, never asks for reserved ports, never changes a mapping it did not create, and asks again by itself — with growing intervals — when the router loses or refuses a forward.
- **"Internet" per app.** One action per app (and a step at installation) that puts the web address under a configured domain — through Cloudflare where that works, directly otherwise, with the reason in words — chooses who may open it, and forwards other ports under a name of their own. Withdrawing removes the address copy, its DNS record, the router mapping and the port's name. A reachability chip on each app says whether it can be reached from the internet and what to do about each problem; nothing is tested from outside.
- **Watch from the internet.** For names served through Cloudflare's proxy the server tests by itself, every few minutes, whether a request from outside really comes back in; it tells the causes apart (the router's forward, the server refusing, a certificate, DNS, Cloudflare itself), confirms a failure before it alarms, and says so in a notification and on Telegram, then again when the server can be reached and for how long it could not.

### Creating and running apps

- **One review before anything is created**, for the Market, the compose import and the new-container form: what is created and published, the addresses, the restart policy, where the data goes, what Cosmos filled in, processor problems, and a tick for each sensitive access to the server (the Docker socket, system folders, privileged mode, host network…). The server checks all of it again before it creates anything.
- **Install any image.** Not only what the Market lists: search Docker Hub or type any registry's image, and Cosmos reads what the image declares without pulling it, proposes a version, where its data goes and its passwords (with built-in settings for common databases and tools), keeps databases unpublished and links them to the apps that need them, then shows the same single review.
- **Apps that do not start, and apps without a web page.** When a new app stops or keeps restarting, Cosmos says why, shows the end of its log and offers the setting its log asks for. A database or another service without a web page gets no web address: Cosmos gives the address other apps use and lets you choose which apps, or the home network, may reach it.
- **Sign-in details for new apps, and web managers for databases.** A username and e-mail set once in the configuration, a password generated by each app's own rules, the details shown on the app's page (after the password confirmation) and an e-mail at installation without the password. A database can be installed with a web manager (pgAdmin, phpMyAdmin, Adminer, Redis Insight), already connected, for administrators only.
- **Apps that refuse their address.** Some apps reject requests because of the name or port they are reached at behind a proxy. Cosmos notices ("Does not open"), says what the app objects to and offers the repair it has just seen work; for known apps it also shows the setting they need.
- **Compose import that honours each line** (ports in all forms, `environment`, `depends_on`, volume options, healthchecks, `shm_size`, `tmpfs`, `init`, `read_only`, exec-form `command`, `network_mode service:` and more) and lists before creating what it does not use.
- **Data places.** A folder per volume is proposed from the server's real disks, never the system disk for bulk data when a data disk exists.
- **Safe edits and leftovers.** Editing, updating or re-deploying a container keeps the old one until its replacement runs, and puts it back on failure. A container left aside because Cosmos stopped mid-edit is reported and can be put back or removed from ServApps.
- Fixes: containers keep Docker's stop timeout instead of being killed; memory and CPU limits survive edits; a busy terminal no longer ends the server; host-port conflicts are named before they fail.

### Forms that propose values

Most forms of the panel were reworked so that they detect and propose what they can, say where a proposal came from, never replace what was typed, and are checked again on the server: the URL form (a simple view in front of upstream's fields), the email settings (server, port and encryption proposed from the address, a "Send test email" that names the failed step), users, scheduled jobs (crontabs said in words), HTTP settings (refused when they would lock the administrator out), a URL's protection as four levels, disk format (refuses a disk in use, waits for the result), mounts, merges and parity, alerts, API tokens, OpenID clients, networks, volumes and Market sources. Geo-blocking's default no longer blocks the administrator's own country.

### The look

- **The skin.** A font, colours and surfaces of the fork's own, AA contrast in light and dark, nothing below 12 px, following the two colours chosen under *Configuration > Theme*, with a light/dark switch in the header.
- **Phone layouts.** At 360–390 px the header, tables (a card per row, with the hidden columns inside), Storage, Users, the Market and the dialogs fit without sideways scrolling, with 44 px targets.
- **Wayfinding and states.** Page names in the browser tab, breadcrumbs that lead back, empty lists that explain themselves, one "needs attention" area per page, and a health summary on the home page.
- **The installable app.** The panel can be installed on computers, phones and tablets (PWA). Its service worker keeps only the panel's own build files — never API answers, apps or login data — and a switch under *Configuration > Theme* removes it from every device.

### Storage and snapshots

- **Incremental snapshots**, a module of the fork's own (standard library only): dated folders per source where unchanged files are hard links to the previous snapshot, a schedule, daily/weekly/monthly retention, exclude patterns, and restore from the panel with the current folder kept beside it. A source that is suddenly empty (a disk not mounted) is not snapshotted and its old snapshots are not thinned out.
- **Data disks outside `/mnt`** are listed, measured and get a "disk nearly full" alert. Permanent mounts made from the panel are written by UUID with `nofail`, so a missing disk cannot stop the boot. Mount, unmount, merge and SnapRAID requests are checked on the server; system mounts can no longer be unmounted from the panel.
- The configuration backup runs on a schedule of your choice; the configuration file is written atomically.

### Monitoring and smaller fixes

The server's network traffic counts only its own network cards and never goes below zero (upstream counted container traffic up to three times); wildcard alerts fire; app icons never hold a page; the home page shows one tile per app opening the address that fits where you are; server updates follow this fork's releases at a configurable maintenance time; the Cosmos sign-in in front of an app works on an HTTPS port other than 443.

---

**Notifications with details.** Each notification has a kind, the thing it is about and plain sentences with a link to where it is fixed, on a page of its own with filters and a search; its details say what happened, what it means, what Cosmos is doing and what you can do, with the state right now, earlier occurrences and, for administrators, the related security entries and log lines. A repeat of the same problem is grouped into one entry with a counter and alerts again only when it gets worse; when the problem is gone the entry is marked resolved with a follow-up. A failed router forward says which port, what it means for reaching the server, what the router answered and when Cosmos tries again.

## Status and honesty

This fork is used in production on one server. It is checked by unit tests, by a start of each build, and by browser checks in Chromium, Firefox and WebKit against throwaway instances and stand-ins for outside services. Some things have **not** been verified for real:

- **Cloudflare:** Authenticated Origin Pulls are built but not confirmed against the real Cloudflare; the token permission check and the public-address lookup are checked against a stand-in only.
- **Certificates:** an order through a DNS provider other than Cloudflare has never been run end to end.
- **Routers:** UPnP port forwarding is tested mostly against a stand-in router; real routers differ in what they list and answer. Nothing watches from outside the network whether the server can be reached.
- **Devices:** the installable app was never tried on real phones or tablets; the panel was not tried on a real phone, real Safari or with a screen reader.
- **Email:** the less common entries of the built-in provider table are unverified.
- **arm64:** the arm64 zip is built and signed by the same checks, but only the amd64 build is started by them, and the install scripts are tried on amd64 machines only.
- **Limited API tokens and roles:** by upstream's design the "Configuration" permission still controls the network edge (URLs, hostnames, trusted proxies, the admin address list), "Resources" is root through Docker and "Admin" through the terminal. Treat a token with one of those as powerful. Turnstile and the blocks at Cloudflare's edge were tried against stand-ins, never against the real Cloudflare.
- Upstream's generated API documentation (`api-docs/`) does not know the fork's routes, and upstream's test `TestEveryRouteIsDocumented` fails for them by design.

The working list of what is left is [`FORK-IDEAS.md`](FORK-IDEAS.md).

---

## Install and update

### What is supported

The fork is built for the owner's own use: **upstream's bare-metal installation on Linux** (Cosmos in a folder such as `/opt/cosmos`, run as the systemd service `CosmosCloud` that `cosmos service install` sets up, configuration in `/var/lib/cosmos`), amd64 and arm64. A new server is installed with `install.sh` (below) on Debian 12 or 13, or Ubuntu 24.04 or 26.04. The fork publishes **no Docker image**; its updater replaces files in place, which a Docker install does not do. Nothing is promised beyond that: no support, no warranty (see [`LICENCE`](LICENCE) and [`FORK-LICENCE.md`](FORK-LICENCE.md)), no guaranteed upgrade path back to upstream. Questions about the fork belong in this repository's issues, not on upstream's Discord or issue tracker.

### Install

On a Debian 12 or 13, or Ubuntu 24.04 or 26.04 server (amd64 or arm64, with systemd) where no Cosmos is installed:

```sh
curl -fsSLO https://github.com/CristianCasapu/Cosmos-Server/releases/latest/download/install.sh
less install.sh                  # it runs as root: read it first
sudo bash install.sh --dry-run   # what it would do; changes nothing
sudo bash install.sh
```

What it does, in this order:

1. It looks for a Cosmos that is already there: this fork, upstream's Cosmos (in a folder or as a container), a `CosmosCloud` unit, an `/opt/cosmos` folder, or the data of an earlier one in `/var/lib/cosmos`. If it finds one it **stops**, before it changes anything. It never runs over an installation, and no option makes it: an installed fork updates itself (see [How updates arrive](#how-updates-arrive)).
2. It checks the system: root, Debian or Ubuntu in a supported version (another is refused unless `--force`, at your own risk), the processor, systemd, free space (about 1.2 GB in `/opt`, 700 MB in `/var/tmp`, 300 MB in `/var/lib`), and free ports: 80 and 443, else 8080/8443, else 2052/2053. On a new server the system's own daily apt run (`apt-daily`, `unattended-upgrades`) often holds apt for a few minutes: the script says which program it waits for, and waits at most 10 minutes for it.
3. It works out which packages are missing and has apt **simulate** installing them, on a copy of the package lists: a package that cannot be had, or a dead network, stops it before anything was changed.
4. It installs `ca-certificates`, `curl`, `unzip` and `openssl` if they are missing, downloads the release from GitHub (or takes it from `--from`) and **verifies it before it unpacks anything**: the Ed25519 signature file against the release keys written into the script when the release was built, the zip's SHA-256 against the signed line, the MD5, and every entry of the zip (no absolute path, no `..`, no link, no duplicate name, nothing outside its top folder). A release that does not verify stops it, and the four tools it just installed are removed again: nothing is left.
5. It installs what Cosmos needs (`iproute2`, `procps`, `util-linux`, `psmisc`, `avahi-daemon` for `.local` names, `iptables`, and the Storage pages' tools `fuse3`, `mergerfs`, `snapraid`, `samba`, `mdadm`, `parted`, `e2fsprogs` unless `--storage-tools no`) and **Docker from Docker's own apt repository** (Docker's signing key is inside the script). A Docker that is already there (the distribution's `docker.io`, Docker's packages, or the snap) is used as it is and never touched; one older than 20.10, or one that does not answer, stops the script.
6. It unpacks the release to `/opt/cosmos`, makes the data folder `/var/lib/cosmos` (mode 0700), writes the systemd unit `CosmosCloud` itself, enables and starts it, and waits until the panel answers.
7. When protection was chosen, it runs the release's protection pieces (see "Protection" below).
8. It prints where to finish the first-run wizard, and the one-time setup code.

It opens nothing to the internet: no firewall rule, no router forward. Everything it installed or wrote is listed in `/var/lib/cosmos-installer/record`, and the uninstaller removes exactly that.

**How far the first download is trusted.** `install.sh` itself comes over HTTPS from GitHub, and that is all that vouches for it (a checksum printed here would come from the same place). From there the script proves what it installs: it verifies the release's signature with the keys stamped into it. To check `install.sh` by hand as well, see [Checking a release by hand](#checking-a-release-by-hand). A copy of `install.sh` keeps trusting the keys it carries, also after a later release replaced a key: always download it anew from the latest release. A kept old copy may refuse a release signed only with a new key, and would go on trusting a key that was retired; when a key is retired because it leaked, the release notes say so, and copies from before that release must not be used.

The script must be run from a file: `curl … | sh` is refused. It installs the release it belongs to or a newer one: when a newer release is asked for (`--version`, or the newest is newer than the script), it takes that release's own `install.sh`, after checking it against the release's signed list of scripts. An older release is refused unless `--allow-older`.

**Where the wizard answers, and the setup code.** Until the first-run wizard has set HTTPS up, it speaks plain HTTP: what is typed into it crosses the network in clear.

- By default (no `--placement`, or a cloud server, the router's exposed host, `--reachable internet`, a public IPv4 address or a global IPv6 address on an interface of the server) the wizard answers on **127.0.0.1 only** until it is done. The script prints the SSH tunnel to open from your computer, for example `ssh -L 8080:127.0.0.1:80 you@your-server`, then `http://127.0.0.1:8080/` on that computer. In the wizard's HTTPS step give the server's real name or address, not 127.0.0.1. Once the wizard is done, Cosmos listens on every address as usual.
- With `--placement home` (and none of the above) the wizard answers on the home network, and the script says that anyone on that network could read the code and the password typed into it, and prints the tunnel as well.
- `--wizard-on-lan` makes the wizard answer on the network in every case: only where you trust that network.

The script checks where the wizard really listens before it says so, and says it loudly if the server listens on the network although 127.0.0.1 was asked.

At its first start a new Cosmos makes a **one-time setup code** (five groups of four letters and digits) and keeps it in `/var/lib/cosmos/setup-code`, readable by root only. `install.sh` prints it at the end (with `--yes` or without a terminal it names the file instead, unless `--print-setup-code`); it is also in the journal: `sudo journalctl -u CosmosCloud | grep -A1 'SETUP CODE'`. The wizard asks for it on its first screen, and the server refuses every setup request without it, so nobody else on the network, on the internet or on the server itself can take the wizard over. After a few wrong codes from one address that address is asked to wait (at most 15 minutes), but the right code is accepted at any time, so nobody can lock you out by guessing. It is removed once the setup is done. A Cosmos installed by hand gets one too.

**Questions and options.** The script asks no questions yet: every run takes the options given and its proposals, and never waits for a keyboard (also without a terminal). An option with an answers-file key can also be given as a line `key=value` of a file named with `--answers <file>` (an option on the command line wins over the file):

| Option | Answers-file key | Default |
|---|---|---|
| `--http-port <n>`, `--https-port <n>` | `http-port`, `https-port` | 80/443, else the first free pair of 8080/8443, 2052/2053 |
| `--storage-tools yes\|no` | `storage-tools` | yes |
| `--reachable internet\|home` | `reachable` | home |
| `--protection yes\|no` | `protection` | yes when reachable from the internet |
| `--unattended-upgrades yes\|no` | `unattended-upgrades` | as protection |
| `--placement home\|exposed-host\|cloud\|unknown` | `placement` | unknown (behind the router at home, the router's exposed host, or a cloud server) |
| `--ssh open\|from:<addresses>\|closed-i-have-a-console` | `ssh` | not given (for the host firewall of a server with no home network) |
| `--keep-ssh-open` | `keep-ssh-open=yes` | no |
| `--no-outside` | `outside-lookup=no` | an outside service may be asked for the public address when the router does not tell it |
| `--wizard-on-lan` | `wizard-on-lan=yes` | no (see above) |
| `--address`, `--upnp`, `--router` | `address`, `upnp`, `router` | accepted already, used by a later version |
| `--version <x.y.z-fork.n>`, `--from <zip or folder>` | — | the newest release, from GitHub |
| `--proxy http://<host>:<port>` | — | none: the proxy variables of your shell are not read |
| `--dry-run`, `--yes`, `--reuse-data`, `--allow-older`, `--print-setup-code`, `--force`, `--help` | — | |

`--from` installs a release you downloaded yourself (the zip with its `cosmos-cloud-<version>.sig` next to it) without the network for the release; the packages still come from apt. `--reuse-data` installs over a data folder the fork left (after an uninstall that kept it); upstream's data is refused.

**Protection, in this release.** `--protection yes` (the default with `--reachable internet`) is meant to run the release's protection pieces: Fail2ban, a firewall guard for the host and automatic security updates. **In this release the installer runs none of them.** The host firewall guard and the automatic security updates are not in the release yet (the script says "the protection piece … is not in this release: skipped"); the Fail2ban piece in `fork/fail2ban/` does not speak the installer's interface yet, so it is not run either ("PIECE NOT RUN"). Cosmos is installed all the same, and the run ends with exit code 16 and the line "installed WITHOUT the protection you asked for". Fail2ban can be installed by hand afterwards (`sudo apt-get install nftables`, then `sudo sh /opt/cosmos/fork/fail2ban/install.sh`; the uninstaller does not remove what was installed by hand). The installer runs the three pieces from a later release on.

**When something goes wrong.** If the script is interrupted (a lost SSH session, a reboot), run the same command again: it continues where it stopped (asked for another version or other ports, it refuses: exit 14). If it fails after it changed something, it says what it changed and how `uninstall.sh` removes it. A package that a failed apt run left half-installed is named and stays in the record. An apt or dpkg step that hangs is stopped after 100 minutes, with everything it started; run the script again, it finishes the package database first.

Exit codes: 0 done (or dry run); 1 failed (what was changed is printed); 2 bad usage; 3 system not supported; 4 the release did not verify (nothing unpacked, nothing left); 5 not root; 6 a package cannot be installed (nothing changed); 7 another run is going; 10 the fork is already installed; 11 upstream's Cosmos is installed; 12 another Cosmos, or a half-removed one, is there; 13 data of an earlier Cosmos is there (`--reuse-data`); 14 an interrupted run used other options; 15 the release is older than the script (`--allow-older`); 16 Cosmos is installed WITHOUT protection you asked for.

The manual procedure (below) stays as a fallback.

### Uninstall

```sh
sudo bash /opt/cosmos/fork/install/uninstall.sh          # says what it will remove, asks first
sudo bash /opt/cosmos/fork/install/uninstall.sh --yes    # unattended
```

It removes what `install.sh` installed and nothing else, as its record lists it: the service `CosmosCloud`, the protection pieces it set up (each with its own uninstaller), `/opt/cosmos`, and Docker's apt source and key. It keeps:

- **your data** (`/var/lib/cosmos`: settings, users, certificates), unless you add `--delete-data` (you then type the folder's name) or `--yes-delete-data`; with the data kept, `install.sh --reuse-data` installs Cosmos over it again;
- **the packages** it installed, unless you add `--remove-packages` (and `--purge` for their configuration files too); even then a package another of yours needs stays, and Docker's packages stay while any container exists or while the Docker daemon does not answer (its containers cannot be counted then);
- Docker's images, containers and volumes, always.

A Cosmos that was not installed by `install.sh` (by hand, or upstream's) is not touched, whatever the options: the uninstaller says "this Cosmos was not installed by install.sh: nothing to remove" and stops with exit code 3. The same holds for a Cosmos that `migrate.sh` moved from upstream: it was not installed from nothing, and the way back is `GOING-BACK.txt` in the migration's backup. Run a second time, it says there is nothing to remove and ends with 0. `--dry-run` shows what would be removed. It too must be run from a file, not from a pipe. Exit codes: 0 done; 1 something could not be removed (it says what); 2 bad usage; 3 nothing `install.sh` installed is here; 5 not root; 7 another run is going.

### What a release is

Releases are on this repository's *Releases* page, named `<upstream version>-fork.<build>` (for example `0.24.0-fork.22`: build 22, based on upstream 0.24.0). Each has, per architecture, `cosmos-cloud-<version>-amd64.zip` / `-arm64.zip` with an `.md5`, and one signature file `cosmos-cloud-<version>.sig` that covers both zips. From the first release with the install scripts on, it also has `install.sh` and `uninstall.sh` (the same files are inside each zip, under `fork/install/`), their `SHA256SUMS`, and `cosmos-cloud-<version>.scripts.sig`, the signed list of the two scripts.

### Checking a release by hand

The signature file is the signed text (`cosmos-fork-release-v1`, the version, and `sha256 <hash> <zip name>` per zip) followed by one or more `signature <base64>` lines. The public release keys are in [`src/forkrelease/keys.go`](src/forkrelease/keys.go) — take that file from the release's tag. With OpenSSL 3, in a folder holding the zip, the `.sig` and `keys.go`:

```sh
V=0.24.0-fork.22
ARCH=amd64
grep -v '^signature ' "cosmos-cloud-$V.sig" > manifest
grep " cosmos-cloud-$V-$ARCH.zip\$" manifest | awk '{print $2 "  " $3}' | sha256sum -c -
for key in $(grep -oE '^[[:space:]]*"[A-Za-z0-9+/]{43}=",' keys.go | grep -oE '[A-Za-z0-9+/]{43}='); do
  { printf '\060\052\060\005\006\003\053\145\160\003\041\000'; printf '%s' "$key" | base64 -d; } > key.der
  openssl pkey -pubin -inform DER -in key.der -out key.pem
  grep '^signature ' "cosmos-cloud-$V.sig" | cut -d' ' -f2 | while read -r s; do
    printf '%s' "$s" | base64 -d > signature.bin
    openssl pkeyutl -verify -pubin -inkey key.pem -rawin -in manifest -sigfile signature.bin
  done
done
```

The zip line must say `OK` and at least one line `Signature Verified Successfully`. (The `printf` adds the fixed 12-byte prefix that turns a raw Ed25519 key into the form OpenSSL reads.)

`install.sh` and `uninstall.sh` are checked the same way with `cosmos-cloud-$V.scripts.sig` (the signed text is `cosmos-fork-scripts-v1`, the version, and `sha256 <hash> <script>` per script), in a folder holding the script, that file and `keys.go`:

```sh
grep -v '^signature ' "cosmos-cloud-$V.scripts.sig" > manifest.scripts
grep ' install.sh$' manifest.scripts | awk '{print $2 "  " $3}' | sha256sum -c -
for key in $(grep -oE '^[[:space:]]*"[A-Za-z0-9+/]{43}=",' keys.go | grep -oE '[A-Za-z0-9+/]{43}='); do
  { printf '\060\052\060\005\006\003\053\145\160\003\041\000'; printf '%s' "$key" | base64 -d; } > key.der
  openssl pkey -pubin -inform DER -in key.der -out key.pem
  grep '^signature ' "cosmos-cloud-$V.scripts.sig" | cut -d' ' -f2 | while read -r s; do
    printf '%s' "$s" | base64 -d > signature.bin
    openssl pkeyutl -verify -pubin -inkey key.pem -rawin -in manifest.scripts -sigfile signature.bin
  done
done
```

### Moving upstream's Cosmos to the fork

`migrate.sh` moves upstream's Cosmos installed on the system itself (as upstream's `get.sh` installs it: `/opt/cosmos`, data in `/var/lib/cosmos`, the service `CosmosCloud`) to the fork. It keeps the settings, users, URLs, certificates and containers. It runs as root; download it from the same release and read it first:

```sh
curl -fsSLO https://github.com/CristianCasapu/Cosmos-Server/releases/latest/download/migrate.sh
less migrate.sh
sudo bash migrate.sh --dry-run    # what it would do; changes nothing
sudo bash migrate.sh
```

It is the same script as `install.sh` with its role changed: the system checks, the packages, the download and the signature checks are those described above.

**What it checks first, changing nothing:**
- The fork already there: exit 10, before anything else, whatever the options. Updates come from the panel.
- No Cosmos at all: exit 19. Use `install.sh`.
- It refuses with exit 17 and says why:
  - upstream's Docker container (not handled yet);
  - other folders than upstream's defaults;
  - a link or a mount point at `/opt/cosmos` or `/var/lib/cosmos` (or on their way): for example the data kept on another disk through a link;
  - a unit whose configuration, as systemd runs it, holds something the script does not know: drop-ins, environment files, `COSMOS_CONFIG_FOLDER` / `CONFIG_FILE`, another folder or user;
  - what an earlier migration left that no state of the script names;
  - a Cosmos newer than the version the fork is built on;
  - users still in MongoDB (before 0.23);
  - a first-run wizard that is not finished;
  - an update of upstream waiting in the folder;
  - a Cosmos that is not running.
- **A licence in the settings stops it with exit 18; `--accept-community-edition` migrates all the same.** The script only reads whether a licence is set. The message says what happens after the move:
  - The fork is built as upstream's community edition, with upstream's licence client unchanged.
  - **What the licence gives on upstream's community edition, it keeps on the fork.** The licence stays in the settings and is renewed with upstream's licence server (`api.cosmos-cloud.io`) when the fork starts and once a day. The licence, whether the server is an agent node and the number of users are sent, as upstream's community edition sends them. While the licence is valid, Constellation, remote storage and backups keep working, with its user and server limits.
  - **It loses only what exists in upstream's Pro build alone:** groups, CI and functions, managed databases, the registry, SeaweedFS and the scheduler.
- The backup's place must be:
  - new, and under folders only root can change;
  - on a local Linux file system (not vfat, exfat, NTFS, CIFS or NFS);
  - not in memory, and not under `/tmp` or `/var/tmp`;
  - with room for the copy and for what a rollback keeps there.
- An older upstream (0.23, for example) is migrated directly: the fork runs at its first start the same forward migrations that upstream's own update would.

**What it does:**
- It installs only what it needs itself (the download tools and jq). The rest of what Cosmos uses is what upstream's installation already put there.
- It notes how Cosmos answers: its settings, the code each of its URLs answers with, the containers, the listening ports, and that the first-run wizard refuses.
- It stops Cosmos (the containers keep running). It backs up `/opt/cosmos`, `/var/lib/cosmos` and the unit into a new folder, private (0700) from its creation. The default is `/var/backups/cosmos-before-fork-<time>`; `--backup-dir` sets another.
- It checks the copy against a list of every file with its SHA-256.
- It renames upstream's folder aside and puts the fork's verified release in `/opt/cosmos`, keeping your `start.sh`, the unit and the data. Then it starts the fork.
- It checks that everything answers as before: the same settings, URLs, containers and listening ports, and the wizard refused. The server keeps its administrator, who signs in with the old password.
- **On any failure in that run from the stop on, it puts everything back by itself and ends with exit 20.**
  - Upstream's folder is renamed back, and the release it tried is moved into the backup.
  - The data is put back only if the fork changed it: the data as it was is moved into the backup and named, then the backup's copy is put back and checked.
  - The unit, `/etc/sysconfig/CosmosCloud`, and the service enabled and running as before.
  - It then compares with before. If something still differs (for example a container someone changed meanwhile), it names it and ends with exit 22. If it could not put things back, it ends with exit 1 and says what to do by hand.
- **It never deletes anything it did not make in that run.** What it replaces is moved into the backup folder, and the path is printed.

**When interrupted** (a lost connection, a power cut), run it again:
- Interrupted before the fork was put in place: it starts upstream's Cosmos again if it ran before, keeps the earlier backup (named), and migrates from the start.
- Interrupted after the fork was put in place: it finishes only if nothing ran since and the data is still the backup's. Otherwise (a reboot started Cosmos, someone used the server) it replaces nothing. It stops with exit 21, says what it found, and waits for your word:
  - `sudo bash migrate.sh --undo` goes back to upstream. The data as it is now is moved into the backup (named), and the backup's copy is put back.
  - `sudo bash migrate.sh --continue` keeps the fork. The data as it is now is first copied into the backup, then the fork is checked and the migration completed.
- `--dry-run` never changes anything: over an interrupted migration it only says what a run and `--undo` would do.
- `uninstall.sh` refuses to touch a server whose migration is not finished (exit 3).

**Afterwards:**
- The backup stays, with `GOING-BACK.txt` in it, and upstream's installation folder stays as `/opt/cosmos-upstream-<version>-before-fork`. The backup holds the server's keys and its users' password hashes: delete both once you keep the fork.
- Everyone signs in once more: sessions opened with upstream's Cosmos are not taken over. The accounts and their passwords are those of before (the data folder is kept as it is).
- Updates come from the panel, as for any installation of the fork.
- `uninstall.sh` does not remove a Cosmos that `migrate.sh` moved (exit 3); going back is done by hand.

**Going back to upstream** is described in `GOING-BACK.txt`. There are two ways:
1. Exactly what was there before, from the backup. Anything changed since the migration is lost: it is moved aside, not deleted.
2. Keeping the data as the fork left it, by unpacking upstream's zip of 0.24.0 or newer over `/opt/cosmos` (keeping `start.sh`) and removing the fork's own files. Upstream ignores the fork's own settings and drops them at its next save. An upstream older than 0.24 cannot read settings that 0.24 converted.

The manual way still works: stop Cosmos, then back up both folders, copy the release's top folder over `/opt/cosmos` keeping your `start.sh`, and start it again.

### How updates arrive

The server checks this fork's releases after it starts and every six hours, and *Configuration > General* shows the running and the latest version with "Check" (installs nothing) and "Update now" (asks first). With upstream's automatic-update setting on, a newer release is installed at the maintenance time (02:00 by default, configurable). Every update is verified twice as described above; a release that does not verify is refused and the server stays on its version. This is the only way an installed fork is updated: **never run `install.sh` again to update**, it refuses to run over an installation (exit 10), however the fork was installed.

A release placed by hand for the launcher needs its signature next to it: `cosmos-update.zip` and `cosmos-update.sig` in the installation folder.

### Going back

The owner can return `production` to an earlier fork release with the "Roll production back" workflow; that is published as a new release under the next build number and installed like any update. Going back across an upstream version is at your own risk (settings already migrated are not migrated back), and a rollback cannot go to a release from before signing. **Never point a fork install at upstream's releases**: fork versions are not comparable with upstream's numbering. Returning to upstream means installing upstream's release by hand; this is not tested.

### How the fork follows upstream

Once a day an automated job takes upstream's latest stable release (once it has been out for three days), merges it on top of `production` as a candidate, builds and tests it and starts it once, and then **waits for the owner's approval** before it is released. If the merge conflicts or fails its checks, nothing is released and the merge is done by hand; on a conflict, **the fork's code wins** and upstream's change is re-applied around it.

---

## Licence

This repository holds code under two sets of terms. Neither replaces the other.

- **[`LICENCE`](LICENCE)** — upstream's licence, unchanged: Apache License 2.0 with the Commons Clause and an Anti Tampering Clause, from Cosmos-Server's author. It governs everything that comes from upstream, and the fork as a whole keeps complying with it. Upstream's README sums it up as: you may use it freely, fork it and redistribute it, but you may not sell it, a derivative of it, or a service based on it. The Anti Tampering Clause ends the licence of anyone who tampers with, circumvents or avoids a subscription, paywall, feature restriction or other licensing mechanism of the software.
- **[`FORK-LICENCE.md`](FORK-LICENCE.md)** — additional terms for the fork's own additions (the files, packages and changes that file lists), Copyright (c) 2026 Cristian Casapu, as section 4 of the Apache License allows for one's own modifications. In short: the additions may be used only as part of, or with, the free community edition of Cosmos-Server and under `LICENCE`; nobody — upstream's author included — may sell them or put them into a paid, hosted or otherwise commercial edition, product or service without the copyright holder's prior written consent; and they are designated "Not a Contribution" to upstream.

This summary is not the licence: the two files are. To ask for permission, open an issue in this repository.

---

## Contributing

See [`CONTRIBUTE.md`](CONTRIBUTE.md): how the fork is built, tested and released, the rules every change follows, and the terms under which contributions are accepted. Contributions meant for Cosmos itself belong in [upstream's repository](https://github.com/azukaar/Cosmos-Server), not here.

---

## About Cosmos (from upstream's README)

*The following is taken from upstream's README, shortened, and describes upstream's Cosmos-Server, credited to its author. Upstream's full README — with its comparison with other home-server systems, its sponsors and its community links — is at <https://github.com/azukaar/Cosmos-Server#readme>.*

![Cosmos panel (upstream's screenshot)](./screenshot1.png)

Cosmos is a way to self-host a home server securely and easily. It acts as a secure gateway to your applications, as well as a server manager. Whether you have a server, a NAS or a Raspberry Pi, you install Cosmos on it and connect to your applications through it, to get built-in security for all your services.

Cosmos is:

- **App Store**: install and manage applications with simple installers, automatic updates and security checks, alongside importing docker-compose files or the docker CLI.
- **Storage Manager**: manage disks, including parity disks and MergerFS.
- **Network Storages**: based on RClone, manage network storage, including remote ones, and share NFS / FTP from the UI. *(Remote storage needs upstream's licence.)*
- **Reverse proxy**: to containers, other servers, or static folders and SPAs, with automatic HTTPS.
- **Authentication server**: multi-factor authentication and several strategies (OpenID, forward headers, HTML).
- **Customisable home page** to reach all applications from one place.
- **Container manager**: manage containers and their settings, keep them up to date and audit their security, with docker-compose support.
- **VPN** (Constellation): reach applications from anywhere without opening router ports. *(Needs upstream's licence.)*
- **Backups**: incremental, encrypted and remote backups based on Restic. *(Needs upstream's licence.)*
- **Monitoring**: persisted, real-time monitoring with customisable alerts and notifications.
- **Identity provider**: manage users and invite friends and family to applications without sharing credentials.
- **SmartShield**: automatic protection with anti-bot and anti-DDoS strategies, including TCP protection.
- **CRON**: schedule tasks on the server or inside containers.

### What is the SmartShield?

SmartShield is an API protection package that secures applications with rate limiting and user restrictions, without manual adjustment of limits and policies:

- **Dynamic rate limiting** calculated from user behaviour.
- **Adaptive actions**: users who exceed their limits are throttled rather than cut off.
- **Bans and strikes**, temporary or permanent, issued automatically against abuse.
- **Global request control**: queues limit the total number of simultaneous requests.
- **User-based metrics**: requests, data and simultaneous connections per user.
- **Privileged access** for chosen user groups, exempt from some restrictions.
- **Customisable policies**.

<p align="center"><img src="./schema.png" width="70%" alt="SmartShield diagram (upstream's)" /></p>

### SDK and Terraform

A JavaScript/TypeScript SDK ([`sdk/README.md`](sdk/README.md)), a Go SDK ([`go-sdk/README.md`](go-sdk/README.md)) and a Terraform provider ([`terraform-provider-cosmos/README.md`](terraform-provider-cosmos/README.md)) come from upstream. They describe upstream's API and do not know the fork's own routes.

### Installing upstream's Cosmos

Upstream installs Cosmos with Docker (`azukaar/cosmos-server`) or bare-metal; its installation guide is at <https://cosmos-cloud.io/doc>. **That installs upstream's Cosmos, not this fork.** To run the fork, start from upstream's bare-metal installation and follow [Install and update](#install-and-update) above.
