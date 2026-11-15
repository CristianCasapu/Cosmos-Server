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
- **Signed releases, verified before install.** Each release carries a signature file (Ed25519 over the version and the SHA-256 of each zip). The server checks it before it places a download, and the launcher checks it again before it unpacks; a refused update leaves the server on its version and is recorded and notified. Downloads come only from this repository on GitHub's hosts, with size and time limits.
- **Sessions and password confirmation.** Session cookies are SameSite=Lax and bound to their hostname; logging out ends every session of the account on every device; a session lasts at most 30 days; the password confirmation for sensitive actions lasts a fixed two hours and the prompt comes back mid-work without losing what was typed. Five wrong two-factor codes lock the second step for fifteen minutes, administrators included.
- **A TLS listener that names nothing to strangers.** A peer from outside the local network and Cloudflare's ranges gets a certificate only for a name that is served directly, and that certificate names nothing else; for an unknown, shielded, `.local` or home name the handshake is refused. Upstream handed such a peer a certificate listing the panel's and every app's name.
- **Permission and hardening fixes** from a security review: the API answers the panel's own pages only (requests started by another site are refused); secrets carried in addresses are kept out of logs; sign-in bodies are bounded; accounts that may not read the configuration get a reduced view; the configuration backup is owner-only; applications receive the visitor's address from the server, not from the visitor; the "forgot password" form no longer tells a stranger which accounts exist; a local address is never banned permanently; dependencies were brought up to date for security.

### Reaching the server

- **Cloudflare zones and the shield.** A guided set-up under *Configuration > Domains*: the API token is checked in plain words, every DNS record is previewed before anything is written, and a record Cosmos did not create is replaced only when ticked. Per address, a switch says whether it is served through Cloudflare's proxy or directly; a shielded name answers only Cloudflare and the local network. Optionally, Cloudflare's *Authenticated Origin Pulls* prove that a request really came through Cloudflare, switched on with a test and an automatic fall-back.
- **Certificates through DNS.** Let's Encrypt certificates that browsers trust without an open port 80/443: through a connected Cloudflare zone (the domain and its wildcard), another DNS provider's token, or one CNAME that hands the proof to a domain Cosmos can write to. Renewal follows each certificate's lifetime and the authority's renewal window, with back-off and limits kept across restarts. A *Certificates* card says per domain who issued it, what it covers, when it ends and why the last attempt failed; the home page warns once per domain with a lasting problem.
- **Local names.** `.local` names are published as address records with the server's LAN address on the interfaces that face the home network (never on Docker bridges or VPNs), checked every minute, and re-published when Avahi loses them; the panel says whether each name is answered and which devices can open one.
- **Names for use at home.** Per Cloudflare domain, an optional label (default `home`) gives every app a name `<app>.home.<domain>` at the server's private address, through one DNS-only wildcard record and one wildcard certificate name — so no app name appears in DNS or in the Certificate Transparency logs. Such a name answers the local network only.
- **Router port forwards.** Over UPnP, Cosmos can ask the router to forward its own HTTPS port and, per app, each published port (off by default). It uses the next free external port when one is taken, never asks for reserved ports, never changes a mapping it did not create, and asks again by itself — with growing intervals — when the router loses or refuses a forward.
- **"Internet" per app.** One action per app (and a step at installation) that puts the web address under a configured domain — through Cloudflare where that works, directly otherwise, with the reason in words — chooses who may open it, and forwards other ports under a name of their own. Withdrawing removes the address copy, its DNS record, the router mapping and the port's name. A reachability chip on each app says whether it can be reached from the internet and what to do about each problem; nothing is tested from outside.

### Creating and running apps

- **One review before anything is created**, for the Market, the compose import and the new-container form: what is created and published, the addresses, the restart policy, where the data goes, what Cosmos filled in, processor problems, and a tick for each sensitive access to the server (the Docker socket, system folders, privileged mode, host network…). The server checks all of it again before it creates anything.
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

## Status and honesty

This fork is used in production on one server. It is checked by unit tests, by a start of each build, and by browser checks in Chromium, Firefox and WebKit against throwaway instances and stand-ins for outside services. Some things have **not** been verified for real:

- **Cloudflare:** Authenticated Origin Pulls are built but not confirmed against the real Cloudflare; the token permission check and the public-address lookup are checked against a stand-in only.
- **Certificates:** an order through a DNS provider other than Cloudflare has never been run end to end.
- **Routers:** UPnP port forwarding is tested mostly against a stand-in router; real routers differ in what they list and answer. Nothing watches from outside the network whether the server can be reached.
- **Devices:** the installable app was never tried on real phones or tablets; the panel was not tried on a real phone, real Safari or with a screen reader.
- **Email:** the less common entries of the built-in provider table are unverified.
- **arm64:** the arm64 zip is built and signed by the same checks, but only the amd64 build is started by them.
- **Limited API tokens:** permission checks for a token or role limited to "Configuration" or "Users" are not complete yet. Treat any token with one of those permissions as an administrator token.
- Upstream's generated API documentation (`api-docs/`) does not know the fork's routes, and upstream's test `TestEveryRouteIsDocumented` fails for them by design.

The working list of what is left is [`FORK-IDEAS.md`](FORK-IDEAS.md).

---

## Install and update

### What is supported

The fork is built for the owner's own use: **upstream's bare-metal installation on Linux** (Cosmos in a folder such as `/opt/cosmos`, run as the systemd service `CosmosCloud` that `cosmos service install` sets up, configuration in `/var/lib/cosmos`), amd64. The fork publishes **no Docker image**; its updater replaces files in place, which a Docker install does not do. Nothing is promised beyond that: no support, no warranty (see [`LICENCE`](LICENCE) and [`FORK-LICENCE.md`](FORK-LICENCE.md)), no guaranteed upgrade path back to upstream. Questions about the fork belong in this repository's issues, not on upstream's Discord or issue tracker.

### What a release is

Releases are on this repository's *Releases* page, named `<upstream version>-fork.<build>` (for example `0.24.0-fork.22`: build 22, based on upstream 0.24.0). Each has, per architecture, `cosmos-cloud-<version>-amd64.zip` / `-arm64.zip` with an `.md5`, and one signature file `cosmos-cloud-<version>.sig` that covers both zips.

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

### First install, over upstream's bare-metal install

1. Back up the configuration folder (`/var/lib/cosmos` by default) and the installation folder.
2. Download and check the release for your architecture (above).
3. Stop Cosmos (`systemctl stop CosmosCloud`), unpack the zip and copy the contents of its top folder over the installation folder, **keeping your `start.sh`**, then start it again (`systemctl start CosmosCloud`).
4. The menu now shows the version with `-fork.<build>`. From here on the server looks for updates in this fork's releases only.

### How updates arrive

The server checks this fork's releases after it starts and every six hours, and *Configuration > General* shows the running and the latest version with "Check" (installs nothing) and "Update now" (asks first). With upstream's automatic-update setting on, a newer release is installed at the maintenance time (02:00 by default, configurable). Every update is verified twice as described above; a release that does not verify is refused and the server stays on its version.

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
