# Flagsmith CLI

The next-generation Flagsmith command-line interface. Currently a proof of
concept focused on installation and authentication — see
[docs/design/001-installation.md](docs/design/001-installation.md) and
[docs/design/002-authentication.md](docs/design/002-authentication.md).

## Build

```sh
go build -o flagsmith .
```

## Use

```sh
flagsmith login                # browser-based OAuth (PKCE, loopback)
flagsmith auth status          # identity, credential source, token expiry
flagsmith auth token           # print an access token for curl/scripts
flagsmith logout               # revoke and remove the stored session
```

Point at a self-hosted instance with `--api` or `FLAGSMITH_API_URL`:

```sh
flagsmith login --api http://127.0.0.1:8000
```
