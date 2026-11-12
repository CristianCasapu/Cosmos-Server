# About this fork

This is a personal fork of [Cosmos-Server](https://github.com/azukaar/Cosmos-Server)
by Yann Stepienik, kept for private use on one server. It is not affiliated
with or endorsed by the original project. The original licence (`LICENCE`:
Apache 2.0 with Commons Clause and Anti Tampering Clause) applies unchanged,
and nothing here alters any paid feature or licensing mechanism.

## Branches

- `master` mirrors the original project.
- `custom` (default) is the original plus the changes below. Releases are built
  from it.

## What is changed

| Change | Files |
| --- | --- |
| "Send test email" in the SMTP settings, usable before saving; reports each step of the attempt | `src/configapi/emailcheck.go`, `src/utils/emails_deliver.go` (+ test), `client/src/pages/config/emailTest.jsx`; modified: `src/httpServer.go`, `client/src/pages/config/configGeneral.jsx`, `client/src/api/config.ts`, `client/src/api/config.demo.ts`, `client/src/utils/locales/en/translation.json` |
| Email delivery: connection timeouts, and implicit TLS when the port is 465 | `src/utils/emails_deliver.go`; modified: `src/utils/emails.go` |
| Skin (font, colours, surfaces) and a light/dark switch | `client/public/skin/`; modified: `client/index.html`, `vite.config.js` |
| "Internet access" in the HTTP settings: shows which hostname Cosmos accepts and can ask the router, over UPnP, to forward Cosmos' own HTTPS port (and, only if asked, the HTTP port). Off by default; talks to the default gateway only; mappings are leased for one hour and renewed | `src/upnp/` (+ tests), `client/src/pages/config/internetAccess.jsx`; modified: `src/utils/types.go` (`HTTPConfig.UPnP`), `src/httpServer.go`, `client/src/pages/config/configHTTP.jsx`, `client/src/pages/config/index.jsx`, `client/src/api/config.ts`, `client/src/api/config.demo.ts`, `client/src/utils/locales/en/translation.json` |
| Updates come from this fork's releases instead of the original's | modified: `src/update.go` |
| Build, release and upstream-sync automation | `.github/workflows/fork-release.yml`, `.github/workflows/fork-sync.yml` |

## Versions and updates

A fork release is numbered `<major>.<minor>.<upstream patch × 1000 + build>`:
`0.23.4001` is build 1 on top of upstream `0.23.4`. Cosmos installs a release
only when its version is higher than the running one. Among the fork's own
releases this numbering sorts correctly (every build on upstream `0.23.5` is
above every build on `0.23.4`), and the server only ever looks at the fork's
releases. It does not sort against upstream's numbers: `0.23.4001` is higher
than upstream `0.23.5`, so a fork install must never be pointed back at
upstream's releases, and "requires Cosmos x.y.z" checks in the app market
compare against the fork number.

- Every push to `custom` builds and publishes a release (`fork-release.yml`),
  except pushes that only change Markdown. The build is started once and must
  serve the UI before it is published. There is no way to roll back by
  publishing a lower version: fix forward, or reinstall by hand.
- Once a day `fork-sync.yml` merges upstream's latest stable release into
  `custom` and makes sure the head of `custom` is released. If the merge
  conflicts, or cannot be pushed, it changes nothing and opens an issue; merge
  by hand and push.
- GitHub pauses scheduled workflows in a repository with no activity for 60
  days; if that happens, re-enable "Sync with upstream" in the Actions tab.
