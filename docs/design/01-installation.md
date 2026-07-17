# Flagsmith CLI v2: Installation

Status: draft

## 1. Distribution channels

Single static binary via goreleaser, from GitHub Releases:

- `brew install flagsmith/tap/flagsmith` (primary mac/linux path)
- `curl -fsSL https://get.flagsmith.com | sh` (CI-friendly, pinnable version)
- `Flagsmith/setup-cli@v1` GitHub Action installs + performs the OIDC exchange
- Docker image
- winget / scoop, deb/rpm
- `go install` for free
- Optional: publish a thin npm wrapper under the existing `@flagsmith` scope that downloads the binary — migration bridge for current npm installers

## 2. Target first-run experience

```
$ brew install flagsmith/tap/flagsmith
$ flagsmith login
✓ Opened browser… authenticated as kim@flagsmith.com (org: Flagsmith)
$ flagsmith flags list --project my-app --environment production
```

and in GitHub Actions, with org trust configured and `id-token: write` permission, the same commands with no secrets at all.

## 3. Open questions

1. **`get.flagsmith.com`** — does the install-script domain exist / who owns provisioning it?
