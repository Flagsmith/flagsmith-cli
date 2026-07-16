# Flagsmith CLI v2 — Installation & Initial Auth

Status: draft for discussion
Scope: the "install → authenticate → first successful API call" flow only.
Stack: Go + cobra, single static binary named `flagsmith`.

## 1. Context

Flagsmith has two API surfaces with disjoint credential models, and the CLI must serve both:

| Surface | Purpose | Credentials today |
|---|---|---|
| Management API (control plane) | CRUD on orgs/projects/environments/flags | User authtoken (legacy, non-expiring), org **Master API keys** (`Authorization: Api-Key <prefix>.<secret>`), OAuth 2.1 bearer tokens (new, 15-min) |
| SDK API (flags consumer) | Evaluate/fetch flags | **Environment keys** via `X-Environment-Key`: client-side (shortuuid) and server-side (`ser.` prefix, expirable) |

The decisive finding from the core repo: **a full OAuth 2.1 authorization server already exists** (`api/oauth2_metadata/`, django-oauth-toolkit 3.1.0), built for the MCP integration:

- Authorization-code + PKCE (S256 required), public clients
- Access tokens **15 min**, refresh tokens **30 days, rotating**
- RFC 8414 metadata, dynamic client registration, revocation, introspection
- Redirect URI validation **already allows `http://localhost`** — a loopback CLI flow is permitted today
- `OAuth2BearerTokenAuthentication` is already in `DEFAULT_AUTHENTICATION_CLASSES`, and **scopes are not enforced** by the default permission stack — a valid access token already authenticates as the user on management endpoints

So the CLI's interactive login can ride the existing OAuth stack with near-zero server work. The genuinely new build is **OIDC federation for CI**.

## 2. The auth matrix (target UX)

| Context | Management API | SDK API |
|---|---|---|
| Local dev, interactive | `flagsmith login` → browser OAuth (PKCE + loopback), refresh token in OS keychain | Auto-resolved via management creds, or `FLAGSMITH_ENVIRONMENT_KEY` |
| Local dev, static | `FLAGSMITH_MASTER_API_KEY` env var | `FLAGSMITH_ENVIRONMENT_KEY` env var |
| CI, zero-secret | **OIDC exchange** (GitHub Actions / GitLab / etc. → short-lived Flagsmith token) — new server work | Auto-resolved, or explicit env var |
| CI, static | `FLAGSMITH_MASTER_API_KEY` from secret store | `FLAGSMITH_ENVIRONMENT_KEY` from secret store |

## 3. Local dev: `flagsmith login`

Browser-based authorization-code + PKCE on a loopback redirect (the `gh auth login` / `gcloud auth login` shape):

1. CLI generates PKCE verifier, starts listener on `http://127.0.0.1:<random-port>/callback`.
2. Opens `{dashboard}/oauth/authorize/?client_id=flagsmith-cli&code_challenge=…&redirect_uri=…&scope=management-api`.
3. User authenticates in the dashboard — **email/password, MFA, Google, GitHub, SAML/SSO all come for free**, because the frontend already handles every method. (This is the killer argument for OAuth over programmatic password login: MFA users get an ephemeral-token dance and SAML users have no password at all. Password login in a CLI is a dead end for exactly the enterprise users we care about.)
4. Code → `POST /o/token/` with PKCE verifier. Refresh token goes to the OS keychain; access token cached and refreshed transparently.
5. CLI prints identity + org, and if the user belongs to multiple orgs, prompts once for a default.

Re-auth is only needed after 30 days of inactivity (rotating refresh extends the window while in use).

Fallbacks:
- `flagsmith login --with-token` — paste a Master API key interactively (stored in keychain, never shell history). Covers browserless preference without server work.
- Device-authorization grant (RFC 8628) for SSH/headless boxes — **not supported by DOT 3.1.0 / oauthlib 3.2.2** (verified: no device modules in the installed packages), so it needs an upstream upgrade or hand-rolled endpoints. Deferred to phase 2; `--with-token` covers the gap.

