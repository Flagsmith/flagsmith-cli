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
  flagsmith [command]

Available Commands:
  ...
```

First run performs the OAuth login and interactively prompts for project and environment to record in the local configuration:

```
$ flagsmith init
No credentials found.
Log in to Flagsmith in your browser:
✓ Logged in to https://api.flagsmith.com as kim@flagsmith.com
? Organisation        › Flagsmith
? Project             › my-app (12345)
? Default environment › Development (WqXhZk8sVY3dGgTqZ9pJmN)
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
flagsmith.json exists; updating it.
? Project             › acme-api (67890)
? Default environment › Production

- "project": 12345,
+ "project": 67890,
- "environment": "WqXhZk8sVY3dGgTqZ9pJmN",
+ "environment": "Production",

? Write changes? (y/N) › y
✓ Wrote flagsmith.json
```


## 1. Examples

### `flagsmith init`, interactive (TTY)

First run, Flagsmith SaaS — no credentials yet:

```
$ flagsmith init
No credentials found.
Log in to Flagsmith in your browser:
✓ Logged in to https://api.flagsmith.com as kim@flagsmith.com
? Organisation        › Flagsmith
? Project             › my-app (12345)
? Default environment › Development
✓ Wrote flagsmith.json — try `flagsmith flags list`
```

```json
{
  "$schema": "https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/main/schema/flagsmith.json",
  "project": 12345,
  "environment": "WqXhZk8sVY3dGgTqZ9pJmN"
}
```

The values are bare IDs and keys; the human-readable names (`my-app`, `Development`) come from the name cache whenever the CLI displays context.

Brand-new user, no project yet. Create one inline (suggested name: the current directory):

```
$ flagsmith init
✓ Logged in as kim@flagsmith.com (keychain)
? Organisation        › Flagsmith
? Project             › Create new project: "acme-web"
✓ Created project acme-web (12388)
? Default environment › Development
✓ Wrote flagsmith.json — try `flagsmith flags list`
```

For multi-org users, the chosen organisation is recorded so org-scoped commands (like the project creation above) never have to ask again:

```json
{
  "$schema": "https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/main/schema/flagsmith.json",
  "project": 12388,
  "organisation": 3,
  "environment": "P6bJQmnTqZ9dGY3sVXhZ8k"
}
```

Re-run with an existing `flagsmith.json`. Current values preselected, changes shown as a diff:

```
$ flagsmith init
✓ Logged in as kim@flagsmith.com (keychain)
flagsmith.json exists — updating it.
? Project             › acme-api (67890)
? Default environment › Production

  project:     12345 (my-app)                  → 67890 (acme-api)
  environment: WqXhZk8sVY3dGgTqZ9pJmN (Development) → K2mVsGdXhZ8kQqZ9pJmNbJ (Production)

