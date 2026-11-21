# Command-line reference

Kitchen Grimoire has two programs:

- **`grimoire`**, the server. It is the only program in the container
  image (`/usr/local/bin/grimoire`, entrypoint; the default command is
  `serve`).
- **`grimoire-import`**, the legacy collection converter. It is a separate
  binary that is never in the image. Build it with `make build-import`.

Flags take one or two dashes (`-status` and `--status` are the same), as
usual for Go programs. `-h` on any subcommand prints its flags.

## `grimoire`

```text
grimoire serve
grimoire migrate [--status] [--reindex] [--data-dir DIR]
grimoire backup-pull --from URL --out DIR [--keep N] [--timeout D]
grimoire restore --from FILE [--data-dir DIR] [--force]
grimoire doctor [DIR]
grimoire version [--json]
grimoire help
```

Exit status: `0` on success, `1` when the command fails, `2` for a usage
error (unknown command, bad or missing flags). With no arguments,
`grimoire` prints the usage and exits with `2`.

### `grimoire serve`

Runs the web server. It takes no flags; everything comes from `GRIMOIRE_*`
environment variables (see [Configuration](configuration.md)).

On startup it:

1. validates the configuration and exits with `1`, listing every problem,
   if anything is wrong;
2. opens `grimoire.db` in the data directory and applies any pending
   migrations (it stops if the database was migrated by a newer version,
   or if an applied migration was changed);
3. in dev mode with an empty database, adds two invented sample recipes;
4. listens on the app port (`GRIMOIRE_LISTEN`) and the admin port
   (`GRIMOIRE_ADMIN_LISTEN`).

While running, it purges trash older than `GRIMOIRE_TRASH_DAYS` at startup
and every 6 hours, and every 6 hours removes image files that no recipe
references any more (files younger than an hour are kept).

`SIGINT` or `SIGTERM` starts a graceful shutdown: in-flight requests get up
to 20 seconds to finish.

### `grimoire migrate`

Applies pending database migrations, or lists them. It needs only the data
directory, so it works without the rest of the server configuration.

| Flag | Default | Meaning |
|---|---|---|
| `--data-dir DIR` | `$GRIMOIRE_DATA_DIR`, else `/data` | Data directory holding `grimoire.db`. The database is created if it doesn't exist. |
| `--status` | off | List every migration and whether it is applied, and change nothing. |
| `--reindex` | off | After migrating, rebuild the full-text search index from the stored recipes. |

```console
$ grimoire migrate --status --data-dir ./data
0001_init                     applied 2026-11-21T04:15:58.031Z
0002_search                   applied 2026-11-21T04:15:58.031Z
$ grimoire migrate --reindex --data-dir ./data
database is up to date
reindexed 2 recipes
```

`serve` runs the same migrations at startup, so you rarely need this
command except for `--status` and `--reindex`.

### `grimoire backup-pull`

Downloads a backup from a running server's admin port
(`GET /admin/backup`), verifies every file against the backup's manifest,
gives it its final name, and deletes the oldest backups beyond `--keep`.

| Flag | Default | Meaning |
|---|---|---|
| `--from URL` | none (required) | Admin base URL of the server, e.g. `http://grimoire-admin:9090`. |
| `--out DIR` | none (required) | Directory to write the backup into. Created if missing. |
| `--keep N` | `14` | Number of `grimoire-*.tar.gz` backups to keep in `--out`; older ones are deleted. At least 1. |
| `--timeout D` | `30m` | Give up after this long (a Go duration). |

```console
$ grimoire backup-pull --from http://grimoire-admin:9090 --out /backup
wrote /backup/grimoire-20261121T041607Z.tar.gz (schema 2, 2 recipes, 0 images, 1 files)
```

Backups are named `grimoire-YYYYMMDDTHHMMSSZ.tar.gz` (UTC). While a
download runs it is a hidden `.partial-*` file, removed if anything fails,
so a failed run never leaves something that looks like a good backup. See
[Back up and restore](../how-to/backup-and-restore.md).

### `grimoire restore`

Verifies a backup and restores it into a data directory. **Stop the server
first.**

| Flag | Default | Meaning |
|---|---|---|
| `--from FILE` | none (required) | The backup file (`grimoire-*.tar.gz`). |
| `--data-dir DIR` | `$GRIMOIRE_DATA_DIR`, else `/data` | Data directory to restore into. |
| `--force` | off | Replace the database and images in a data directory that isn't empty. |

