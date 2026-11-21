# Kitchen Grimoire documentation

Kitchen Grimoire is a self-hosted recipe library for one household: a
single Go binary with a phone-first web UI, behind your own single sign-on.
It is an heirloom archive first and a recipe app second: original wording,
who a recipe came from, and every earlier version are kept.

The pages follow [Diátaxis](https://diataxis.fr/), which sorts
documentation by what you need right now:

- **Tutorials** are lessons: follow them start to finish to learn by
  doing.
- **How-to guides** are recipes (the other kind): steps for one task you
  already know you want to do.
- **Reference** is the exact facts: every setting, command, route, and
  format, kept accurate to the code.
- **Explanation** is the background: why it works the way it does.

The reading paths below pick out what each kind of reader needs first;
every page is listed once under [All pages](#all-pages).

## Reading paths

### Try it

1. [Your first run](tutorials/first-run.md): run it locally with `make dev`,
   add a recipe, scale it, cook it, find it, and put it on your phone.

### Use it in the kitchen

1. [Add and edit recipes](how-to/add-and-edit-recipes.md): the editor's
   conventions, photos, history, and the trash.
2. [Import a recipe from a link](how-to/import-a-recipe-from-a-link.md).
3. [Use cook mode](how-to/use-cook-mode.md): one step at a time, timers,
   and the screen staying on.
4. [Find recipes](how-to/find-recipes.md): search, browse, and filters.

### Run it for your household

1. [Deploy on Kubernetes behind single sign-on](tutorials/deploy-on-kubernetes.md).
2. [Configuration](reference/configuration.md): every setting.
3. [Make someone an admin](how-to/make-someone-an-admin.md).
4. [Back up and restore](how-to/backup-and-restore.md), then
   [Operate](how-to/operate.md) and [Upgrade](how-to/upgrade.md).

### Bring in a family collection

1. [Import a legacy collection](how-to/import-a-legacy-collection.md):
   convert a long cookbook document into reviewable recipes.
2. [Bulk import and export](how-to/bulk-import-and-export.md): load the
   bundle, safely and repeatably.
3. [Review imported recipes](how-to/review-imported-recipes.md).

### Understand how it's built

1. [How Kitchen Grimoire is built](explanation/architecture.md).
2. [Security model](explanation/security-model.md).
3. [How it preserves what you put in](explanation/data-preservation.md).
4. [Design document](DESIGN.md): goals, requirements, the plan, and the
   decision log. [Status log](status.md): what was built when.

## All pages

| Page | Kind | What it covers |
|---|---|---|
| [Your first run](tutorials/first-run.md) | Tutorial | `make dev`; add, scale, cook, and search a recipe; add it to a phone's home screen |
| [Deploy on Kubernetes behind single sign-on](tutorials/deploy-on-kubernetes.md) | Tutorial | An overlay on `deploy/base`, Authelia forward auth, the trust boundary, the first backup |
| [Add and edit recipes](how-to/add-and-edit-recipes.md) | How-to | Fields, ingredient and step groups, categories and tags, photos, history, trash, favorites |
| [Import a recipe from a link](how-to/import-a-recipe-from-a-link.md) | How-to | URL import, duplicates, and what to do when a site refuses |
| [Use cook mode](how-to/use-cook-mode.md) | How-to | Steps, wake lock, timers, check-offs, offline |
| [Find recipes](how-to/find-recipes.md) | How-to | Search, browse, and filters |
| [Review imported recipes](how-to/review-imported-recipes.md) | How-to | The review queue and what each reason means |
| [Import a legacy collection](how-to/import-a-legacy-collection.md) | How-to | `grimoire-import`: tokens, mapping file, convert, report, bundle |
| [Bulk import and export](how-to/bulk-import-and-export.md) | How-to | The admin API with `curl`: dry run, report, conflicts, moving instances |
| [Make someone an admin](how-to/make-someone-an-admin.md) | How-to | `GRIMOIRE_ADMIN_GROUPS` versus `GRIMOIRE_ADMINS` |
| [Back up and restore](how-to/backup-and-restore.md) | How-to | The backup CronJob, verifying, restore drills, off-cluster copies |
| [Operate](how-to/operate.md) | How-to | Logs, health, metrics, backups, the trash, reindexing |
| [Upgrade](how-to/upgrade.md) | How-to | Image tags, migrations at startup, rolling back |
| [Configuration](reference/configuration.md) | Reference | Every `GRIMOIRE_*` environment variable |
| [Command line](reference/cli.md) | Reference | `grimoire` and `grimoire-import` commands and flags; Make targets |
| [HTTP routes](reference/http-routes.md) | Reference | Every route on the app and admin ports, and who may call it |
| [Search syntax](reference/search-syntax.md) | Reference | Words, prefixes, phrases, exclusions, fields, filters |
| [Interchange format](reference/interchange-format.md) | Reference | The JSON-LD + `x-grimoire` bundle, import keys, the import report |
| [Metrics](reference/metrics.md) | Reference | Every Prometheus metric and its labels |
| [How Kitchen Grimoire is built](explanation/architecture.md) | Explanation | One binary, SQLite and FTS5, the image store, one ingest pipeline, templ + htmx |
| [Security model](explanation/security-model.md) | Explanation | Trusted-header auth, NetworkPolicy, the SSRF guard, CSP, uploads, the admin port |
| [How it preserves what you put in](explanation/data-preservation.md) | Explanation | Raw text, revisions, original text, raw source, editions, the trash |
| [Design document](DESIGN.md) | Explanation | The design, plan, and decision log |
| [Status log](status.md) | Record | Session-by-session log of what was built |

## Not yet available

Planned for Phase 5 and not described as working anywhere in these pages:
browser capture of recipes from sites that block servers (a bookmarklet and
an iOS Shortcut for the share sheet), per-user API tokens, an admin import
page, and a settings page. See [DESIGN.md §19](DESIGN.md#19-plan).
