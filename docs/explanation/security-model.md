# Security model

Kitchen Grimoire holds a household's recipes, notes, and photos. The
threats it is designed against are ordinary ones: someone on the internet
or elsewhere in the cluster reading or changing the library, a recipe page
that tries to make the server attack the local network, and a malicious
file disguised as a photo. This page explains the defenses and why they
are shaped the way they are. Design details are in
[DESIGN.md §12 and §15](../DESIGN.md#15-security).

## The trust boundary: identity from the proxy

Kitchen Grimoire has no passwords, sessions, or login page. Signing in is
the job of an SSO reverse proxy (forward auth, such as Authelia) that the
household already uses for other apps. After authenticating a request, the
proxy adds headers saying who it is: `Remote-User`, and optionally
`Remote-Name`, `Remote-Email`, and `Remote-Groups` (D-08).

Headers are trivial to forge, so the app believes them **only** when the
TCP connection comes from an address in `GRIMOIRE_TRUSTED_PROXIES`. Any
other request to a non-public path gets `401`, and the reason ("peer is
not a trusted proxy", "missing Remote-User header") is logged, not sent.
Users are created automatically the first time a name appears.

In Kubernetes the proxy is the ingress controller, and it connects from a
pod IP, which can be any address in the pod network. So
`GRIMOIRE_TRUSTED_PROXIES` has to be the pod network, and on its own that
would trust **every** pod in the cluster. The second half of the boundary
is the **NetworkPolicy**: it admits connections to the app port only from
the ingress controller's pods. Together they mean "headers are believed
only from the proxy". Either half alone is not enough:

| If you have | Anyone who can... | ...can sign in as anyone |
|---|---|---|
| Trusted proxies, no NetworkPolicy | run a pod in the cluster | yes |
| NetworkPolicy, trusted proxies `0.0.0.0/0` | reach the pod some other way (port-forward, host network) | yes |
| Both | only the ingress controller | no |

The proxy must also **overwrite** the identity headers on every request it
forwards, rather than passing along whatever the client sent. ingress-nginx
does this for the headers listed in `auth-response-headers`.

A few paths are **public** in the app, with no identity needed: `/healthz`,
`/readyz`, `/sw.js`, and `/static/` (CSS, scripts, icons, the manifest).
They contain no household data. Everything else, including photos under
`/img/`, needs a signed-in user.

In development, `GRIMOIRE_AUTH_MODE=none` makes everyone the user `dev`.
The server refuses that mode unless `GRIMOIRE_DEV=1` is also set, and logs
a warning on every start.

## Authorization

Everyone who can sign in can read and edit everything: it's one shared
household library, and every change is a revision that can be undone
(OQ-02). The few actions that can't be undone or that move the whole
library are **admin only**: emptying the trash, bulk import, and export.
Admins come from `GRIMOIRE_ADMINS` (usernames) or `GRIMOIRE_ADMIN_GROUPS`
(groups in the header, which is trusted on the same terms as the identity
headers). See [Make someone an admin](../how-to/make-someone-an-admin.md).

## Cross-site requests

Every state-changing request is checked with Go's cross-origin protection
(`http.CrossOriginProtection`), which uses the browser's `Sec-Fetch-Site`
and `Origin` headers: a form on another site can't make your browser post
to Kitchen Grimoire with your session. The public origin from
`GRIMOIRE_BASE_URL` is trusted explicitly, because behind a proxy the
`Host` header may differ. Non-browser clients (such as `curl` with a
session cookie) send neither header and pass; they're authenticated by the
proxy like everything else.

## The admin port

Metrics and the backup stream are on a separate port, 9090, with **no
authentication**. That is deliberate: the backup CronJob can pull a backup
with no credentials to store or rotate. The price is that this port must
never be reachable by anyone else:

- the `grimoire-admin` Service is ClusterIP only and no Ingress points at
  it;
- the NetworkPolicy admits only pods labelled as the backup job (add your
  metrics scraper explicitly if you use one).

Anyone who can reach port 9090 can download the whole library.

## SSRF guard

URL import makes the **server** fetch a page someone pasted. Without care,
that lets a user (or a recipe page that redirects) make the server fetch
internal addresses: the cluster API, a router's admin page, or a cloud
metadata service. All server-side fetches go through `internal/safefetch`:

- only `http` and `https`, on ports 80 and 443;
- the **resolved** address of every connection is checked at connect time,
  including after each redirect, so DNS tricks that resolve to a private
  address on the second lookup don't work;
- refused: loopback, private (RFC 1918), carrier-grade NAT, link-local,
  unique-local IPv6, multicast, unspecified and reserved ranges, IPv4
  addresses embedded in IPv6 (mapped, NAT64, 6to4), and similar;
- at most 5 redirects, a 15-second total timeout, a 5 MiB limit for pages
  and 10 MiB for images, and only HTML or image content types;
- no cookies or credentials are sent, and the `User-Agent` honestly says
  `KitchenGrimoire/<version>`.

The NetworkPolicy's egress rules repeat this at the network level: the app
may reach DNS and ports 80/443 on the internet, but not private ranges.
`GRIMOIRE_FETCH_ALLOW_HOSTS` lifts the guard for named hosts and exists
only for tests.

## Content Security Policy and output

Every response carries a strict CSP: scripts, styles, images, and
connections only from the app's own origin, no inline scripts or styles,
no plugins, no `<base>` changes, forms only to the app, and no framing
(`frame-ancestors 'none'`). htmx is configured not to evaluate code or
run scripts from swapped content. Also set: `Referrer-Policy: same-origin`,
`X-Content-Type-Options: nosniff`, and a `Permissions-Policy` allowing only
the camera (for photo uploads).

All text, whether typed or scraped, is stored as plain text and escaped by
the templates. Imported HTML is stripped to text before it's stored.
Links built from stored data are only ever `http(s)`.

## Uploads are re-encoded

A file uploaded as a "photo" (or fetched from a recipe page, or found in a
bundle) is never stored or served as given:

1. it is read up to the size limit (15 MiB for uploads);
2. its real format is detected from the content, not the filename or
   declared type; only JPEG, PNG, GIF, and WebP are decoded, with limits on
   dimensions and pixel count to stop decompression bombs;
3. it is re-encoded as JPEG (or PNG if it has transparency), at most 2048
   px on the long edge, plus a thumbnail. Re-encoding drops EXIF data,
   including GPS location;
4. it is stored under the SHA-256 of the new bytes and served with a fixed
   content type.

## Private data and the repository

The repository is public. A gitignored `local/` directory holds anything
private (real deployment overlays, the real legacy cookbook), and
`scripts/check-private.sh`, run by a pre-commit hook and in CI, fails if a
denylisted term appears in a commit. Test fixtures are invented. See
DESIGN.md §17. To report a vulnerability, see
[SECURITY.md](../../SECURITY.md).
