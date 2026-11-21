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
