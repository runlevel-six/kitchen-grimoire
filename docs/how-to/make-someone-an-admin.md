# How to make someone an admin

Everyone who can sign in can read, add, edit, delete, and restore every
recipe. **Admins** can also:

- empty the trash at once (`POST /trash/empty`);
- import bundles (`POST /api/v1/import/bundle`);
- export the library (`GET /api/v1/export`).

Anything else that needs an admin (managing other people's API tokens) is
not yet available.

There are two ways to make someone an admin. A person is an admin if
**either** applies.

## By group (recommended with an SSO proxy)

If your SSO proxy sends the user's groups, name the admin groups:

```sh
GRIMOIRE_ADMIN_GROUPS=admins
```

The server reads the groups header (`Remote-Groups` by default,
`GRIMOIRE_GROUPS_HEADER` to change it) on every request: a comma-separated
list such as `family,admins`. Matching is case-insensitive. Group
membership is not stored, so adding or removing someone in your identity
provider takes effect on their next request, with no restart.

With Authelia, `Remote-Groups` must be in the Ingress's
`auth-response-headers` (see
[Deploy on Kubernetes](../tutorials/deploy-on-kubernetes.md)). The base
ConfigMap in `deploy/base` already sets `GRIMOIRE_ADMIN_GROUPS: "admins"`.

## By username

List usernames, exactly as the proxy sends them in `Remote-User`:

```sh
GRIMOIRE_ADMINS=alex,sam
```

Matching is case-insensitive. Use this when the proxy doesn't send groups,
or to make one person an admin without touching the identity provider.
Changing it needs a restart (in Kubernetes, edit the ConfigMap in your
overlay and roll the Deployment).

## Which to use

| | `GRIMOIRE_ADMIN_GROUPS` | `GRIMOIRE_ADMINS` |
|---|---|---|
| Source of truth | Your identity provider | Kitchen Grimoire's configuration |
| Takes effect | Next request | After a restart |
| Needs | A proxy that sends a groups header | Nothing extra |

## Check it

Sign in as the person and open **Trash** (in the footer) with at least one
recipe in it. Admins see **Empty the trash** at the bottom; others don't.
From the command line, an admin gets a zip from `GET /api/v1/export` and
anyone else gets `403 {"error": "admin only"}`.

In dev mode (`GRIMOIRE_AUTH_MODE=none`), the fixed user `dev` is always an
admin.

## Groups and headers are trusted only from the proxy

Like the identity headers, the groups header is believed only from
addresses in `GRIMOIRE_TRUSTED_PROXIES`. That's what stops someone from
sending `Remote-Groups: admins` themselves. Make sure your proxy
**overwrites** the groups header with its own value on every request. See
[Security model](../explanation/security-model.md).
