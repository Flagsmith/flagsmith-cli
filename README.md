# Flagsmith CLI

The Flagsmith command-line interface helps you manage and roll out your feature flags without leaving your terminal.

## Install

```sh
brew install Flagsmith/tap/flagsmith
```

Or:

```sh
curl -fsSL https://get.flagsmith.com | sh
```

Installs to `$HOME/.local/bin` and adds it to your `PATH`. Options:

<!-- x-release-please-start-version -->

```sh
curl -fsSL https://get.flagsmith.com | sh -s -- --version v2.0.1 --bin-dir /usr/local/bin --no-modify-path
curl -fsSL https://get.flagsmith.com | sh -s -- --help
```

<!-- x-release-please-end -->

`FLAGSMITH_CLI_VERSION`, `FLAGSMITH_INSTALL_DIR` and `FLAGSMITH_NO_MODIFY_PATH` do the same if exported first.

To pin the installer itself, fetch it at a commit you trust: `raw.githubusercontent.com/Flagsmith/flagsmith-cli/<sha>/install.sh`.

On Windows:

```powershell
irm https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/main/install.ps1 | iex
```

Using npm:

```sh
npm install -g @flagsmith/cli
```

Alternatively, `go install github.com/Flagsmith/flagsmith-cli/v2@v2.0.1` (installs as `flagsmith-cli`), or grab an archive from [Releases](https://github.com/Flagsmith/flagsmith-cli/releases). <!-- x-release-please-version -->

## Build

```sh
go build -o flagsmith .
```

## Docker

```sh
docker run --rm -v "$PWD:/work" -e FLAGSMITH_API_KEY ghcr.io/flagsmith/flagsmith-cli flag list
```

A container has no keyring, so `flagsmith login` cannot store credentials there — pass `FLAGSMITH_API_KEY` or `FLAGSMITH_ENVIRONMENT_KEY`.

## Quickstart

```sh
flagsmith init          # log in, pick a project + environment, write flagsmith.json
flagsmith flag list    # list the flags in the current environment
```

## Commands

Full reference for every command and flag: <https://flagsmith.github.io/flagsmith-cli/>.

Reading works against any Flagsmith instance. Changing flags — `flag update`, `flag enable`/`disable`, `flag reorder`, `flag delete` — needs Flagsmith 2.263.0 or newer.

- `flagsmith init` — bind the current directory to a project (writes `flagsmith.json`).
- `flagsmith flag list` — list feature flags in the current environment.
- `flagsmith flag get <feature>` — show a single flag's state (`--segment <id>` or `--identifier <id>` for an override).
- `flagsmith flag update <feature>` — toggle (`--enable`/`--disable`), set the value (`--value`, `--type`), or re-weight a multivariate flag's variants (`--weight <key|id>=<percentage>`); `--segment <id>` or `--identifier <id>` targets an override.
- `flagsmith flag enable|disable <feature>` — shorthand for `flag update --enable`/`--disable` (same `--segment`/`--identifier` targeting).
- `flagsmith flag delete <feature> --segment <id>|--identifier <id>` — delete a segment or identity override.
- `flagsmith segment list` — list segments (`--include-feature-specific` to include feature-scoped ones).
- `flagsmith segment get <segment>` — show a segment and its rule tree.
- `flagsmith segment create <name> --rules @rule.json` — create a segment (`--description`, `--feature`).
- `flagsmith segment update <segment>` — replace the rules (`--rules`), description, or feature.
- `flagsmith segment delete <segment>` — delete a segment.
- `flagsmith feature list` — list project features (`--include-archived`).
- `flagsmith feature get <feature>` — show a feature and its variants.
- `flagsmith feature create <name>` — create a feature (`--value`, `--enabled`, `--description`, `--variants`).
- `flagsmith feature update <feature>` — update description or archive (`--description`, `--archive`/`--unarchive`).
- `flagsmith feature delete <feature>` — delete a feature.
- `flagsmith feature variant list|add|update|delete <feature>` — manage a multivariate feature's variants (by id or key).
- `flagsmith organisation list|get|create|update|delete` (alias `org`) — manage organisations.
- `flagsmith project list|get|create|update|delete` — manage projects (`create` uses `--organisation`).
- `flagsmith environment list|get|create|update|delete|clone` (alias `env`) — manage environments (by name or API key).
- `flagsmith environment key list|create|delete <environment>` — manage server-side SDK keys.
- `flagsmith environment document [environment]` — output the environment document (local-evaluation JSON).
- `flagsmith evaluate [feature]` (alias `eval`) — the flags an SDK resolves for the current environment (`--identity`, `--trait key=value`, `--persist`); `--js` writes the state a frontend SDK hydrates from, `--test` fails when a named flag is disabled.
- `flagsmith config` — show the resolved context and where each value comes from.
- `flagsmith login` / `logout` — browser OAuth (PKCE, loopback); also `auth login`/`auth logout`.
- `flagsmith auth status` — identity, organisations, credential source, token expiry.
- `flagsmith auth token` — print the active Admin API credential for curl/scripts.
- `flagsmith api <path>` — call any Flagsmith endpoint with the CLI's credentials applied (curl-like; `--sdk` for the SDK API, `-F`/`-f` fields).

## Conventions

- `--json` (or `FLAGSMITH_JSON_OUTPUT`) for machine-readable output; `--jq <expr>` to filter it.
- Static credentials: `FLAGSMITH_API_KEY` (Admin API), `FLAGSMITH_ENVIRONMENT_KEY` (SDK). When self-hosting Flagsmith, append the host and port, doubling `-` and writing `.` and `:` as `_`: `https://flagsmith-staging.com:8000` reads `FLAGSMITH_API_KEY_flagsmith__staging_com_8000`.
- Self-hosted: `--api-url` or `FLAGSMITH_API_URL`.
