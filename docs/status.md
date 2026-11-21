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
