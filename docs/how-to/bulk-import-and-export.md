# How to bulk import and export recipes

The admin API moves the whole library in and out as a **bundle**: a zip of
schema.org JSON-LD recipes plus their photos (see
[Interchange format](../reference/interchange-format.md)). Use it to:

- load the output of the [legacy converter](import-a-legacy-collection.md);
- take a portable copy of every recipe, readable by other software;
- move recipes from one Kitchen Grimoire instance to another.

Both endpoints are **admin only** ([Make someone an admin](make-someone-an-admin.md)).
The easiest way to import is **Settings → Import a bundle** in the app
(admins only), with a dry run first. The `curl` commands below do the same
from a terminal.

> An export is not a backup. It holds live recipes only: no trash, no
> revision history, no users or favorites. For disaster recovery use
> [Back up and restore](backup-and-restore.md).

## Authenticate your requests

The API is on the app port, behind your SSO proxy, like the pages. Capture
tokens only work for `/api/v1/capture`, so `curl` has to present your SSO
session:

1. Sign in to Kitchen Grimoire in a browser as an admin.
2. In the browser's developer tools, copy the value of your SSO session
   cookie for the site (for Authelia it is named `authelia_session` by
   default).
3. Put it in a variable for the commands below:

   ```sh
   export KG=https://recipes.example.com
   export KG_COOKIE='authelia_session=PASTE-VALUE-HERE'
   ```

Treat that cookie like a password, and don't leave it in your shell
history on a shared machine.

To try a bundle without touching the real library, run a local instance
with `make dev` instead (see [Your first run](../tutorials/first-run.md)):
in dev mode you are an admin with no sign-in, so the commands below work
with `KG=http://localhost:8080` and no cookie.

## Export the library

```sh
curl -fsS -b "$KG_COOKIE" -OJ "$KG/api/v1/export"
```

`-OJ` saves it under the server's name,
`kitchen-grimoire-export-YYYYMMDD.zip`. It contains every recipe not in the
trash, with its photos. Exporting an unchanged library twice gives
byte-identical files, so you can compare exports with `cmp` or a checksum.

To look inside:

```sh
unzip -l kitchen-grimoire-export-*.zip
unzip -p kitchen-grimoire-export-*.zip recipes.jsonl | head -1 | python3 -m json.tool
```

## Import a bundle

Always do a **dry run** first. It goes through exactly the same checks
against the live database and returns the same report, but writes nothing:

```sh
curl -fsS -b "$KG_COOKIE" \
  -H 'Content-Type: application/zip' \
  --data-binary @bundle.zip \
  "$KG/api/v1/import/bundle?dry_run=1"
```

The `Content-Type: application/zip` header is required (`415` otherwise),
and the body is the raw zip (`--data-binary`, not a form upload). The
response is a JSON report:

```json
{
  "dryRun": true,
  "counts": {
    "created": 412,
    "updated": 0,
    "skipped_conflict": 0,
    "invalid": 1,
    "unchanged": 0
  },
  "items": [
    {
      "index": 17,
      "importKey": "legacy:example-family-cookbook:0018:example-cheese-ball",
      "title": "EXAMPLE CHEESE BALL",
      "outcome": "invalid",
      "reasons": ["prepMin: is out of range"]
    }
  ]
}
```

To see just the problems:

```sh
curl -fsS -b "$KG_COOKIE" -H 'Content-Type: application/zip' \
  --data-binary @bundle.zip "$KG/api/v1/import/bundle?dry_run=1" \
  | jq '.counts, [.items[] | select(.outcome == "invalid" or .outcome == "skipped_conflict" or .warnings)]'
```

When the dry run looks right, run it for real by dropping `?dry_run=1`:

```sh
curl -fsS -b "$KG_COOKIE" \
  -H 'Content-Type: application/zip' \
  --data-binary @bundle.zip \
  "$KG/api/v1/import/bundle" > import-report.json
```

Keep the report: it lists the slug of every recipe created or updated.

## What happens to each recipe

Recipes are matched by their import key (or, without one, by canonical
URL), so importing is **safe to repeat**:

| Outcome | Meaning |
|---|---|
| `created` | New recipe. |
| `updated` | Already imported and not edited since: updated in place, as a new revision. |
| `unchanged` | Already identical; nothing written. |
| `skipped_conflict` | Someone edited it since it was imported, it's in the trash, or it was never imported: **left alone**. The reason says which. |
| `invalid` | Bad JSON, not a recipe, fails validation, or a repeated key in the same bundle. |

So you can fix the legacy mapping file, convert again, and re-import: the
recipes nobody has touched are refreshed, and the ones someone has already
corrected by hand are kept. To deliberately replace an edited recipe, move
it to the trash, empty the trash (admin), and import again.

Flagged recipes (`needsReview`) land in the
[review queue](review-imported-recipes.md).

## Move to another instance

1. Export from the old instance.
2. On the new one, dry-run the import, then import.

Recipes keep their created and updated dates, import keys, original text,
and review flags. Photos are decoded and re-encoded on import, so their
files (and names) differ from the source; they look the same. Revision
history, the trash, favorites, and users don't travel in a bundle: to move
an instance with all of that, restore a [backup](backup-and-restore.md)
instead.

## Limits and troubleshooting

| Problem | Fix |
|---|---|
| `403 {"error": "admin only"}` | You aren't an admin. See [Make someone an admin](make-someone-an-admin.md). |
| `401` or an HTML sign-in page | The session cookie is missing or expired, or the proxy redirected `curl`. Copy a fresh cookie. |
| `413` from the app | Bundles are capped at 512 MiB. Split the bundle. |
| `413` from the proxy | The base Ingress allows 520 MB bodies (`nginx.ingress.kubernetes.io/proxy-body-size: 520m`). If your overlay or proxy sets a lower limit, raise it before importing a large bundle. |
| `400` with a message | The zip isn't a valid bundle: wrong file names, a path outside the bundle, too many entries, or a line over 8 MiB. The message says which. |
| An item has a `warnings` entry about an image | The image was missing or unreadable; the recipe was imported without it. |

Bundle uploads and exports aren't held to the server's usual 60-second
read and write timeouts, so large ones over a slow connection still finish.
Your proxy may have its own timeout (for ingress-nginx,
`proxy-read-timeout`); raise it if a very large import is cut off.
