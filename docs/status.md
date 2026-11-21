# Status log

Append-only session log, newest at the bottom (docs/DESIGN.md §18.2).
No private data here (§17); specifics go in `local/notes.md`.

## 2026-11-20 — session 1 (phase P0)
- Done: P0.1, P0.2
- Decisions:
  - Dev tools (staticcheck, govulncheck, go-licenses) are pinned in the Makefile and run with `go run tool@version`, so they never enter `go.mod`.
  - `go.mod` pins `toolchain go1.27.4`: govulncheck flagged GO-2026-6859 (`net/http`) in go1.27.3.
  - Module path is `kitchen-grimoire` (per §1) until the public repo location is confirmed; `product.RepoURL` is empty until then, so the User-Agent omits the URL.
  - Git hooks live in `.githooks/` (`make hooks`); `check-private.sh` matches denylist terms case-insensitively as whole words, to avoid false positives on short terms.
- Defaults used: OQ-04 (Apache-2.0), OQ-05 (public from day one), OQ-11 (working name)
- New questions: none
- Blockers: S2 needs the original `.docx` of the legacy collection (only a PDF export is present locally). gitleaks is not installed locally; the hook skips it with a warning.
- Next: P0.3 (CI), S1 (SQLite driver)

## 2026-11-20 — session 2 (phase P0)
- Done: S2; maintainer confirmations applied (license, NOTICE, repo location: D-10); betterleaks replaces gitleaks (D-11); DESIGN.md refers to itself by its real file name.
- S2 conclusion: the legacy collection is available as two editions (an older Word 97–2003 `.doc` and a newer PDF export). Neither has paragraph styles that survive text conversion, so splitting is **heuristic** (§8.4, updated). Title fields are tab-separated in the `.doc` text and collapsed to single spaces in the PDF text; the mapping file's tokens recover the PDF fields almost completely. No page headers, footers, or numbers. Recipes can span page breaks. Some recipes use tab-aligned multi-column ingredient tables, which must be flagged. Details in `local/notes.md`.
- Decisions: D-12 (converter input is UTF-8 text; external conversion; `scripts/pdf2txt.js` added)
- Defaults used: none
- New questions: OQ-13 (how to combine the two editions)
- Blockers: none
- Next: P0.3 (CI), S1 (SQLite driver)

## 2026-11-20 — session 2, addendum
- Decisions: D-13 (maintainer answered OQ-13: the newer PDF edition is primary; every older-only recipe is kept and imported; likely renames are matched, not duplicated)
- Next: P0.3 (CI), S1 (SQLite driver)

## 2026-11-20 — session 3 (phase P0)
- Done: P0.3 (CI workflow, Dockerfile, `make image`); S1 verified locally, pending CI
- S1 so far: `modernc.org/sqlite` builds with `CGO_ENABLED=0` for darwin/arm64, linux/amd64, and linux/arm64. `grimoire doctor` confirms WAL, FTS5 with the §10 tokenizer and prefix indexes, bm25 weights, and `VACUUM INTO` on macOS. The in-image check on both architectures runs in CI (`image` job), and no container runtime is available locally. Tick S1 after the first green CI run.
- Decisions: D-14 (SQLite driver, PROPOSED until CI), D-15 (CI layout)
- Defaults used: none
- New questions: none
- Blockers: S1 completion needs a push so CI runs (maintainer approval required, §18.4)
- Next: S3 (scrape coverage; needs `local/fixtures/urls.txt`), S4 (UI prototype)

## 2026-11-20 — session 3, addendum
- Done: S1. The first CI run on `phase-0-bootstrap` passed all jobs. In the distroless image on native amd64 and arm64 runners, `grimoire doctor` reports SQLite 3.54.1 with WAL, FTS5, bm25, and `VACUUM INTO` working under a read-only root, and `/healthz` answers. Image sizes: ~13.5 MB (amd64) and ~12.9 MB (arm64), against a 40 MB budget.
- Decisions: D-14 now DECIDED.
- Next: S3 (needs `local/fixtures/urls.txt`), S4 (UI prototype)

