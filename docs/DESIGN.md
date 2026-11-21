# Kitchen Grimoire — Design Document

> **Working title.** Product name "Kitchen Grimoire"; repo `kitchen-grimoire`, Go module `github.com/runlevel-six/kitchen-grimoire`; binary `grimoire`. A rename is a find-and-replace and is tracked as OQ-11.

| | |
|---|---|
| Status | Draft v0.1, ready for Phase 0 |
| Audience | The maintainer, and the Claude Code agent implementing it |
| License | Apache-2.0 (DECIDED, D-10) |
| Last updated | 2026-11-19 |

---

## 0. How to use this document

**For the implementing agent (Claude Code):**

1. Read this whole document before writing code.
2. If `local/CONTEXT.md` exists, read it too. It holds private deployment details and personal context. **Never copy anything from `local/` into a tracked file**: not names, hostnames, recipe text, or file paths outside the repo. If something there seems to belong in a public file, rewrite it generically and ask the maintainer first.
3. Work in phase order (§19). Within the current phase, pick the lowest-numbered unchecked task.
4. Follow the project-management protocol in §18 at the start and end of every working session.
5. When the document is silent or ambiguous, use the default in §21 (Open Questions), log that you used it, and keep going. Stop and ask only for the items marked **BLOCKING**.

**Tags used in this document:**

- **[DECIDED]**: agreed with the maintainer. Changing it needs a new Decision Log entry (§22).
- **[PROPOSED]**: the designer's recommendation, not yet confirmed. Implement it unless the maintainer says otherwise.
- **[SPIKE]**: depends on a Phase 0 finding. Record the result before building on it.

---

## 1. Summary

Kitchen Grimoire is a self-hosted recipe library for one household. It ships as a single static Go binary with a mobile-first web UI. Recipes get in three ways:

1. **URL import.** Paste a link; the server fetches the page, extracts the schema.org `Recipe` data, and shows a preview to edit before saving.
2. **Manual entry.** Typing on a phone has to be fast.
3. **Bulk import.** A standalone converter turns a legacy family collection (a large word-processor document) into the app's interchange format and loads it in one pass.

The guiding principle: this is an **heirloom archive first and a recipe app second.** Nothing the household puts in, whether an original wording, a person's name, or a note in the margin, is ever silently lost.

## 2. Goals and non-goals

### Goals

- **G1. Phone-first.** Easy to read and use on a phone in the kitchen: big tap targets, the screen stays awake, and you never lose your place.
- **G2. Fast URL import** that works for most mainstream recipe sites with no per-site code.
- **G3. Fast manual entry**, with one ingredient per line.
- **G4. Lossless preservation.** Keep raw ingredient lines, the original imported text, the original scraped JSON-LD, revision history, and provenance ("who this came from").
- **G5. Operationally boring.** One binary, one SQLite file plus an image directory, scheduled backups, a tested restore, and a minimal container.
- **G6. Portable.** A full export in an open format (schema.org JSON-LD plus images) that re-imports identically.
- **G7. Public open source.** The repo is public from day one with no private data in it.

### Non-goals (v1)

- Meal planning, shopping lists, pantry inventory, nutrition.
- Multi-household or SaaS tenancy.
- A headless browser, or any bot-protection evasion when fetching.
- Native mobile apps. A PWA is enough.
- Unit conversion between US and metric (OQ-08).
- Shipping the legacy-collection converter as an in-app feature. It's a separate tool (D-07).

## 3. Users and scenarios

A household of roughly 1–10 people, authenticated by an SSO proxy in front of the app.

- **S1. Saving a recipe found online:** on a laptop, or from the phone's share sheet.
- **S2. Cooking:** phone propped on the counter, hands messy. The user needs big type, steps one at a time, ingredients to check off, and the screen not dimming.
- **S3. Writing down a family recipe:** typed on a phone, possibly with a photo of the handwritten card.
- **S4. Finding something:** for example "the cheese ball Aunt X used to make," "what can I make with buttermilk," or "Mom's Christmas cookies."
- **S5. Preserving a legacy collection:** a one-time load of hundreds of recipes with reviewable results.

## 4. Requirements

### Functional

| ID | Requirement |
|---|---|
| FR-01 | Create, edit, view, and soft-delete recipes. Deleted recipes go to a trash that is purged after 30 days. |
| FR-02 | A recipe has: title, description, yield (text, with optional numeric servings), prep/cook/total time, ordered ingredient groups, ordered instruction groups, notes, categories (hierarchical), tags, images, source URL, author/source (as published), **contributed by** (the person who gave it to the household), and **collection** (e.g. a family cookbook name). |
| FR-03 | Import from a URL: extract schema.org `Recipe` from JSON-LD, falling back to Microdata. Show an editable preview before saving. |
| FR-04 | Import from page HTML supplied by the user's own browser (bookmarklet / share sheet), for sites that block server-side fetches. |
| FR-05 | Bulk import of a schema.org JSON-LD bundle. It must be idempotent and report conflicts. |
| FR-06 | Export the full library as a JSON-LD bundle with images. Re-importing it into an empty instance gives the same library. |
| FR-07 | Full-text search across title, ingredients, instructions, notes, tags, and contributor, with filters by category, tag, contributor, and collection. |
| FR-08 | Scale servings. Only parsed quantities scale; unparsed lines are shown with a visible "not scaled" marker. |
| FR-09 | Cook mode: screen wake lock, large type, step-by-step focus, ingredient check-off, and tap-to-start timers for durations found in step text. |
| FR-10 | Revision history per recipe, with the ability to view and restore an older revision. |
| FR-11 | "View original": imported recipes keep their verbatim source text, viewable alongside the structured version. |
| FR-12 | Review queue: recipes flagged `needs_review` by import are listable and can be cleared. |
| FR-13 | Favorites per user. |
| FR-14 | Printable single-recipe view (browser print CSS). |
| FR-15 | Image upload from a phone camera. Images are resized and have metadata stripped. |

### Non-functional

| ID | Requirement |
|---|---|
| NFR-01 | Single static binary, `CGO_ENABLED=0`. Runtime image is distroless-static or scratch, target < 40 MB. |
| NFR-02 | Usable on a 375 px wide viewport. Lighthouse mobile accessibility ≥ 95. Respects `prefers-reduced-motion` and `prefers-color-scheme`. |
| NFR-03 | p95 page render under 150 ms server-side at 5,000 recipes on modest hardware. |
| NFR-04 | Dependency budget enforced (§5.2). |
| NFR-05 | The server-side fetcher cannot reach private, loopback, link-local, or cluster addresses (§15.1). |
| NFR-06 | Backup and restore are scripted and exercised in CI (§14.4). |
| NFR-07 | No private data in the repository, enforced by tooling (§17). |

## 5. Technology

### 5.1 Stack

