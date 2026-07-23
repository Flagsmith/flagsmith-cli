# Flagsmith CLI v2: CRUD conventions

Status: draft/poc

The shared conventions every resource command follow.

## 1. Command shape

`flagsmith <resource> <verb>`, resource singular:

- `list` — every resource in the current context.
- `get <id>` — one resource.
- `create` — a new resource, field values provided via flags.
- `update <id>` — modify an existing resource.
- `delete <id>` — remove one.

Credentials come from [03-authentication.md](03-authentication.md); project/environment context from [04-project-config.md](04-project-config.md). CRUD commands resolve both through the usual chains and never re-ask for them.

## 2. Addressing

Resources are addressed by their ID. Each resource may define its own natural human identifier, and resolve it within the current context, while still accepting the ID. In case of ambigous natural human identifier, the CLI offers a choice from candidates when in TTY, and errors 2 otherwise.

## 3. Result model

- Reads write their data to stdout: a table (`list`) or key/value view (`get`) for humans, the API resource shape as JSON under `--json`. The human `list` view shows the item count.
- Mutations print a `✓ <verb> <resource>` confirmation to stderr. `create` and `update` also write the resulting resource to stdout as the data result; `delete` has no data result, so stdout stays empty (`✓ Deleted <resource>` to stderr, exit 0).
- JSON mirrors the Admin API: a bare object for a single resource, a bare array for a list (`[]` when empty), so `--jq` expressions match the API. No CLI envelope.
- Empty list: `[]` as JSON; `No <resources>.` to stdout for humans; exit 0.

The stdout/stderr split, `--json`/`--jq` and common exit codes are defined in [02-output-and-interactivity.md](02-output-and-interactivity.md).

## 4. Mutations

- `create`/`update` take resource fields as flags; per-resource help docs enumerate them. A missing required field follows 02: prompt in a TTY, otherwise exit 2 naming the flag.
- `delete` is destructive, so it confirms per 02: prompts unless `--yes`, and without a TTY `--yes` is required.
- `--json` on any mutation returns the affected resource, so scripts can capture generated identifiers (e.g. `flagsmith flag create … --json --jq .id`).

## 5. Errors

- `get`/`update`/`delete` on a missing resource exits 1 with a message naming what wasn't found.
- `create` colliding with an existing unique field exits 1, surfacing the API's reason.

## 6. Pagination

`list` fetches every page and returns the aggregate. `--limit N` caps the number of items.
