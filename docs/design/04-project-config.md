# Flagsmith CLI v2: Project Config

Status: draft

Stored, and checked in version control, project-local configuration to enable robust workflows, created with an init command, and inspected with a config command.

## 1. Examples

### `flagsmith init`

With no credentials stored, and no project configuration present, the user is gently nudged to run `flagsmith init`:

```
$ flagsmith
The Flagsmith command-line interface.

Don't know where to start? Try:
  flagsmith init

Usage:
  ...
```

First run performs the OAuth login and interactively prompts for project and environment to record in the local configuration:

```
$ flagsmith init
No credentials found.
Log in to Flagsmith in your browser:
✓ Logged in to https://api.flagsmith.com as kim@flagsmith.com

┃ Project
┃ > my-app (12345)
┃   my-other-app (12346)
┃   Create a new project
↑ up • ↓ down • / filter • enter submit

✓ Wrote flagsmith.json
You're all set! Try:
  flagsmith flags list
```

Organisation prompt is skipped if user is only part of one organisation. For multi-org users, the chosen organisation is recorded so org-scoped commands don't have to ask again.

When selecting projects, the user has an option to create a new one, cwd name proposed as default for new project.

With an existing config in place, `flagsmith init` can be run to regenerate it. Current values preselected, changes shown as a diff:

```
$ flagsmith init
✓ Logged in as kim@flagsmith.com (keychain)
flagsmith.json exists — updating it.

- "project": 12345,
+ "project": 67890,
- "environment": "WqXhZk8sVY3dGgTqZ9pJmN",
+ "environment": "K2mVsGdXhZ8kQqZ9pJmNbJ",

┃ Write changes?
┃   Yes     No
←/→ toggle • enter submit • y Yes • n No

✓ Wrote flagsmith.json
```

### `flagsmith config`

The user wants to inspect their CLI configuration. `flagsmith config` enumerates and displays all configured values, correctly interpreting their source:

```
$ FLAGSMITH_ENVIRONMENT=K2mVsGdXhZ8kQqZ9pJmNbJ flagsmith --api-url https://flagsmith.acme.com config
Config file   /work/my-app/flagsmith.json           default
Project       my-app (12345)                        config
Organisation  Acme (3)                              config
Environment   Production (K2mVsGdXhZ8kQqZ9pJmNbJ)   env
API           https://flagsmith.acme.com            cli
SDK API       https://flagsmith.acme.com            default
```

JSON output is supported:

```
$ flagsmith config --json
{
  "configPath": { "value": "/work/my-app/flagsmith.json", "source": "default" },
  "project": { "value": 12345, "name": "my-app", "source": "config" },
  "organisation": { "value": 3, "name": "Acme", "source": "config" },
  "environment": { "value": "WqXhZk8sVY3dGgTqZ9pJmN", "name": "Development", "source": "config" },
  "apiUrl": { "value": "https://api.flagsmith.com", "source": "default" },
  "sdkApiUrl": { "value": "https://edge.api.flagsmith.com", "source": "default" }
}
```

`source` may be as follows, in order of precedence:
1. `cli` means provided via CLI flag.
2. `env` means environment variable.
3. `config` means the value comes from the configuration file.
4. `default` means derived from other value, or a global default.

## 2. `flagsmith.json`

### Discovery

By default, Flagsmith CLI looks for `flagsmith.json` in the current working directory, then traverses its parents up to the git toplevel. Outside a git repository, only the current working directory is checked — a stray `flagsmith.json` higher up the tree (which can carry an `apiUrl`) never applies silently.

Path to config can be provided explicitly via a global `-c/--config-path` flag, or a `FLAGSMITH_CONFIG_PATH` environment variable.

In addition to a `flagsmith.json` file, every value can be provided via a global CLI flag or a `FLAGSMITH_`-prefixed environment variable.

### Schema

`flagsmith.json`'s schema is described by `schema/flagsmith.json`.

Every file generated via `flagsmith init` includes a `"$schema": "https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/<CLI version tag>/schema/flagsmith.json"` entry. 

## 3. Name cache

Displaying organisations, projects (stored as IDs) and environments (stored as public keys) is enriched with names, stored in a local cache at `os.UserCacheDir()/flagsmith/cache.json`, keyed by `apiUrl`. Any Admin API command refreshes it opportunistically.

The names are strictly cosmetic: never consulted for authorisation or resolution, and a cache miss degrades to showing the bare ID/key. This keeps names out of committed values while keeping the CLI human-readable offline.