## 2026-11-20 — session 4 (phase P0)
- Done: S4 prototype built; **awaiting maintainer sign-off on OQ-01** (S4 stays unticked until then). S3 task reworded: seed list from widely used recipe sites, growing with use (maintainer answer).
- S4 prototype (`make dev`, then open `/` on a phone on the same network):
  - Recipe view: provenance line, times, a scaler with ½×/1×/2×/3× (htmx swap with the URL kept in sync; a plain GET form without JS), "not scaled" markers on unparsed lines, ingredient check-off, step numbering continued across groups, notes, "View original", two columns from 900 px.
  - Cook mode: one step at a time; Back/Next links that work without JS; with JS, instant step changes, swipe, arrow keys, Screen Wake Lock with an indicator and a fallback message, tap-to-start timers on detected durations (target-time based, kept across reloads), and an ingredients drawer.
  - Light/dark from the system setting; no inline scripts, styles, or handlers (tested); CSP and security headers on every response.
  - Verified in WebKit (Playwright) at 375 px and on an iPad landscape viewport, including the no-JS paths. The CSP console messages seen during automated screenshots come from Playwright's own screenshot stylesheet, not the app.
  - Reusable pieces kept from the prototype: quantity formatting and scaling with unit plurals (`internal/ingredient`), step-duration detection (`internal/recipe`), asset cache-busting, security headers.
- Bug found and fixed while testing: `cook.js` read the timer store before its key constant was initialised, so timers vanished on reload.
- Decisions: D-16 (templ generated code committed; htmx vendored)
- Defaults used: OQ-01 (templ + htmx), pending sign-off
- New questions: none
- Blockers: S4 sign-off (OQ-01); S3 can proceed
- Next: S3 (seed URL list, scrape probe)

## 2026-11-20 — session 4, addendum
- Done: S4. The maintainer tested the prototype on an iPhone and a Mac and approved the stack; OQ-01 is answered (D-17: templ + htmx).
- Next: S3 (the last Phase 0 task), then the Phase 0 exit review

## 2026-11-20 — session 5 (phase P0)
- Done: S3. `cmd/scrapeprobe` (throwaway, not in the image; remove at P3.7) was run against a seed list of 42 recipe pages from 32 widely used sites, kept in `local/fixtures/urls.txt`.
- S3 results:
  - **Structured data:** 36 pages were reachable with an honest User-Agent, and 35 of them (97%) publish a schema.org `Recipe` in JSON-LD. Every one of those has a title, ingredients, instructions, an image, at least one time, a yield, and an author.
  - **Where the Recipe node sits:** `@graph` (16), top-level arrays (9), and top-level objects (10), so all three shapes in §8.2 are needed. Instructions appear as `HowToStep` lists (28), mixed lists (4), plain strings (2), and `HowToSection` (1).
  - **Fallbacks:** one site publishes only Microdata, which confirms the fallback (P3.3) is worth building. One page had invalid JSON-LD (raw newlines inside strings) that a lenient repair recovers; §8.2 is updated.
  - **Blocking:** 6 fetches were refused (403/429) across 5 sites. Two of those sites had answered 200 minutes earlier, so blocking is intermittent and reacts to repeat fetches. This confirms the browser-capture path (Phase 5) and the no-retry rule (§8.2).
- Decisions: none new. `golang.org/x/net` (already in the dependency budget) is added for HTML parsing.
- Defaults used: none
- New questions: none
- Blockers: none
- **Phase 0 exit criteria are met:** CI green, S1–S4 recorded, OQ-01 resolved. Waiting on maintainer sign-off (`Signed off: P0`).
- Next: Phase 1 (P1.1 schema and migrations) after sign-off

## 2026-11-20 — Phase 0 sign-off
Signed off: P0 (maintainer, given in session; recorded by the agent)

