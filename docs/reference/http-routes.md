# HTTP routes

The server listens on two ports:

- the **app port** (`GRIMOIRE_LISTEN`, default `:8080`): pages, the API,
  and health checks. Put this behind the SSO proxy.
- the **admin port** (`GRIMOIRE_ADMIN_LISTEN`, default `:9090`): metrics
  and the backup stream. It has **no authentication**; keep it internal.

Routes are Go `net/http` patterns. `{slug}` is a recipe's URL name (e.g.
`example-lemon-cream-bars`).

## Who may call what

On the app port, every route needs an authenticated user (trusted identity
headers from the proxy, or dev mode) **except** the public paths
`/healthz`, `/readyz`, `/sw.js`, and everything under `/static/`. A request
without a valid identity gets `401` with no detail; the reason is logged.

Every authenticated user can read and edit every recipe. A few actions are
**admin only** and answer `403` for others (see
[Make someone an admin](../how-to/make-someone-an-admin.md)).

State-changing requests (`POST`) are also checked by Go's cross-origin
protection (`Sec-Fetch-Site` and `Origin`): cross-site browser requests
are refused. Same-site requests and non-browser clients such as `curl` pass.

## Pages

| Method and path | Auth | Purpose |
|---|---|---|
| `GET /` | user | Home: search box, your favorites, recently added, recently edited. |
| `GET /search` | user | Search results and filtered lists. Query parameters below. htmx updates the results as you type. |
| `GET /browse` | user | Categories, people ("From"), collections, and tags, with counts. |
| `GET /browse/category/{path...}` | user | Recipes in a category and its sub-categories, e.g. `/browse/category/desserts/cookies`. |
| `GET /browse/contributor/{name}` | user | Recipes contributed by one person (exact name, case-insensitive). |
| `GET /review` | user | The review queue: recipes flagged `needs_review`. |
| `GET /new` | user | Empty recipe editor. |
| `POST /new` | user | Create a recipe from the editor form. Redirects to the new recipe. |
| `GET /r/{slug}` | user | Recipe view. `?x=0.5`, `1`, `2`, or `3` scales the ingredients. |
| `POST /r/{slug}` | user | Save an edit. Answers `409` with your text kept if someone else saved first. |
| `GET /r/{slug}/edit` | user | Editor for a recipe, including its photos. |
| `GET /r/{slug}/cook` | user | Cook mode. `?step=N` opens a given step. |
| `POST /r/{slug}/delete` | user | Move to the trash. |
| `POST /r/{slug}/undelete` | user | Restore from the trash. |
| `POST /r/{slug}/favorite` | user | Set (`on=1`) or clear your favorite. |
| `POST /r/{slug}/reviewed` | user | Clear the review flag (no new revision). |
| `GET /r/{slug}/history` | user | List of revisions. |
| `GET /r/{slug}/history/{rev}` | user | One old revision. |
| `POST /r/{slug}/history/{rev}/restore` | user | Make an old revision current (saved as a new revision). |
| `POST /r/{slug}/images` | user | Upload a photo (multipart field `photo`, max 15 MiB). |
| `POST /r/{slug}/images/{id}/delete` | user | Remove a photo from the recipe. |
| `POST /r/{slug}/images/{id}/primary` | user | Make a photo the main one. |
| `GET /img/{file}` | user | A stored photo: `<sha256>.jpg`, `<sha256>.png`, or `<sha256>-thumb.jpg`. |
| `GET /import` | user | URL import form. `?url=` pre-fills the field. |
| `POST /import` | user | Fetch a page and show the extracted recipe in the editor. Nothing is saved yet. |
| `POST /import/save` | user | Save the checked import, then fetch its photo. |
| `GET /trash` | user | The trash. |
| `POST /trash/empty` | **admin** | Permanently delete everything in the trash. Needs the form field `confirm=yes`. |

### Search parameters

`/search` accepts these query parameters; the browse and review pages use
the same ones. See [Search syntax](search-syntax.md) for `q`.

| Parameter | Meaning |
|---|---|
| `q` | Search text. Empty lists everything A–Z. |
| `cat` | Category path slug, e.g. `desserts/cookies`. Includes sub-categories. |
| `tag` | Tag slug, e.g. `make-ahead`. |
| `by` | Contributor ("From"), exact name, case-insensitive. |
| `col` | Collection, exact name, case-insensitive. |
| `review` | `1`: only recipes that need review. |
| `photo` | `1`: only recipes with a photo. |
| `fav` | `1`: only your favorites. |
| `page` | Page number; 40 results per page. |

## Static files and health

| Method and path | Auth | Purpose |
|---|---|---|
| `GET /static/...` | public | CSS, JavaScript, icons, and `manifest.webmanifest`. URLs with `?v=<hash>` are cached for a year. No directory listings. |
| `GET /sw.js` | public | The PWA service worker, served from the root so it controls every page. |
| `GET /healthz` | public | Liveness: `ok` while the process runs. |
| `GET /readyz` | public | Readiness: `ok` when the database answers within 2 seconds, otherwise `503`. |

## API

JSON in, JSON out, on the app port, so requests go through the SSO proxy
like any other. Errors are `{"error": "..."}`. Examples are in
[Bulk import and export](../how-to/bulk-import-and-export.md).

| Method and path | Auth | Purpose |
|---|---|---|
| `POST /api/v1/import/bundle` | **admin** | Import a bundle zip (body, `Content-Type: application/zip`, max 512 MiB). `?dry_run=1` (or `true`) reports without writing. Answers with the [import report](interchange-format.md#import-report). |
| `GET /api/v1/export` | **admin** | Download every live recipe as a bundle: `kitchen-grimoire-export-YYYYMMDD.zip`. |

Status codes for the import: `200` with the report (even when some items
were invalid or skipped), `400` for a malformed bundle, `403` if you are not
an admin, `413` if the body is over 512 MiB, `415` for a wrong
`Content-Type`.

**Not yet available:** `POST /api/v1/capture` and the `/capture` page
(browser capture from a bookmarklet or the iOS share sheet, with per-user
API tokens), and a settings page. These are planned for Phase 5 (DESIGN.md
§8.3, §19).

## Admin port

| Method and path | Auth | Purpose |
|---|---|---|
| `GET /metrics` | none | Prometheus metrics in text format. See [Metrics](metrics.md). |
| `GET /admin/backup` | none | Streams a backup (`.tar.gz`: database snapshot, images, manifest). Used by `grimoire backup-pull`. |

Anyone who can reach the admin port can download the whole library. In
Kubernetes, the `grimoire-admin` Service is ClusterIP-only and the
NetworkPolicy admits only the backup job.

## Response headers

Every app-port response carries:

- `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`
- `Referrer-Policy: same-origin`
- `X-Content-Type-Options: nosniff`
- `Permissions-Policy: camera=(self), geolocation=(), microphone=()`
- `X-Request-Id`, which also appears in the request's log line.

Pages are sent with `Cache-Control: no-store`.
