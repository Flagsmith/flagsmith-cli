# Flagsmith CLI v2: Authentication

Status: draft/poc

## 1. Context

Flagsmith has two distinct API surfaces with different credentials, and the CLI must serve both:

| Surface | Purpose | Credentials today |
|---|---|---|
| SDK API | Evaluate/fetch flags | Environment keys via `X-Environment-Key`: client-side (public) and server-side (secret) |
| Admin API | Everything else | Master API keys (`Authorization: Api-Key <prefix>.<secret>`), OAuth 2.1 bearer tokens |

Note: we're omitting the legacy non-expiring user authtokens because they will be replaced with PATs further down the line, and because OAuth fully covers user auth. If static credentials have to be used, Master API Keys are available.

## 2. The auth matrix: target UX

| Context | Admin API | SDK API |
|---|---|---|
| Local dev, interactive | `flagsmith login` → browser OAuth (PKCE + loopback), refresh token in OS keychain | `environment` from `flagsmith.json`, or `FLAGSMITH_ENVIRONMENT_KEY` |
| Local dev, static | `FLAGSMITH_API_KEY` env var | `FLAGSMITH_ENVIRONMENT_KEY` env var |
| CI, zero-secret | OIDC exchange (Actions OIDC token to short-lived Flagsmith token) — new server work | `environment` from `flagsmith.json`, or explicit env var |
| CI, static | `FLAGSMITH_API_KEY` from secret store | `FLAGSMITH_ENVIRONMENT_KEY` from secret store |

Flagsmith already implements OAuth 2.1, so the CLI's interactive login can ride the existing OAuth stack. The genuinely new build is OIDC federation for CI.

## 3. Local dev: `flagsmith login`

Browser-based authorization-code + PKCE on a loopback redirect a-la `gh auth login`:

1. CLI generates PKCE verifier, starts listener on `http://127.0.0.1:<random-port>/callback`.
2. Opens `{dashboard}/oauth/authorize/?client_id=flagsmith-cli&code_challenge=…&redirect_uri=…&scope=admin-api`.
3. User authenticates in the dashboard.
4. Code sent to `POST /o/token/` with PKCE verifier. Refresh token goes to the OS keychain; access token cached and refreshed transparently.
5. CLI prints identity.

Re-auth after 30 days of inactivity.

`flagsmith login` is browser-only. Static credentials are supplied through `FLAGSMITH_API_KEY` expecting a Master API key, never stored by `login`.

Legacy user authtokens (40-char hex, `Authorization: Token …`) are **not supported** — the CLI never sends the `Token` header. Master API keys are the only long-lived credential until personal access tokens exist (phase 2).

## 4. CI: OIDC federation

Goal: zero long-lived secrets in CI. A GitHub Actions job proves its identity with its OIDC token; Flagsmith exchanges it for a short-lived Admin API token.

### API

#### Trust configuration

The "API Keys" Organisation settings is renamed to "API Access" and expanded with Trust relationships.

When adding a Trust relationship, the user is prompted for:
- Trusted issuer URL.
- Expected audience.
- Claim matching rules — e.g. for GitHub: repo, environment, workflow filename.
- RBAC and/or admin toggle (same as with Master API Keys.)

Admin API exposes a `POST /api/v1/auth/oidc/token/` exchange endpoint which validates the signature, checks claim, and mints a short-lived access token.

### CLI

Unless a static credential is set explicitly, the `Flagsmith/setup-cli@v1` action fetches the Actions OIDC token, exchanges it, and stores the resulting short-lived bearer under the `FLAGSMITH_ACCESS_TOKEN` environment variable.

## 5. SDK API auth

- Credential: `FLAGSMITH_ENVIRONMENT_KEY` — SDK auth, takes precedence. Accepts client-side *and* server-side (`ser.*`) keys; the env var is the only home for server-side keys, which are secrets and rejected everywhere else. Because it can hold a secret, it has a host-scoped form (§6), scoped to the *SDK* API URL's host. `FLAGSMITH_ENVIRONMENT` needs none: client-side keys are public.
- Context: `FLAGSMITH_ENVIRONMENT` — the *default environment* as a client-side key, same semantics as `environment` in `flagsmith.json` (see 04-project-config.md). Client-side keys double as SDK auth, so either source alone suffices for flag evaluation with zero Admin API creds.

