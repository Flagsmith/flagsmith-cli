# Flagsmith CLI

Manage Flagsmith from your terminal and your pipeline.

```bash
curl -sSL https://get.flagsmith.com | sh
flagsmith init
```

[![Release](https://img.shields.io/github/v/release/Flagsmith/flagsmith-cli)](https://github.com/Flagsmith/flagsmith-cli/releases) [![License](https://img.shields.io/badge/license-MIT-blue)](./LICENSE)

## Repo binding for the whole team

```bash
flagsmith init
```

Writes a `flagsmith.json` you commit with your code, recording which project and environment this repo maps to.  Teammates can clone, log in, and they're pointed at the right project. No setup docs, and no changes landing in the wrong environment.

If you work across several projects, this is even more useful, both for you and for LLMs interacting on your behalf.

## Install

**macOS and Linux**
```bash
curl -sSL https://get.flagsmith.com | sh
```

Installs to `~/.local/bin`. The installer takes options:

```bash
curl -sSL https://get.flagsmith.com | sh -s -- --version v2.0.0 --bin-dir /usr/local/bin --no-modify-path
curl -sSL https://get.flagsmith.com | sh -s -- --help
```

`FLAGSMITH_CLI_VERSION`, `FLAGSMITH_INSTALL_DIR` and `FLAGSMITH_NO_MODIFY_PATH` do the same if exported first. To pin the installer itself, fetch it at a commit you trust:

```bash
curl -sSL https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/<sha>/install.sh | sh
```

**Windows**
```powershell
irm https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/main/install.ps1 | iex
```

**Docker**
```bash
docker run --rm -e FLAGSMITH_API_KEY ghcr.io/flagsmith/flagsmith-cli flag list
```

**Go**
```bash
go install github.com/Flagsmith/flagsmith-cli/v2@latest
```
Note this installs the binary as `flagsmith-cli`, not `flagsmith`.

Or download an archive from [Releases](https://github.com/Flagsmith/flagsmith-cli/releases).

## Authentication

```bash
flagsmith login          # opens your browser, stores credentials in your OS keyring
flagsmith auth status    # who am I, and against which instance
flagsmith auth token     # print the current token, for scripts
```

Without a browser, use static credentials:

- `FLAGSMITH_API_KEY` — Admin API, for managing flags, features, projects and so on
- `FLAGSMITH_ENVIRONMENT_KEY` — SDK-side, for evaluation and environment documents

Containers have no keyring, so `flagsmith login` can't persist there — pass one of the above instead.

Self-hosted? Point at your own instance with `--api-url` or `FLAGSMITH_API_URL`:

```bash
flagsmith --api-url https://flagsmith.internal/api/v1 flag list
```

## What can you do with the CLI?

The CLI is designed for both humans and machines, it provides full management capabilities for daily work, and integrates natively with CI pipelines. A few notable capabilities that enable this flexibility include:

### Built-in JSON parsing and jq filtering

```bash
flagsmith flag list                                    # a table
flagsmith flag list --json                             # JSON
flagsmith flag list --json --jq '.[] | select(.enabled) | .name'
```

jq is compiled into the binary so your agent doesn't have to pipe the output anywhere.

### Stopping a release that depends on a flag that's off

```bash
flagsmith evaluate checkout-v2 --test
```

Exits non-zero when a flag your release needs isn't enabled. Drop it into any pipeline to block a bad deploy before it ships:

```yaml
- name: Verify release flags
  run: flagsmith evaluate checkout-v2 --test
  env:
    FLAGSMITH_API_KEY: ${{ secrets.FLAGSMITH_API_KEY }}
```

### Using it with a coding agent

The CLI occupies less context than a full MCP tool catalogue: your agent gets one binary with self-documenting help, and pays for it only when it runs something.

## Migrating from the previous CLI.

The previous version of the CLI, which could only fetch flag states at build time and write to a file, is being deprecated, and replaced by this newer version.

**Already using the old package?** The new CLI provides the same functionality using the following commands:

```bash
flagsmith evaluate --js              # the state a frontend SDK hydrates from
flagsmith environment document       # the local-evaluation environment document
```

We encourage you to migrate to the new version to both maintain existing functionality while enjoying the benefits of the full capabilities of the new version.

## Commands

Every command carries worked examples in `--help`, which is always current:

```bash
flagsmith flag --help
```

<details>
<summary><b>Full command list</b></summary>

### Setup

- `flagsmith init` — log in, pick a project and environment, write `flagsmith.json`
- `flagsmith config` — show every setting and where it came from
- `flagsmith login` / `logout`
- `flagsmith auth status` / `auth token`

### Flags

- `flagsmith flag list` — list flags in the current environment
- `flagsmith flag get <feature>` — show a flag's state
- `flagsmith flag enable|disable <feature>` — toggle in the current environment
- `flagsmith flag update <feature>` — change value or state, including `--segment` and `--identifier` overrides
- `flagsmith flag delete <feature> --segment <id>|--identifier <id>` — delete a segment or identity override

### Features

- `flagsmith feature list` — list project features (`--include-archived`)
- `flagsmith feature get <feature>` — show a feature and its variants
- `flagsmith feature create <name>` — `--value`, `--enabled`, `--description`, `--variants`
- `flagsmith feature update <feature>` — `--description`, `--archive` / `--unarchive`
- `flagsmith feature delete <feature>`
- `flagsmith feature variant list|add|update|delete <feature>` — manage a multivariate feature's variants

### Segments

- `flagsmith segment list` — `--include-feature-specific` to include feature-scoped segments
- `flagsmith segment get <segment>` — show a segment and its rule tree
- `flagsmith segment create <name> --rules @rule.json` — `--description`, `--feature`
- `flagsmith segment update <segment>` — replace rules, description, or feature
- `flagsmith segment delete <segment>`

### Environments, projects, organisations

- `flagsmith environment list|get|create|update|delete|clone` (alias `env`) — by name or API key
- `flagsmith environment key list|create|delete <environment>` — server-side SDK keys
- `flagsmith environment document [environment]` — the local-evaluation environment document
- `flagsmith project list|get|create|update|delete` — `create` takes `--organisation`
- `flagsmith organisation list|get|create|update|delete` (alias `org`)

### Evaluation

- `flagsmith evaluate [feature]` (alias `eval`) — the flags an SDK would resolve for the current environment
  - `--identity <id>` and `--trait key=value` — evaluate as a specific identity
  - `--test` — exit non-zero when a named flag is disabled
  - `--js` — write the state a frontend SDK hydrates from
  - `--persist` — write the result to disk

### Anything else

- `flagsmith api <path>` — call any Flagsmith endpoint with your credentials applied, e.g. `flagsmith api /projects/`

</details>

## Docs, and getting help

- Documentation: [docs.flagsmith.com](https://docs.flagsmith.com/integrating-with-flagsmith/CLI)
- Bugs and requests: [open an issue](https://github.com/Flagsmith/flagsmith-cli/issues)
- Include `flagsmith --version` and how you installed it
