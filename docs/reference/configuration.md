# Configuration

`grimoire serve` is configured entirely by environment variables, all
starting with `GRIMOIRE_`. They are read once at startup
(`internal/config/config.go`). If any value is missing or invalid, the
server prints **every** problem it found and exits with status 1, so a
misconfigured deployment can be fixed in one pass.

Values are trimmed of surrounding spaces, and an empty value counts as
unset. List values are comma-separated; spaces around items and empty
items are ignored.

## Server

| Variable | Default | Meaning |
|---|---|---|
| `GRIMOIRE_BASE_URL` | none (**required**) | The address people use, such as `https://recipes.example.com`: an absolute `http` or `https` URL. A trailing `/` is removed. Its origin is trusted by the cross-origin (CSRF) protection, because the proxy may present a different `Host`. |
| `GRIMOIRE_LISTEN` | `:8080` | Address of the app port: pages, the API, `/healthz`, and `/readyz`. |
| `GRIMOIRE_ADMIN_LISTEN` | `:9090` | Address of the admin port: `/metrics` and `/admin/backup`. It has **no authentication**; never expose it through an Ingress or proxy. |
| `GRIMOIRE_DATA_DIR` | `/data` | Holds the database (`grimoire.db`, plus its `-wal`/`-shm` files) and the image store (`images/`). The only directory the server writes to, apart from temporary files. |
| `GRIMOIRE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. Logs are JSON lines on standard error. |
| `GRIMOIRE_TRASH_DAYS` | `30` | Days a deleted recipe stays in the trash before it is purged. Whole number, at least 1. |
| `GRIMOIRE_DEV` | unset (off) | Development mode. Accepts Go boolean values (`1`, `true`, `0`, `false`, ...). Required for `GRIMOIRE_AUTH_MODE=none`. With an empty database it also adds two invented sample recipes at startup. |

## Sign-in

Kitchen Grimoire does not sign people in itself. An SSO reverse proxy in
front of it authenticates people and passes their identity in request
headers (see [Security model](../explanation/security-model.md)).

| Variable | Default | Meaning |
|---|---|---|
| `GRIMOIRE_AUTH_MODE` | `trusted-header` | `trusted-header` for real use, or `none` for local development (every request is the user `dev`, who is an admin). `none` is refused unless `GRIMOIRE_DEV` is true, and logs a loud warning. |
| `GRIMOIRE_TRUSTED_PROXIES` | none (**required** in `trusted-header` mode) | Comma-separated CIDR ranges (`10.244.0.0/16`) or single addresses (`172.30.0.2`) of the proxy. Identity headers are believed only when the connecting peer is inside one of them; any other request gets `401`. |
| `GRIMOIRE_USER_HEADER` | `Remote-User` | Header with the user's stable username. Required on every non-public request. |
| `GRIMOIRE_NAME_HEADER` | `Remote-Name` | Header with the display name (optional). |
| `GRIMOIRE_EMAIL_HEADER` | `Remote-Email` | Header with the email address (optional). |
| `GRIMOIRE_GROUPS_HEADER` | `Remote-Groups` | Header with the user's groups, comma-separated. Read on every request and used only to decide who is an admin. |
| `GRIMOIRE_ADMINS` | empty | Comma-separated usernames (as sent in the user header) who are admins. Case-insensitive. |
| `GRIMOIRE_ADMIN_GROUPS` | empty | Comma-separated groups whose members are admins, such as `admins`. Case-insensitive. |

Identity header values longer than 256 characters, or containing control
characters, are rejected (user header) or ignored (name and email). See
[Make someone an admin](../how-to/make-someone-an-admin.md) for what admins
can do.

## URL import

| Variable | Default | Meaning |
|---|---|---|
| `GRIMOIRE_FETCH_TIMEOUT` | `15s` | Total time allowed for fetching a recipe page or its photo: DNS, connecting, redirects, and reading the body. A Go duration (`10s`, `1m`); must be positive. |
| `GRIMOIRE_FETCH_ALLOW_HOSTS` | empty | **Testing only.** Comma-separated host names that the fetcher may reach on any port and at any address, including private ones. It switches off the SSRF guard for those hosts; never set it in production. |

## Examples

Production, behind an SSO proxy whose pods run in `10.244.0.0/16`:

```sh
GRIMOIRE_BASE_URL=https://recipes.example.com
GRIMOIRE_TRUSTED_PROXIES=10.244.0.0/16
GRIMOIRE_ADMIN_GROUPS=admins
```

Local development (this is what `make dev` sets):

```sh
GRIMOIRE_DEV=1
GRIMOIRE_AUTH_MODE=none
GRIMOIRE_LISTEN=:8080
GRIMOIRE_BASE_URL=http://localhost:8080
GRIMOIRE_DATA_DIR=./local/data
```

## Variables read by other commands

`grimoire migrate` and `grimoire restore` read only `GRIMOIRE_DATA_DIR`, as
the default for their `--data-dir` flag. The other commands read no
environment variables. See the [CLI reference](cli.md).
