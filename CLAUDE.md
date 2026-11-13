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
  fork is the owner's decision: it is free, comes with no warranty, and anyone
  (upstream included) may take from it. Do not re-argue that decision.
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
go test ./src/upnp/ ./src/configapi/ ./src/snapshots/ ./src/storage/ ./src/metrics/ ./src/user/ ./src/reach/ ./src/security/ ./src/telegram/ ./src/fail2ban/ -count=1
go test ./src/ -skip TestEveryRouteIsDocumented -count=1
go test ./src/docker/ -count=1
go test ./src/proxy/ -count=1

# The checks on GitHub do not run as root: run the tests as another user before
# pushing (`docker run --user 1000:1000 -e HOME=/tmp -e GOCACHE=/tmp/gocache`,
# the repository and the module cache mounted read-only). A test that relies
# on root's rights passes here and fails there.
#
# The container edit against a real Docker, never the server's: start
# docker:27-dind on a network of its own and run
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
  mcr.microsoft.com/playwright:v1.49.1-noble sh -c 'npm i playwright@1.49.1 && node fork-ui-check.mjs'
```

For the pages that list containers the test instance needs a Docker of its
own, never the server's: start `docker:27-dind` (`--privileged`,
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

`scripts/fork-ui-check.mjs` logs in, confirms the password (`/api/sudo`: most
admin actions need it), visits the main pages in three engines and at phone
width, and reports script errors. Look at the screenshots it writes. Errors
about containers are expected: the test instance has no Docker. Remove the
container, the network and the scratch folders afterwards.
