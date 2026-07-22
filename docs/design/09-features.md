# Flagsmith CLI v2: `flagsmith feature`

Status: draft

A project-level definition for flags.

## 1. Examples

### List and get

```
$ flagsmith feature list
NAME          ID   TYPE          VALUE   DESCRIPTION
checkout-v2   88   standard      green   New checkout flow
banner-copy   91   multivariate  hello   A/B banner text

2 features
```

Archived features are hidden by default; `--include-archived` shows them:

```
$ flagsmith feature list --include-archived
NAME          ID   TYPE          VALUE   DESCRIPTION
checkout-v2   88   standard      green   New checkout flow
banner-copy   91   multivariate  hello   A/B banner text
legacy-copy   40   standard      old     Retired (archived)

3 features
```

```
$ flagsmith feature get banner-copy
Feature      banner-copy (91)
Description  A/B banner text
Type         multivariate
Value        hello
Enabled      false

Variants
  VALUE      WEIGHT  KEY   ID
  headline   30      hero  201
  subhead    70      sub   202
```

```
$ flagsmith feature get banner-copy --json
{
  "id": 91,
  "name": "banner-copy",
  "description": "A/B banner text",
  "type": "multivariate",
  "value": "hello",
  "enabled": false,
  "variants": [
    { "id": 201, "value": "headline", "weight": 30, "key": "hero" },
    { "id": 202, "value": "subhead", "weight": 70, "key": "sub" }
  ]
}
```

### Create

```
$ flagsmith feature create checkout-v2 --value green --description "New checkout flow"
✓ Created feature checkout-v2 (88)
<feature output>
```

Multivariate: variants inline (a JSON array from a file, `-`, or a string):

```
$ cat variants.json
[
  { "value": "headline", "weight": 30 },
  { "value": "subhead",  "weight": 70 }
]

$ flagsmith feature create banner-copy --value hello --variants @variants.json
✓ Created feature banner-copy (91)
<feature output>
```

### Update

Only the mutable fields — description, tags, archive. `name`, `--value`, and `--enabled` are fixed at create; variants are managed with `feature variant`.

```
$ flagsmith feature update checkout-v2 --description "Checkout redesign" --archive
✓ Updated feature checkout-v2 (88)
<feature output>
```

### Variants

Ongoing variant edits are granular (by id or key), so per-environment weight overrides are preserved:

```
$ flagsmith feature variant list banner-copy
VALUE     WEIGHT  KEY   ID
headline  30      hero  201
subhead   70      sub   202

$ flagsmith feature variant add banner-copy --value cta --weight 20 --key button
✓ Added variant cta (203) to banner-copy

$ flagsmith feature variant update banner-copy hero --weight 40
✓ Updated variant headline (201)

$ flagsmith feature variant delete banner-copy hero --yes
✓ Deleted variant headline (201) from banner-copy
```

### Delete

```
$ flagsmith feature delete checkout-v2 --yes
✓ Deleted feature checkout-v2 (88)
```

## 2. Behaviour

- Referenced by name or numeric id (see 05-crud.md); project from context.
- Archived features are hidden from `list` by default. `--include-archived` drops the filter to show them alongside active ones. Archive/unarchive a feature with `update --archive`/`--unarchive`.
- Create/update asymmetry: `name`, `--value`, and `--enabled` are set at create and immutable afterwards. `update` changes description, tags, and archive only. Per-environment value/state changes go through `flag update` (07-flags.md).
- Value typing: `--value` is the feature's default seed, stored as a plain string. Variant `--value` is typed. Variant type is inferred from provided value, overridable with `--type string|integer|boolean`.
- Weights: variant weights are percentage allocations and must sum to ≤ 100; the remainder is the control (the feature's own `--value`).
- Variants are managed granularly. `create` accepts inline `--variants` (keyless). Afterwards use `feature variant add|update|delete <feature> <id|key>`: a project-level variant's id anchors every environment/segment/identity weight override. `feature update` does not touch variants.
- Type is automatic: a feature becomes `multivariate` when it has variants and reverts to `standard` when the last is removed.