| Layer | Choice | Status |
|---|---|---|
| Language | Go, latest stable at bootstrap (check go.dev) | [DECIDED] |
| HTTP | `net/http` with method + path pattern routing; no framework | [PROPOSED] |
| Templates | `github.com/a-h/templ` (type-checked components, compiled to Go) | [DECIDED] D-17 |
| Interactivity | htmx (vendored and embedded), plus one small hand-written JS module for cook mode, wake lock, timers, and the service worker | [DECIDED] D-17 |
| CSS | Hand-written CSS with custom properties, no build step. Mobile-first. | [PROPOSED] |
| Database | SQLite via the pure-Go driver `modernc.org/sqlite` (FTS5 and `VACUUM INTO` verified) | [DECIDED] D-14 |
| Queries | `sqlc` (build-time codegen) or hand-written `database/sql`; agent's choice, recorded as a decision | [PROPOSED] |
| Migrations | Embedded SQL files with a minimal in-repo runner | [PROPOSED] |
| HTML parsing | `golang.org/x/net/html` | [PROPOSED] |
| Images | `image/jpeg`, `image/png`, `golang.org/x/image/webp` (decode only), `golang.org/x/image/draw` (resize) | [PROPOSED] |
| IDs | UUIDv7 or ULID | [PROPOSED] |
| Logging | `log/slog`, JSON output | [DECIDED] |
| Metrics | Prometheus text format on the admin port. Use `prometheus/client_golang` only if hand-rolling becomes awkward. | [PROPOSED] |

Node.js is allowed **only** as a dev/CI dependency, for Playwright end-to-end tests. It never ships in the image.

### 5.2 Dependency policy

- **Runtime Go module dependencies:** the driver, templ runtime, `x/net`, `x/image`, and an ID library. Anything else needs a Decision Log entry giving the reason, the license, and the alternatives considered.
- **Licenses:** no GPL/AGPL/LGPL in the shipped binary. A CI license check enforces this.
- **Frontend:** vendored and pinned, with checksums recorded in `web/vendor/README.md`.

### 5.3 Why not X

- **Why no Python scraping sidecar:** the main path, JSON-LD extraction, is small and easy to own in Go. A sidecar doubles the images and supply chain to maintain (D-05).
- **Why no React SPA:** server-rendered templ + htmx keeps the codebase in one language and needs no JS build pipeline. The container size is the same either way (assets are embedded), so the deciding factor is the amount of frontend code to maintain (OQ-01).
- **Why no Postgres:** one household, one writer, and SQLite on a block volume is simpler to run and back up (D-03).

## 6. Architecture

```
                 ┌──────────────── SSO reverse proxy (forward-auth) ─────────────────┐
 phone / laptop ─┤  sets Remote-User / Remote-Name / Remote-Email                    │
                 └──────────────────────────────┬────────────────────────────────────┘
                                                │ :8080 (app port)
┌───────────────────────────────────────────────┴───────────────────────────────────────┐
│ grimoire serve                                                                        │
│                                                                                       │
│  http: pages (templ+htmx) ── handlers ── service layer ── store (SQLite + FTS5)       │
│                                   │                 └──── imagestore (filesystem)      │
│                                   ├── ingest: normalize → validate → save (one path)  │
│                                   │      ▲          ▲            ▲                    │
│                                   │  scraper   capture(HTML)  bulk JSON-LD            │
│                                   │      │                                            │
│                                   │  safefetch (SSRF-guarded HTTP client)             │
│                                   └── ingredient parser / scaler                      │
│                                                                                       │
│  :9090 admin port (not exposed via ingress): /metrics, /admin/backup (stream)         │
└───────────────────────────────────────────────────────────────────────────────────────┘
        /data/grimoire.db   /data/images/…          ▲ grimoire backup-pull (CronJob)

 grimoire-import (separate binary, never in the image)
   legacy document → split → classify → normalize → JSON-LD bundle + review report
   → POST /api/v1/import/bundle
```

### 6.1 Repository layout

```
cmd/grimoire/            main: serve, backup-pull, restore, migrate, version
cmd/grimoire-import/     legacy collection converter (not built into the image)
internal/config/
internal/httpx/          server, middleware (auth, CSRF, logging, recover)
internal/web/            handlers + templ components; static assets embedded
internal/recipe/         domain types, service layer, validation
internal/store/          SQLite access, migrations, FTS
internal/ingest/         normalize → validate → persist; idempotency keys
internal/scrape/         JSON-LD + Microdata extraction, ISO 8601 durations
internal/safefetch/      guarded HTTP client
internal/ingredient/     line parser, quantity math, scaler, formatter
internal/imagestore/     content-addressed storage, resize, thumbnail
internal/exportfmt/      schema.org JSON-LD bundle read/write
internal/legacy/         converter library used by cmd/grimoire-import
web/static/              css, js (cook mode, sw.js), vendored htmx
deploy/base/             generic Kustomize base
deploy/compose/          docker compose example
docs/                    DESIGN.md (this), status.md, operations.md, adr/ (optional)
testdata/                synthetic fixtures only
local/                   GITIGNORED: private context, real overlay, real data
scripts/                 check-private.sh, dev helpers
```

## 7. Data model

### 7.1 Principles

- **Raw first, parsed second.** Every ingredient keeps its `raw_text`. Parsed fields are derived, nullable, and can be recomputed.
- **Provenance has real fields**, not notes:
  - `source_url`: where it was fetched from.
  - `author`: the author as published, e.g. a TV chef.
  - `contributed_by`: the person who gave it to the household.
  - `collection`: e.g. a family cookbook name.
- **Never destroy:**
  - soft delete;
  - a revision snapshot on every save;
  - `original_text` for imports;
  - `raw_source` (the extracted JSON-LD) for URL imports.

### 7.2 Tables (indicative; finalize in P1.1)

```
users            id, remote_user (unique), display_name, email, created_at, last_seen_at
recipes          id, slug (unique), title, description,
                 yield_text, servings (real, null), prep_min, cook_min, total_min,
                 notes (markdown),
                 source_kind ('manual'|'url'|'capture'|'bundle'|'legacy'),
                 source_url, source_site, author, contributed_by, collection,
                 import_key (unique, null),          -- idempotency for bulk/legacy
                 original_text (null),               -- verbatim legacy block
                 raw_source (null, JSON),            -- extracted JSON-LD object
                 needs_review (bool), review_reasons (JSON array),
                 created_by, created_at, updated_at, deleted_at (null), revision (int)
ingredient_groups id, recipe_id, position, name (null)       -- "Filling", "Frosting"
ingredients      id, group_id, position, raw_text,
                 qty_min (real,null), qty_max (real,null), unit (null),
                 size_text (null),       -- "8-oz" in "1 8-oz pkg cream cheese"
                 item (null), note (null), parse_ok (bool)
step_groups      id, recipe_id, position, name (null)
steps            id, group_id, position, text
categories       id, parent_id (null), name, slug     -- "Desserts" > "Cookies"
recipe_categories recipe_id, category_id
tags             id, name, slug
recipe_tags      recipe_id, tag_id
images           id, recipe_id, sha256, mime, width, height, position, is_primary,
                 source_url (null), created_at
revisions        recipe_id, revision, snapshot (JSON), edited_by, edited_at
favorites        user_id, recipe_id, created_at
api_tokens       id, user_id, name, hash, scope ('capture'), created_at, last_used_at, revoked_at
recipes_fts      FTS5 virtual table (see §9)
```

