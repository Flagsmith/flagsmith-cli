# Flagsmith CLI v2 — Installation

Status: draft for discussion
Scope: how the `flagsmith` binary gets onto machines — local dev and CI. Authentication is covered in [002-authentication.md](002-authentication.md).
Stack: Go + cobra, single static binary named `flagsmith`, cross-compiled and released with goreleaser.

## 1. Distribution channels

Single static binary via goreleaser, from GitHub Releases:

- `brew install flagsmith/tap/flagsmith` (primary mac/linux path)
- `curl -fsSL https://get.flagsmith.com | sh` (CI-friendly, pinnable version)
- GitHub Action (`flagsmith/setup-cli@v1`) that installs + performs the OIDC exchange — makes the CI story one workflow line
- winget / scoop, deb/rpm, Docker image, `go install` for free
- Optional: publish a thin npm wrapper under the existing `@flagsmith` scope that downloads the binary — migration bridge for current npm installers

## 2. Target first-run experience

```
$ brew install flagsmith/tap/flagsmith
$ flagsmith login
✓ Opened browser… authenticated as kim@flagsmith.com (org: Flagsmith)
$ flagsmith flags list --project my-app --environment production
```

and in GitHub Actions, with org trust configured, the same commands with **no secrets at all**.

## 3. Open questions

1. **`get.flagsmith.com`** — does the install-script domain exist / who owns provisioning it?
