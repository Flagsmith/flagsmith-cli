# Flagsmith CLI v2: `flagsmith flag`

Status: draft/poc

A flag is a feature's state in the current environment: its on/off and value for a given environment, segment, or identity.

## 1. Resource

The natural human identifier for `flag` is the feature name:

```
$ flagsmith flag get checkout-v2
Feature              checkout-v2
Description          Checkout redesign
Type                 standard
State                on
Value                green
Segment overrides    1
Identity overrides   1
Code references      0
Lifecycle stage      new
```

To get a segment/identity override, use flags:

```
$ flagsmith flag get checkout-v2 --identifier id123
Feature      checkout-v2
Type         standard
Identifier   id123
State        on
Value        orange

$ flagsmith flag get checkout-v2 --segment 1147496
Feature   checkout-v2
Type      standard
Segment   1147496
State     on
Value     orange
```

For a multivariate feature, the view grows a Variants block:

```
$ flagsmith flag get banner-copy
Feature              banner-copy
Description          A/B banner text
Type                 multivariate
State                on
Value                hello
Segment overrides    1
Identity overrides   0
Code references      0
Lifecycle stage      new

Variants
  VALUE     WEIGHT  KEY   ID
  headline  25%     hero  30011
  subhead   75%     sub   30010

$ flagsmith flag get banner-copy --segment 101
Feature   banner-copy
Type      multivariate
Segment   101
State     on
Value     hello

Variants
  VALUE     WEIGHT  KEY   ID
  headline  100%    hero  30011
  subhead   0%      sub   30010
```

A detail human result view includes:
- Feature name
- Feature description
- Feature type (standard/multivariate)
- Flag state (on/off)
- Flag value
- Number of segment overrides
- Number of identity overrides
- Number of code references
- Lifecycle stage
- Variants with this scope's weights (multivariate only)

JSON output includes the above curated field list:

```json
{
  "feature": "checkout-v2",
  "type": "standard",
  "description": "Checkout redesign",
  "enabled": true,
  "value": "green",
  "segment_overrides": 1,
  "identity_overrides": 1,
  "code_references": 0,
  "lifecycle_stage": "new"
}
```

A segment override (`--segment`) has its own curated shape: `feature`, `type`, `segment`, `enabled`, `value`.

Multivariate features add `"variants": [{"id", "key", "value", "weight"}]` with the weights of the requested scope.

## 2. Mutation

Flags always exist for environments, so a bare `flag create` / `flag delete` wouldn't make sense. The following will exit 2:

```
$ flagsmith flag create brand-new-feature
Error: flags exist per environment, so there is nothing to create.
To create the feature itself, use `flagsmith feature create brand-new-feature`.
```

```
$ flagsmith flag delete checkout-v2
Error: provide --segment <id> or --identifier <id> to delete an override
```

Toggling the state, setting the value, and managing segment/identity overrides is handled by `flagsmith flag update`.

Toggle the environment default:

```
$ flagsmith flag update --enable checkout-v2 --yes
✓ Enabled checkout-v2 in environment Production (9P8YT5rKerRW9E7Bpzv2X9)
Feature              checkout-v2
Description          Checkout redesign
Type                 standard
State                on
Value                green
Segment overrides    1
Identity overrides   1
Code references      0
Lifecycle stage      new

$ flagsmith flag update --disable checkout-v2 --yes
✓ Disabled checkout-v2 in environment Production (9P8YT5rKerRW9E7Bpzv2X9)
Feature              checkout-v2
Description          Checkout redesign
Type                 standard
State                off
Value                green
Segment overrides    1
Identity overrides   1
Code references      0
Lifecycle stage      new
```

`flag enable` and `flag disable` are shorthand for that toggle:

```
$ flagsmith flag enable checkout-v2 --yes
✓ Enabled checkout-v2 in environment Production (9P8YT5rKerRW9E7Bpzv2X9)
Feature              checkout-v2
Description          Checkout redesign
Type                 standard
State                on
Value                green
Segment overrides    1
Identity overrides   1
Code references      0
Lifecycle stage      new
```

They take the same `--segment`/`--identifier` targeting, so an override toggles directly:

```
$ flagsmith flag disable checkout-v2 --segment 1147496 --yes
✓ Disabled checkout-v2 for segment 1147496 in environment Production (9P8YT5rKerRW9E7Bpzv2X9)
Feature   checkout-v2
Type      standard
Segment   1147496
State     off
Value     orange
```

To set a value while toggling, stay on `flag update`; `enable`/`disable` only flip the state.

Set environment default value:

```
$ flagsmith flag update checkout-v2 --value green --yes
✓ Set checkout-v2 to "green" in environment Production (9P8YT5rKerRW9E7Bpzv2X9)
Feature              checkout-v2
Description          Checkout redesign
Type                 standard
State                off
Value                green
Segment overrides    1
Identity overrides   1
Code references      0
Lifecycle stage      new
```

Set an identity override and enable its flag:

```
$ flagsmith flag update checkout-v2 --value orange --identifier id123 --enable --yes
✓ Set checkout-v2 to "orange" for identifier id123 in environment Production (9P8YT5rKerRW9E7Bpzv2X9)
✓ Enabled checkout-v2 for identifier id123 in environment Production (9P8YT5rKerRW9E7Bpzv2X9)
Feature      checkout-v2
Type         standard
Identifier   id123
State        on
Value        orange
```

Re-weight a multivariate flag's distribution with `--weight <key|id>=<n>`. The flag can be repeated:

```
$ flagsmith flag update banner-copy --weight hero=25 --weight sub=75 --yes
✓ Set banner-copy weights to hero=25, sub=75 in environment Production (9P8YT5rKerRW9E7Bpzv2X9)
Feature              banner-copy
Description          A/B banner text
Type                 multivariate
State                on
Value                hello
Segment overrides    1
Identity overrides   0
Code references      0
Lifecycle stage      new

Variants
  VALUE     WEIGHT  KEY   ID
  headline  25%     hero  30011
  subhead   75%     sub   30010
```

Weights compose with the other mutations in the same single request:

```
$ flagsmith flag update banner-copy --enable --value hello --weight hero=50 --yes
```

Per segment, the same flag re-weights the override:

```
$ flagsmith flag update banner-copy --segment 101 --weight hero=100,sub=0 --yes
✓ Set banner-copy weights to hero=100, sub=0 for segment 101 in environment Production (9P8YT5rKerRW9E7Bpzv2X9)
Feature   banner-copy
Type      multivariate
Segment   101
State     on
Value     hello

Variants
  VALUE     WEIGHT  KEY   ID
  headline  100%    hero  30011
  subhead   0%      sub   30010
```

Delete a segment override:

```
$ flagsmith flag delete checkout-v2 --segment 1147496 --yes
✓ Deleted checkout-v2 override for segment 1147496 in environment Production (9P8YT5rKerRW9E7Bpzv2X9)
```

All flag mutations are powered by `/api/experiments/environments/{environment_key}/update-flag-v2/`. 

### Variant weights

Depends on [Flagsmith/flagsmith#7955](https://github.com/Flagsmith/flagsmith/pull/7955) and [Flagsmith/flagsmith#8000](https://github.com/Flagsmith/flagsmith/pull/8000) (MV support and key-based variant identification in the update-flag endpoints).

- Weights only. Variant existence, value, and key are project-level and belong to `feature variant` (09-features.md); `flag` never adds, deletes, or revalues a variant, even though the endpoint allows it.
- A single `flag update` sends one request with one valid distribution, hence the `--weight` flag rather than a `flag variant update` subcommand.
- The endpoint's env-level `multivariate_feature_state_values` list is absolute; omitting a variant deletes it. The CLI therefore fetches the current variants, overlays the given weights, and sends the full list. A partial `--weight` re-weights what it names, keeps the rest, and never deletes anything.
- The merged weights must sum to ≤ 100, validated client-side before the request.
- `control` is rejected as a key.
- A numeric left-hand side is a variant id (for keyless variants). An unknown key or id exits 2 pointing at `feature variant add`.
- `--weight` on a standard feature exits 2 pointing at `feature variant add`. `--weight` with `--identifier` always exits 2.
- Resetting a segment's custom weights back to the environment's is deleting the override: `flag delete --segment`.

## 3. `flag list`

A list human result view includes:
- Feature name
- Feature type (standard/multivariate)
- Flag state (on/off)
- Flag value
- Lifecycle stage

JSON output is a bare array of the curated flag shape (see §1), one entry per flag.

### Segment overrides

`--segment <id>` lists the flags overridden for that segment, showing each override's state instead of the environment default (over the same features endpoint, `?environment=…&segment=<id>`). Only flags with an override for the segment appear, and lifecycle stage is dropped — it is an environment-level concept:

```
$ flagsmith flag list --segment 1147496
NAME          TYPE       STATE   VALUE
checkout-v2   standard   on      orange

1 flag
```

```
$ flagsmith flag list --segment 1147496 --json
[
  {
    "feature": "checkout-v2",
    "type": "standard",
    "segment": 1147496,
    "enabled": true,
    "value": "orange"
  }
]
```
