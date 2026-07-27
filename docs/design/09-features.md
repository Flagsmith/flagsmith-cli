# Flagsmith CLI v2: `flagsmith feature`

Status: draft/poc

A project-level definition for flags.

## 1. Examples

### List and get

```
$ flagsmith feature list
NAME          ID       TYPE           DEFAULT VALUE   DESCRIPTION
checkout-v2   233884   standard       green           New checkout flow
banner-copy   233885   multivariate   hello           A/B banner text

2 features
```

Archived features are hidden by default; `--include-archived` shows them:

```
$ flagsmith feature list --include-archived
NAME          ID       TYPE           DEFAULT VALUE   DESCRIPTION
checkout-v2   233884   standard       green           New checkout flow
banner-copy   233885   multivariate   hello           A/B banner text
legacy-copy   233887   standard       old             Retired banner copy

3 features
```

```
$ flagsmith feature get banner-copy
Feature         banner-copy (233885)
Description     A/B banner text
Type            multivariate
Default value   hello
Enabled         false

Variants
  VALUE     WEIGHT  KEY   ID
  headline  30      hero  30011
  subhead   50      sub   30010
```

```
$ flagsmith feature get banner-copy --json
{
  "id": 233885,
  "name": "banner-copy",
  "description": "A/B banner text",
  "type": "multivariate",
  "default_value": "hello",
  "enabled": false,
  "variants": [
    {
      "id": 30011,
      "value": "headline",
      "weight": 30,
      "key": "hero"
    },
    {
      "id": 30010,
      "value": "subhead",
      "weight": 50,
      "key": "sub"
    }
  ]
}
```

### Create

```
$ flagsmith feature create checkout-v2 --value green --description "New checkout flow"
✓ Created feature checkout-v2 (233884)
Feature         checkout-v2 (233884)
Description     New checkout flow
Type            standard
Default value   green
Enabled         false
```

Multivariate: variants inline (a JSON array from a file, `-`, or a string). They're
created keyless — add keys later with `feature variant`:

```
$ cat variants.json
[
  { "value": "headline", "weight": 30 },
  { "value": "subhead",  "weight": 50 }
]

$ flagsmith feature create banner-copy --value hello --variants @variants.json
✓ Created feature banner-copy (233885)
Feature         banner-copy (233885)
Description
Type            multivariate
Default value   hello
Enabled         false

Variants
  VALUE     WEIGHT  KEY  ID
  headline  30           30011
  subhead   50           30010
```

### Update

Only the mutable fields — description, tags, archive. `name`, `--value`, and `--enabled` are fixed at create; variants are managed with `feature variant`.

```
$ flagsmith feature update checkout-v2 --description "Checkout redesign" --archive
✓ Updated feature checkout-v2 (233884)
Feature         checkout-v2 (233884)
Description     Checkout redesign
Type            standard
Default value   green
Enabled         false
```

### Variants

Ongoing variant edits are granular (by id or key), so per-environment weight overrides are preserved:

```
$ flagsmith feature variant list banner-copy
VALUE      WEIGHT   KEY    ID
headline   30       hero   30011
subhead    50       sub    30010

$ flagsmith feature variant add banner-copy --value cta --weight 20 --key button
✓ Added variant cta (30012) to banner-copy

$ flagsmith feature variant update banner-copy hero --weight 25
✓ Updated variant headline (30011)

$ flagsmith feature variant delete banner-copy hero --yes
✓ Deleted variant headline (30011) from banner-copy
```

### Delete

```
$ flagsmith feature delete checkout-v2 --yes
✓ Deleted feature checkout-v2 (233884)
```

## 2. Behaviour

- Referenced by name or numeric id. Resolution uses the name cache established in 04-project-config.md. Project comes from config context.
- Archived features are hidden from `list` by default. `--include-archived` drops the filter to show them alongside active ones. Archive/unarchive a feature with `update --archive`/`--unarchive`.
- Create/update asymmetry: `name`, `--value`, and `--enabled` are set at create and immutable afterwards. `update` changes description, tags, and archive only. Per-environment value/state changes go through `flag update` (07-flags.md).
- Value typing: `--value` (alias `--default-value`) is the feature's default seed, stored as a plain string. Variant `--value` is typed. Variant type is inferred from provided value, overridable with `--type string|integer|boolean`.
- Weights: variant weights are percentage allocations and must sum to ≤ 100; the remainder is the control (the feature's own `--value`).
- Variants are managed granularly. `create` accepts inline `--variants` (keyless). Afterwards use `feature variant add|update|delete <feature> <id|key>`: a project-level variant's id anchors every environment/segment/identity weight override. `feature update` does not touch variants.
- Type is automatic: a feature becomes `multivariate` when it has variants and reverts to `standard` when the last is removed.
