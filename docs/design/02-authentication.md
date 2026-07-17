# Flagsmith CLI v2: Authentication

Status: draft

## 1. Context

Flagsmith has two distinct API surfaces with different credentials, and the CLI must serve both:

| Surface | Purpose | Credentials today |
|---|---|---|
| SDK API | Evaluate/fetch flags | Environment keys via `X-Environment-Key`: client-side (public) and server-side (secret) |
| Admin API | Everything else | User authtoken (legacy, non-expiring), org Master API keys (`Authorization: Api-Key <prefix>.<secret>`), OAuth 2.1 bearer tokens |

## 2. The auth matrix: target UX

| Context | Admin API | SDK API |
|---|---|---|
| Local dev, interactive | `flagsmith login` → browser OAuth (PKCE + loopback), refresh token in OS keychain | Auto-resolved via Admin API creds, or `FLAGSMITH_ENVIRONMENT_KEY` |
| Local dev, static | `FLAGSMITH_API_KEY` env var | `FLAGSMITH_ENVIRONMENT_KEY` env var |
| CI, zero-secret | OIDC exchange (Actions OIDC token to short-lived Flagsmith token) — new server work | Auto-resolved, or explicit env var |
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

Browserless options:

- `flagsmith login --token` to paste a Master API key interactively.
- `flagsmith login --token-stdin` to pipe the key to the CLI.

Legacy user authtokens (40-char hex, `Authorization: Token …`) are **not supported** — the CLI never sends the `Token` header. Master API keys are the only long-lived pasteable credential until personal access tokens exist (phase 2).

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

Unless `FLAGSMITH_API_KEY` is set explicitly, the `Flagsmith/setup-cli@v1` action fetches the Actions OIDC token, exchanges it, and stores the result under `FLAGSMITH_API_KEY` environment variable.

## 5. SDK API auth

- Explicit: `FLAGSMITH_ENVIRONMENT_KEY` — no Admin API creds needed.
- Derived: when the user has Admin API creds and names a project/environment, the CLI resolves the environment key via the Admin API and caches it. Nobody should copy-paste environment keys to use the CLI locally.

## 6. Credential precedence & env vars

Highest wins:

1. Command-line flags
2. `FLAGSMITH_API_KEY` (Admin API) / `FLAGSMITH_ENVIRONMENT_KEY` (SDK, `X-Environment-Key`)
3. OIDC
4. Logged-in profile (keychain)

`FLAGSMITH_API_KEY` accepts either Admin API credential. The CLI picks the header by token shape:

- contains a dot → Master API key (`{8-char prefix}.{32-char secret}`, alphabet a-zA-Z0-9) → `Authorization: Api-Key …`.
- dotless → user/OAuth access token (30 alphanumeric chars, includes OIDC-exchanged tokens) → `Authorization: Bearer …`.
- starts with `ser.` → error pointing at `FLAGSMITH_ENVIRONMENT_KEY`.

`flagsmith auth status` always prints which source is active.

## 7. Storage

All credentials are stored in the OS keychain, one entry per instance, keyed by API URL. On headless Linux, fallback to a `0600` plaintext file with a visible warning.

If a non-SaaS API URL provided via `--api-url`, it is stored in flagsmith.json (see 03-project-config.md).

## 8. Command surface (auth slice)

```
flagsmith login [--api URL] [--token] [--token-stdin]
flagsmith logout [--api URL]
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