## 6. Credential precedence & env vars

Admin API:

1. `FLAGSMITH_API_KEY[_<HOST>]`. Host-scoped if `apiUrl` explicitly set to a non-default host.
2. `FLAGSMITH_ACCESS_TOKEN[_<HOST>]`. Host-scoped if `apiUrl` explicitly set to a non-default host.
3. Logged-in profile (keychain) — OAuth session. Always host-scoped.

SDK API (sent as `X-Environment-Key`):

1. Command-line flags. `-e` with a client-side key.
2. `FLAGSMITH_ENVIRONMENT_KEY[_<HOST>]` SDK auth: client- or server-side key. Host-scoped if `sdkApiUrl` explicitly set to a non-default host.
3. `FLAGSMITH_ENVIRONMENT`. Client-side key, or name from which the client-side key is derived.
4. `environment` in the nearest `flagsmith.json`.

Each Admin API variable maps to one credential kind:

- `FLAGSMITH_API_KEY` is Master API key, sent as `Authorization: Api-Key …`.
- `FLAGSMITH_ACCESS_TOKEN` is OAuth-style access token, sent as `Authorization: Bearer …`.

`FLAGSMITH_API_KEY` is validated on read to turn the common mistakes into actionable errors:

- starts with `ser.`: a server-side environment key in the wrong variable. Point at `FLAGSMITH_ENVIRONMENT_KEY`.
- a legacy 40-char hex authtoken: point at `flagsmith login` or a Master API key.

`flagsmith auth status` always prints which source is active, naming the exact variable when a host-scoped one was used.

### Host-scoped credentials

Every secret-bearing credential variable has a host-scoped form, and the unscoped form is trusted only for default SaaS hosts.

`<HOST>` is the API URL's host and port, with `-` written `__` and `.`, `:` written `_`. Matching is case-insensitive:

| API URL | Variable |
|---|---|
| `https://api.flagsmith.com` | `FLAGSMITH_API_KEY_api_flagsmith_com` |
| `https://flagsmith.corp-internal.io` | `FLAGSMITH_API_KEY_flagsmith_corp__internal_io` |
| `http://localhost:8000` | `FLAGSMITH_API_KEY_localhost_8000` |

The scheme is not part of the scope: one scope covers `host:port` over either scheme.

If host-scoped credential not provided, CLI looks in the keychain next.

## 7. Storage

The login session is stored in the OS keychain, one entry per instance, keyed by API URL — nowhere else.

When no keychain is available (headless Linux, containers, SSH), `flagsmith login` **fails closed** before starting any flow and points at `FLAGSMITH_API_KEY` (a Master API key) instead. This is rarely a real loss: those machines can't complete the loopback browser flow anyway. CI never touches disk — env vars/OIDC only.

The phase 2 device-authorization grant makes headless *OAuth* sessions genuinely possible, and will introduce a persistent store for the refresh token at that point.

If a non-SaaS API URL provided via `--api-url`, it is stored in flagsmith.json (see 04-project-config.md).

## 8. Command surface (auth slice)

```
flagsmith login [--api-url URL]
flagsmith logout [--api-url URL]
flagsmith auth status        # identity, credential source, expiry, org
flagsmith auth token         # print a current access token for curl/scripts
```

## 9. Core API changes

### Phase 1. CI OIDC federation

https://github.com/Flagsmith/flagsmith/issues/8040

- Trust relationships CRUD API + JWKS-validating exchange endpoint.
- Dashboard UI for trust relationships.

### Phase 2. Hardening & polish

- Scope enforcement on Admin API endpoints, then granular scopes.
- Device-authorization grant (print URL, open elsewhere.)
- Personal access tokens. Retire the legacy single non-expiring authtoken.
