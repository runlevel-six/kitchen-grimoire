# How Kitchen Grimoire is built

Kitchen Grimoire is meant to be **operationally boring**: something one
person can run for a household for years without thinking about it. Most
of its shape follows from that. This page is a short tour; the full
reasoning, with alternatives considered, is in the
[design document](../DESIGN.md) and its decision log (§22), referenced
below as D-*nn*.

## One binary, two ports

Everything the server does is in one static Go binary, `grimoire`, built
with `CGO_ENABLED=0` and shipped in a distroless image of about 13 MB
(D-02, D-14). The same binary is the web server, the migration tool, the
backup client, the restore tool, and a self-check (`grimoire doctor`).
There's nothing else to install: no sidecar, no external database, no
Node.js at runtime.

It listens on two ports:

- **8080, the app**: pages, the API, and health checks, behind the SSO
  proxy.
- **9090, the admin port**: Prometheus metrics and a backup stream. It has
  no authentication, which keeps backups simple, so it is never exposed;
  the NetworkPolicy admits only the backup job.

```text
                SSO proxy (forward auth)
phone/laptop ───────────────┬──────────────
                            │ :8080
┌───────────────────────────┴────────────────────────────────────┐
│ grimoire serve                                                 │
│  pages (templ + htmx) → handlers → recipe service → store      │
│                            │                  (SQLite + FTS5)  │
│                            │                  image store (fs) │
│                            ├─ ingest: normalize → validate → save
│                            │     ▲ URL import (scrape, safefetch)
│                            │     ▲ bundle import (JSON-LD)     │
│                            └─ ingredient parser / scaler       │
│ :9090 admin: /metrics, /admin/backup                           │
└────────────────────────────────────────────────────────────────┘
   /data/grimoire.db   /data/images/      ◀── grimoire backup-pull (CronJob)

grimoire-import (separate binary): legacy document → drafts → bundle
```

## Storage: SQLite and a directory of photos

All state lives in one data directory:

- `grimoire.db`, a SQLite database in WAL mode, accessed through the pure-Go
  driver `modernc.org/sqlite` (D-14). One household, one writer, and a
  block volume make SQLite simpler to run and back up than a database
  server (D-03). That is also why the Deployment has exactly one replica
  and the `Recreate` strategy.
- `images/aa/bb/<sha256>.jpg` (or `.png`), plus a `-thumb.jpg`, for photos.
  Files are named by the hash of their bytes, so each is stored once and
  never changes; the database holds only metadata.

The schema is plain embedded SQL migrations, applied in order at startup,
with checksums so an edited migration or a database newer than the binary
stops the server rather than corrupting data (D-18). Queries are
hand-written `database/sql` (D-19). Edits use optimistic concurrency on a
revision number: if two people save the same recipe, the second save is
refused and their text is kept, instead of silently overwriting.

### Search

Full-text search is SQLite FTS5 with one row per recipe in five weighted
columns (title, ingredients, tags and categories, people, and everything
else), an accent-folding tokenizer, and prefix indexes so the word being
typed matches as a prefix. The row is rewritten in the same transaction as
every save, so the index can't drift. User input is rebuilt into a quoted
FTS5 query, so it can never be a syntax error. Details:
[Search syntax](../reference/search-syntax.md).

## One ingest pipeline

Recipes arrive in three ways: typed into the editor, imported from a link,
and loaded in bulk from a bundle. Every path produces a draft in the same
shape and ends in the same service: normalize (trim, drop blank lines,
parse ingredient lines), validate, then save with a revision and a search
update (D-04). Source adapters only have to produce a draft, so behavior
such as "keep the raw line" or "flag rather than drop" is implemented
once.

- **URL import** fetches the page through `safefetch`, a guarded HTTP
  client, and extracts schema.org `Recipe` data: JSON-LD first, then
  Microdata, then an OpenGraph stub flagged for review. There are no
  per-site scrapers, no headless browser, and no bot evasion; the fetcher
  identifies itself honestly and never retries a refusal (D-05). The user
  checks a pre-filled editor before anything is saved.
- **Bulk import** reads a bundle of schema.org JSON-LD with an
  `x-grimoire` extension, matches items by import key, and reports what it
  did per item (D-21). See [Interchange format](../reference/interchange-format.md).
- **The legacy converter**, `grimoire-import`, turns a long word-processor
  cookbook into that same bundle format. It's a separate program, not part
  of the server or the image, because it runs once and needs human
  judgement (a mapping file) (D-07, D-12).

## Ingredients: raw text first

Each ingredient line is stored exactly as written. A small parser reads
quantity, size, unit, item, and note from it where it can (fractions,
ranges, "1 8-oz pkg"), and those parsed fields are derived data that can be
recomputed (D-06). Scaling multiplies only what was parsed and never
writes to the database; anything the parser didn't understand is shown as
written, marked "not scaled". Guessing would be worse than admitting it
doesn't know.

## Server-rendered pages: templ and htmx

The UI is HTML rendered on the server with [templ](https://templ.guide/)
(type-checked components compiled to Go) and enhanced with
[htmx](https://htmx.org/) and one small hand-written module for cook mode
and the PWA (D-16, D-17). The alternative, a React single-page app
embedded in the binary, would have cost the same in image size but needed
a JavaScript build pipeline and a second language to maintain.

Choosing this has visible consequences:

- **Everything works without JavaScript**: reading, searching, the editor,
  the scaler, even cook mode's Back and Next are plain forms and links.
  JavaScript adds instant step changes, swipe, timers, wake lock, offline
  reading, and as-you-type search.
- **A strict Content Security Policy** is easy: no inline scripts or
  styles at all, and htmx is configured not to evaluate code.
- **The generated templ code is committed**, so a plain `go build` (and the
  Dockerfile) needs no extra tools; CI fails if it is stale.

## Phone first, and a PWA

The layout starts at 360 px wide, with 44 px tap targets and large type in
cook mode; a second column appears from about 900 px. A web app manifest
and a service worker make it installable. The worker fetches pages
network-first (so you always get fresh data when online) and keeps recent
recipe pages and favorites for offline reading; static assets have
content-hashed URLs and are cached indefinitely. There's no offline
editing: two phones editing offline would need conflict resolution that a
household doesn't need.

## Operations

- **Configuration** is environment variables only, all validated at
  startup ([Configuration](../reference/configuration.md)).
- **Logs** are JSON, one line per request with route, status, duration,
  and user; recipe text is never logged.
- **Metrics** are a handful of Prometheus series, hand-rolled to avoid a
  dependency ([Metrics](../reference/metrics.md)).
- **Backups** stream a consistent snapshot (`VACUUM INTO`) plus the photos
  and a checksummed manifest; restores verify everything before touching
  the data directory ([Back up and restore](../how-to/backup-and-restore.md)).
- **CI** runs vet, staticcheck, govulncheck, tests (including the fuzz
  targets' seed corpora), a license check, a secret scan, a private-data check, the
  image build on both architectures with a read-only root, and Playwright
  tests at phone and tablet sizes.

## Browser capture

For sites that block servers, the user's own browser sends the page: a
bookmarklet opens `/capture` and posts the page with `postMessage`, and an
iOS Shortcut posts it to `/api/v1/capture` with a capture-scoped token,
then opens a short-lived draft. Both end in the same "check and save"
preview as URL import, and the captured HTML is never stored.

## Out of scope for v1

Meal planning, shopping lists, and unit conversion.
