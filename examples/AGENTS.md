# Flagsmith

This repo uses [Flagsmith](https://flagsmith.com) for feature flags, managed through the `flagsmith` CLI.

## Setup

The CLI is bound to this repo via `flagsmith.json`, which records the project and environment. Don't edit it by hand and don't pass `--project` or `--environment` to override it — if a command seems to be pointing at the wrong place, run `flagsmith config` and report what it says.

If the `flagsmith.json` file does not exist, prompt the user to generate it by running `flagsmith init`.

Check you're authenticated before anything else:

```bash
flagsmith auth status
```

If that fails, stop and ask the user to run `flagsmith login`. Don't attempt to authenticate on their behalf and don't look for API keys in the repo or the environment.

## Getting flag state

Use `--json` and filter with `--jq`. A spec-compliant jq is built into the binary, so it's always available.

```bash
flagsmith flag list --json
flagsmith flag get checkout-v2 --json
flagsmith flag list --json --jq '.[] | select(.enabled) | .name'
```

Evaluate flags the way the application would see them, which can also target specific user identities and traits:

```bash
flagsmith eval --identity user-123 --json
flagsmith eval --identity user-123 --trait plan=enterprise --json
```

## Safe to run without asking

Read-only commands. Run these freely when you need to understand flag state:

- `flagsmith flag list`, `flagsmith flag get`
- `flagsmith feature list`, `flagsmith feature get`
- `flagsmith segment list`, `flagsmith segment get`
- `flagsmith environment list`, `flagsmith project list`
- `flagsmith eval`
- `flagsmith config`, `flagsmith auth status`
- `flagsmith --help` and any `--help` subcommand

## Ask the user first

These change state that affects real users. **Ask before running, and say exactly which flag and which environment you're about to change.**

- `flagsmith flag enable`, `flagsmith flag disable`, `flagsmith flag update`
- `flagsmith feature create`, `flagsmith feature update`
- `flagsmith segment create`, `flagsmith segment update`
- `flagsmith environment create`, `flagsmith environment clone`

## Never run

- `flagsmith flag delete`, `flagsmith feature delete`, `flagsmith segment delete` — deleting a flag that code still references breaks the application. Surface the suggestion to the user and confirm with them or let them do it.
- `flagsmith api` with anything other than `GET` — it reaches endpoints that have no guardrails.
- Anything against a production environment. If a command would target production, stop and ask for confirmation.

## When writing flag code

Discover the exact flag name before referencing it. Don't guess, and don't invent a name that seems plausible:

```bash
flagsmith flag list --json --jq '.[].name'
```

If the flag doesn't exist yet, say so and ask whether to create it, rather than writing code against a name that isn't there.

Note that Flagsmith distinguishes **features** (the thing that exists in a project) from **flags** (that feature's state in a given environment). To create something new, use `flagsmith feature create`. `flagsmith flag` only ever operates on something that already exists.

## Gating a build on flag state

If asked to add a flag check to CI:

```bash
flagsmith eval --test checkout-v2
```

Exits non-zero when the flag is disabled. In a workflow:

```yaml
- name: Verify release flags
  run: flagsmith eval --test checkout-v2
  env:
    FLAGSMITH_API_KEY: ${{ secrets.FLAGSMITH_API_KEY }}
```

## If something doesn't work

Read the error text — the CLI's errors say what to do next, including when you've reached for the wrong command. Then check `flagsmith --help` or the subcommand's help, which carry worked examples.

Don't fall back to raw `curl` against the API. If the CLI can't do it, say so.
