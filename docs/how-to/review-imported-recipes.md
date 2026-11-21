# How to review imported recipes

Imports are cautious: when the app or the legacy converter isn't sure it
read a recipe correctly, it saves the recipe anyway and **flags it for
review** with one or more reasons. Nothing is dropped. This guide shows
how to work through the flagged recipes.

## Open the review queue

Tap **Review queue** in the footer of any page (or go to `/review`). It
lists every flagged recipe, A–Z, with "needs review" on each card. You can
search within the queue with the search box.

## Review one recipe

1. Open a recipe from the queue. A banner at the top says **Flagged for
   review:** and lists the reasons.
2. If the recipe came from the legacy converter (it has original text), the
   **Original** is shown beside the structured version, so you can compare
   them line by line.
3. Fix what's wrong with **Edit**, and **Save**. Saving does not clear the
   flag by itself.
4. When it's right, tap **Mark reviewed** in the banner. The recipe leaves
   the queue and you go back to it.

Marking reviewed changes no content, so it doesn't add a revision. After
that, the original text is still available under **View original** on the
recipe.

## What the reasons mean

From a link import:

| Reason | Check |
|---|---|
| `no_structured_data` | The page had no recipe data; only the title, description, and photo came across. Type in the ingredients and steps. |
| `no_ingredients`, `no_steps` | The page's recipe data was missing them. |
| `no_title` | The page's recipe had no name. |
| `truncated` | A field was longer than the app allows (a very long title, description, or ingredient list) and was cut. Compare with the source page. |

From the legacy converter (details in
[Import a legacy collection](import-a-legacy-collection.md#7-review-the-report)):

| Reason | Check |
|---|---|
| `unclassified_token:<t>`, `unsplit_fields:<t>` | A title-line field the mapping file doesn't know. Fix "From", category, and tags. |
| `title_parse_uncertain` | The title may include part of the category, or the reverse. |
| `possible_missed_title` | Part of this recipe may really be another recipe. Compare with the original; if so, create the second recipe and remove its lines here. |
| `multi_column_ingredients` | A table with several batch sizes, kept as written and not scaled. |
| `group_label_ambiguous` | A `Filling:`-style label that wasn't turned into a group. Make groups by hand if it helps. |
| `no_ingredients`, `no_steps` | Sometimes right (a drink with no steps), sometimes the ingredient/instruction boundary was misjudged. |
| `only_in_older_edition` | Only the older edition had this recipe, so its wording is the older one. |
| `title_changed_between_editions` | Matched to a slightly different title in the older edition. Check it really is the same recipe; the older wording is revision 1 in **History**. |

## Tips

- Work in passes by reason: search within the queue for a word, or sort
  `report.csv` from the converter by reason.
- If many recipes share one cause (a missing token in the mapping file),
  it's often quicker to fix the map and re-run the converter, then import
  again: unedited recipes are updated in place. Recipes you've already
  edited are skipped, so your fixes are never overwritten. See
  [Bulk import and export](bulk-import-and-export.md).
