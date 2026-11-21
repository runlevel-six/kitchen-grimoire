# How Kitchen Grimoire preserves what you put in

Kitchen Grimoire is an **heirloom archive first and a recipe app second**.
A family recipe collection carries more than quantities: a grandmother's
wording, who gave a recipe to whom, a note in the margin, the version from
an older printing. Tidying recipes up for search and scaling inevitably
interprets them, and interpretations can be wrong. So the rule throughout
is: **nothing the household puts in is ever silently lost.** Structure is
added on top of the original, never in place of it.

This page explains the mechanisms. The decisions are D-06, D-13, and D-21
in the [design document](../DESIGN.md#22-decision-log).

## Raw first, parsed second

Every ingredient is stored as the line someone wrote: `1 8-oz pkg cream
cheese, softened`. The parser then derives quantity, size, unit, item, and
note from it, but those fields are **derived**: they can be thrown away and
recomputed, and a better parser in a later version can re-read every line.
A line the parser doesn't understand isn't forced into a shape; it's kept
as written and marked "not scaled" when scaling. Bulk exports write only
the raw lines and re-parse them on import.

Steps, notes, and descriptions are stored as plain text, exactly as entered
(apart from trimming whitespace).

## Provenance has its own fields

Where a recipe came from is recorded in proper fields, not buried in the
notes, so it survives edits and can be searched and browsed:

| Field | Example | Meaning |
|---|---|---|
| From (`contributed_by`) | Aunt Example | The person who gave the recipe to the household. |
| Collection | Example Family Cookbook | Where it lives, such as a family cookbook. |
| Author as published | Example Test Kitchen | The author named by the source. |
| Source link, site | `https://...` | Where it was imported from. |

## Every save is a revision

Each save stores a complete snapshot of the recipe as a new numbered
revision, with who saved it and when. **History** lists them, any can be
viewed, and **Restore this version** brings one back by saving it as a new
revision, so restoring is itself undoable.

Two people editing at once can't overwrite each other by accident: a save
based on an out-of-date revision is refused, and the editor keeps the
second person's text so they can compare and save again on purpose.

Marking a recipe as reviewed changes only a flag, not content, so it
doesn't add a revision.

## Originals are kept beside the structure

Imports keep their source as well as the result:

- **`original_text`**: for recipes from the legacy converter, the verbatim
  block from the source document, title line and all. It's shown beside the
  structured recipe while the recipe is in the review queue, and behind
  **View original** afterwards. Editing the recipe never changes it.
- **`raw_source`**: for recipes imported from a link, the schema.org
  `Recipe` object exactly as the site published it. It isn't shown in the
  app, but it is kept (and exported) so a future version can re-read it
  with a better extractor.

## Editions: older wording becomes history

A legacy collection may exist in more than one edition, with recipes added,
removed, or reworded between them (D-13). The converter matches recipes
across editions, including slightly renamed ones. When a recipe is in both,
the **older wording is stored as revision 1** and the newer one as revision
2, so the change between printings is visible in **History**. A recipe that
appears only in the older edition is imported anyway and flagged
`only_in_older_edition`; a likely rename is flagged
`title_changed_between_editions` rather than imported twice.

## Flag, don't drop

When an import isn't sure, it saves the recipe and **flags it for review**
with a reason (`no_ingredients`, `unclassified_token:<t>`,
`multi_column_ingredients`, ...), rather than skipping it or guessing
quietly. A table with several batch sizes is kept as written rather than
parsed into one quantity. The review queue is where a person decides.
See [Review imported recipes](../how-to/review-imported-recipes.md).

## Re-imports respect human edits

Bulk imports are idempotent: each recipe carries an import key, and
importing the same bundle again changes nothing. If a newer bundle has a
different version of a recipe, it updates the recipe in place (as a new
revision) **only if nobody has edited it since it was imported**. A recipe
someone has corrected by hand is skipped and reported as a conflict, so
running the converter again can never undo a person's work.

## Deleting is slow on purpose

Deleting a recipe moves it to the **trash**, with an **Undo** right away.
It stays restorable for 30 days (`GRIMOIRE_TRASH_DAYS`), then is purged
automatically along with its history. Emptying the whole trash early is
the one action that asks for confirmation, and only admins can do it.
Photos are deleted from disk only when no recipe uses them any more.

## Backups and exports

Two copies leave the server, for different purposes:

- **Backups** are the whole data directory: a consistent snapshot of the
  database (every revision, the trash, users, favorites) and every photo,
  with a manifest of checksums. They restore an instance exactly. See
  [Back up and restore](../how-to/backup-and-restore.md).
- **Exports** are the live recipes in an open format, schema.org JSON-LD
  plus photos, readable by other software and re-importable without loss
  of recipe content: groups, original text, the scraped source, provenance,
  and review flags all travel. They're the "you can always leave" guarantee.
  See [Interchange format](../reference/interchange-format.md).

A backup stored on the same storage as the data doesn't survive losing
that storage, so copy backups off the server too.