Images live on disk at `/data/images/<aa>/<bb>/<sha256>.<ext>` plus `…-thumb.jpg`. The database stores only metadata.

### 7.3 Interchange format

The canonical import/export format is **schema.org `Recipe` JSON-LD**. Fields schema.org lacks go in an `x-grimoire` object, so ordinary consumers can ignore them:

```json
{
  "@context": "https://schema.org",
  "@type": "Recipe",
  "name": "Example Cheese Ball",
  "recipeCategory": ["Appetizers"],
  "keywords": "party, make-ahead",
  "recipeYield": "1 ball",
  "recipeIngredient": ["1 8-oz pkg cream cheese, softened", "..."],
  "recipeInstructions": [
    {"@type": "HowToSection", "name": "Ball",
     "itemListElement": [{"@type": "HowToStep", "text": "Beat cream cheese..."}]}
  ],
  "author": {"@type": "Person", "name": "As published"},
  "x-grimoire": {
    "version": 1,
    "importKey": "legacy:example-collection:0042",
    "contributedBy": "Example Contributor",
    "collection": "Example Family Cookbook",
    "categoryPath": [["Appetizers"]],
    "ingredientGroups": [{"name": null, "lines": ["1 8-oz pkg cream cheese, softened"]}],
    "originalText": "EXAMPLE CHEESE BALL  Appetizers  Example Contributor\n1 8-oz pkg ...",
    "needsReview": false,
    "reviewReasons": [],
    "notes": "..."
  }
}
```

A bundle is a `.zip` containing `recipes.jsonl` (one object per line) and `images/<sha256>.<ext>`, with images referenced from `image` by relative path.

## 8. Ingestion

All paths end in **one ingest pipeline**: `normalize → validate → dedupe → persist → index`. A source adapter only produces a draft in the interchange shape.

### 8.1 Manual entry

- **Ingredients:** one textarea per group, one ingredient per line. A "+ group" button adds a named group. Lines are parsed on save, and lines that fail to parse are shown subtly in the editor.
- **Steps:** one textarea per group; a blank line separates steps.
- The editor works with no JavaScript. htmx improves it: autosave of drafts, inline add-group.

### 8.2 URL import

1. The user submits a URL. The server fetches it with `safefetch` (§15.1).
2. Extract every `<script type="application/ld+json">` block. Parse tolerantly: skip blocks with invalid JSON, and try a lenient repair before giving up (raw control characters such as newlines inside string literals, which S3 found in the wild; trailing commas; HTML entities).
3. Find the Recipe node. It may sit at the top level, in an array, inside `@graph`, or inside `mainEntity`. `@type` may be a string or an array containing `"Recipe"`. Resolve `@id` references within the same document.
4. Map the fields:
   - `recipeInstructions` can be a string, an array of strings, an array of `HowToStep`, or `HowToSection` → `itemListElement` (possibly nested). Map sections to step groups.
   - Times are ISO 8601 durations (`PT1H15M`); be lenient about malformed ones.
   - `image` can be a string, an array, or an `ImageObject`; pick the largest.
   - `author` can be a string, a `Person`, or an array.
   - `recipeYield` can be a string, a number, or an array.
   - `recipeCategory` and `keywords` can be strings or arrays.
5. Strip HTML tags and decode entities in every text field. Store plain text only.
6. **Fallback:** Microdata (`itemtype="…schema.org/Recipe"`). If that also fails, OpenGraph title, description, and image become a stub recipe with `needs_review=true` and reason `no_structured_data`.
7. Store the extracted Recipe object in `raw_source`.
8. Fetch the primary image via `safefetch`, then validate, re-encode, and resize it (§8.5).
9. Show the preview/edit form with everything pre-filled. Nothing is saved until the user confirms.
10. **Dedupe:** canonical URL (`<link rel=canonical>`, then the final URL after redirects, minus tracking parameters). If it already exists, offer "open existing" or "save as new."

No site-specific scrapers in v1. When a site fails, record the domain and outcome in metrics and logs, and handle it with an issue-driven, opt-in adapter later.

Some sites block server fetches intermittently: S3 saw sites that answered 200 and then 403 minutes later. Never retry a refusal in a loop; offer the capture path instead.

**Rules for the agent:** no headless browser, no User-Agent spoofing to get around blocks, no retrying through proxies. Send an honest `User-Agent` like `KitchenGrimoire/<version> (+repo URL)`. If a site refuses, the answer is the capture path (§8.3).

### 8.3 Browser capture (Phase 5)

For sites that block server fetches, the user's own browser sends the HTML it already has.

- **Desktop bookmarklet.** It opens `/capture` in a new window, then `postMessage`s `document.documentElement.outerHTML` and `location.href` to it. The capture page:
  - accepts the message;
  - runs the same extraction as §8.2 steps 2–10;
  - shows the preview.

  This avoids cross-site cookie problems: the capture page is a top-level, first-party navigation. *Verify bookmarklet behavior under page CSP on the target browsers; record the results in `docs/operations.md`.*
- **iOS Shortcut (share sheet):** "Run JavaScript on Web Page" returns the HTML, and "Get Contents of URL" POSTs it to `/api/v1/capture` with a per-user **capture-scoped API token**. The SSO proxy bypasses only that exact path (documented in operations), and the app enforces the token. Tokens are stored hashed, revocable, and shown once.
- Captured HTML is capped at 5 MiB and never stored. Only the extracted object goes into `raw_source`.

### 8.4 Bulk import and the legacy converter

**Bulk endpoint:** `POST /api/v1/import/bundle` accepts a bundle (§7.3).

- **Idempotency** is keyed on `x-grimoire.importKey`, falling back to canonical `url` when present:
  - new key → create;
  - existing key, recipe unchanged since import (`revision == import_revision`) → update in place;
  - existing key, recipe edited by a human → **skip and report a conflict** (OQ-09).
- The response is a report: created, updated, skipped-conflict, and invalid counts, with per-item reasons. A `?dry_run=1` mode gives the report without writing.

**Legacy converter `cmd/grimoire-import`** is a separate binary, never included in the container image. It's a generic tool for **line-structured recipe documents**: a title line carrying category, subcategory, and attribution fields, then ingredient lines, then instruction paragraphs. Stages:

1. **Read.** UTF-8 plain text only (D-12). Source documents are converted first with standard tools, documented in `docs/operations.md`:
   - Word 97–2003 `.doc` (and `.docx`, `.rtf`): `textutil -convert txt` on macOS, or LibreOffice `--convert-to txt` elsewhere. This keeps tabs and leaves each paragraph on one line.
   - PDF: `scripts/pdf2txt.js` (macOS PDFKit) or poppler `pdftotext -layout`. Paragraphs come out wrapped at the page width, and PDFKit collapses runs of spaces.

   S2 found no paragraph styles that survive conversion, so splitting is heuristic.