? Write changes? (y/N) › y
✓ Wrote flagsmith.json
```

### `flagsmith init`, non-interactive (no TTY)

All inputs come from flags and ambient credentials. Confirmations require `--yes`.

With a static master key (`--environment` accepts an environment name when Admin API credentials can resolve it; a client-side key always works, creds or not):

```
$ FLAGSMITH_API_KEY=***** flagsmith init --project 12345 --environment production --yes
✓ Authenticated with master API key ($FLAGSMITH_API_KEY)
✓ Verified access to my-app (12345)
✓ Wrote flagsmith.json
```

GitHub Actions with OIDC trust configured — zero secrets:

```
$ flagsmith init --project 12345 --yes
✓ Authenticated via GitHub Actions OIDC (repo: acme/web)
✓ Verified access to my-app (12345)
✓ Wrote flagsmith.json
```

Missing required input — usage error, exit 2, names the flag:

```
$ flagsmith init --yes < /dev/null
Error: no TTY and no --project given — cannot prompt.
Usage: flagsmith init --project <id> [--environment <key|name>] [--api <url>] --yes
(exit 2)
```

No usable credentials — exit 1, lists the options:

```
$ flagsmith init --project 12345 --yes
Error: no credentials found, and a browser login needs a TTY.
Set FLAGSMITH_API_KEY, run in a CI OIDC context with an org trust
relationship, or run `flagsmith login` interactively first.
(exit 1)
```

Existing file without `--yes` — refuses to overwrite, exit 1:

```
$ flagsmith init --project 67890
Error: flagsmith.json exists. Pass --yes to overwrite it non-interactively.
(exit 1)
```

### Self-hosted instance

```
$ flagsmith init --api https://flagsmith.acme.internal
```

```json
{
  "$schema": "https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/main/schema/flagsmith.json",
  "project": 42,
  "environment": "M9pJmNbJK2mVsGdXhZ8kQq",
  "apiUrl": "https://flagsmith.acme.internal"
}
```

The SDK API defaults to the same host: `sdkApiUrl` → `apiUrl` → Edge. One key for self-hosted, zero for SaaS.

### Split SDK topology (dedicated flags proxy/CDN)

```json
{
  "$schema": "https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/main/schema/flagsmith.json",
  "project": 42,
  "environment": "K2mVsGdXhZ8kQqZ9pJmNbJ",
  "apiUrl": "https://flagsmith.acme.internal",
  "sdkApiUrl": "https://flags.acme.com"
}
```

### SDK-only consumer (no Admin API credentials, no project)

```json
{
  "$schema": "https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/main/schema/flagsmith.json",
  "environment": "K2mVsGdXhZ8kQqZ9pJmNbJ"
}
```

The environment key alone is a complete config: flag evaluation works with zero Admin API credentials, and no Admin-surface identifiers are needed. Client-side keys are public by design and safe to commit; server-side keys (`ser.*`) fail schema validation and are refused by `flagsmith init`.

### Monorepo

```
acme/
  apps/web/flagsmith.json    → "project": 12345
  apps/api/flagsmith.json    → "project": 67890
