# Flagsmith CLI v2: Output & Interactivity

Status: draft

Cross-cutting conventions every command follows.

## 1. Interactivity

Interactive behaviour and output are governed by global flags / environment variables:

| Flag | Env var |
|---|---|
| `--yes`/`--no-input` | `FLAGSMITH_NO_INPUT` |
| `--json` | `FLAGSMITH_JSON_OUTPUT` |

- Prompts and opening a browser require a TTY. Without one (CI, pipes, redirected stdin), the CLI behaves as if `--no-input` was set: it never prompts and never opens a browser.
- `--yes` and `--no-input` are aliases of one global flag, also settable as `FLAGSMITH_NO_INPUT=1`: never prompt, never open a browser, answer confirmations affirmatively.
- Every prompt has a flag equivalent, enforced structurally. Flags always win, and a fully-flagged invocation asks nothing even in a TTY.
- Missing input that a TTY prompt would need always loudly exits with code 2, naming the flag. (That including confirmations, where `--yes` is the flag.)

## 2. Output

- stdout carries the data result; stderr carries everything else — `✓` confirmations, progress, `Warning:` lines, and prompts. A script can parse stdout without decoration leaking in.
- `--json` is the scripting interface, available on every command that outputs data. Also settable as `FLAGSMITH_JSON_OUTPUT=1`.
- `--jq <expr>` enforces JSON output and filters it through the provided jq expression.

### Result model

Applies uniformly to every command:

- Reads (`list`, `get`, `config`, `auth status`/`token`) write their data to stdout — a table or key/value view for humans, the resource shape as JSON otherwise.
- Mutations print a `✓ <verb> <resource>` confirmation to stderr. Those that produce a resource (`create`, `update`) also write it to stdout as the data result; `delete` has no data result, so stdout stays empty (`✓ Deleted <resource>` to stderr, exit 0).
- JSON shape mirrors the Admin API: a bare object for a single resource, a bare array for a list (`[]` when empty), so `--jq` expressions match the API. No CLI envelope. (`config` keeps its bespoke keyed shape — it is CLI-specific, not an API resource.)
- Empty list: `[]` as JSON; `No <resources>.` to stdout for humans; exit 0.

## 3. Exit codes

- `0`: success. Check stdout for output.
- `2`: missing or invalid input that a prompt would have collected interactively; the error names the flag.
- `1`: everything else (no credentials, no access, network failures). Errors go to stderr.