2. **Split into recipe blocks.** A title line is an ALL-CAPS title followed by a known category token. The known-category list comes from the mapping file (step 4).
   - Ignore page markers and form feeds; a recipe may continue across a page break.
   - Lines that look like titles but end in a colon (inline sub-recipes such as `SPECIAL SAUCE:`) are not titles.
   - The fields after the title are separated by tabs when the source kept them. When it didn't (PDF text), split them by longest match against the mapping file's tokens. Any text left over makes the recipe `needs_review` with reason `unsplit_fields`.
3. **Within a block,** ingredient lines come first, then the instructions. The boundary is the first line that is long (over about 75 characters) or ends with sentence punctuation. Wrapped instruction lines (PDF sources) are joined back into paragraphs. Tab-aligned multi-column ingredient tables (e.g. a "single batch" quantity column beside a "double batch" one) are flagged `multi_column_ingredients` and never parsed as a single quantity. Lines like `Filling:`/`Frosting:`/`Topping:` at the start of a block or paragraph become named groups where it's unambiguous; otherwise they stay in the text and the recipe is flagged.
4. **Classify the attribution fields** using a **mapping file** (`--map attribution-map.csv`, columns `token,class,canonical,comment`). Classes: `category`, `subcategory`, `contributor`, `published_source` (e.g. a TV chef or restaurant), `tag` (e.g. "Microwave", "Sugar-free"), `note`, `group_label`, and `compound`. A `compound` token holds several fields at once; its `canonical` is `class=value; class=value`, e.g. a tag and a contributor run together. The `canonical` column also merges spelling variants of the same person or source. An unknown token makes the recipe `needs_review` with reason `unclassified_token:<token>`. The converter has a `--emit-tokens` mode that lists every distinct token with counts, so the maintainer can fill in the map.
5. **Normalize** legacy ingredient naming (e.g. `Cheese--Cheddar` → `Cheddar cheese`, `Flour--All Purpose` → `all-purpose flour`) using a rule table and an override file. Always keep the original line in `originalText`.
6. **Emit** a bundle plus `report.md` and `report.csv`: per-recipe flags, counts by category, unknown tokens, recipes with zero ingredients or zero steps.
7. **Load:** `grimoire-import push --bundle out.zip --url … --token …`, or upload through the admin UI.

**Import keys** are `legacy:<collection-slug>:<ordinal>:<title-slug>`. They stay stable across re-runs as long as the source order doesn't change. The report warns if the title at an ordinal changes.

**Fixtures:** only a synthetic sample document in `testdata/legacy/` that imitates the structure with invented recipes and names. The real source document stays in `local/` (§17).

### 8.5 Images

- **Accept:** JPEG, PNG, WebP (decode only), and GIF (first frame). Max 15 MiB upload, 10 MiB fetched.
- **Store:** re-encode to JPEG (quality ~85; PNG kept for images with transparency), long edge at most 2048 px, plus a 480 px thumbnail. Re-encoding strips EXIF/GPS. Files are named by the SHA-256 of the stored bytes.
- **Unsupported formats** (e.g. AVIF): keep the remote URL in `images.source_url`, show a placeholder, and log it.

## 9. Ingredient parsing and scaling

### 9.1 Line grammar

```
line   := [qty] [size] [unit] item [", " note]  |  free text
qty    := number | number "-" number | number " to " number
number := integer | decimal | fraction | integer " " fraction | integer unicode_fraction | unicode_fraction
size   := number "-"? ("oz"|"ounce"|"lb"|"g"|"ml") | "(" … ")"        -- "8-oz", "(15 oz)"
```

- **Unicode fractions** ½ ⅓ ⅔ ¼ ¾ ⅛ ⅜ ⅝ ⅞ ⅕ ⅙ are accepted, including when glued to an integer (`1½`).
- **Units:** an alias table (`tsp`, `teaspoon(s)`, `t`; `Tbsp`, `tablespoon(s)`, `T`; `cup(s)`, `c`; `oz`; `lb`; `pkg`/`package`; `can`; `envelope`; `stick`; `clove`; `pinch`; `dash`; `pint`; `quart`; `gallon`; `bunch`; `jar`; `box`; `bag`; `slice`; `sprig`). `t` vs `T` is case-sensitive; anything else is case-insensitive.
- **No match:** `parse_ok=false` and the line is kept verbatim. Never guess.

### 9.2 Scaling

- Multiply `qty_min`/`qty_max` by the factor. **Never scale `size_text`:** "1 8-oz pkg" × 2 is "2 8-oz pkg."
- **Formatting:** US volume units round to the nearest 1/8 and render as unicode fractions; weights round sensibly; counts of 1/2 and above round to friendly values.
- Unparsed lines are shown unchanged with a "not scaled" marker.
- Scaling is display-only and never written to the database.

### 9.3 Durations in steps (cook mode timers)

Detect `N minutes`, `N-M minutes`, `N hour(s)`, `N to M minutes`, and `overnight` (not tappable). Make the detected spans tappable; tapping starts a client-side timer, using the upper bound of a range. Timers survive the screen locking (store the target timestamp, not a countdown) and show a notification if permission was granted.

## 10. Search

The FTS5 table `recipes_fts` has columns `title, ingredients, tags, contributor, body` (body = instructions + notes + description). It uses the `unicode61 remove_diacritics 2` tokenizer and prefix indexes on 2 and 3 characters.

- **Ranking:** `bm25(recipes_fts, 10.0, 5.0, 5.0, 3.0, 1.0)`.
- **Query syntax:** plain words are ANDed with automatic prefix on the last term; `"quoted phrases"`; `-exclude`. Users never see raw FTS syntax errors; invalid input falls back to a phrase search.
- **Filters:** category (including descendants), tag, contributor, collection, favorites, needs-review, has-photo.
- Reindex on every save. `grimoire migrate --reindex` rebuilds the index.

## 11. UI and UX

### 11.1 Principles

- **Mobile-first layout** from 360 px up, with a two-column recipe view (ingredients beside steps) from about 900 px.
- **Tap targets** at least 44 px. Base font 18 px, and 22 px or more in cook mode.
- **Calm by default:** autosave drafts, undo toasts instead of "are you sure?" dialogs (except emptying the trash), no modal stacks.
- **Works without JavaScript** for read, search, create, and edit. JavaScript adds cook mode, timers, wake lock, offline, and htmx niceties.
- **Light and dark themes** following the system setting, with a manual override.

### 11.2 Key screens

1. **Home:** search box first, then recently viewed, favorites, and recently added.
2. **Browse:** a category tree and contributor list, each with counts.
3. **Recipe view:**
   - header (title, photo, yield with scaler, times, provenance line "From *Contributor* · *Collection*");
   - ingredients with check-off;
   - steps;
   - notes;
   - a "View original" toggle when `original_text` exists;
   - edit and favorite buttons.
