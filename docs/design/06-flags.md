# Flagsmith CLI v2: `flagsmith flag`

Status: draft

A flag is a feature's state in the current environment: its on/off and value for a given environment, segment, or identity.

## 1. Resource

The natural human identifier for `flag` is the feature name:

```
$ flagsmith flag get checkout-v2
```

To get a segment/identity override, use flags:

```
$ flagsmith flag get checkout-v2 --identifier id123
$ flagsmith flag get checkout-v2 --segment 12
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

JSON output is a curated flag shape, not the raw features-endpoint item: the environment state is hoisted to the top level (`enabled`, `value`) alongside the metadata the human view shows, dropping dashboard-only noise. Human and JSON output stay in lockstep.

```json
{
  "feature": "checkout-v2",
  "type": "standard",
  "description": "…",
  "enabled": true,
  "value": "green",
  "segment_overrides": 2,
  "identity_overrides": 0,
  "code_references": 3,
  "lifecycle_stage": "live"
}
```

A segment override (`--segment`) has its own curated shape: `feature`, `type`, `segment`, `enabled`, `value`.

## 2. Mutation

Flags always exist for environments, so a bare `flag create` / `flag delete` wouldn't make sense. The following will exit 2:

```
$ flagsmith flag create brand-new-feature
Did you mean flagsmith feature create brand-new-feature?

Usage:
  ...
```

```
$ flagsmith flag delete checkout-v2
Error: provide either one of --segment, --identifier.
```

Toggling the state, setting the value, and managing segment/identity overrides is handled by `flagsmith flag update`.

Toggle the environment default:

```
$ flagsmith flag update --enable checkout-v2 --yes
✓ Enabled checkout-v2 in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
<INSERT FLAG OUTPUT HERE>
$ flagsmith flag update --disable checkout-v2 --yes
✓ Disabled checkout-v2 in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
<INSERT FLAG OUTPUT HERE>
```

Set environment default value:

```
$ flagsmith flag update checkout-v2 --value green --yes
 ✓ Set checkout-v2 to "green" in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
<INSERT FLAG OUTPUT HERE>
```

Set an identity override and enable its flag:

```
$ flagsmith flag update checkout-v2 --value orange --identifier id123 --enable --yes
 ✓ Set checkout-v2 to "orange" for identifier id123 in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
 ✓ Enabled checkout-v2 for identifier id123 in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
 <INSERT FLAG OUTPUT HERE>
```

Delete a segment override:

```
$ flagsmith flag delete checkout-v2 --segment 12 --yes
 ✓ Deleted checkout-v2 override for segment Premium users (12) in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
```

All flag mutations are powered by `/api/experiments/environments/{environment_key}/update-flag-v2/`. 

## 3. `flag list`

A list human result view includes:
- Feature name
- Feature type (standard/multivariate)
- Flag state (on/off)
- Flag value
- Lifecycle stage

JSON output is a bare array of the curated flag shape (see §1), one entry per flag. 
