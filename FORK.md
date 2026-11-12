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
| Updates come from this fork's releases instead of the original's | modified: `src/update.go` |
| Build, release and upstream-sync automation | `.github/workflows/fork-release.yml`, `.github/workflows/fork-sync.yml` |

## Versions and updates

A fork release is numbered `<major>.<minor>.<upstream patch × 1000 + build>`:
`0.23.4001` is build 1 on top of upstream `0.23.4`. Cosmos installs a release
only when its version is higher than the running one, and this numbering sorts
each fork build above the upstream release it is based on and below the next
upstream patch.

- Every push to `custom` builds and publishes a release (`fork-release.yml`).
- Once a day `fork-sync.yml` merges upstream's latest stable release into
  `custom` and starts a release. If the merge conflicts it changes nothing and
  opens an issue; merge by hand and push.