4. **Cook mode:** full-screen, one step at a time with swipe or large next/previous buttons, ingredients in a drawer, Screen Wake Lock (with a visible indicator and graceful fallback when unsupported), and timers.
5. **Editor:** described in §8.1.
6. **Import:** a URL field, then a preview/edit form.
7. **Review queue:** flagged recipes with their reasons, a side-by-side original/structured view, and a "mark reviewed" action.
8. **Settings:** API tokens, theme, export, and an admin import page.

### 11.3 PWA

- A manifest, so the app can be added to the home screen.
- A service worker with a **network-first** strategy for pages and cache-first for static assets.
- Recently viewed and favorited recipes are cached for offline reading. Their offline availability is shown in the UI.
- No offline editing in v1.

## 12. Auth and users

- **`GRIMOIRE_AUTH_MODE=trusted-header`** (production) [DECIDED]:
  - Identity comes from `Remote-User`, and optionally `Remote-Name` and `Remote-Email`. Header names are configurable.
  - Headers are honored **only** when the immediate peer is inside `GRIMOIRE_TRUSTED_PROXIES` (a list of CIDRs). Otherwise the request gets 401.
  - Users are created automatically on first request.
- **`GRIMOIRE_AUTH_MODE=none`** (development only): a fixed user `dev`. It refuses to start unless `GRIMOIRE_DEV=1`, and logs a loud warning.
- **API tokens** are bearer tokens for `/api/v1/capture` only (§8.3).
- **Authorization v1:** every authenticated user can read and edit everything (OQ-02). An `admin` role, for bulk import, emptying the trash, and token administration of other users, comes from a configured list of usernames.
- **CSRF:** use `net/http`'s cross-origin protection (`http.CrossOriginProtection`, Sec-Fetch-Site based; confirm it is available in the Go version used) on all state-changing routes, as well as SameSite cookies at the proxy.
- OIDC mode is future work.

## 13. Routes (indicative)

```
GET  /                              home
GET  /search?q=&cat=&tag=&by=       results (htmx partial when HX-Request)
GET  /r/{slug}                      recipe view        GET /r/{slug}/cook
GET  /r/{slug}/edit  POST /r/{slug} edit/update        POST /r/{slug}/delete
GET  /new            POST /new      create
GET  /import         POST /import   URL → preview      POST /import/save
GET  /capture                       bookmarklet target
GET  /review                        review queue       POST /r/{slug}/reviewed
GET  /r/{slug}/history  POST /r/{slug}/restore/{rev}
GET  /browse  /browse/category/{slug}  /browse/contributor/{slug}
GET  /settings  /trash
POST /api/v1/capture                (token)  html+url → draft id
POST /api/v1/import/bundle          (admin)  zip → report
GET  /api/v1/export                 (admin)  zip
GET  /healthz  /readyz              app port
GET  /metrics  GET /admin/backup    admin port only
```

## 14. Deployment and operations

### 14.1 Configuration (environment variables)

| Variable | Default | Meaning |
|---|---|---|
| `GRIMOIRE_LISTEN` | `:8080` | App listener |
| `GRIMOIRE_ADMIN_LISTEN` | `:9090` | Metrics + backup stream; never route through ingress |
| `GRIMOIRE_DATA_DIR` | `/data` | DB + images |
| `GRIMOIRE_BASE_URL` | (required) | Absolute URL for links and capture |
| `GRIMOIRE_AUTH_MODE` | `trusted-header` | or `none` (with `GRIMOIRE_DEV=1`) |
| `GRIMOIRE_TRUSTED_PROXIES` | (required in trusted-header) | CIDR list |
| `GRIMOIRE_USER_HEADER` etc. | `Remote-User`, `Remote-Name`, `Remote-Email` | Header names |
| `GRIMOIRE_ADMINS` | (empty) | Comma-separated usernames |
| `GRIMOIRE_FETCH_TIMEOUT` | `15s` | safefetch total timeout |
| `GRIMOIRE_FETCH_ALLOW_HOSTS` | (empty) | Test-only allowlist that permits private targets |
| `GRIMOIRE_TRASH_DAYS` | `30` | Purge delay |
| `GRIMOIRE_LOG_LEVEL` | `info` | |

### 14.2 Container

- **Multi-stage build:** golang builder, then `go build -trimpath -ldflags="-s -w"` with `CGO_ENABLED=0`. Generated templ code is committed, so the build needs no templ CLI (D-16).
- **Runtime:** `gcr.io/distroless/static-debian13:nonroot` (pinned by digest), or a current equivalent. It includes CA roots and tzdata.
- **Multi-arch:** `linux/amd64` and `linux/arm64`. The builder stage cross-compiles on the build platform. CI builds and tests each architecture on a native runner (D-15).
- **Labels:** OCI labels including source, revision, and license.
- **Runs as non-root** with a read-only root filesystem. `/data` and `/tmp` are writable volumes.

### 14.3 Kubernetes (generic base in `deploy/base/`)

- **Deployment:** `replicas: 1`, `strategy: Recreate` (one SQLite writer, ReadWriteOnce volume). `runAsNonRoot`, `readOnlyRootFilesystem`, drop ALL capabilities, `seccompProfile: RuntimeDefault`. Resource requests and limits. Liveness `/healthz`, readiness `/readyz`.
- **PVC:** `grimoire-data` (RWO, default 10 Gi).
- **Services:** `grimoire` (8080) and `grimoire-admin` (9090, ClusterIP, never used by an Ingress).
- **Ingress:** a generic placeholder. The real host, TLS, and SSO middleware or annotations live in the private overlay.
- **NetworkPolicy:**
  - ingress on 8080 only from the ingress controller's namespace;
  - ingress on 9090 only from the backup job's pods (and the metrics scraper if one is used);
  - egress to DNS, plus 80/443 to the internet **excluding** the cluster pod/service CIDRs and RFC 1918 ranges. This is defense in depth for §15.1; the overlay sets the CIDRs.
- **Backup CronJob** using the same image: `grimoire backup-pull --from http://grimoire-admin:9090 --out /backup --keep 14`, writing to a `grimoire-backup` PVC.
- **`deploy/compose/`:** a compose file with the same settings, for people not running Kubernetes.

### 14.4 Backup and restore

- **`GET /admin/backup`** streams a `.tar.gz` containing:
  - a consistent database snapshot (SQLite `VACUUM INTO` a temporary file, or the online backup API);
  - the images;
  - a `manifest.json` (version, counts, checksums).
- **`grimoire restore --from file.tar.gz --data-dir /data`** refuses to run against a non-empty data directory unless given `--force`, and verifies the checksums.
- **CI test:** seed data, back up, restore into an empty directory, and compare. Counts, checksums, and the export bundle must be byte-identical after normalization.
- **Operations doc:** `docs/operations.md` describes the restore drill and how to copy backups **off the cluster**. A backup on the same storage system as the data is not a disaster backup (OQ-10).

### 14.5 Observability

