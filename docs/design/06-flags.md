# Flagsmith CLI v2: Flags

Status: draft

A flag is a feature's state in the current environment: its on/off and value, as the SDK resolves it.

## 1. Resource

Flags are read from the SDK API (`sdkApiUrl` + environment key, resolved per [04-project-config.md](04-project-config.md)), so `flags` read commands need only an environment key — no Admin API credentials. Note: this will break server-side only features.

| Field | Meaning |
|---|---|
| `name` | Feature name; the identifier. Unique per project, case-insensitive. |
| `enabled` | On/off in the environment. |
| `value` | The feature-state value: a string, number, boolean, or null. |
| `type` | `STANDARD` or `MULTIVARIATE`. |

## 2. `flags list`

`GET {sdkApiUrl}/api/v1/flags/` with `X-Environment-Key`, returning every flag as a bare array.

Human output is a `NAME` / `ENABLED` / `VALUE` table with a count; JSON is the array as returned. This is the minimum viable command to make the nudge from `flagsmith init` real.

## 3. Later

- `flags get <name>`.
- `flags enable`/`disable`/`set <name> <value>` — mutate a feature state. Should consume the experimental endpoint.
