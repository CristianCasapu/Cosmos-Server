# Working on this fork

Personal fork of Cosmos-Server, running in production on one server. `FORK.md`
says what the fork changes and how releases and upstream syncs work. Read it
first, and keep it current: every change to the fork gets a row there.

## Rules from the owner

These apply to every change, however small.

1. **Keep the existing front-end styling.** New UI must look like it was always
   there: MUI components, the form helpers in
   `client/src/pages/config/users/formShortcuts.jsx`, `MainCard`, the icons and
   spacing the neighbouring screens use, strings through `t()`. Colours and
   surfaces come from the MUI theme and the skin's variables
   (`client/public/skin/theme.css`), never hard-coded per component. No new UI
   library and no one-off look.
2. **Test before every commit, push, publish and deploy.** At minimum: Go build
   of the server and launcher, `go test` for the packages touched, and a full UI
   build. Behaviour that can be exercised must be exercised, not assumed. Say
   plainly what was tested and what could not be.
3. **Clean up after tests and after deploy/publish.** Remove test containers and
   images, temporary files, test data and build output that is no longer needed,
   on the server and in the repository.
4. **Build for every browser and screen.** Current Chrome, Firefox, Safari and
   Edge; phone, tablet and desktop widths. Use MUI's responsive props and
   breakpoints, give browser-specific features a fallback, and check narrow
   widths for overflow.
5. **This is a production server: add no vulnerability and no new attack
   surface.** Every new endpoint checks permissions like its neighbours do
   (`utils.CheckPermissions`) and validates its input. Never return, log or
   forward secrets; treat masked values (`***`) as masked. No new listening
   ports, no unauthenticated routes, no weakened TLS or cookie settings, no
   shelling out with request data, no new third-party script or font loaded
   from another origin. Think about what a request with hostile input does
   before writing the handler.

6. **Ask through the question dialog.** When a decision is the owner's, ask it
   with the interactive question tool (options to choose from, the
   recommendation first), not as questions written into a reply.

## Things that are easy to get wrong here

- **`production` is the server.** A push to `production` publishes a release
  and the server installs releases by itself. Never commit to `production`
  directly: work on `custom`, wait for its checks, then run
  `scripts/fork-promote.sh`. `custom` is not deployed, but keep it releasable.
- **Merging upstream.** The daily sync only merges what applies cleanly and
  passes the checks. When it cannot (an issue titled "Upstream vX.Y.Z does not
  merge cleanly..." is open), nothing is forced: the merge waits for a Claude
  session. Resolve it on `custom` (`git merge vX.Y.Z`). The owner's rule for
  conflicts: **what the fork has wins**. Keep the fork's behaviour and
  additions, and re-apply them on top of upstream's new code when upstream
  restructured the surroundings (taking "ours" blindly can leave code that no
  longer fits). Never drop a fork feature to make a merge easier; if keeping
  it is not possible, stop and ask. Then build, test, start the build on a
  copy of the production configuration, and only then promote.
- **Keep the diff against upstream small.** Prefer a new file plus a one-line
  hook over editing upstream code, so the daily upstream merge keeps applying
  cleanly. Mark additions with a `Fork addition:` comment.
- **Licence.** Apache 2.0 with Commons Clause and an Anti Tampering Clause: do
  not alter or bypass any licensing check or paid-feature gate, and do not
  copy, move or reuse the code of upstream's paid features (`src/pro`,
  `src/constellation`, `src/backups`, `src/storage/rclone.go`,
  `src/utils/cloud.go`). The owner decided on 2026-11-12, after the risk was
  put to them, that the fork may carry modules of its own, written
  independently, even where upstream sells something similar (first one:
  incremental snapshots, `src/snapshots`). Keep such modules self-contained:
  their own package, their own settings and screens, nothing imported from the
  paid code and nothing of upstream's gating changed. What gets built on this
  fork is the owner's decision. The fork's own code is offered under
  `FORK-LICENCE.md`: for use with the free community edition only, no
  commercial use by anyone (upstream included) without the owner's written
  consent, and not a contribution to upstream; it comes with no warranty.
  Keep that file's list of what it covers current when a package of the
  fork's own is added. Do not re-argue these decisions.
- **Translations.** Add new strings to `client/src/utils/locales/en/translation.json`
  and pass the English text as the `t()` default so other languages fall back.

