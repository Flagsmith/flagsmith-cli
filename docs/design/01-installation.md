# Flagsmith CLI v2: Installation

Status: draft

## 1. Distribution channels

Single static binary via goreleaser, from GitHub Releases:

- `curl -fsSL https://get.flagsmith.com | sh` (CI-friendly, pinnable version via FLAGSMITH_CLI_VERSION)
- `Flagsmith/setup-cli@v1` GitHub Action installs + performs the OIDC exchange
- Docker image
- `go install` for free
- Optional: publish a thin npm wrapper under the existing `@flagsmith` scope that downloads the binary — migration bridge for current npm installers

## 2. `get.flagsmith.com`

`get.flagsmith.com` redirects to https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/main/install.sh. Security-conscious users are able to use the github CDN url directly, pinning a SHA they trust.

Upon successfull installation, `install.sh` prompts the user to run `flagsmith init`.

## 3. Later / deprioritised

- `brew install flagsmith/tap/flagsmith` (primary mac/linux path)
- winget / scoop, deb/rpm
