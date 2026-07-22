# Flagsmith CLI

The next-generation Flagsmith command-line interface (work in progress).

## Build

```sh
go build -o flagsmith .
```

## Quickstart

```sh
flagsmith init          # log in, pick a project + environment, write flagsmith.json
flagsmith flag list    # list the flags in the current environment
```

## Commands

- `flagsmith init` — bind the current directory to a project (writes `flagsmith.json`).
- `flagsmith flag list` — list feature flags in the current environment.
- `flagsmith flag get <feature>` — show a single flag's state (`--segment <id>` or `--identifier <id>` for an override).
- `flagsmith flag update <feature>` — toggle (`--enable`/`--disable`) or set the value (`--value`, `--type`); `--segment <id>` or `--identifier <id>` targets an override.
- `flagsmith flag delete <feature> --segment <id>|--identifier <id>` — delete a segment or identity override.
- `flagsmith segment list` — list segments (`--include-feature-specific` to include feature-scoped ones).
- `flagsmith segment get <segment>` — show a segment and its rule tree.
- `flagsmith segment create <name> --rules @rule.json` — create a segment (`--description`, `--feature`).
- `flagsmith segment update <segment>` — replace the rules (`--rules`), description, or feature.
- `flagsmith segment delete <segment>` — delete a segment.
- `flagsmith config` — show the resolved context and where each value comes from.
- `flagsmith login` / `logout` — browser OAuth (PKCE, loopback); also `auth login`/`auth logout`.
- `flagsmith auth status` — identity, organisations, credential source, token expiry.
- `flagsmith auth token` — print the active Admin API credential for curl/scripts.
- `flagsmith api <path>` — call any Flagsmith endpoint with the CLI's credentials applied (curl-like; `--sdk` for the SDK API, `-F`/`-f` fields).

## Conventions

- `--json` (or `FLAGSMITH_JSON_OUTPUT`) for machine-readable output; `--jq <expr>` to filter it.
- Static credentials: `FLAGSMITH_API_KEY` (Admin API), `FLAGSMITH_ENVIRONMENT_KEY` (SDK).
- Self-hosted: `--api-url` or `FLAGSMITH_API_URL`.

## Design

[Installation](docs/design/01-installation.md) ·
[Output & interactivity](docs/design/02-output-and-interactivity.md) ·
[Authentication](docs/design/03-authentication.md) ·
[Project config](docs/design/04-project-config.md) ·
[CRUD conventions](docs/design/05-crud.md) ·
[API](docs/design/06-api.md) ·
[Flags](docs/design/07-flags.md) ·
[Segments](docs/design/08-segments.md)
