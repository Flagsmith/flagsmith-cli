# Flagsmith CLI v2: `flagsmith evaluate`

Status: draft

`evaluate` shows what a Flagsmith SDK would return for the current environment — the resolved flags an app receives at runtime, optionally for a given identity and traits.

`eval` is an accepted alias.

## 1. Examples

### Environment defaults

With no identity, the flags a fresh SDK sees (multivariate shows its control value):

```
$ flagsmith evaluate
FEATURE        ENABLED  VALUE
banner-text    on       Welcome!
button-colour  on       blue
onboarding     off      -

3 flags
```

### For an identity

Resolved for one user. Segment overrides applied, multivariate allocated deterministically:

```
$ flagsmith evaluate --identity user-123
FEATURE        ENABLED  VALUE
banner-text    on       Welcome!
button-colour  on       red
onboarding     on       -

3 flags
```

### What-if traits

Overlay traits to simulate a user. Nothing is persisted: by default, the identity and traits are evaluated transiently:

```
$ flagsmith evaluate --identity user-123 --trait plan=premium --trait age=42
FEATURE        ENABLED  VALUE
banner-text    on       Welcome!
button-colour  on       red
onboarding     on       beta

3 flags
```

Traits without an identity evaluate an anonymous user:

```
$ flagsmith evaluate --trait country=US
```

If need be, `--persist` can be used to force persistence:

```
$ flagsmith eval --identity user-123 --trait plan=premium --persist
```

### A single feature

```
$ flagsmith evaluate onboarding --identity user-123
Feature  onboarding
Enabled  on
Value    beta
```

### JSON

```
$ flagsmith evaluate --identity user-123 --json
[
  { "feature": "banner-text", "enabled": true, "value": "Welcome!" },
  { "feature": "button-colour", "enabled": true, "value": "red", "variant": "red" },
  { "feature": "onboarding", "enabled": true, "value": null }
]
```

## 2. Behaviour

- Powered by the latest Flagsmith Golang SDK, with User-Agent overridden to CLI's.
- Read-only what-if by default: identity evaluation is sent with `transient: true`, so the identity and any `--trait` values are never persisted. Can be overruled with `--persist`.
- `--trait` values are typed by inference: true/false resolved to boolean, digits to integer, `1.5` to float, else string.
