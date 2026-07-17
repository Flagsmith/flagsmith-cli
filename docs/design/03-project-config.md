# Flagsmith CLI v2: `flagsmith init` & Project Config

Status: draft

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
  "environment": "development"
}
```

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
  "environment": "development"
}
```

Re-run with an existing `flagsmith.json`. Current values preselected, changes shown as a diff:

```
$ flagsmith init
✓ Logged in as kim@flagsmith.com (keychain)
flagsmith.json exists — updating it.
? Project             › acme-api (67890)
? Default environment › Production

  project:     12345 (my-app) → 67890 (acme-api)
  environment: development    → production

? Write changes? (y/N) › y
✓ Wrote flagsmith.json
```

### `flagsmith init`, non-interactive (no TTY)

All inputs come from flags and ambient credentials. Confirmations require `--yes`.

With a static master key:

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
Usage: flagsmith init --project <id> [--environment <name>] [--api <url>] --yes
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
  "environment": "development",
  "apiUrl": "https://flagsmith.acme.internal"
}
```

The SDK API defaults to the same host: `sdkApiUrl` → `apiUrl` → Edge. One key for self-hosted, zero for SaaS.

### Split SDK topology (dedicated flags proxy/CDN)

```json
{
  "$schema": "https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/main/schema/flagsmith.json",
  "project": 42,
  "environment": "production",
  "apiUrl": "https://flagsmith.acme.internal",
  "sdkApiUrl": "https://flags.acme.com"
}
```

### SDK-only consumer (no Admin API credentials)

```json
{
  "$schema": "https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/main/schema/flagsmith.json",
  "project": 12345,
  "environment": "production",
  "environmentKey": "AbCdEf1234"
}
```

Flag evaluation works with zero Admin API credentials. Client-side keys are public by design and safe to commit; server-side keys (`ser.*`) fail schema validation and are refused by `flagsmith init`.

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

The nearest `flagsmith.json` wins, walking up from the current directory.

### Inspecting resolved context

```
$ FLAGSMITH_ENVIRONMENT=production flagsmith config
Project       my-app (12345)                 flagsmith.json
Organisation  Acme (3)                       derived from project
Environment   production                     $FLAGSMITH_ENVIRONMENT
API           https://api.flagsmith.com      default
SDK API       https://edge.api.flagsmith.com default
```

```
$ flagsmith config --json
{
  "configPath": "/work/my-app/flagsmith.json",
  "project": { "value": 12345, "name": "my-app", "source": "flagsmith.json" },
  "organisation": { "value": 3, "name": "Acme", "source": "derived:project" },
  "environment": { "value": "production", "source": "env:FLAGSMITH_ENVIRONMENT" },
  "apiUrl": { "value": "https://api.flagsmith.com", "source": "default" },
  "sdkApiUrl": { "value": "https://edge.api.flagsmith.com", "source": "default" }
}
```

`flagsmith config --json` is the scripting interface: it returns the fully resolved context with per-field sources. Scripts read it instead of parsing `flagsmith.json`.

## 2. The file

`flagsmith.json` — committed, visible (no leading dot), never contains secrets. Written by `flagsmith init`, hand-editable with autocomplete and validation via the published schema ([schema/flagsmith.json](../../schema/flagsmith.json)).

| Field | Type | Required | Meaning |
|---|---|---|---|
| `project` | integer | yes | Project ID — stable across renames; `flagsmith config` shows the name |
| `organisation` | integer | no | Organisation ID for org-scoped commands with no project context (e.g. creating a project); otherwise derived from `project` |
| `environment` | string | no | Default environment *name* for `-e`, matched case-insensitively |
| `apiUrl` | string | no | Admin API base URL; omit for SaaS |
| `sdkApiUrl` | string | no | SDK API base URL; falls back to `apiUrl`, then Edge |
| `environmentKey` | string | no | Client-side SDK key for Admin-API-credential-free flag evaluation; `ser.*` rejected |

Decisions, briefly:

- **JSON with a schema, not TOML/YAML** — universal parsing, editor autocomplete/validation via `$schema`, `depot.json`/`vercel.json` precedent. Human-readable context comes from `flagsmith config`, not file comments.
- **`organisation` is optional and rarely consulted** — project IDs are instance-unique, so any command with project context derives the org from the project. The key exists for multi-org users running org-scoped commands (project creation, `projects list`) and to preselect the org picker in `flagsmith init`. There is no user-level config file; repo context is the only place org preference lives.
- **`environment` is a name, not a key** — context stays on the Admin API surface; environment keys are resolved via the Admin API when needed.

## 3. Context precedence

Highest wins:

1. Command-line flags (`--project`, `-e/--environment`)
2. `FLAGSMITH_PROJECT` / `FLAGSMITH_ENVIRONMENT` env vars
3. Nearest `flagsmith.json`

Commands print which context source was used whenever the choice is ambiguous — the same transparency rule `auth status` follows for credentials.

## 4. `flagsmith init`

`flagsmith login` answers *who am I* (user-level, keychain). `flagsmith init` answers *where am I* (repo-level, file), and subsumes `login` when needed:

1. Load credentials through the normal chain; if none, run the browser login inline. If credentials exist, print the current identity first — catches the wrong-account case before anything is written.
2. Pick organisation — only when the user belongs to more than one; written to `flagsmith.json` as `organisation` in that case.
3. Pick project — interactive select, with a create-project option (suggested name: the current directory) so a brand-new user gets end-to-end value from one command.
4. Pick default environment — skippable; defaults to Development.
5. Write `flagsmith.json` to the current directory. If the file exists, show current values and confirm the changes — never clobber silently.
6. Verify with one cheap authorized call (list the project's environments) so missing access surfaces at init time, not on the first real command.
7. Print next steps.

### Interactivity rules

- Prompts and the browser login require a TTY. Without one (CI, pipes, redirected stdin) — or with `--no-input` set — `flagsmith init` never prompts and never opens a browser. `--no-input` exists to get CI-identical behaviour in a terminal.
- Every prompt has a flag equivalent: `--api`, `--project`, `--environment`. Flags always win; a fully-flagged invocation asks nothing even in a TTY.
- Confirmations (overwriting an existing `flagsmith.json`) become `--yes` when prompting is unavailable.
- Exit codes: `2` for missing input that a prompt would have collected (usage error, names the flag); `1` for everything else (no credentials, no access, network). Errors go to stderr.

## 5. Open questions

1. **`environmentKey` in the committed file** — convenient for SDK-only consumers, but it invites confusion with server-side keys and duplicates state resolvable from `environment` + Admin API credentials. Ship without it and add on demand?
2. **Multiple environments per file** — an `environments` object (per-env keys/overrides) vs the single default. Start with the single default; revisit when a concrete need appears.
3. **Schema URL longevity** — the `$schema` URL is GitHub raw on `main`, embedded in every committed `flagsmith.json`. Repo/branch renames or a later vanity URL mean stale references in user repos. Register on SchemaStore (matches `flagsmith.json` by filename, no `$schema` line needed) and/or claim a vanity URL before GA?