Implementation notes (validated against the phase 0 server, see §9):
- First-party public client is pre-registered by data migration with pinned `client_id=flagsmith-cli` — no per-install DCR.
- The consent screen is **always shown**, even for first-party clients; `skip_authorization` is repurposed as an `is_verified` trust badge in the consent UI. (Earlier draft proposed skipping consent; the shipped design keeps it, which is fine — it's one click.)
- The loopback listener must bind **literal `127.0.0.1` (or `[::1]`)** and use it in `redirect_uri` — DOT's RFC 8252 loopback port-wildcard only applies to those hostnames; `http://localhost:<port>/callback` is rejected with "Mismatching redirect URI". Any ephemeral port works.
- The CLI must **always explicitly request `scope=management-api`**: with scope omitted, the per-client policy filters the global default (`mcp`) down to an empty scope set — works today only because scopes aren't enforced yet.
- Rotating refresh tokens race when concurrent CLI processes refresh simultaneously; take a file lock around refresh. Server sets a 120 s `REFRESH_TOKEN_GRACE_PERIOD_SECONDS`, so a client that loses the rotation response can retry with the previous token (verified working).

## 4. CI: OIDC federation (the new build)

Goal: **zero long-lived secrets in CI**. A GitHub Actions job proves its identity with its OIDC token; Flagsmith exchanges it for a short-lived management token.

Server side (new):
1. **Org-level trust configuration** (dashboard + API): trusted issuer URL, expected audience, and claim-matching rules (`repository`, `ref`, `environment`, `sub` patterns), each rule bound to a principal — reuse the `APIKeyUser`/RBAC machinery so a trust rule grants the same admin-or-RBAC-scoped access a Master API key does.
2. **Exchange endpoint** — `POST /api/v1/auth/oidc/token/` with `{token: <ci-oidc-jwt>}`: validate signature against the issuer's JWKS, check audience/expiry/claim rules, mint a short-lived access token (15–60 min; a DOT `AccessToken` row keeps validation on the existing bearer path). A custom endpoint beats wedging RFC 8693 token-exchange into DOT/oauthlib, which don't support it — but keep the request/response fields RFC 8693-shaped so we can migrate later. Natural home: a new `api/oidc_federation/` app alongside `api_keys`.

CLI side:
- Auto-detect ambient OIDC: GitHub Actions (`ACTIONS_ID_TOKEN_REQUEST_URL`/`_TOKEN`), GitLab (`id_tokens`), CircleCI, Buildkite. If present and no explicit creds are set, the CLI fetches the CI token, exchanges it, and proceeds — **`flagsmith flags list` just works in CI with no configuration in the workflow file**.
- `flagsmith login --oidc` to force it; clear error pointing at the org trust config if the exchange is rejected.

Until this ships (and forever, as the escape hatch): `FLAGSMITH_MASTER_API_KEY` from the CI secret store works with today's API.

## 5. SDK API auth

- Explicit: `FLAGSMITH_ENVIRONMENT_KEY` (client key or `ser.` server key) — no management creds needed; right for runtime-ish use.
- Derived: when the user has management creds and names a project/environment, the CLI resolves the environment key via the management API and caches it. Nobody should copy-paste environment keys to use the CLI locally.

## 6. Credential precedence & env vars

Highest wins; `flagsmith auth status` always prints **which source is active** — silent precedence is the #1 debugging pain in CLIs.

1. Command-line flags
2. `FLAGSMITH_MASTER_API_KEY` (management, `Api-Key` header) / `FLAGSMITH_ENVIRONMENT_KEY` (SDK, `X-Environment-Key`)
3. Ambient CI OIDC (auto-detected)
4. Logged-in profile (keychain)

Two explicit variables, not one generic `FLAGSMITH_API_KEY`: master keys and environment keys are different header schemes on different surfaces, and key-format sniffing is fragile (master key prefixes are random). Also: `FLAGSMITH_API_URL` and `FLAGSMITH_EDGE_API_URL` for self-hosted, honored everywhere.

## 7. On-disk layout

- `~/.config/flagsmith/config.yml` (XDG / `os.UserConfigDir`): named **profiles** (api_url, auth method, default org) — multi-instance (SaaS + self-hosted) from day one.
- Secrets: OS keychain (macOS Keychain / Windows Credential Manager / Secret Service) via a go-keyring library; fallback to a `0600` plaintext file with a visible warning on headless Linux. CI never touches disk — env vars/OIDC only.
- `flagsmith.toml` in the repo (committed): project/environment mapping and api_url for the project — **never secrets**. Written by `flagsmith init`.

## 8. Command surface (auth slice)

```
flagsmith login [--host URL] [--with-token] [--oidc] [--profile NAME]
flagsmith logout [--profile NAME]
flagsmith auth status        # identity, credential source, expiry, org
flagsmith auth token         # print a current access token for curl/scripts
```

## 9. Core API changes, phased

**Phase 0 — interactive login: SHIPPED as [Flagsmith/flagsmith#8029](https://github.com/Flagsmith/flagsmith/pull/8029)** (docker image `ghcr.io/flagsmith/flagsmith:pr-8029`). Validated end-to-end against that image: signup → authorize (GET app info, POST consent) → code with state → PKCE token exchange (public client, no secret) → 15-min `management-api` bearer token accepted on management endpoints → refresh rotation with 120 s grace. Scope policy cross-denies correctly (`flagsmith-cli`↛`mcp`, DCR clients↛`management-api`).
- Data migration: first-party `flagsmith-cli` public client, loopback redirect URIs `http://127.0.0.1/callback http://[::1]/callback` (port-agnostic per RFC 8252), `skip_authorization=True` — repurposed as the `is_verified` consent badge, consent itself is always shown. OAuth clients are instance-global (DOT's stock `Application` model — no org FK; tenancy comes from the authenticating user's org memberships, not the client), so one well-known client works for every org. The migration must pin a fixed `client_id` (DOT auto-generates one by default) so the binary can hardcode it and work against SaaS and any self-hosted instance.
- Add a `management-api` scope to `OAUTH2_PROVIDER["SCOPES"]`. Precise meaning: *the token may call the Management API surface, acting as the authenticating user* — a ceiling, not a grant; RBAC/org membership still decides what succeeds. Scopes name surfaces (`mcp` = MCP surface, `management-api` = management surface), never clients — client identity already travels in `client_id`. Cosmetic today (scopes aren't enforced) but it future-proofs consent text, enforcement, and granular subdivision (`management-api:read`, …).
- Issuance policy via a custom `SCOPES_BACKEND_CLASS` (`get_available_scopes(application=…)` is per-client): DCR-registered third-party clients may only request `mcp`; the first-party `flagsmith-cli` client may request `management-api`. Client identity controls what scopes can be issued; scopes control what surface a token reaches; RBAC controls what the user can do there.
- Set `REFRESH_TOKEN_GRACE_PERIOD_SECONDS`.

**Phase 1 — CI OIDC federation:**
- `oidc_federation` app: trust-config model + admin API, JWKS-validating exchange endpoint minting short-lived tokens bound to RBAC-scoped principals.
- Dashboard UI for trust rules.

**Phase 2 — hardening & polish:**
- Scope enforcement on management endpoints (`TokenHasScope`-style), then granular scopes.
- Device-authorization grant (DOT/oauthlib upgrade or hand-rolled).
- Personal access tokens (user-scoped, listable, expiring) to retire the legacy single non-expiring authtoken — nice-to-have; OAuth + master keys already cover the CLI matrix.

## 10. Installation

Single static binary via goreleaser, from GitHub Releases:

- `brew install flagsmith/tap/flagsmith` (primary mac/linux path)
- `curl -fsSL https://get.flagsmith.com | sh` (CI-friendly, pinnable version)
- GitHub Action (`flagsmith/setup-cli@v1`) that installs + performs the OIDC exchange — makes the CI story one workflow line
- winget / scoop, deb/rpm, Docker image, `go install` for free
- Optional: publish a thin npm wrapper under the existing `@flagsmith` scope that downloads the binary — migration bridge for current npm installers

Target first-run experience:

```
$ brew install flagsmith/tap/flagsmith
$ flagsmith login
✓ Opened browser… authenticated as kim@flagsmith.com (org: Flagsmith)
$ flagsmith flags list --project my-app --environment production
```

and in GitHub Actions, with org trust configured, the same commands with **no secrets at all**.

## 11. Open questions

1. **Scope enforcement timing** — any valid OAuth token (including `mcp`-scoped MCP tokens) currently authenticates on all management endpoints. Fine to ship phase 0 on, but worth a deliberate decision + roadmap slot.
2. **OIDC principal binding** — bind trust rules to RBAC roles directly (cleaner, but non-admin requires the enterprise `rbac` package) vs. an admin/non-admin toggle mirroring Master API keys (simpler, coarser).
3. ~~**Consent screen**~~ — resolved by #8029: consent is always shown; first-party clients get an `is_verified` badge instead of a skip.
5. **Default scopes for first-party clients** — with no `scope` param, `flagsmith-cli` gets an *empty* scope set (global default `mcp` is policy-filtered, nothing substitutes `management-api`). CLI works around it by always requesting explicitly; consider making `get_default_scopes` return `FIRST_PARTY_SCOPES` for first-party clients (feedback for #8029 or follow-up).
4. **`get.flagsmith.com`** — does the install-script domain exist / who owns provisioning it?