- **Logs:** structured, with request id, user, route, status, and duration. Never log recipe bodies or captured HTML.
- **Metrics:**
  - `grimoire_http_requests_total{route,code}`
  - `grimoire_http_duration_seconds`
  - `grimoire_scrape_total{result}`, where result is one of `jsonld`, `microdata`, `opengraph_stub`, `blocked`, `error`
  - `grimoire_recipes`
  - `grimoire_backup_last_success_timestamp`
  - Domain names go in logs, not metric labels.

## 15. Security

### 15.1 SSRF-guarded fetching (`internal/safefetch`)

- **Schemes:** `http` and `https` only. **Ports:** 80 and 443 only, unless allowlisted in tests.
- **Address check at connect time.** Use a custom `net.Dialer` with a `Control` function that checks the **resolved** IP of every connection, including after redirects. This blocks DNS-rebinding tricks. Deny:
  - loopback, private (RFC 1918), CGNAT `100.64/10`, link-local `169.254/16` and `fe80::/10`;
  - ULA `fc00::/7`, multicast, unspecified, broadcast;
  - IPv4-mapped forms of all of the above;
  - any extra CIDRs from configuration (the cluster ranges).
- **Limits:** at most 5 redirects, each validated again. Total timeout 15 s. Response body cap 5 MiB for HTML and 10 MiB for images. Accept `text/html` or `application/xhtml+xml` for pages and `image/*` for images.
- **Request hygiene:** no cookies, no auth headers, an honest User-Agent.
- **Tests:** table-driven cases covering every denied range, plus a redirect-to-private case, run with `httptest` and a stub resolver.

### 15.2 Other

- **Output:** all user and scraped text is plain text, escaped by templ. Notes use a restricted Markdown renderer with no raw HTML, or plain text if a renderer would add a dependency (OQ-12).
- **Uploads:** content-sniffed, then decoded and re-encoded. Never served with the uploaded filename or MIME type.
- **Security headers:** a CSP with `default-src 'self'` and `img-src 'self' data:`, no inline scripts (htmx config included), `frame-ancestors 'none'`, `Referrer-Policy: same-origin`, and `X-Content-Type-Options`.
- **Fuzz targets** (Go native fuzzing) for the JSON-LD extractor, the ingredient parser, the duration parser, and the legacy splitter. Run them briefly in CI and longer locally.
- **`SECURITY.md`** points to GitHub private vulnerability reporting.

## 16. Testing

| Level | What |
|---|---|
| Unit | Ingredient grammar (table tests including unicode fractions, ranges, sizes, "t" vs "T"); scaler formatting; ISO 8601 durations; FTS query sanitizer; mapping-file classifier |
| Golden | Scraper: **synthetic** HTML fixtures covering every JSON-LD shape in §8.2 (top-level, array, `@graph`, `@id` refs, `HowToSection` nesting, string instructions, malformed JSON). Expected output stored as JSON. |
| Local regression | Real page captures and the real legacy document live in `local/fixtures/` (gitignored). A `make test-local` target runs the same golden harness over them when present. They are never committed (copyright and privacy). |
| Fuzz | See §15.2 |
| Integration | `httptest` server + temp data dir: CRUD, import preview/save, bundle idempotency and conflicts, revisions/restore, trash purge, auth header trust boundary |
| Round-trip | Export, then import into an empty instance, then export again: bundles equal after normalization |
| Backup/restore | §14.4 |
| E2E | Playwright at iPhone and iPad viewports: search, view, cook mode step navigation, create recipe, URL import against a local fixture server |
| Build | Image builds for both architectures; container starts with a read-only root filesystem and passes the readiness probe |

CI runs `go vet`, `staticcheck`, `govulncheck`, unit/integration/golden tests, fuzz (short), the license check, betterleaks, `scripts/check-private.sh`, and the image build. Playwright runs on PRs into `main`.

## 17. Open-source hygiene and the private boundary

- **First commit** contains `LICENSE`, `NOTICE`, `.gitignore` (with `local/`, `*.db`, `/data/`, Playwright traces and screenshots, `dist/`), and `README.md`. `local/` is created only after that.
- **Copyright and commit identity:** see `local/CONTEXT.md`. Set the identity repo-locally and verify it with `git log -1 --format='%an <%ae>'` before the first push. Never write email addresses into tracked files.
- **betterleaks** (gitleaks-compatible secret scanner, D-11) runs as a pre-commit hook and in CI.
- **`scripts/check-private.sh`** (committed, generic) reads `local/denylist.txt` (gitignored) and fails if the staged diff or the commit message contains any listed term, case-insensitive. In CI, without the denylist, it checks only for an empty `local/` tree.
- **All test fixtures are synthetic.** Invented recipes and invented names only; no recipe text copied from real sites or books.
- **The real legacy document,** its mapping file, its converter output, and real deployment overlays live in `local/`.
- **If private data is ever pushed:** stop, tell the maintainer, and do not silently rewrite history. The maintainer decides whether to rewrite or rotate.
- The repo includes `SPDX-License-Identifier` headers in source files.

## 18. Project management

The plan, status, and decisions all live in git, next to the code.

### 18.1 Artifacts

| File | Purpose | Who edits |
|---|---|---|
| `docs/DESIGN.md` | What and why (this file). Task checkboxes in §19. | Agent and maintainer; design changes always come with a Decision Log entry |
| `docs/status.md` | Append-only session log, newest at the bottom | Agent at the end of every session; maintainer for sign-offs |
| `docs/operations.md` | Deploy, backup, restore drill, SSO bypass rule for capture, troubleshooting | Agent, as features land |
| `local/notes.md` | Private working notes (real recipe names, real site results) | Agent and maintainer; never referenced from tracked files |

### 18.2 Session protocol (agent)

**Start of session:**

1. Read §0, the current phase in §19, §21, and the last 3 entries of `docs/status.md`.
2. Confirm `git status` is clean and you are on the current phase branch.
3. State which task IDs you plan to do this session.

**End of session:**

1. Tick the completed task checkboxes in §19, in the same commit as the work or a follow-up docs commit.
2. Append a `docs/status.md` entry:

```markdown
## 2026-11-20 — session N (phase P1)
- Done: P1.3, P1.4
- Decisions: D-12 (chose sqlc over hand-written queries — reason)
- Defaults used: OQ-08 (no unit conversion)
- New questions: OQ-13 (…)
- Blockers: none
- Next: P1.5
```

Status entries must contain **no private data** (§17). Put specifics in `local/notes.md`.

### 18.3 Changing the design

- A small clarification goes straight into the doc in the same commit as the code.
- A changed behavior, a new dependency, or a reversed [PROPOSED] item gets a Decision Log entry (§22) with context, the decision, alternatives, and consequences. Mark it `[PROPOSED]` until the maintainer confirms.
- Never let the code silently diverge from this document. If they disagree, fix one of them in the same PR.

### 18.4 Git workflow

- `main` is always deployable. Work on `phase-N-<slug>` branches, one PR per phase or per coherent slice.
- **Conventional Commits with the task ID:** `feat(scrape): P3.2 handle @graph and @id refs`.
- Keep commits small; each should build and pass tests.
- **The agent commits locally and never pushes, merges, tags, or force-pushes without the maintainer's explicit OK.**
- Tags are `vMAJOR.MINOR.PATCH`. Each phase that ships to the household gets a minor version.

