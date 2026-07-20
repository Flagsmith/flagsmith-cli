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
- Every prompt has a flag equivalent. Flags always win; a fully-flagged invocation asks nothing even in a TTY.
- Missing input that a prompt would have collected is a usage error naming the flag (exit `2`, see below) — never a hang, never a guess.

## 2. Output

- stdout carries the result; stderr carries logs. Success lines are prefixed `✓`, warnings with `Warning:`.
- `--json` is the scripting interface, available on every command that outputs data. Also settable as `FLAGSMITH_JSON_OUTPUT=1`.

## 3. Exit codes

- `0`: success. Check stdout for output.
- `2`: missing or invalid input that a prompt would have collected interactively; the error names the flag.
- `1`: everything else (no credentials, no access, network failures). Errors go to stderr.
