# Flagsmith CLI v2: `flagsmith project` & `flagsmith organisation`

Status: draft

Organisations and projects are flat CRUD resources, with brief caveats.

## 1. Examples

### Organisations

```
$ flagsmith organisation list
NAME    ID
Acme    3
Beta    7

2 organisations

$ flagsmith organisation get Acme
Organisation  Acme (3)

$ flagsmith organisation create "Acme Labs"
✓ Created organisation Acme Labs (12)

$ flagsmith organisation update Acme --force-2fa
✓ Updated organisation Acme (3)

$ flagsmith organisation delete Beta --yes
✓ Deleted organisation Beta (7)
```

`org` is an accepted alias for `organisation`.

### Projects

```
$ flagsmith project list
NAME       ID    ORGANISATION
acme-api   101   Acme
acme-web   102   Acme

2 projects

$ flagsmith project get acme-api
Project       acme-api (101)
Organisation  Acme (3)

$ flagsmith project create acme-mobile --organisation Acme
✓ Created project acme-mobile (103)

$ flagsmith project update acme-api --hide-disabled-flags
✓ Updated project acme-api (101)

$ flagsmith project delete acme-web --yes
✓ Deleted project acme-web (102)
```

`project list` lists every accessible project; `--organisation` scopes it to one. Settings are flat flags; `--json` mirrors the API's fields.

## 2. Behaviour

- Referenced by name or numeric id. Resolution uses the name cache established in 04-project-config.md.
- Project create requires an organisation: `--organisation` (or the resolved context) plus an org-level create permission/quota. A project's organisation is fixed at creation: `update` never changes it.
- Some project fields are read-only or plan-gated. The CLI does not expose read-only fields. Plan-gated ones are accepted but ignored server-side on lower plans.
- Delete needs admin on the object (organisation admin / project admin); otherwise the API's permission error is surfaced.