Without `--force`, restore refuses a data directory that holds anything
other than `lost+found`. The archive is unpacked and checked (sizes,
SHA-256 checksums, no missing or extra files, SQLite integrity check) in a
staging directory first; if anything fails, the data directory is left as
it was.

### `grimoire doctor`

Checks that the SQLite features the server relies on (WAL, FTS5 with the
search tokenizer and prefix indexes, bm25 ranking, `VACUUM INTO`) work in
this environment, using a scratch database in `DIR` (default: the OS
temporary directory). It prints the result as JSON and exits with `1` if any
check fails. CI runs it inside the image; the Compose example uses it as a
health check.

### `grimoire version`

Prints the version, commit, and Go version. `--json` prints the same as a
JSON object:

```console
$ grimoire version --json
{"version":"v0.4.0","commit":"a059a8e...","go":"go1.27.4"}
```

`--version` and `-v` are aliases for `version`.

## `grimoire-import`

```text
grimoire-import tokens  --in FILE [--format tabbed|pdftext|auto] [--map FILE]
grimoire-import convert --in FILE [--format ...] --map FILE [--normalize FILE]
                        --collection NAME [--older FILE --older-format ...] --out DIR
grimoire-import bundle  --drafts DIR/drafts.jsonl --out FILE.zip
grimoire-import help
```

Exit status: `0` on success (including `-h`), `1` when the command fails,
`2` for an unknown command or no arguments. The whole workflow is in
[Import a legacy collection](../how-to/import-a-legacy-collection.md).

### `grimoire-import tokens`

Lists every distinct title-line token with its count and current class
(`UNKNOWN` when the map lacks it), as CSV (`token,count,class,canonical`)
on standard output. `--emit-tokens` is an alias for `tokens`.

| Flag | Default | Meaning |
|---|---|---|
| `--in FILE` | none (required) | Source text file (UTF-8). |
| `--format F` | `auto` | `tabbed` (word-processor text with tabs between title fields), `pdftext` (PDF text with page markers), or `auto`. |
| `--map FILE` | none | Attribution mapping CSV, to show the classes it already knows. |

### `grimoire-import convert`

Splits the document into recipes, classifies the title-line fields with
the mapping file, and writes `drafts.jsonl`, `report.md`, and `report.csv`
into `--out`.

| Flag | Default | Meaning |
|---|---|---|
| `--in FILE` | none (required) | Source text file (UTF-8); the newer edition when `--older` is given. |
| `--format F` | `auto` | Format of `--in`, as for `tokens`. |
| `--map FILE` | none (required) | Attribution mapping CSV (`token,class,canonical,comment`). |
| `--normalize FILE` | none | Ingredient-name override CSV (`from,to`). |
| `--collection NAME` | none (required) | Collection name, e.g. `"Example Family Cookbook"`. Also used in import keys. |
| `--older FILE` | none | An older edition of the same collection, to match against (D-13). |
| `--older-format F` | `auto` | Format of `--older`. |
| `--out DIR` | none (required) | Output directory. |

It prints `N recipes (M need review) → DIR`, and a warning if titles moved
between ordinals since the previous run in the same directory.

### `grimoire-import bundle`

Packs `drafts.jsonl` into a bundle zip for
[`POST /api/v1/import/bundle`](http-routes.md#api). Older editions matched
by `convert` travel as `x-grimoire.olderEdition` and become revision 1 on
import.

| Flag | Default | Meaning |
|---|---|---|
| `--drafts FILE` | none (required) | The `drafts.jsonl` written by `convert`. |
| `--out FILE` | none (required) | The bundle zip to write. |

There is no `push` subcommand: upload the bundle with `curl` as shown in
[Bulk import and export](../how-to/bulk-import-and-export.md).

## Make targets

For working on the code:

| Target | What it does |
|---|---|
| `make dev` | Generate templates, build, and run the server with auth off on `:8080` (data in `local/data`). Override the address with `DEV_LISTEN=:8081`. |
| `make build` | Build `bin/grimoire`. |
| `make build-import` | Build `bin/grimoire-import`. |
| `make test` | `go test ./...`. |
| `make check` | Everything CI's check job runs: generated-code check, gofmt, vet, staticcheck, tests, govulncheck, license check, private-data check, betterleaks. |
| `make generate` | Regenerate the templ code (`*_templ.go`, committed). |
| `make image` | Build the container image `kitchen-grimoire:dev` with Docker. |
| `make test-local` | Golden tests over private captures in `local/fixtures/`, if present. |
| `make hooks` | Use the repository's git hooks (`.githooks/`). |
| `make clean` | Remove `bin/` and `dist/`. |
