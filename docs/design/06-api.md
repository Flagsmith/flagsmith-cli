# Flagsmith CLI v2: `flagsmith api`

Status: draft/poc

A curl-like command to allow users to call Flagsmith's API with the CLI's resolved credentials.

## 1. Examples

### Admin API

```
# The resolved Admin credential is applied automatically
$ flagsmith api api/v1/projects/ --jq '.results[].name'

# A field implies POST; typed fields are encoded as JSON
$ flagsmith api api/v1/projects/ -F name=acme -F organisation=3

# Explicit method, raw body from stdin
$ echo '{"name":"acme"}' | flagsmith api api/v1/projects/ -X POST --input -

# --yes not expected for DELETE
$ flagsmith api api/v1/projects/12/features/34/ -X DELETE

# Status line and response headers
$ flagsmith api api/v1/projects/999/ -i
```

### SDK API

`--sdk` targets the SDK API with the environment key instead of the Admin credential:

```
$ flagsmith api --sdk api/v1/flags/ --jq '.[] | {name: .feature.name, enabled}'
```

## 2. Behaviour

### Path

The path is taken from the instance host root; a leading slash is optional. It is appended to the resolved base URL, so self-hosted instances work unchanged. Nothing is auto-prefixed — explicitness keeps every API version reachable.

### Credentials

By default the Admin API is called with the resolved Admin credential (see 03-authentication.md), so OAuth refresh and Master API keys apply transparently. `--sdk` switches to the SDK base URL and sends the resolved environment key as `X-Environment-Key` (see 04-project-config.md).

### Request

- `-X, --method` sets the HTTP method. Defaults to GET, or POST when a body or field is supplied.
- `-F, --field key=value` adds a typed field: values that look like numbers, booleans, or `null` are encoded as such. `-f, --raw-field key=value` forces a string. Together they build a JSON body — or, on GET, query-string parameters.
- `--input <file>` sends a raw request body from a file, or `-` for stdin. Mutually exclusive with the field flags.
- `-H, --header 'Name: value'` adds a request header (repeatable).
- `Content-Type: application/json` is set automatically for a field/JSON body.

### Response

- The response body is written to stdout verbatim, so it composes with the global `--jq` filter. `--json` does not apply — output is already the API's own shape — and is ignored.
- `-i, --include` prepends the status line and response headers.
- A non-2xx response writes the body to stderr and exits non-zero, surfacing the status code.

### Destructive calls

`api` is a power tool with curl semantics: no confirmation prompts, for any method including `DELETE`. Callers own what they send.
