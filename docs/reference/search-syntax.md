# Search syntax

The search box on the home and search pages uses SQLite full-text search
(FTS5). What you type is turned into a safe query (`store.FTSQuery`), so
no input can cause a syntax error, and the raw FTS5 syntax is never exposed.

## What you can type

| You type | It finds | Notes |
|---|---|---|
| `lemon bars` | Recipes containing **both** words, anywhere | Words are ANDed. There is no OR. |
| `butter` | `butter`, and also `buttermilk`, `butterscotch` | The **last** word also matches as a prefix (it is the one you are still typing), if it is a plain word of at least 2 characters. |
| `butter sugar` | `butter` exactly, and `sugar`, `sugared`, ... | Only the last word is a prefix; earlier words must match whole. |
| `"cream cheese"` | The words next to each other, in this order | A phrase. Never a prefix. An unclosed quote runs to the end of the input. |
| `cookies -nuts` | Recipes with `cookies` that don't mention `nuts` | `-` directly before a word or phrase excludes it. When the input ends with an exclusion or a phrase, no word is a prefix. |
| `-nuts` | Everything (the exclusion is ignored) | A search needs at least one word to look for. |
| `jalapeno` | `jalapeño` as well | Accents and case are ignored. |

Punctuation other than `"` and a leading `-` separates words inside one
term, which then behaves like a phrase: `half-and-half` finds the words
`half and half` next to each other, and `it's` finds `it s`. Letters and
digits together stay one word: `9x13` is a single term.

## What is searched

Each recipe has one search row with five columns. A match in a heavier
column ranks higher (the bm25 weight):

| Column | Contents | Weight |
|---|---|---|
| title | The title | 10 |
| ingredients | Every ingredient line as written, and ingredient group names | 5 |
| tags | Tags and the names of every category level | 5 |
| contributor | "From" (contributed by), author as published, collection | 3 |
| body | Description, steps, step group names, notes | 1 |

So `aunt` finds recipes from "Aunt Example", and `desserts` finds recipes in
the Desserts category or any of its sub-categories.

The original text of imported recipes ("View original") is **not**
searched; only the structured fields are. Recipes in the trash are never
returned.

Results are ordered best match first, then by title. With no search text,
the list is A–Z by title.

## Filters

Filters narrow a search and combine with each other and with the text. They
are query parameters on `/search` (see [HTTP routes](http-routes.md#search-parameters));
you usually get them by following links on the Browse page or a recipe,
and remove them with the ✕ on the filter chip above the results.

| Filter | Parameter | Matches |
|---|---|---|
| Category | `cat=desserts/cookies` | The category and all its sub-categories |
| Tag | `tag=make-ahead` | The tag (by slug) |
| From | `by=Aunt Example` | Contributed by, exact name, case-insensitive |
| Collection | `col=Example Family Cookbook` | Collection, exact name, case-insensitive |
| Needs review | `review=1` | Flagged recipes |
| Has a photo | `photo=1` | Recipes with at least one photo |
| Favorites | `fav=1` | Your favorites |

## Rebuilding the index

The index is updated in the same transaction as every save, so it never
disagrees with the recipes. If you ever need to rebuild it, run
`grimoire migrate --reindex` (see [Operate](../how-to/operate.md#rebuild-the-search-index)).