## Build and test

Toolchains are not installed on the server; run them in containers, with the
repository mounted at `/src` (Go from `go.mod`'s version, Node 22).

```sh
# server + launcher (what build.sh does), and the fork's Go tests
cp src/update.go src/launcher/update.go
CGO_ENABLED=0 go build -o /tmp/cosmos src/*.go
CGO_ENABLED=0 go build -o /tmp/cosmos-launcher ./src/launcher/launcher.go ./src/launcher/update.go
go test ./src/utils/ ./src/authorizationserver/ -count=1
go test ./src/upnp/ ./src/configapi/ ./src/snapshots/ ./src/storage/ ./src/metrics/ ./src/user/ ./src/reach/ ./src/security/ ./src/telegram/ ./src/fail2ban/ ./src/maildetect/ ./src/dnsrecords/ ./src/cloudflare/... ./src/forkrelease/ ./src/market/ ./src/turnstile/ ./src/edgeblock/ ./src/certs/ ./src/reachwatch/ ./src/anyimage/ ./src/behind/ ./src/appsignin/ ./src/cron/ ./src/guides/ ./src/edithints/ ./src/watch/ -count=1
go test ./src/ -skip TestEveryRouteIsDocumented -count=1
go test ./src/docker/ -count=1
go test ./src/proxy/ -count=1

# The checks on GitHub do not run as root: run the tests as another user before
# pushing (`docker run --user 1000:1000 -e HOME=/tmp -e GOCACHE=/tmp/gocache`,
# the repository and the module cache mounted read-only). A test that relies
# on root's rights passes here and fails there.
#
# The container edit against a real Docker, never the server's: start
# docker:29-dind on a network of its own and run
#   COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 go test ./src/docker/ -run SafeEditLive -count=1

# the frontend's pure logic (quote the glob)
node --test "client/src/utils/*.test.mjs"

# UI (output in ./static, which is git-ignored)
npm ci --ignore-scripts --no-audit --no-fund
npx vite build --base=/cosmos-ui/
```

## Checking the UI in a browser

There is no browser on the server and the pages need a login, so UI work is
checked against a throwaway instance with a headless browser. Never point this
at the production instance.

```sh
# 1. build ./static and the server binary (see above) into a scratch folder with a meta.json,
#    start it in a container on its own Docker network (name: cosmos-smoke, port 18080,
#    COSMOS_CONFIG_FOLDER inside the container, no Docker socket)
# 2. finish the setup wizard over the API (POST /cosmos/api/newInstall with step 2, then
#    step 3 {httpsCertificateMode: "DISABLED", hostname: "cosmos-smoke"}, step 4 {nickname,
#    password}, step 5), then start the binary again: step 5 exits the process
# 3. run the checks from the Playwright image on the same network
docker run --rm --network <net> --ipc=host -v <dir with fork-ui-check.mjs>:/work -w /work \
  mcr.microsoft.com/playwright:v1.65.1-noble sh -c 'npm i playwright@1.65.1 && node fork-ui-check.mjs'
```

For the pages that list containers the test instance needs a Docker of its
own, never the server's: start `docker:29-dind` (`--privileged`,
`-e DOCKER_TLS_CERTDIR=`) on the same network, create a couple of dummy
containers inside it, and start the test instance with
`DOCKER_HOST=tcp://<dind>:2375`. Run the binary in a loop
(`while true; do ./cosmos; done`): saving a domain or finishing the wizard
makes it exit and expect to be restarted. The test containers show up in the
production panel's ServApps list while they exist; the owner has clicked on
them before, which recreates them and wipes their state, so keep the run short
and say what they are. `scripts/fork-ui-check-apps.mjs` covers these pages, `scripts/fork-ui-check-forms.mjs` the port, schedule, volume and mount forms (its header lists the dummy containers it expects).

WebKit does not keep the session cookie on a test hostname without a dot: give the test instance a dotted name for WebKit runs. An address under `/cosmos/api/` opened directly in a browser answers 403 (`HTTP009`, the same-origin check): scripts use `fetch` from a panel page, or a client without browser headers.

