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
- Missing input that a TTY prompt would need always loudly exits with code 2, naming the flag. (That includes confirmations, where `--yes` is the flag.)

## 2. Output

- stdout carries the data result; stderr carries everything else — `✓` confirmations, progress, `Warning:` lines, and prompts. A script can parse stdout without decoration leaking in.
- `--json` is the scripting interface, available on every command that outputs data. Also settable as `FLAGSMITH_JSON_OUTPUT=1`.
- `--jq <expr>` enforces JSON output and filters it through the provided jq expression.

## 3. Exit codes

- `0`: success. Check stdout for output.
- `2`: missing or invalid input that a prompt would have collected interactively; the error names the flag.
- `1`: everything else (no credentials, no access, network failures). Errors go to stderr.

## 4. Help and errors

Usage errors print usage. An exit 2 error prints the nearest command's usage to stderr after the error, so the fix is in front of the user. Exit 1 errors print the error alone.

Each command's help carries an examples block of representative invocations for various popular use cases. It is the fastest documentation at the point of use. Examples offer command lines only, no output (unless it really helps with the use case).

Errors may carry a hint. Any error, exit 1 or 2, can append an optional hint on its own line after `Error: ...` (before the usage block, when one is printed). A hint is one or both of:
- a recovery command — what to run to fix the problem, retry, or reach the approximated goal (e.g. `Did you mean 'flagsmith feature create'?`, `Run 'flagsmith login' first.`).
- context — a link that explains or unblocks: `https://flagsmith.com/pricing` when an action is plan-gated, `https://docs.flagsmith.com/...` when the usage is wrong in a way the docs cover.
