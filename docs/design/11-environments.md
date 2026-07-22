# Flagsmith CLI v2: `flagsmith environment`

Status: draft

An environment is a project's deployment target, identified by its client-side API key. Referenced by name or key; the project comes from context.

`env` is an accepted alias for `environment`.

## 1. Examples

### List and get

```
$ flagsmith environment list
NAME         KEY                      DESCRIPTION
Development  WqXhZk8sVY3dGgTqZ9pJmN   Local dev
Production   9P8YT5rKerRW9E7Bpzv2X9   Live

2 environments

$ flagsmith environment get Production
Environment  Production (9P8YT5rKerRW9E7Bpzv2X9)
Project      acme-api (101)
Description  Live
Versioning   v2
```

### Create

Create mints a client-side key. The project is required and fixed afterwards:

```
$ flagsmith environment create Staging
✓ Created environment Staging (Zse3pNq7hV2kLmBxR8dWca)
Environment  Staging (Zse3pNq7hV2kLmBxR8dWca)
Project      acme-api (101)
```

### Update / delete

```
$ flagsmith environment update Staging --description "Pre-prod" --hide-disabled-flags
✓ Updated environment Staging (Zse3pNq7hV2kLmBxR8dWca)

$ flagsmith environment delete Staging --yes
✓ Deleted environment Staging (Zse3pNq7hV2kLmBxR8dWca)
```

### Clone

```
$ flagsmith environment clone Production "Production Copy"
✓ Cloned Production into Production Copy (Kb2mVsGdXhZ8kQqZ9pJmNb)
```

### Server-side SDK keys

Server-side (`ser.`) keys live under an environment. `create` prints the secret once — it is not retrievable again:

```
$ flagsmith environment key list Production
NAME     ID   ACTIVE  CREATED                    EXPIRES AT
CI key   14   true    2026-07-01T21:33:17.82257  2026-08-01T21:33:17.82257

$ flagsmith environment key create Production --name "backend"
✓ Created server-side key backend (15)
Store the output value now, it will not be shown again:
ser.8kQqZ9pJmNbJK2mVsGdXhZ

$ flagsmith environment key delete Production 14 --yes
✓ Deleted server-side key 14 from Production
```

## 2. Behaviour

- Referenced by their client-side `api_key`, not an integer id. The CLI addresses them by key and resolves names to a key within the project. `list` is scoped to the project and shows the key; `--json` mirrors the API's fields.
- Server-side keys are secret. `environment key create` returns a `ser.` key shown once; there is no rotate — replace by creating a new key and deleting the old, or disabling via its `active` field. These are the secrets for `FLAGSMITH_ENVIRONMENT_KEY` server-side use (see 03-authentication.md).
- Settings are flat flags (`--description`, `--hide-disabled-flags`, `--allow-client-traits`, `--banner-text`, …).
- Permission errors are surfaced as-is.
