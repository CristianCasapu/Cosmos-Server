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

## Things that are easy to get wrong here

- **A push to `custom` is a deploy.** `fork-release.yml` publishes a release for
  every push (documentation-only pushes excepted) and the server installs
  releases by itself. Do not push work in progress to `custom`.
- **Keep the diff against upstream small.** Prefer a new file plus a one-line
  hook over editing upstream code, so the daily upstream merge keeps applying
  cleanly. Mark additions with a `Fork addition:` comment.
- **Licence.** Apache 2.0 with Commons Clause and an Anti Tampering Clause: do
  not alter, bypass or re-implement any paid feature or licensing check.
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
go test ./src/utils/ -run 'DeliverEmail|UsesImplicitTLS' -count=1

# UI (output in ./static, which is git-ignored)
npm ci --ignore-scripts --no-audit --no-fund
npx vite build --base=/cosmos-ui/
```
