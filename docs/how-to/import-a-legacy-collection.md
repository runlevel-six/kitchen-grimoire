# How to import a legacy recipe collection

This guide takes a family cookbook kept as one long word-processor
document (or a PDF of one) and turns it into recipe drafts you can review
and load into Kitchen Grimoire. The design behind it is in
[DESIGN.md §8.4](../DESIGN.md#84-bulk-import-and-the-legacy-converter);
decisions D-07, D-12, and D-13 explain why it works this way.

The converter, `grimoire-import`, is a separate program. It is not part of
the server or its container image.

It expects a **line-structured** document: each recipe starts with a title
line (an ALL-CAPS title followed by a category and attribution fields,
such as a subcategory, who contributed it, or a tag), then ingredient lines,
then instruction paragraphs.

> Keep the real document, its mapping file, and the converter output out of
> the repository: put them under `local/` (gitignored). The files in
> `testdata/legacy/` are an invented sample you can practise on.

## 1. Build the converter

```sh
make build-import        # writes bin/grimoire-import
```

## 2. Convert the source to UTF-8 text

The converter reads UTF-8 text only.

- **Word (`.doc`, `.docx`, `.rtf`)** on macOS:

  ```sh
  textutil -convert txt -encoding UTF-8 -output local/import/cookbook.txt local/import/cookbook.doc
  ```

  Elsewhere: `soffice --headless --convert-to txt:Text local/import/cookbook.doc`.
  This keeps the tabs between title-line fields and puts each paragraph on
  one line (the `tabbed` format).

- **PDF** on macOS:

  ```sh
  osascript -l JavaScript scripts/pdf2txt.js "$PWD/local/import/cookbook.pdf" "$PWD/local/import/cookbook-pdf.txt"
  ```

  Elsewhere: `pdftotext -layout`. PDF text has runs of spaces collapsed,
  paragraphs wrapped at the page width, and a form feed plus `[[PAGE n]]`
  line at each page (the `pdftext` format). The converter ignores the page
  markers and rejoins wrapped paragraphs.

The format is detected automatically (page markers mean `pdftext`, tabs
mean `tabbed`); pass `--format` to override.

## 3. List the title-line tokens

```sh
bin/grimoire-import tokens --in local/import/cookbook.txt > local/import/tokens.csv
```

This prints every distinct field from the title lines with its count, as
CSV: `token,count,class,canonical`. Without a map every class is
`UNKNOWN`. In PDF text the fields have no separators, so until the map
knows some tokens, a whole run of fields shows up as one token; fill in
what you can and run it again.

## 4. Fill in the mapping file

Create `local/import/attribution-map.csv` with the columns
`token,class,canonical,comment`. The classes are:

| Class | Meaning | Example |
|---|---|---|
| `category` | Top-level category; also how title lines are recognized | `Desserts` |
| `subcategory` | Nested under the preceding category | `Cookies` |
| `contributor` | Who gave the recipe to the household | `Aunt Example` |
| `published_source` | Author as published (a TV chef, a restaurant) | `Chef Example` |
| `tag` | A tag | `Microwave`, `Sugar-free` |
| `note` | Text for the recipe's notes (`canonical` is the note) | `Holiday` |
| `group_label` | An extra ingredient-group label word | `Glaze` |
| `compound` | Several fields run together | see below |

- Use `canonical` to merge spellings: `Aunt Exmple,contributor,Aunt Example,typo`.
- A `compound` token's canonical lists its parts:
  `Microwave/Cousin Sample,compound,tag=Microwave; contributor=Cousin Sample,`.
- Lines starting with `#` are comments.

Title lines are found by their category token, so list **every** category
before converting. A recipe whose category is missing gets merged into the
recipe before it; the converter flags that recipe `possible_missed_title`.

`testdata/legacy/attribution-map.csv` is a complete example.

## 5. Optionally, add ingredient-name overrides

Legacy names written as `Noun--Qualifier` (or with an em dash) are turned
into natural order: `Cheese--Cheddar` becomes `Cheddar cheese`,
`Flour--All Purpose` becomes `all-purpose flour`. When a rule gets one
wrong, add a line to an override CSV with the columns `from,to`:

```csv
from,to
Chips--Peanut Butter,peanut butter baking chips
```

The original line is always kept in the recipe's original text.

## 6. Convert

```sh
bin/grimoire-import convert \
  --in local/import/cookbook-pdf.txt \
  --older local/import/cookbook.txt \
  --map local/import/attribution-map.csv \
  --normalize local/import/normalize.csv \
  --collection "Example Family Cookbook" \
  --out local/import/out
```

`--older` is only for a collection that exists in two editions (D-13).
The `--in` edition is primary. Recipes in both editions keep the older
text as an earlier revision. Recipes only in the older edition are imported
and flagged `only_in_older_edition`. Likely renames ("CORN BREAD" and
"CORNBREAD") are matched and flagged `title_changed_between_editions`.

The output directory gets:

- `drafts.jsonl`: one recipe per line, with an `older` field when the
  editions were matched;
- `report.md`: a summary for reading;
- `report.csv`: one row per recipe, for sorting and filtering.

Each recipe has an import key, `legacy:<collection>:<ordinal>:<title>`. The
key stays the same across runs while the order of the source doesn't change,
so you can convert again after fixing the map. If a re-run finds a different
title at an existing ordinal, both the command and `report.md` warn you.

## 7. Review the report

Work through `report.md` from the top:

1. **Unknown tokens.** Add each one to the mapping file and convert again
   until the list is empty, or only holds text you accept.
2. **Editions.** Check that each rename is really the same recipe.
3. **Recipes with no ingredients or no steps.** Some are legitimate (a
   float has no steps). Others mean the ingredient/instruction boundary
   was guessed wrong.
4. **Review reasons.** Every flagged recipe lands in the app's review queue
   with these reasons:

| Reason | What to check |
|---|---|
| `unclassified_token:<t>` / `unsplit_fields:<t>` | A title-line field the map doesn't know |
| `title_parse_uncertain` | The title was glued to its category or had a mixed-case suffix |
| `possible_missed_title` | A line inside the recipe looks like another recipe's title |
| `multi_column_ingredients` | A table with several batch sizes; the lines are kept as written |
| `group_label_ambiguous` | A `Filling:`-style label the converter didn't turn into a group |
| `no_ingredients` / `no_steps` | See above |
| `only_in_older_edition` / `title_changed_between_editions` | See step 6 |

Nothing is lost when a rule guesses wrong: every recipe keeps its verbatim
source block, shown by "View original" in the app.

## 8. Import

The last step turns `drafts.jsonl` into a bundle and posts it to the
server's bulk import endpoint (`POST /api/v1/import/bundle`, with a dry
run first). That step arrives with the bulk import endpoint (Phase 2,
P2.1–P2.2), and this guide will be extended then.