### 18.5 Definition of done (every task)

- Code, tests, and docs updated.
- CI green locally: `make check`.
- No new dependency without a Decision Log entry.
- `check-private.sh` passes.
- §19 checkbox ticked.

### 18.6 Phase gates

A phase is complete when every exit criterion has been demonstrated and the maintainer has written `Signed off: Pn` in `docs/status.md`. The next phase doesn't start before sign-off. Phase 0 spikes may block later tasks; their results are recorded before dependent work begins.

## 19. Plan

### Phase 0: Bootstrap and spikes

- [x] **P0.1** Repo bootstrap per §17: LICENSE, NOTICE, .gitignore, README stub, SECURITY.md, betterleaks hook, `check-private.sh`, Makefile (`make check`, `make test-local`).
- [x] **P0.2** Go module, `cmd/grimoire` with `serve` and `version`, config loading, slog, `/healthz`, graceful shutdown.
- [x] **P0.3** CI: vet, staticcheck, govulncheck, test, license check, betterleaks, image build (multi-arch).
- [x] **S1** SQLite driver: confirm a pure-Go driver builds with `CGO_ENABLED=0`, runs in the distroless image on both architectures, and **supports FTS5 and `VACUUM INTO`**. Pick the driver and record it in the Decision Log.
- [x] **S2** Legacy source: inspect the real source documents in `local/import/` (paragraph styles, tabs vs spaces in title lines, page breaks). Decide between style-based and heuristic splitting. Record the details in `local/notes.md` and the conclusion in status.
- [x] **S3** Scrape coverage: using a throwaway `cmd/scrapeprobe` and a seed list in `local/fixtures/urls.txt` of recipe pages from widely used recipe sites (the maintainer has no fixed set of sites; the list grows as the app is used), measure JSON-LD presence and field completeness. Record the percentages in status (domains only in `local/notes.md`).
- [x] **S4** UI stack check: a templ + htmx prototype of the recipe view and cook mode at a 375 px viewport. The maintainer signs off on OQ-01, or redirects.

**Exit:** CI green; S1–S4 results recorded; OQ-01 resolved.

### Phase 1: Core MVP (manual recipes, deployed)

- [ ] **P1.1** Schema and migrations (§7.2), the migration runner, and `grimoire migrate`.
- [ ] **P1.2** Store layer and the recipe service, with validation, slugs, revisions on save, and soft delete with trash purge.
- [ ] **P1.3** Ingredient parser and formatter (§9.1), with table tests and a fuzz target.
- [ ] **P1.4** Auth middleware (§12) with trust-boundary tests; automatic user creation.
- [ ] **P1.5** Pages: home, recipe view, editor (§8.1), create/edit/delete, trash, history/restore.
- [ ] **P1.6** Categories (hierarchical) and tags; browse pages.
- [ ] **P1.7** FTS5 search with filters (§10).
- [ ] **P1.8** Image upload and imagestore (§8.5).
- [ ] **P1.9** Serving scaler (§9.2) on the recipe view.
- [ ] **P1.10** Container image, Kustomize base, NetworkPolicy, compose example.
- [ ] **P1.11** Admin port, backup stream, `backup-pull`, `restore`, CronJob, CI backup/restore test; `docs/operations.md` restore drill.
- [ ] **P1.12** Deploy to the maintainer's cluster using the private overlay (maintainer present).

**Exit:** the maintainer creates, edits, finds, and scales a recipe on a phone against the deployed instance; a restore drill from a real backup succeeds; signed off.

### Phase 2: Legacy collection import

- [ ] **P2.1** Interchange format read/write (`internal/exportfmt`), bundle zip, round-trip tests.
- [ ] **P2.2** `POST /api/v1/import/bundle` with idempotency, conflict policy, dry run, and report.
- [ ] **P2.3** `internal/legacy` reader (docx + txt) and splitter, built on the S2 findings, against the synthetic fixture.
- [ ] **P2.4** Mapping-file classifier and `--emit-tokens`.
- [ ] **P2.5** Ingredient normalization rules and override file.
- [ ] **P2.6** Group detection (`Filling:`, etc.) and flagging rules.
- [ ] **P2.7** Reports (md + csv).
- [ ] **P2.8** Review queue page, "View original" toggle, mark-reviewed.
- [ ] **P2.9** Run against the real document in `local/`. The maintainer completes the mapping file. Dry run, then real import.
- [ ] **P2.10** Export endpoint (`GET /api/v1/export`) and round-trip test against an empty instance.

**Exit:** the full collection is loaded; every recipe is either clean or in the review queue with a reason; the maintainer spot-checks 20 random recipes against the source with no lost content; signed off.

### Phase 3: URL import

- [ ] **P3.1** `internal/safefetch` with the full §15.1 test table.
- [ ] **P3.2** JSON-LD extractor covering every shape in §8.2, with golden fixtures and a fuzz target.
- [ ] **P3.3** Microdata fallback and OpenGraph stub.
- [ ] **P3.4** Field mapping, HTML stripping, durations, image selection.
- [ ] **P3.5** Import page: preview/edit, then save; dedupe by canonical URL; `raw_source` retention.
- [ ] **P3.6** Remote image fetch through safefetch.
- [ ] **P3.7** Scrape metrics and logs; `make test-local` over real captures.

**Exit:** at least 90% of the S3 seed URLs, plus any added during use, that publish schema.org Recipe import with title, ingredients, and steps correct without editing; sites with no structured data produce flagged stubs, not errors; signed off.

### Phase 4: Kitchen features

- [ ] **P4.1** Cook mode screen, with wake lock and fallback.
- [ ] **P4.2** Ingredient check-off, persisted per recipe in `sessionStorage`, with a reset button.
- [ ] **P4.3** Step duration detection and timers (§9.3).
- [ ] **P4.4** Favorites.
- [ ] **P4.5** PWA manifest and service worker; offline cache for recent and favorite recipes, with an availability indicator.
- [ ] **P4.6** Print stylesheet for a single recipe.
- [ ] **P4.7** Playwright E2E at phone and tablet viewports in CI.

**Exit:** the maintainer cooks a full recipe on a phone, with the screen staying on, timers working, and no lost place; the recipe works offline after being viewed once; signed off.

### Phase 5: Capture and polish

- [ ] **P5.1** API tokens (create, list, revoke; hashed storage) in settings.
- [ ] **P5.2** `/capture` page and bookmarklet; CSP behavior verified on the target browsers and documented.
- [ ] **P5.3** `/api/v1/capture` with token auth; iOS Shortcut setup documented in operations, including the SSO path-bypass rule.
- [ ] **P5.4** Admin import page (bundle upload with dry-run report).
- [ ] **P5.5** Hardening pass: rate limit capture/import, review security headers, `govulncheck` clean, README and screenshots (synthetic data only).

