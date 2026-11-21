# Interchange format

Kitchen Grimoire imports and exports recipes as **schema.org `Recipe`
JSON-LD**, so other software can read them. Fields schema.org has no place
for go in an `x-grimoire` object, which ordinary consumers ignore. Recipes
travel in a **bundle**: a zip of JSON lines plus images. The implementation
is `internal/exportfmt`; the design is in
[DESIGN.md §7.3](../DESIGN.md#73-interchange-format) and decision D-21.

The same format is used by:

- `GET /api/v1/export` (writes a bundle of every live recipe);
- `POST /api/v1/import/bundle` (reads a bundle);
- `grimoire-import bundle` (writes a bundle from converter drafts).

## Bundle layout

```text
recipes.jsonl                 one Recipe object per line, UTF-8
images/                       optional directory entry
images/<sha256>.jpg|png       photos, named by the SHA-256 of their bytes
```

A recipe refers to its photos from `image` by relative path
(`images/<sha256>.jpg`), main photo first. Each image file is stored once,
however many recipes use it.

The reader accepts **only** those names. It rejects absolute paths, `..`,
backslashes, duplicate names, and any other entry, and enforces these
limits:

| Limit | Value |
|---|---|
| Zip entries | 50,000 |
| Recipes (lines) | 20,000 |
| `recipes.jsonl` size | 256 MiB |
| One line | 8 MiB |
| One image | 15 MiB |
| Total uncompressed | 4 GiB |
| Upload to the API | 512 MiB (compressed) |

Exports are deterministic: recipes are ordered by import key, then title,
then ID; images by path; zip timestamps are fixed. Exporting an unchanged
library twice gives identical bytes.

## A recipe object

This is one line of an export, pretty-printed, from the sample recipes that
`make dev` creates (one image added):

```json
{
  "@context": "https://schema.org",
  "@type": "Recipe",
  "name": "Example Lemon Cream Bars",
  "description": "A shortbread crust under a tangy lemon and cream cheese layer. Invented sample recipe.",
  "recipeYield": "24 bars",
  "prepTime": "PT25M",
  "cookTime": "PT40M",
  "totalTime": "PT3H5M",
  "recipeCategory": ["Cookies"],
  "keywords": "make-ahead, party",
  "recipeIngredient": [
    "2 cups all-purpose flour",
    "½ cup powdered sugar",
    "1 8-oz pkg cream cheese, softened"
  ],
  "recipeInstructions": [
    {
      "@type": "HowToSection",
      "name": "Crust",
      "itemListElement": [
        {"@type": "HowToStep", "text": "Preheat the oven to 350 degrees. Line a 9x13 pan with foil."},
        {"@type": "HowToStep", "text": "Bake 18-20 minutes, until the edges are just golden."}
      ]
    },
    {
      "@type": "HowToSection",
      "name": "Filling",
      "itemListElement": [
        {"@type": "HowToStep", "text": "Beat the cream cheese and sugar until smooth."}
      ]
    }
  ],
  "image": ["images/3f1c...e9.jpg"],
  "datePublished": "2026-11-21T04:15:58.032Z",
  "dateModified": "2026-11-21T04:15:58.032Z",
  "x-grimoire": {
    "version": 1,
    "importKey": "recipe:01a1fc51-7050-78f1-ab43-33dee08c6ede",
    "sourceKind": "manual",
    "contributedBy": "Aunt Example",
    "collection": "Example Family Cookbook",
    "servings": 24,
    "categoryPath": [["Desserts", "Cookies"]],
    "tags": ["make-ahead", "party"],
    "ingredientGroups": [
      {"name": "Crust", "lines": ["2 cups all-purpose flour", "½ cup powdered sugar"]},
      {"name": "Filling", "lines": ["1 8-oz pkg cream cheese, softened"]}
    ],
    "stepGroups": [
      {"name": "Crust", "lines": [
        "Preheat the oven to 350 degrees. Line a 9x13 pan with foil.",
        "Bake 18-20 minutes, until the edges are just golden."
      ]},
      {"name": "Filling", "lines": ["Beat the cream cheese and sugar until smooth."]}
    ],
    "needsReview": false,
    "reviewReasons": []
  }
}
```

Real exports are one object per line, with keys sorted alphabetically.

## schema.org fields

| Field | Written as | Notes |
|---|---|---|
| `@context`, `@type` | `https://schema.org`, `Recipe` | An object whose `@type` isn't `Recipe` (or an array containing it) is invalid. |
| `name` | Title | Required. |
| `description` | Text | |
| `recipeYield` | Text, as typed ("24 bars") | |
| `prepTime`, `cookTime`, `totalTime` | ISO 8601 durations, `PT#H#M` | Whole minutes. |
| `recipeIngredient` | Every ingredient line, as written | Groups are flattened here; see `ingredientGroups`. |
| `recipeInstructions` | `HowToSection` per named step group, bare `HowToStep`s for an unnamed one | |
| `recipeCategory` | The last level of each category path | Full paths are in `categoryPath`. |
| `keywords` | Tags joined with `", "` | Tags are in `tags` too, since a tag may contain a comma. |
| `author` | `{"@type": "Person", "name": ...}` | The author **as published**, e.g. a cookbook or TV cook. |
| `url` | Source link | |
| `image` | Array of bundle paths | Main photo first. |
| `datePublished`, `dateModified` | The recipe's created and updated times, UTC, milliseconds | Kept on import. |

## `x-grimoire` fields

| Field | Type | Meaning |
|---|---|---|
| `version` | number | Extension version, currently `1`. A newer version imports with a warning; unknown fields are ignored. |
| `importKey` | string | Identity for idempotent import (see below). Every exported recipe has one. |
| `sourceKind` | string | How the recipe first arrived: `manual`, `url`, `capture`, `bundle`, or `legacy`. |
| `canonicalUrl` | string | Canonical URL of the page it was imported from; used to spot duplicates. |
| `sourceSite` | string | The site's name as published. |
| `contributedBy` | string | Who gave the recipe to the household ("From"). |
| `collection` | string | Collection, such as a family cookbook. |
| `servings` | number | Numeric servings, used by the scaler. |
| `categoryPath` | array of arrays | Full category paths, e.g. `[["Desserts", "Cookies"]]`. |
| `tags` | array | Tags. |
| `ingredientGroups` | array of `{name, lines}` | Ingredient lines by group; `name` is `null` for an unnamed group. |
| `stepGroups` | array of `{name, lines}` | Steps by group, one step per entry. |
| `notes` | string | Notes. |
| `originalText` | string | The verbatim source text of an imported recipe ("View original"). |
| `needsReview` | boolean | Whether the recipe is in the review queue. |
| `reviewReasons` | array | Why, e.g. `no_ingredients`, `unclassified_token:<t>`. |
| `rawSource` | object | The JSON-LD extracted from the page a URL import came from, kept verbatim. |
| `olderEdition` | object | **Import only**, written by `grimoire-import bundle`: the same recipe as an older edition of its source had it. It is stored as revision 1, with the main object as revision 2. Exports never include it. |

When `x-grimoire` is present it is **authoritative**: its list fields are
always written (`[]` when empty), and a reader falls back to the plain
schema.org field only when the key is absent. Without `x-grimoire`, an
object is read the way a scraped web page is (HTML stripped, every common
JSON-LD shape accepted), so bundles written by other software can be
imported too.

Parsed ingredient quantities are never written: the raw lines are the
source of truth, and they are parsed again on import.

## Import keys and what an import does

Each item is matched to an existing recipe by its `importKey`, or, when it
has none, by `canonicalUrl`. An item with neither is always created.

| Key form | Used for |
|---|---|
| `legacy:<collection-slug>:<0042>:<title-slug>` | Recipes from the legacy converter |
| `recipe:<id>` | Exported recipes that had no key of their own (typed in or imported from a link). It also matches the recipe with that ID, so re-importing an export into its own library changes nothing. |
| anything else | Your own keys, if you generate bundles yourself |

Every item gets one outcome:

| Outcome | When |
|---|---|
| `created` | No matching recipe. |
| `updated` | A match that hasn't been edited since it was imported: it is updated in place as a new revision. |
| `unchanged` | A match whose content is already identical (ignoring the two dates). Nothing is written. |
| `skipped_conflict` | A match that was edited since import, never came from a bundle import, or is in the trash. It is left alone. |
| `invalid` | The line isn't valid JSON, isn't a Recipe, fails validation, or repeats a key already seen in this bundle. |

Each item is its own transaction; one bad item never stops the rest.
Images are decoded and re-encoded like an upload, except that bytes
identical to a file the store already has are reused. A missing or
unreadable image is a warning on the item, not a failure.

## Import report

`POST /api/v1/import/bundle` answers with a report like this:

```json
{
  "dryRun": true,
  "counts": {
    "created": 0,
    "updated": 0,
    "skipped_conflict": 0,
    "invalid": 0,
    "unchanged": 2
  },
  "items": [
    {
      "index": 0,
      "importKey": "recipe:01a1fc51-7050-78f1-ab43-33dee08c6ede",
      "title": "Example Lemon Cream Bars",
      "outcome": "unchanged",
      "slug": "example-lemon-cream-bars"
    }
  ]
}
```

| Field | Meaning |
|---|---|
| `dryRun` | Whether this was a dry run (nothing written). |
| `counts` | Items per outcome. |
| `items[].index` | Line number in `recipes.jsonl`, from 0. |
| `items[].importKey`, `title` | The item's key and title, when it had them. |
| `items[].outcome` | One of the outcomes above. |
| `items[].slug` | The recipe it created or matched. |
| `items[].reasons` | Why it was skipped or invalid, or notes such as `older edition kept as revision 1`. |
| `items[].warnings` | Problems that didn't stop the item, such as a missing image. |