## 2026-11-20 — session 6 (phase P1)
- Branch: `phase-1-core` (from `main` after the Phase 0 merge, PR #1)
- Done: P1.1. Schema in `internal/store/migrations/0001_init.sql`; migration runner (`store.Migrate`, `store.Status`) with checksums, refusal of edited migrations and of newer databases, and per-migration transactions; `grimoire migrate [--status] [--data-dir]`; `serve` migrates at startup; `/readyz` checks the database. Tests cover the runner's rules and the schema's constraints (foreign keys, cascades, CHECKs, STRICT types, one primary image, category paths).
- Container: the image now ships a nonroot-owned `/data`, so a fresh named volume is writable. CI now probes `/readyz` and runs `migrate --status` against the volume.
- Decisions: D-18 (schema details and migration rules)
- Defaults used: none
- New questions: none
- Blockers: none
- Next: P1.2 (store layer and recipe service)

## 2026-11-20 — session 7 (phase P1, unattended run)
- Done on `phase-1-core` (pushed, not yet merged): P1.2 recipe service and store (revisions, optimistic concurrency, trash), P1.3 ingredient parser (fuzzed), P1.4 trusted-header auth with logging, recovery, and CSRF, P1.5 store-backed pages (home, view, editor, trash, history and restore), P1.6 browse facets, P1.7 FTS5 search (migration 0002, safe query builder, filters, `migrate --reindex`), P1.8 photos (image store, upload, display, GC), P1.9 (scaler, done in S4).
- Decisions: D-19 (hand-written queries, IMMEDIATE transactions, optimistic concurrency), D-20 (golang.org/x/text). Both PROPOSED.
- Deferred: editor draft autosave (htmx).
- In progress when the usage limit was reached, in agent worktrees under `.claude/worktrees/` (unmerged, possibly incomplete): P1.11 backup, restore, and metrics; P3.1 safefetch; P3.2–P3.4 scrape; logo, icons, and PWA manifest.
- New maintainer requests to carry forward: a logo (app and README header), Diátaxis docs, the app as a PWA, and Authelia access for both the admins and family groups once deployed (admin role from a groups header).
- Next: merge the agent branches, then P1.10 (container and Kustomize), P1.11, and P1.12 (deploy to the cluster with Authelia).

## 2026-11-20 — session 7 (continued): Phase 1 merged and deployed
- Phase 1 merged to `main` (PR #2) and tagged `v0.1.0`. CI published `ghcr.io/runlevel-six/kitchen-grimoire:0.1.0` (public, multi-arch).
- P1.12: deployed to the maintainer's cluster from the private overlay (`local/deploy/overlays/`, gitignored), in its own namespace. Verified:
  - migrations applied at startup;
  - anonymous requests redirect to SSO;
  - the web manifest is reachable for the PWA;
  - a manual backup job wrote a verified archive through the admin port;
  - a non-proxy pod sending spoofed identity headers cannot connect (NetworkPolicy).
- SSO: added an access rule for the app host that admits the family and admins groups (one factor), with a path-limited health-check bypass. Before applying, the new config was validated with Authelia's own `validate-config` against the live one, and afterwards `access-control check-policy` confirmed family and admins are admitted and other groups denied. Admins inside the app come from `GRIMOIRE_ADMIN_GROUPS=admins` (the `Remote-Groups` header). Forward-auth hosts returned 500 for about 15 seconds while the SSO pod restarted, then recovered.
- Merged from parallel agents: P3.2–P3.4 `internal/scrape` (JSON-LD, Microdata, OpenGraph; golden fixtures; fuzzed).
- Not done for the Phase 1 exit (needs the maintainer): create, edit, find, and scale a recipe on a phone against the deployed instance; a restore drill from a real backup; sign-off.
- Next: P3.5–P3.7 (import page), Phase 2 converter (parallel agent), Phase 4 (PWA service worker, cook mode), Diátaxis docs.

## 2026-11-20 — session 8 (phase P2, agent branch `p2-bundle`)
- Done: P2.1 `internal/exportfmt` (JSON-LD ↔ recipe mapping, deterministic bundle writer, hardened bundle reader with a fuzz target), P2.2 `internal/ingest` bulk importer and `POST /api/v1/import/bundle` (admin; idempotency keys, update in place, skip-and-report conflicts, unchanged detection, dry run, JSON report), P2.10 `GET /api/v1/export` (admin) and the §16 round-trip test (A → B → C, re-imports are no-ops, edits conflict).
- Store/service additions: `CreateImported`/`UpdateImported` (the only paths that set `import_revision`), image rows written with the recipe in one transaction, `GetRecipeByCanonicalURL`, `LiveRecipeIDs`; `imagestore.Process` (re-encode without writing) and `Lookup`; `scrape.MapObject` for bundles from other apps.
- Decisions: D-21 (x-grimoire additions incl. `stepGroups`, `recipe:<id>` keys, unchanged outcome, image re-encoding and round-trip normalization, dry-run design). PROPOSED.
- Defaults used: OQ-09 (skip and report).
- New questions: whether to adopt canonical JPEG/PNG bytes from bundles without re-encoding, to avoid generation loss across instance migrations (D-21 point 5).
- Blockers: none
- Next: P2.3 onward (legacy converter), P5.4 admin import page on top of this API.