**Exit:** a recipe from a site that blocks server fetches is captured from a phone and a laptop; v1.0.0 tagged; signed off.

## 20. Future (not scheduled)

- LLM-assisted extraction (preferably a local model) for pages with no structured data and for messy legacy blocks, always as a reviewed suggestion, never a silent write.
- OCR of photographed handwritten recipe cards into a draft, with the photo kept as the "original."
- Cook log ("made it on …, notes"), ratings.
- Meal plans and shopping lists.
- Unit conversion and ingredient-aware weights.
- An OIDC auth mode; per-user permissions; sharing a read-only collection link with extended family.
- Import adapters for other apps' exports.
- A whole-collection printable cookbook (PDF) for gifting.

## 21. Open questions (defaults apply until answered)

| ID | Question | Default | Blocking? |
|---|---|---|---|
| OQ-01 | UI stack: templ + htmx, or a React/TS SPA embedded in the binary? | **Answered:** templ + htmx (D-17) | — |
| OQ-02 | Who uses it, and can everyone edit? | Household multi-user, shared library, everyone edits; admins from config | No |
| OQ-03 | What sits behind SSO? | Everything except `/api/v1/capture` (token auth) and health endpoints | No |
| OQ-04 | License | **Answered:** Apache-2.0 (D-10) | — |
| OQ-05 | Public repo from day one? | **Answered:** yes, with the §17 boundary (D-10) | — |
| OQ-06 | Ship the legacy converter in the public repo? | Yes, as a generic tool with synthetic fixtures; the real mapping file stays private | No |
| OQ-07 | Deployment host, SSO policy, storage class | See `local/CONTEXT.md`; ask if missing | At P1.12 |
| OQ-08 | Unit conversion in v1? | No | No |
| OQ-09 | Re-import conflict when a recipe was edited after import | Skip and report | No |
| OQ-10 | Off-cluster backup destination | Backup PVC only, plus a documented manual off-cluster copy until decided | No (flag at P1.11) |
| OQ-11 | Final product name | "Kitchen Grimoire" (working) | No |
| OQ-12 | Markdown in notes? | Plain text with line breaks in v1 | No |
| OQ-13 | The legacy collection exists as two editions (an older word-processor file and a newer PDF export) that overlap but differ. How should they combine? | **Answered** (D-13) | — |

## 22. Decision log

| ID | Date | Decision | Status |
|---|---|---|---|
| D-01 | 2026-11-19 | Build rather than adopt an existing recipe manager. The maintainer values the design/build process and an exact fit; a previously self-hosted alternative was weak on small screens. | DECIDED |
| D-02 | 2026-11-19 | Go, a single static binary, minimal runtime image. | DECIDED |
| D-03 | 2026-11-19 | SQLite on a block volume; backups via a scheduled job, not an operator. | PROPOSED (matches the maintainer's other apps) |
| D-04 | 2026-11-19 | schema.org Recipe JSON-LD (plus `x-grimoire`) is the single interchange format; every ingest path converges on one pipeline. | PROPOSED |
| D-05 | 2026-11-19 | Scraping is in-process Go (JSON-LD → Microdata → OpenGraph stub); no Python sidecar, no headless browser, no bot evasion. Blocked sites use browser capture. | PROPOSED |
| D-06 | 2026-11-19 | Raw ingredient text is canonical; parsed fields are derived. Imported originals, scraped JSON-LD, and revisions are always retained. | PROPOSED |
| D-07 | 2026-11-19 | The legacy converter is a separate binary producing a bundle; it is not an in-app feature and is not in the image. | PROPOSED |
| D-08 | 2026-11-19 | Trusted-header auth behind an SSO proxy, restricted to trusted proxy CIDRs; capture uses scoped API tokens. | PROPOSED |
| D-09 | 2026-11-19 | Plan, status, and decisions are kept in git (§18); the agent never pushes without approval. | PROPOSED |
| D-10 | 2026-11-20 | Apache-2.0; public repository from day one; Go module `github.com/runlevel-six/kitchen-grimoire`. Resolves OQ-04 and OQ-05. | DECIDED |
| D-11 | 2026-11-20 | Secret scanning uses betterleaks (gitleaks-compatible CLI and config) instead of gitleaks, in the pre-commit hook, `make check`, and CI. | DECIDED |
| D-12 | 2026-11-20 | The legacy converter reads UTF-8 text only. Word and PDF sources are converted with standard external tools first (S2: no paragraph styles survive conversion, and one source is a PDF). Drops the planned `.docx` reader. | PROPOSED |
| D-13 | 2026-11-20 | Combining the two legacy editions (resolves OQ-13): the newer PDF edition is primary. Every recipe found only in the older edition is imported; none were removed on purpose. Older-only recipes get `needs_review` reason `only_in_older_edition`, since their text is the older wording. A recipe present in both keeps the older text as an earlier revision. Titles that differ only slightly between editions (likely renames) are matched as the same recipe and flagged `title_changed_between_editions`, not imported twice. | DECIDED |
| D-14 | 2026-11-20 | SQLite driver: `modernc.org/sqlite` (BSD-3-Clause; pure Go, bundles SQLite 3.54.x). Verified with `CGO_ENABLED=0`: WAL, FTS5 (`unicode61 remove_diacritics 2`, prefix indexes), the §10 bm25 weights, and `VACUUM INTO` (`grimoire doctor`, `internal/store` tests). Transitive runtime modules, all MIT/BSD: `modernc.org/{libc,mathutil,memory}`, `github.com/dustin/go-humanize`, `github.com/google/uuid`, `github.com/mattn/go-isatty`, `github.com/ncruces/go-strftime`, `github.com/remyoudompheng/bigfft`, `golang.org/x/sys`. `google/uuid` arrives anyway and supports UUIDv7, so it is the default choice for IDs. Alternative considered: `github.com/ncruces/go-sqlite3` (Wasm-based); not needed. | DECIDED (S1 confirmed in CI on amd64 and arm64) |
| D-15 | 2026-11-20 | CI pins every Action by commit SHA. Images are built and tested per architecture on native GitHub runners (`ubuntu-24.04`, `ubuntu-24.04-arm`) instead of under QEMU: size limit, `grimoire doctor` with a read-only root, and `/healthz` readiness. Dev tools (including betterleaks in CI) run via pinned `go run`. | PROPOSED |
| D-16 | 2026-11-20 | templ v0.3.1118 (MIT) for components; the generator runs via `go run` at the version in `go.mod`, so the two cannot drift. Generated `*_templ.go` files are committed so plain `go build` (and the Dockerfile) needs no extra tool; `make generate-check` (part of `make check` and CI) fails if they are stale. htmx 2.0.11 (0BSD) is vendored at `web/static/vendor/`, with checksums in `web/vendor/README.md`. | PROPOSED |
| D-17 | 2026-11-20 | UI stack is templ + htmx with a small hand-written JS module (resolves OQ-01). The maintainer tried the S4 prototype on an iPhone and a Mac and approved it. | DECIDED |