```

```
$ cd apps/api/handlers && flagsmith flags list
Using project acme-api (67890) from apps/api/flagsmith.json
```

The nearest `flagsmith.json` wins, walking up from the current directory. Names in context lines come from the local name cache. With a cold cache, the line shows the bare ID.

### Inspecting resolved context

```
$ FLAGSMITH_ENVIRONMENT=K2mVsGdXhZ8kQqZ9pJmNbJ flagsmith config
Project       my-app (12345)                        default
Organisation  Acme (3)                              default
Environment   Production (K2mVsGdXhZ8kQqZ9pJmNbJ)   env
API           https://api.flagsmith.com             default
SDK API       https://edge.api.flagsmith.com        default
Config file   /work/my-app/flagsmith.json
```

```
$ flagsmith config --json
{
  "configPath": "/work/my-app/flagsmith.json",
  "project": { "value": 12345, "name": "my-app", "source": "default" },
  "organisation": { "value": 3, "name": "Acme", "source": "default" },
  "environment": { "value": "K2mVsGdXhZ8kQqZ9pJmNbJ", "name": "Production", "source": "env" },
  "apiUrl": { "value": "https://api.flagsmith.com", "source": "default" },
  "sdkApiUrl": { "value": "https://edge.api.flagsmith.com", "source": "default" }
}
```

`flagsmith config --json` is the scripting interface: the fully resolved context with per-field sources. Scripts read it instead of parsing `flagsmith.json`. It works without credentials and offline: `value` and `source` are always present; `name` is best-effort enrichment from the local cache.

## 2. The file

`flagsmith.json` — committed, visible (no leading dot), never contains secrets. Written by `flagsmith init`, hand-editable with autocomplete and validation via the published schema ([schema/flagsmith.json](../../schema/flagsmith.json)).

Strict JSON, schema-validated. Field values are bare IDs and keys; human-readable names never live in the file — they come from the name cache wherever the CLI displays context (`config`, context lines, `init` diffs).

| Field | Type | Required | Meaning |
|---|---|---|---|
| `project` | integer | one of project/environment | Project ID — stable across renames |
| `environment` | string | one of project/environment | Default environment as its **client-side SDK key** — unique, public by design, doubles as the SDK credential; `ser.*` rejected |
| `organisation` | integer | no | Organisation ID for org-scoped commands with no project context (e.g. creating a project); otherwise derived from `project` |
| `apiUrl` | string | no | Admin API base URL; omit for SaaS |
| `sdkApiUrl` | string | no | SDK API base URL; falls back to `apiUrl`, then Edge |

Decisions, briefly:

- **JSON with a schema** — universal parsing, `$schema` editor tooling, `depot.json`/`vercel.json` precedent. One schema serves file, env vars and flags. Names stay out of the file entirely; the name cache humanizes output.
- **`environment` is a client-side key, not a name** — environment names have **no uniqueness constraint** server-side (verified in core), so names can't safely identify anything; keys are unique, public, and make the SDK-only case a one-field file. Names appear only as cached display strings.
- **`organisation` is optional and rarely consulted** — project IDs are instance-unique, so any command with project context derives the org from the project. The key exists for multi-org users running org-scoped commands (project creation, `projects list`) and to preselect the org picker in `flagsmith init`. There is no user-level config file; repo context is the only place org preference lives.

## 3. Context resolution

One schema, three mechanisms. Fields map one-to-one:

| Field | Flag | Env var |
|---|---|---|
| `project` | `-p`/`--project` | `FLAGSMITH_PROJECT` |
| `environment` | `-e`/`--environment` | `FLAGSMITH_ENVIRONMENT` (alias: `FLAGSMITH_ENVIRONMENT_KEY`) |
| `apiUrl` | `--api`/`--api-url` | `FLAGSMITH_API_URL` |
| `sdkApiUrl` | `--sdk-api`/`--sdk-api-url` | `FLAGSMITH_SDK_API_URL` |
| — (not persisted) | `--yes`/`--no-input` | `FLAGSMITH_NO_INPUT` |

Precedence, highest wins: **flag → env → default** (the nearest `flagsmith.json`, then built-ins). Those three words are also the full `source` enum in `flagsmith config --json` — derived values (like organisation-from-project) report `default`, and `configPath` discloses which file "default" means.

Environment values are canonically client-side keys. As a convenience, `-e`/`--environment` also accepts an environment *name*, resolved live via the Admin API when credentials exist; because names aren't unique, an ambiguous name is an error listing the candidate keys.

### Name cache

Names shown anywhere (context lines, `config`, diffs in `init`) come from a local cache at `os.UserCacheDir()/flagsmith/cache.json` (`~/Library/Caches`, `~/.cache`, `%LocalAppData%`), keyed by instance URL: project/organisation IDs and environment keys → names. Any Admin API command refreshes it opportunistically. Strictly cosmetic: never consulted for authorization or resolution, and a cache miss degrades to showing the bare ID/key. This keeps names out of committed values while keeping the CLI human-readable offline.

## 4. `flagsmith init`

`flagsmith login` answers *who am I* (user-level, keychain). `flagsmith init` answers *where am I* (repo-level, file), and subsumes `login` when needed:

1. Load credentials through the normal chain; if none, run the browser login inline. If credentials exist, print the current identity first (email for user credentials, organisation for a master key) — catches the wrong-account case before anything is written.
2. Pick organisation — only when the user belongs to more than one; written to `flagsmith.json` as `organisation` in that case.
3. Pick project — interactive select, with a create-project option (suggested name: the current directory) so a brand-new user gets end-to-end value from one command.
4. Pick default environment — skippable; defaults to Development. Written as the environment's client-side key.
5. Write `flagsmith.json` to the current directory. If the file exists, show current values and confirm the changes — never clobber silently.
6. Verify with one cheap authorized call (list the project's environments) so missing access surfaces at init time, not on the first real command. Also seeds the name cache.
7. Print next steps.

## 5. Open questions

1. **Multiple environments per file** — an `environments` object (per-env keys/overrides) vs the single default. Start with the single default; revisit when a concrete need appears.
2. **Schema URL longevity** — the `$schema` URL is GitHub raw on `main`, embedded in every committed `flagsmith.json`. Repo/branch renames or a later vanity URL mean stale references in user repos. Register on SchemaStore (matches `flagsmith.json` by filename, no `$schema` line needed) and/or claim a vanity URL before GA?
3. **`FLAGSMITH_ENVIRONMENT_KEY` alias** — kept for SDK-ecosystem familiarity alongside the canonical `FLAGSMITH_ENVIRONMENT`. Keep both long-term, or deprecate the alias before GA?
