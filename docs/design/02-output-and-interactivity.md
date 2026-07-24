# Flagsmith CLI v2: Output & Interactivity

Status: draft/poc

Cross-cutting conventions every command follows.

## 1. Interactivity

Interactive behaviour and output are governed by global flags / environment variables:

| Flag | Env var | Meaning |
|---|---|---|
| `--no-input` | `FLAGSMITH_NO_INPUT` | never prompt or open a browser; fail if required input is missing |
| `--yes` | — | answer confirmations affirmatively |
| `--json` | `FLAGSMITH_JSON_OUTPUT` | machine-readable output |

- Prompts and opening a browser require a TTY. Without one (CI, pipes, redirected stdin), the CLI behaves as if `--no-input` was set: it never prompts and never opens a browser.
- Every prompt has a flag equivalent, enforced structurally. Flags always win, and a fully-flagged invocation asks nothing even in a TTY.
- Missing input that a TTY prompt would need always loudly exits with code 2, naming the flag.
- A confirmation resolves as: proceed with `--yes`; otherwise prompt if TTY available; otherwise (`--no-input`, `FLAGSMITH_NO_INPUT`, or no TTY) exit 2 naming `--yes`.

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
- context — a link that explains or unblocks. Plan limits split by how they're lifted: a self-serve upgrade (seats, billing) points at `https://flagsmith.com/pricing`; an enterprise-negotiable quota cap (feature / segment / segment-override limits) points at support, since those are raised on request, not by upgrading. Misuse the docs cover points at `https://docs.flagsmith.com/...`.
