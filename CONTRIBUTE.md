# Contributing to this fork

This repository is a personal fork of [Cosmos-Server](https://github.com/azukaar/Cosmos-Server). This file says how the fork is worked on. **Contributions meant for Cosmos itself go to [upstream](https://github.com/azukaar/Cosmos-Server), under upstream's own rules** (see [Upstream's contribution guide](#upstreams-contribution-guide) below and upstream's [`cla.md`](cla.md)); a change made here is not offered to upstream.

Read [`FORK.md`](FORK.md) first (what the fork changes, how releases and upstream merges work), then [`CLAUDE.md`](CLAUDE.md) (the working notes this file is drawn from) and [`FORK-IDEAS.md`](FORK-IDEAS.md) (what is left).

## Branches and the way to a release

- `master` mirrors upstream.
- `custom` is the development branch. Every push to it is built, tested and started once (`.github/workflows/fork-ci.yml`); nothing is released from it.
- `production` is what installed servers receive: a push to it publishes a signed release, which servers install by themselves. **Nobody commits to `production` directly**; it only moves by promotion from `custom`, by an approved upstream sync, or by the rollback workflow. Larger work is done on a branch of its own and merged into `custom`, which must stay releasable.
- `sync/<tag>` exists only while an upstream release is being checked.

A change reaches a release like this:

1. It is merged into `custom` and the checks pass for that exact commit (UI build, the fork's Go and JavaScript tests, server and launcher build, one start of the built server).
2. `scripts/fork-promote.sh` moves `production` forward to the head of `custom`. It refuses unless the checks passed for exactly that commit and `custom` already contains everything on `production`.
3. `.github/workflows/fork-release.yml` builds again in a job with a read-only token and no secret, then a second job that runs no code from the repository signs the zips with the release key and refuses to publish what this build or the previous release could not verify. A push that only changes Markdown files does not cut a release.
4. Servers find the release, verify its signature twice (server, then launcher) and install it.

What the reviewer looks at before promoting: that the checks are green for that commit, that the change has a row in `FORK.md`, that every new behaviour has a test that fails without it, that the browser checks were run where the UI changed, and that nothing in the diff breaks a rule below.

## How upstream changes come in

Once a day `.github/workflows/fork-sync.yml` takes upstream's latest stable release (once it is three days old), merges it on top of `production` as `sync/<tag>`, runs the same checks and waits for the owner's approval in the GitHub environment `upstream-approval`. Only after approval is it released and brought into `custom`. When it does not merge cleanly or fails the checks, nothing is released and an issue is opened; the merge is then made by hand on `custom`.

On a conflict, **what the fork has wins**: keep the fork's behaviour and re-apply it on top of upstream's new code where upstream restructured the surroundings (taking "ours" blindly can leave code that no longer fits). Never drop a fork feature to make a merge easier. For conflicts in dependency files: in `go.mod` and `package.json` take the higher version of each entry; keep both sides of `go.sum`, then `go mod tidy`; for `package-lock.json` take upstream's file, then `npm install --package-lock-only` with the merged `package.json`; afterwards `govulncheck ./...` and `npm audit`.

## Where fork code lives

Fork additions live in **files of their own** — `fork_*.go`, packages of the fork's own (`src/security`, `src/reach`, `src/telegram`, `src/fail2ban`, `src/forkrelease`, `src/cloudflare`, `src/maildetect`, `src/certs`, `src/snapshots`, `src/upnp`), new frontend files, `scripts/fork-*`, `fork/`, `client/public/skin/` — with the **smallest possible hook** in upstream's files, each marked by a comment starting `Fork addition:`. This keeps the daily upstream merge applying cleanly, and it marks exactly which lines are the fork's, which matters for the licence (see below). Every change gets a row in `FORK.md`.

## Rules that are not negotiable

- **Security first.** No vulnerability and no new attack surface: no new unauthenticated endpoint, no new listening port, no weakened TLS or cookie setting. Every new route checks permissions like its neighbours (`utils.CheckPermissions`, password confirmation where they need it); state-changing routes are POST/PUT/DELETE only. Every input is bounded (size, count, length) and validated on the server, whatever the form validates. Never build a shell command from input: `exec.Command` with fixed arguments only. No secret in logs, events, notifications, error messages or API answers; a secret in the configuration is masked for readers. Text from a request is never rendered as HTML, and what goes to a log another program parses is sanitised.
- **The licence boundary.** Never alter, bypass or imitate a licence check or a paid-feature gate. Never copy, move, enable or reuse the code of upstream's paid features: `src/pro`, `src/constellation`, `src/backups`, `src/storage/rclone.go`, `src/utils/cloud.go`. A module of the fork's own in an area upstream sells is written independently, in its own package, with its own settings and screens, and imports nothing from the paid code. Without a licence the panel hides paid features; it never unlocks them.
- **Nothing private in the repository.** Code, tests, fixtures, comments, commit messages and scripts never contain a real server's addresses, machine or interface names, a real domain, email, account name, public address, token or key — not even as an example. Use documentation values: `192.0.2.x`, `198.51.100.x`, `203.0.113.x`, `192.168.1.x`, `example.org`, `server.local`. Test tokens are obviously made up.
- **English text through translation keys:** `t('key', 'English fallback')`, with the key added to `client/src/utils/locales/en/translation.json` only. Plain words an administrator understands; icon-only buttons have a tooltip; every command gives feedback (give a new command a label in `client/src/utils/activity.js`).
- **Every behaviour with a test that fails without it.** Frontend logic that can be pure goes into plain JS modules with `node --test` tests next to them (`*.test.mjs`).
- **Three browsers and a phone.** Every UI change is checked in Chromium, Firefox and WebKit, and at 390 px wide: no sideways page scroll, nothing cut off. Stay within the skin (`client/public/skin/theme.css`): MUI components and the helpers the neighbouring screens use, AA contrast in both modes, nothing below 12 px, no new UI library or one-off look.
- Do not regenerate `api-docs/`: upstream's test `TestEveryRouteIsDocumented` fails for the fork's routes by design, and the checks skip it.

## Building and testing

No toolchain is assumed on the host: run everything in containers with the repository mounted at `/src` — Go in `golang:1.27` (the version in `go.mod`), Node in `node:22`. From `/src`:

```sh
# server and launcher (what build.sh does)
cp src/update.go src/launcher/update.go
CGO_ENABLED=0 go build -o /tmp/cosmos src/*.go
CGO_ENABLED=0 go build -o /tmp/cosmos-launcher ./src/launcher/launcher.go ./src/launcher/update.go

# the fork's Go tests (the same list the checks run)
go test ./src/forkrelease/ -count=1
go test ./src/utils/ ./src/authorizationserver/ -count=1
go test ./src/upnp/ ./src/configapi/ ./src/snapshots/ ./src/storage/ ./src/metrics/ ./src/user/ ./src/reach/ ./src/security/ ./src/telegram/ ./src/fail2ban/ ./src/maildetect/ ./src/dnsrecords/ ./src/cloudflare/... ./src/certs/ -count=1
go test ./src/ -skip TestEveryRouteIsDocumented -count=1
go test ./src/docker/ -count=1
go test ./src/proxy/ -count=1

# the frontend's pure logic (quote the glob)
node --test "client/src/utils/*.test.mjs"

# the UI (output in ./static, which is git-ignored)
npm ci --ignore-scripts --no-audit --no-fund
npx vite build --base=/cosmos-ui/
```

**Run the Go tests as a non-root user too**, as the checks on GitHub do — a test that relies on root's rights passes as root and fails there:

```sh
docker run --rm --user 1000:1000 -e HOME=/tmp -e GOCACHE=/tmp/gocache \
  -v "$PWD":/src:ro -w /src golang:1.27 go test ./src/utils/ -count=1
```

Some tests are live and opt-in; each runs only against a throwaway of its own, never a real server, disk, router or Docker: the container edit and creation tests (`COSMOS_FORK_LIVE_DOCKER=1` with a `DOCKER_HOST` pointing at a `docker:27-dind` of the test's own), the storage and format tests (`COSMOS_FORK_LOOP=1`, in a privileged throwaway container against loop files), the mDNS test (`MDNS_E2E=…`, in a container with its own Avahi), the certificate proof (`sh scripts/fork-acme-pebble.sh`, against Pebble and a Cloudflare stand-in on an internal network — never the real Let's Encrypt), and the router tests (`UPNP_LIVE=1`, only when the owner asks). Each test file's header has its setup.

## Checking the UI in a browser

UI work is checked with a headless browser against a **throwaway instance**, never a real server: build `./static` and the server, start it in a container on a Docker network of its own (with its own `docker:27-dind` if the pages list containers), finish the setup wizard over the API, then run the checks from the Playwright image on the same network:

```sh
docker run --rm --network <net> --ipc=host -v <dir with the check script>:/work -w /work \
  mcr.microsoft.com/playwright:v1.49.1-noble sh -c 'npm i playwright@1.49.1 && node fork-ui-check.mjs'
```

- `scripts/fork-ui-check*.mjs` are the checks per area (each header lists the setup it expects; most take `ENGINES=chromium,firefox,webkit` and `SIZES=d,m`). Outside services are always stand-ins on an internal network: `scripts/fork-fake-cloudflare.go`, `scripts/fork-fake-telegram.go`, `scripts/fork-fake-mail/`, `scripts/fork-fake-igd.go` (a router). Nothing is ever sent to the real Cloudflare, Telegram, mail servers or a real router.
- `scripts/fork-ui-gallery.mjs` (with `-seed.mjs`, `-noise.sh`, `-sheet.mjs` and `-fixtures.mjs`) pictures every page, tab and main dialog in light and dark at 1920, 1366 and 390 px and writes a summary per picture (script errors, sideways scroll, small text, contrast); with `CHECK=1` it fails on overflow, overlaps and small targets. Run it before and after a styling change and look at the pictures yourself.
- **Service workers:** a check that answers API calls itself must block service workers, or the panel's worker fetches past the interception. The installable-app check (`scripts/fork-ui-check-pwa.mjs`) needs HTTPS with a certificate the browser trusts.
- An address under `/cosmos/api/` opened directly in a browser answers 403 (the same-origin check): scripts call the API with `fetch` from a panel page. WebKit keeps no session cookie on a host name without a dot: give the test instance a dotted name.
- Remove the containers, networks and scratch folders afterwards.

## Licence of contributions

> **Note:** the following paragraph is a legal statement drafted for the fork's owner and has not yet been confirmed by the owner or reviewed by a lawyer.

The fork's own additions are offered under [`FORK-LICENCE.md`](FORK-LICENCE.md) on top of upstream's [`LICENCE`](LICENCE). By submitting a contribution to this repository (a pull request, a patch, or code in an issue), you agree that:

1. your contribution is your own work, or you have the right to submit it, and you are entitled to grant the licence below;
2. your contribution becomes part of the fork's additions and is offered under the terms of `FORK-LICENCE.md`, and, where it changes upstream's code, also under upstream's `LICENCE`;
3. you grant the copyright holder named in `FORK-LICENCE.md` a perpetual, worldwide, non-exclusive, royalty-free, irrevocable licence to use, reproduce, modify, distribute and sublicense your contribution, including the right to give the written consent that `FORK-LICENCE.md` mentions, on the copyright holder's own terms;
4. like the rest of the fork's additions, your contribution is not a contribution to upstream Cosmos-Server, and is not submitted to upstream's Licensor by being published here.

If you do not agree, please do not submit the contribution. Contributions meant for upstream go to upstream, under upstream's contributor licence agreement ([`cla.md`](cla.md)).

---

## Upstream's contribution guide

*The text below is upstream's own contribution guide, kept as it was. It describes how to work on upstream Cosmos-Server; parts of it (Node 16, Go 1.21, MongoDB, the `unstable` branch) do not apply to this fork, which uses the commands above.*

Thanks for your interest in contributing to Cosmos, and its source code!

### Getting Started

#### Basics

You need Node/NPM, Docker and golang in order to run Cosmos.
Docker can be any version.

Install Docker

```
curl -fsSL https://get.docker.com | sudo sh
```

Install Node **V16**: https://nodejs.org/en/download

Install GOlang v1.21.6: https://go.dev/doc/install

#### Required files

Aside from those, you will need a bunch of files. First, You optionally need GeoLite2-Country.tar.gz if you want to test the geolocation (contains IP infos). You can get it from https://maxmind.com/
Second you need the Nebula binaries from https://github.com/slackhq/nebula/releases/download/v1.7.2/nebula-linux-arm64.tar.gz and https://github.com/slackhq/nebula/releases/download/v1.7.2/nebula-linux-amd64.tar.gz
Make sure you have the binary set in the root of your workspace like  this:

![image](https://github.com/azukaar/Cosmos-Server/assets/7872597/11de2778-e799-47b7-b0ba-443e658965dd)


#### Setting up and running the dev client

The Front-end is compiled using Vite, a nodejs app that bundles frontend. It is written in React.
First, install dependencies with `npm install`.
Once node is installed, you can simply start the frontend's code with: `npm run client`. This will start the client dev server on :5173

#### Building the Client

One you are done developing, you can build the client locally with `npm run client-build`

#### Setting up the dev server

Server is a bit more tricky. Once Go is installed, first use `npm run build` to build the server. it will be built in the build/ folder.
Make sure you build the client at least once (previous section) as the build script simply copy paste the output of the `client-build` script.

The server can be started with `npm run start` this will run Cosmos with a bunch of arguments. The most importants are

 * COSMOS_CONFIG_FOLDER=./zz_test_config : this overwrites the config file to be zz_test_config in the cosmos folder (easier to reach)
 * CONFIG_FILE=./config_dev.json : this sets the config file to be config_dev.json

This means that you can find both config files  and config folders at the root of your workspace when running the test build.

When running the client is `npm run client` it will connect to this dev server on port :8443

#### Recommandations

It is recommended that you run the local server with 
 * HTTP self-signed: it's better to test in HTTPS than HTTP for closer-to-real-life setup
 * External DB, so you dont lose it during testing stuff (you can use Mongo Atlas for free)
 * Docker on
 * DEBUG level logs
 * Monitoring off for less relevant logs to be removed

#### Build and test docker image

You can build a test docker image with `npm run dockerdevbuild` (or `npm run dockerdev` to build it and run it as well). This will use the non-production dockerfile at `dockerfile.local` as opposed to the prod `dockerfile`

#### About the demo

There is a demo system integrated that builds a FE with mocked API calls. You can test it with `npm run devdemo` or build it with `npm run demo`

#### Branches

The main code is on master, the unstable (aka testing version) is on the unstable branch. Any PR should be made against the  relevant  branch (new features on unstable, hotfixes only on master directly) 

#### Other considerations

Try to communicate with the rest of the team **before** you make changes to make sure that it is inline with the vision, and you dont end up doing something we can't merge in. Keep the PR simple, break them into multiple PRs if required.

Thanks again for your consideration, see you soon in the PR section ;)
