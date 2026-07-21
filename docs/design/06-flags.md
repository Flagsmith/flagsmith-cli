# Flagsmith CLI v2: `flagsmith flag`

Status: draft

A flag is a feature's state in the current environment: its on/off and value, as the SDK resolves it.

## 1. Resource

The natural human identifier for `flag` is the feature name:

```
$ flagsmith flag get checkout-v2
```

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

Toggle the environment default:

```
$ flagsmith flag enable checkout-v2
✓ Enabled checkout-v2 in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
$ flagsmith flag disable checkout-v2
✓ Disabled checkout-v2 in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
$ flagsmith flag toggle checkout-v2
✓ Enabled checkout-v2 in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
$ flagsmith flag toggle checkout-v2
✓ Disabled checkout-v2 in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
```

 Set environment default value:

 ```
 $ flagsmith flag set-value checkout-v2 green
 ✓ Set checkout-v2 to "green" in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
 ```

Set an identity override and enable its flag:

```
$ flagsmith flag set-value checkout-v2 orange --identifier id123 --enable
 ✓ Set checkout-v2 to "orange" for identifier id123 in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
 ✓ Enabled checkout-v2 for identifier id123 in environment Production (K2mVsGdXhZ8kQqZ9pJmNbJ)
```



Flags are read from the SDK API (`sdkApiUrl` + environment key, resolved per [04-project-config.md](04-project-config.md)), so `flag` read commands need only an environment key — no Admin API credentials. Note: this will break server-side only features.

| Field | Meaning |
|---|---|
| `name` | Feature name; the identifier. Unique per project, case-insensitive. |
| `enabled` | On/off in the environment. |
| `value` | The feature-state value: a string, number, boolean, or null. |
| `type` | `STANDARD` or `MULTIVARIATE`. |

## 2. `flag list`

`GET {sdkApiUrl}/api/v1/flags/` with `X-Environment-Key`, returning every flag as a bare array.

Human output is a `NAME` / `ENABLED` / `VALUE` table with a count; JSON is the array as returned. This is the minimum viable command to make the nudge from `flagsmith init` real.

## 3. Later

- `flag get <name>`.
- `flag enable`/`disable`/`set <name> <value>` — mutate a feature state. Should consume the experimental endpoint.