`scripts/fork-ui-check-telegram.mjs` (with `scripts/fork-fake-telegram.go` standing in for Telegram on an internal test network) and `scripts/fork-ui-check-fail2ban.mjs` (needs a Debian container with Fail2ban installed by `fork/fail2ban/install.sh`) cover those two cards; nothing is ever sent to the real Telegram and the installer is never run on the host by a test.

`scripts/fork-ui-check-sudo.mjs` checks that the password prompt comes back mid-work without losing a form.

`scripts/fork-ui-check-cloudflare.mjs` (with `scripts/fork-fake-cloudflare.go` answering as Cloudflare's API) and `scripts/fork-ui-check-email.mjs` (with `scripts/fork-fake-mail` as name server and mail server) run on an `--internal` Docker network; their headers list what they expect. Nothing is ever sent to the real Cloudflare or to real mail servers.

`scripts/fork-ui-check-upnp.mjs` runs against `scripts/fork-fake-igd.go`, a stand-in router; the test Cosmos needs `--cap-add NET_ADMIN` and `ip route replace default via <stand-in>` because the UPnP client only talks to the default gateway (the script's header has the details). The live tests against a real router are opt-in: `UPNP_LIVE=1 go test ./src/upnp/ -run TestLiveRouter$ -v` (read only) and `UPNP_LIVE_WRITE=1 … -run TestLiveRouterMapping` (maps port 39999 for a minute); run them only when the owner asks.

The form checks are `scripts/fork-ui-check-url-form.mjs`, `-all-addresses`, `-users`, `-cron-job`, `-port-chips`, `-requests`, `-throttled` and `-url-wizard` (each header lists the containers it expects; `ENGINES=chromium,firefox,webkit SIZES=d,m`). A gotcha when writing forms: Formik computes each change from the last render's values, so two `setFieldValue` calls made in one go lose the first: set several values with one `setValues`.

For the origin-pulls part the Cloudflare browser check needs the test Cosmos started with `--cap-add NET_ADMIN`, a Cloudflare address added inside its own network namespace, and the stand-in's `edge` role (the script header has the commands; `PART=pulls` is repeatable, the rest needs a fresh instance); the Fail2ban check has `PHASE=cloudflare`. The built-in Cloudflare shared origin-pull CA expires on 2029-11-01 and must be replaced in the source before then (a test fails when it has expired).

For a look at everything, `scripts/fork-ui-gallery.mjs` (with `-seed.mjs` to fill a throwaway instance, `-noise.sh` to make incidents from other addresses and `-sheet.mjs` for contact sheets) captures every page, tab and main dialog in light and dark at 1920, 1366 and 390 px and writes a JSON summary per picture; run it before and after a styling change and compare (the headers hold the usage). The owner asked for styling improvements on 2026-11-13: the old rule "keep the existing styling exactly" now means "stay within the skin" (`client/public/skin/theme.css`), AA contrast in both modes, nothing below 12 px.

A test Cosmos container's host name must not be a single hex letter (it can match a container ID prefix in its test Docker, and Cosmos then takes itself for that container); the creation check's `/mnt/data` must be on another device than the instance's other bind mounts (a tmpfs does not count as a data disk).

`scripts/fork-ui-check-creation.mjs`, `fork-ui-check-new-url.mjs` and `fork-ui-check-leftovers.mjs` check the creation paths, the New URL dialog and the leftovers of interrupted edits (setup in each header: a test Cosmos with its own Docker in Docker, a `registry:3` inside it, a data disk on another device and a local market source on an `--internal` network; the compose files of a test market are served over HTTPS: the browser blocks `http:` ones on the HTTPS panel and the script policy refuses `data:` ones). The live Docker tests, `COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 go test ./src/docker/ -run "CreationLive|SafeEditLive|LeftoversLive"`, run only against a Docker in Docker of the test's own (it needs `busybox:1.36` and `hello-world`), never the host's Docker. The gallery takes a `patch` option to change API answers per view; its seed makes a failing job and points its unreachable app at a documentation address; `fork-ui-check.mjs` stops at Storage's sixth tab on a test instance without disks.

The panel enforces a script policy: new code must not need eval, `new Function` or inline scripts (`forkScriptPolicy.test.mjs`), and a browser check must not use `page.waitForFunction` (Playwright evaluates it with eval; `checkScripts.test.mjs` forbids it). Commands of the API take POST only: a check script or seed that acts must send POST. `scripts/fork-ui-check-csp.mjs` (phases wizard, walk, headers, flows) walks every page under the policy; `fork-ui-check-commands.mjs` checks the POST-only commands; `fork-ui-check-turnstile.mjs` and `fork-ui-check-turnstile-interplay.mjs` check the sign-in check (setup in the header: the fake Cloudflare also serving siteverify, the test Cosmos in Turnstile test mode with Cloudflare's public test keys, the policy taken from the server); `fork-ui-check-edge-blocks.mjs` checks the edge-block card and the dashboard's "Block at Cloudflare" (the stand-in with `/token?firewall=1`, its edge role inside the test server's network). The compose files of a test market are served over HTTPS (the policy refuses `data:` addresses). The live label test is `-run ImageLabelsLive` against a Docker in Docker of the test's own.

`scripts/fork-ui-check-notifications.mjs` checks the notifications page, the details and the bell as an admin and as another user (`BASE`, `ADMIN_USER`/`ADMIN_PASS`, `USER_USER`/`USER_PASS`, `ENGINES`, `SIZES`, `MODES`, `SHOTS`); run one engine at a time, since parallel runs race on the read state. The stand-in router (`scripts/fork-fake-igd.go`) takes `/break?set=1` to close the connection on every request for a forward, which is how a real failure and recovery is made on a test instance.

`sh scripts/fork-reach-watch-e2e.sh up|clean` (it needs `./static`) builds the path a visitor takes on internal networks — a name, a Cloudflare stand-in that is also the name server (`scripts/fork-fake-edge.go`), a router container whose forwards follow the stand-in router's table (`fork-fake-igd.go` with `IGD_NAT_CHAIN`), the test Cosmos — so that an outage can be made for real; `scripts/fork-ui-check-reach-watch.mjs` (`PHASE`, `ENGINES`, `SIZES`; the browser container with `--add-host example.org:192.168.1.10`) pictures each phase.

The panel is on React 19, react-router 8 (import from `react-router`), MUI 9 and Vite 8. For new UI: `Grid` takes `size={{ xs: 12, md: 6 }}` and has no column direction; spacing and colours go in `sx`, not in system props; a loading button is `<Button loading>` (there is no lab package); a chart of react-apexcharts needs `width="100%"`; `Stack` resets its children's margins. For check scripts: find a dialog with `[role=dialog]:visible` (the closed phone drawer has that role too); an alert is `.MuiAlert-standard.MuiAlert-colorX`; compound classes such as `containedPrimary` are gone. A build and the unit tests do not catch a broken major (yup, the cookie provider and the charts each broke only in a browser): run the gallery and the check scripts. The build fetches nebula and restic from their own releases with SHA-256 values pinned in `.github/actions/fork-build/action.yml`: raising either needs the new hashes and a run of the paid code's command lines against it (see the plan).

Automatic dependency updates: `node scripts/fork-deps-update.mjs` (its header gives the manual command; `scripts/fork-deps-holds.txt` holds a package with the reason), tests `node --test scripts/fork-deps-lib.test.mjs scripts/fork-deps-update.test.mjs scripts/fork-deps-ci.test.mjs` (they need git, bash and jq), quick checks `sh scripts/fork-deps-quick.sh go|npm`; the browser smoke of the checks is `START=docker sh scripts/fork-ci-smoke.sh` after a build. The workflow `fork-deps-update.yml` runs by hand until its `schedule` lines are uncommented; the machine never edits files under `.github/workflows/` (it lists those pins for a session). `./src/launcher/` is tested after `cp src/update.go src/launcher/update.go` (remove the copy afterwards).

The update rollback (`src/forkrelease/trial*.go`): the trial file's format must not change incompatibly, because the proven (older) launcher reads what the new server writes: add fields only, and raise the format version only with both sides tolerating it; the same holds for the mark names (`cosmos-update.mark.<id>.<run>.<kind>`, `<id>.0.restore`, `cosmos-update.mark.noted`). A fix to the trial code takes effect one release later. The rollback is exercised for real only on throwaway instances: a scratch copy with a generated key in `keys.go`, releases zipped and signed like `fork-release.yml`, a GitHub stand-in and `scripts/fork-fake-telegram.go` on an `--internal` network, the loop `./cosmos-launcher && ./cosmos` under `--init`, installs through `POST /api/force-server-update`, a full disk as `--tmpfs …,exec`, Docker cut through a small proxy in front of a `docker:29-dind`, and the install folder bind-mounted when a debian:13 container must follow the README's reinstall block (curl, unzip and openssl installed first; the stand-in's CA through `CURL_CA_BUNDLE`). Never with the real key or GitHub.

`sh scripts/fork-deps-check.sh` says whether a dependency has a known vulnerability that is not yet in `scripts/fork-deps-known.txt` (exit 1: a new finding; 2: a tool could not run; `OUTDATED=1` also lists newer versions); a finding is either fixed by an update or added to that file with the reason it does not apply. The creation check needs registry images that carry multi-platform indexes (`docker buildx imagetools create --platform …`), or the refusal of an image for another processor is never reached. After a change of `package-lock.json`, a worktree needs a fresh `npm ci` (in a `node:26` container): hard-linking `node_modules` from the main clone is right only while the two lock files are the same.

Installing any image (`src/anyimage/`): `go test ./src/anyimage/` is in the list; against real images `COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 go test ./src/anyimage/ -run RecipesLive -v` (`ONLY=postgres,rustfs` narrows it; the Docker in Docker needs a way out, `mirror.gcr.io` works as its registry mirror) and `ANYIMAGE_LIVE_HUB=1 go test ./src/anyimage/ -run HubLive`; in the browser `scripts/fork-ui-check-any-image.mjs` with `scripts/fork-fake-dockerhub.go` (the setup is in its header: the test network uses 198.51.100.0/24 so the address check treats the stand-ins as internet hosts, and the test CA goes in `SSL_CERT_FILE` of the test Cosmos and in the Docker in Docker's system bundle, because `certs.d` is not used for docker.io). A new recipe is an entry of `src/anyimage/recipes.json` that cites the image's documentation. Sign-in details and managers (`src/appsignin/`): the recipes' sign-in, live: `COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 LIVE_HUB_MIRROR=mirror.gcr.io go test ./src/anyimage/ -run 'RecipesLive(Login|Managers)' -v` (about 18 minutes, `ONLY=` narrows it); `go test ./src/docker/ -run PrivateNetLive` (needs busybox:1.36) proves that databases and managers stay off the default bridge; `go test ./src/appsignin/ ./src/cron/ ./src/guides/ ./src/edithints/ -run RepairLive` runs the start-problem repair through the real creation route (needs postgres:18, dpage/pgadmin4:9, busybox:1.36). `sh scripts/fork-ui-check-credentials.sh up|check PHASE=story|look|delete|repair|more|rebuild|clean` plays the owner's story with the mail stand-in (`SUBNET=` when 198.51.100 is taken); `fork-ui-check-services.mjs` takes `INSECURE=1` against such a self-signed test instance. Redis Insight lists a pre-registered database only after its terms are accepted in its page; pgAdmin reads its server list and pass file only at its first start. A secret must never leave through the card's plain fields, the e-mail, the notification, the install plan or the security record: `src/appsignin/secrets.go` and its property test are the place to extend. Apps behind the proxy (`src/behind/`, `src/proxy/fork_public_host.go`): a known app is an entry of `src/behind/apps.json` with its source and a real example of its refusal (`apps_test.go` checks that each example matches its pattern and that no entry switches an app's protection off or widens who gets in); end to end with real images `BIN=<cosmos built with CGO_ENABLED=0> sh scripts/fork-behind-e2e.sh` (`up` keeps it, `clean`; `APPS=…`; its own Docker in Docker with `--internal` apps and a Cloudflare stand-in; it pulls through `mirror.gcr.io`), the browser story `scripts/fork-ui-check-behind.mjs` (setup in its header). Restarts of the web server are serialised and coalesced (`src/fork_server_lifecycle.go`); `BIN=<cosmos> sh scripts/fork-restart-e2e.sh` (`up`/`clean`, `SCALE`, `ONLY`, `GAPS`, `SETTINGS`, `PREFIX`, `SCRATCH`) hammers them end to end. A script that saves URLs still waits for the reload before it uses the new address (about half a second). Nothing the reload calls (`reloadForRestart`: the configuration, the OpenID server, the container bootstrap) may ask for a restart synchronously, and it never takes `ConfigLock`: `TestReloadForRestartNeverRestartsItself` guards that. `scripts/fork-ui-check-services.mjs` (`PHASE=story|bad|look`; the setup is in its header: the test Cosmos runs inside its own Docker in Docker so that it takes the server's path to containers, on a documentation subnet (`SERVER_IP`) plus a private one (`LAN_IP`) for the home-network address, with a registry and the Hub stand-in serving `DESCRIPTIONS`, and the switches `COSMOS_ANYIMAGE_TEST_NO_RECIPES=1` / `_NO_DESCRIPTIONS=1` of the `anyimage_testnet` build) plays an image that needs a setting, a database without a web address and a URL whose app is down. The recipes' live test takes `LIVE_HUB_MIRROR=mirror.gcr.io` (a full run is about 41 minutes). Test data a test reads must not have a name `.gitignore` hides (`*.log`): `fork_testdata_test.go` fails on that. `fork-ci-smoke.sh` builds the panel's service worker only with a relative `--outDir`: build in a hard-linked copy of the worktree when another build uses `./static`. `scripts/fork-ui-check-sensitive-edit.mjs` drives the tick an edit needs for new sensitive access (its header says how to set up its three test containers); the browser check's Cosmos is built with `-tags anyimage_testnet`, which a release build never passes (it lets the address guard accept the test network through `COSMOS_ANYIMAGE_TEST_ALLOW`).

Forms, part D (Telegram chats, scheduled jobs, alerts, monitoring database, snapshot excludes): `sh scripts/fork-ui-check-forms-d.sh up|setup|check|rebuild|clean` with `scripts/fork-ui-check-forms-d.mjs` (PHASE=wizard before setup, then telegram, cron, alert, database, snapshots; ENGINES, SIZES): a test Cosmos inside its own Docker in Docker with postgres:18 'maindb', a prepared /srv/appdata and the Telegram stand-in (`/updates?set=none|private|group|both|webhook`) on an --internal network. A rebuild restarts the stand-in and Cosmos together, because the stand-in makes a new certificate authority at each start. The existing `fork-ui-check-telegram.mjs` needs the saved minimum severity "low". `go test ./src/cron/` is in the list.

An app's links (plan 49): `BIN=<cosmos> STATIC=<static> sh scripts/fork-ui-check-app-links.sh up|check|clean` (`PHASE=all|seed|home|away|user|role`, `ENGINES`, `SIZES`) checks an app's links from home and from away: two `--internal` networks (192.168.77/24 home, 203.0.113/24 internet), the stand-in proxy `scripts/fork-fake-proxy.go` trusted by the test Cosmos, a Docker in Docker with busybox tagged as the apps' images. A URL's page opens on Overview: a script clicks the Setup tab before the form. Every link to an app is built by `client/src/utils/appInterfaces.js` and shown by `client/src/components/appOpen.jsx`: a new place that links to an app uses them, never a host turned into an href by hand.

Guides (`src/guides/`, plan 50): every code block of a guide carries `id=`, `runner=` and `chain=`; `BIN=<cosmos> STATIC=<static> sh scripts/fork-guide-examples.sh up`, then `run` (`ONLY=`, `PLACES=`) runs each block as the panel shows it, against RustFS through a test Cosmos and a Cloudflare stand-in that refuses over 100 MB, all inside one Docker in Docker; it also runs checks after a chain (`POST` in the script), a simulated attack before the recovery chain, and real tests of Cosmos' shield and timeouts; `browser PHASE=seed|home|away|user|console` runs `scripts/fork-ui-check-guides.mjs`; `clean` removes everything (only a folder holding its own marker). A new guide is a new file plus its runners; a claim in a guide about what a key can or cannot do, or about a restore, must have a block that proves it. `go test ./src/guides/` is in the list. The shield's arithmetic lives in `utils.ShieldLimits` only: the proxy and the guide's facts both call it (`TestBudgetViolationUsesTheShownLimits`).

The app's edit tabs (plan 47, I2c): `sh scripts/fork-ui-check-edit-tabs.sh up|check|clean` (`PHASE=…|unchanged`) checks the Network, Storage and Docker tabs on a Cosmos inside its own Docker in Docker on `--internal` networks with a home-network subnet, a data disk of its own and a fake render node made with mknod; `PHASE=unchanged` saves each tab of a worst-case container without changes and compares the WHOLE inspected container; `fork-ui-check-sensitive-edit.mjs` also runs on that setup (`DIND=http://198.51.100.5:2375`). Live tests against a real Docker: `COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 go test ./src/docker/ -run 'UnchangedSaveLive|DisguisedVolumeLive|PlainRecreateLive|SubnetChangedLive'` (headers in the test files). Every update of a container is a recreate: anything a tab does not send must survive it (`fork_edit_keep.go`, `fork_edit_network.go`); a new field of Docker's container shape needs no code there, but a new FORM field does. The sensitive rules are shared by Go and the panel through `src/docker/sensitive_cases.json`. `go test ./src/edithints/` is in the list.

Telegram messages (plan 48): the model of the threads is part of `go test ./src/telegram/` (3000 fixed seeds, 1500 with late events, and the seeds that failed once; `-short` runs a tenth of the fixed ones). Random passes: `MODEL_RANDOM_ONLY=1 MODEL_RUNS=n go test ./src/telegram/ -run 'TestModelOfTheThreads$|TestModelWithLateEvents$'` (or `MODEL_SECONDS=n`); replay one seed with its trace: `MODEL_SEED=n go test ./src/telegram/ -run 'TestModelSeed$' -v` (`MODEL_LATE=1` for a seed of the late events); split the race run across containers with `MODEL_SEED_RANGE=a-b`. End to end: `sh scripts/fork-telegram-messages-e2e.sh up|events|events2|events3|messages|clean` (`PREFIX`, `SCRATCH`, `SUBNET`): a throwaway Cosmos, its own Docker and the Telegram stand-in on an `--internal` network; `events` a sign-in burst, an app that keeps stopping, a disk alert and a restart, `events2` Docker not answering and a scheduled job that fails then works again, `events3` a 2 GiB tmpfs test disk filling fast (the server built with the tag `watch_fastclock`, which a release never has; `COSMOS_FILL_TEST_MOUNTS` is read only in that build), `messages` prints what the stand-in received. Nothing runs `--privileged`: the test Cosmos gets no capability or device and its S.M.A.R.T. watch is off, the test's Docker is rootless (`docker:29-dind-rootless`). It is a test bed: never run it on a production server, never against a real bot. Browser checks on its network (setup in their headers): `scripts/fork-ui-check-telegram-messages.mjs` (the "Telegram messages" card) and `scripts/fork-ui-check-mark-resolved.mjs` ("Mark resolved" on a notification's details).

The Cloudflare stand-in's `/token?` takes `waf`, `wafread` and `account` for what the token may do about blocks, and `/custom`, `/custom/add`, `/custom/delete` and `/accountrules` show and change its custom rules and account rules; `scripts/fork-ui-check-edge-blocks.mjs` says in its header how to set it up. The gallery also pictures the blocks at Cloudflare's edge (`domains-edge`, guarded, and `security-edge`, with the fixtures `edgeMock` and `edgeOverviewPatch`) and always blocks service workers. `fork-ui-check-cloudflare.mjs` stops at its per-app part (it looks for the "Domains" button the Internet section replaced: to repair). In Firefox a "dark" run of a check script can render light after the server's Cross-Origin-Opener-Policy process switch (the gallery is not affected).

The gallery holds fixtures for Storage and Users (`scripts/fork-ui-gallery-fixtures.mjs`: answers for the storage calls, users, notifications, events), blocks service workers and picks tabs by title; `CHECK=1 ONLY='^(storage|users|users-create)$'` guards those pages with the measures `cardOverflow`, `textOverlaps` and `squeezed`: a page that test instances show empty must get fixtures before it is called seen. `fork-ui-check-settings-forms.mjs` stops at its HTTP step where two buttons match "Add" (to repair).

The gallery also measures buttons inside links, heading jumps and the header's spacing, has views for the first pages, the Market from a stand-in answer, the review without DNS, every ServApps notice, a throttled home page, the More menu and the new-container steps, and takes `ANON_PAUSE` (default 6 s between the views shown without a session: the server throttles anonymous requests). `fork-ui-check-leftovers.mjs` opens the folded notice area first. Shell wrappers must quote environment values that contain spaces.

`scripts/fork-ui-check-forms4.mjs` checks the OpenID form, the storage dialogs, trusted proxies, the domain dialog and the small forms (setup in its header: self-signed HTTPS, 2052/2053, `ENGINES`/`SIZES`). A browser check that answers API calls itself must block service workers, or the panel's worker fetches past the interception. The live storage test, `COSMOS_FORK_LOOP=1 COSMOS_FORK_LOOP_DIR=… go test ./src/storage/ -run StorageFormsLive`, runs only in a privileged throwaway container with e2fsprogs, mergerfs and util-linux, against loop files of its own. The TLS listener's live check needs the server's peers on separate networks routed in through a router container, as the home names check does (`openssl s_client` and `curl` from the `golang:1.27` image). Nothing of the owner's goes into the repository: tests, fixtures and comments use documentation addresses and names (`192.0.2.x`, `198.51.100.x`, `203.0.113.x`, `192.168.1.x`, `example.org`, `server.local`), never a real address, machine name, domain or token.

`scripts/fork-ui-check-settings-forms.mjs` checks the HTTP settings, the URL protection levels, the alerts editor, the API token form and the format dialog (its header has the setup; the format dialog is answered by the browser, no disk is touched). The live format test, `COSMOS_FORK_LOOP=1 COSMOS_FORK_LOOP_DIR=… go test ./src/storage/ -run FormatLive`, runs only in a privileged throwaway container against loop files of its own (setup in the test file's header): never a storage command against this host's disks.

`CHECK=home sh scripts/fork-acme-pebble.sh` proves the names for use at home end to end (one order with the wildcard home name, the DNS-only record, a home visitor served, a foreign visitor routed in through a second internal network refused, switching off); `scripts/fork-ui-check-home-names.mjs` checks the card in three browsers (run on `<PREFIX>-net` with `--dns <SUBNET>.2` and Playwright pre-installed; its header has the details).

`scripts/fork-ui-check-pwa.mjs` checks the installable app (`PHASE=online|offline|all|update|off|switch|install|fresh|untrusted`, `ENGINES=chromium,firefox,webkit`; its header lists the phases). It needs HTTPS with a certificate the browser trusts: Playwright's Firefox ignores the enterprise policy, so the script puts the test CA into each Firefox profile with `certutil`; Playwright's WebKit keeps no cache storage in a saved profile, use `PHASE=all` there. The gallery takes `STANDALONE=1`, `SAFE_AREA=47,0,34,0` and `SHOT=viewport` to picture the panel as an installed app on a notched phone.

`scripts/fork-ui-check-expose.mjs` checks the Internet dialog and the installer's exposure step in three browsers; its header lists the setup (a test Cosmos, Docker in Docker with a `registry:3` inside it because compose installs always pull, the Cloudflare stand-in and the router stand-in, all on an `--internal` network: never the owner's Cloudflare account or router).

`sh scripts/fork-acme-pebble.sh` proves certificate orders for real against Pebble and the Cloudflare stand-in on an `--internal` network with the real server binary (options `PREFIX`, `SCRATCH`, `SUBNET`, `PEBBLE_IMAGE`, `KEEP=1`; `clean` removes what a kept run left; the variables `FORK_ACME_TEST_DIRECTORY`/`_CA`/`_TICK`/`_NO_ARI` are honoured only with a test authority); `scripts/fork-ui-check-certificates.mjs` then checks the Certificates card in three browsers against what a `KEEP=1` run left (its lookups are limited to 30 an hour: restart the test server before a second run). Never order from the real Let's Encrypt, production or staging, in a test.

`MDNS_E2E=new|old go test ./src/proxy/ -run MDNSE2E` runs only inside a container that has its own Avahi and dbus (Debian 13's systemd-resolved needs a drop-in with `MulticastDNS=yes` to act as a client); never against the host's Avahi. The sign-in in front of an app can be tried end to end on a throwaway instance with `httpsCertificateMode: SELFSIGNED`, a port other than 443 and Playwright's `ignoreHTTPSErrors` (a route with `AuthEnabled`, then open the app's address as a visitor).

`scripts/fork-ui-check.mjs` logs in, confirms the password (`/api/sudo`: most
admin actions need it), visits the main pages in three engines and at phone
width, and reports script errors. Look at the screenshots it writes. Errors
about containers are expected: the test instance has no Docker. Remove the
container, the network and the scratch folders afterwards.
