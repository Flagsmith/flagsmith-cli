# Flagsmith CLI v2: `flagsmith segment`

Status: draft

A segment is a named rule tree, evaluated against the evaluation context to decide segment membership. Everything but the rule tree is ordinary CRUD (see 05-crud.md); the rule tree is authored as JSON in accordance with the evaluation context schema.

## 1. Examples

### List and get

```
$ flagsmith segment list
NAME         ID     CONDITIONS  DESCRIPTION
us-adults    42     2           Users in the US aged 18+
beta-optin   57     1           Opted into the beta

2 segments
```

Feature-specific segments are hidden by default; `--include-feature-specific` shows them:

```
$ flagsmith segment list --include-feature-specific
NAME          ID     CONDITIONS  DESCRIPTION
us-adults     42     2           Users in the US aged 18+
beta-optin    57     1           Opted into the beta
beta-cohort   58     1           Beta cohort for checkout-v2

3 segments
```

`get` renders the rule tree as an indented view; `--json` emits the curated segment:

```
$ flagsmith segment get us-adults
To get segment overrides, run flagsmith flag list --segment 42

Segment      us-adults (42)
Description  Users in the US aged 18+

All of the below:
  Any of the below:
    country  IN                      US, CA
    age      GREATER_THAN_INCLUSIVE  18
```

```
$ flagsmith segment get us-adults --json
{
  "id": 42,
  "name": "us-adults",
  "description": "Users in the US aged 18+",
  "rules": {
    "$schema": "https://raw.githubusercontent.com/Flagsmith/flagsmith/main/sdk/evaluation-context.json#/$defs/SegmentRule",
    "type": "ALL",
    "rules": [
      {
        "type": "ANY",
        "conditions": [
          { "property": "country", "operator": "IN", "value": "[\"US\", \"CA\"]" },
          { "property": "age", "operator": "GREATER_THAN_INCLUSIVE", "value": "18" }
        ]
      }
    ]
  }
}
```

### Create

The rule tree comes from a file, stdin (`-`), or an inline string:

```
$ flagsmith segment create us-adults --description "Users in the US aged 18+" --rules @rule.json
✓ Created segment us-adults (42)
<segment output>
```

`--feature` scopes the segment to a single feature (a feature-specific segment):

```
$ flagsmith segment create beta-cohort --feature checkout-v2 --rules @rule.json
✓ Created segment beta-cohort (58)
<segment output>
```

### Update (round-trip)

`get` output is editable and re-appliable; `--jq` extracts just the rule (which already carries its `$schema`):

```
$ flagsmith segment get us-adults --jq '.rules' > rule.json   # edit
$ flagsmith segment update us-adults --rules @rule.json
✓ Updated segment us-adults (42)
<segment output>
```

### Delete

```
$ flagsmith segment delete us-adults --yes
✓ Deleted segment us-adults (42)
```

## 2. Rule JSON

`--rules` takes a single `SegmentRule` as the segment's top-level rule, as defined by the [evaluation context schema](https://raw.githubusercontent.com/Flagsmith/flagsmith/refs/heads/main/sdk/evaluation-context.json). A single object, rather than the API's `rules` array, both matches the dashboard convention of one top-level wrapper rule and can carry a `$schema` pointer for editor validation. The CLI maps it to the API's single-element `rules` array; on read it takes the sole top-level rule. An array with more than one rule is wrapped under a synthetic `ALL`.

Two things the schema cannot express, which the CLI handles:

- `IN` values. The schema types an `IN` value as a string array (`["US", "CA"]`), but the Admin API stores every condition `value` as a plain string. The CLI JSON-dumps the array to a JSON-array string on the wire (`"[\"US\",\"CA\"]"`).
- Depth. The schema's `SegmentRule` is arbitrarily recursive, but the Admin API caps nesting at two levels: the top-level rule holds sub-rules, and each sub-rule holds conditions. The CLI rejects a deeper tree before calling the API.

## 3. Behaviour

Segments are referenced by name or numeric id; the project comes from context. Create and update send the whole rule tree in one request. Update is a wholesale replace, no rule/condition ids are sent. `create` and `update` print the resulting segment, `delete` only a confirmation.

## 4. Schema

`segment get --json` stamps a `$schema` pointer to the upstream evaluation-context `#/$defs/SegmentRule` fragment onto the emitted rule. On input `$schema` is ignored.
