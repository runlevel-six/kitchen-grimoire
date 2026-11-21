# Legacy import report: Example Family Cookbook

- Source format: pdftext
- Recipes: 14
- Needing review: 9
- Lines ignored before the first title: 2
- Older edition format: tabbed; lines ignored before its first title: 3

## Editions

- Matched by title: 11
- Matched as likely renames (flagged `title_changed_between_editions`): 1
  - CORNBREAD ← CORN BREAD (similarity 1.00)
- Only in the older edition (imported, flagged `only_in_older_edition`): 1
  - PEANUT BUTTER FUDGE
- Only in the newer edition: 1
  - SLOW COOKER CHILI

## Recipes by category

| Category | Recipes |
|---|---|
| Beverages | 2 |
| Breads | 2 |
| Salads | 2 |
| Appetizers | 1 |
| Breakfast Dishes | 1 |
| Desserts | 1 |
| Desserts > Cakes | 1 |
| Desserts > Cookies | 1 |
| Main Dishes > Casseroles | 1 |
| Soups | 1 |
| Vegetables | 1 |

## Review reasons

| Reason | Recipes |
|---|---|
| `title_parse_uncertain` | 2 |
| `group_label_ambiguous` | 1 |
| `multi_column_ingredients` | 1 |
| `no_ingredients` | 1 |
| `no_steps` | 1 |
| `only_in_older_edition` | 1 |
| `title_changed_between_editions` | 1 |
| `unsplit_fields` | 1 |

## Unknown tokens

Add these to the mapping file, then convert again.

| Kind | Text | Recipes |
|---|---|---|
| unsplit_fields | Neighbor Invented | 1 |

## Recipes with no ingredients

- 8. PARTY PUNCH

## Recipes with no steps

- 7. ROOT BEER FLOAT

## All recipes

| # | Title | Category | Contributor | Flags |
|---|---|---|---|---|
| 1 | CHEESE BALL | Appetizers | Aunt Example |  |
| 2 | CORNBREAD | Breads | Grandma Placeholder | title_changed_between_editions |
| 3 | CHOCOLATE CHIP COOKIES | Desserts > Cookies | Cousin Sample |  |
| 4 | LEMON POPPY SEED CAKE | Desserts > Cakes |  |  |
| 5 | HAMBURGER CASSEROLE | Main Dishes > Casseroles | Cousin Sample |  |
| 6 | SUGAR-FREE FRUIT SALAD | Salads |  | unsplit_fields:Neighbor Invented |
| 7 | ROOT BEER FLOAT | Beverages | Uncle Fictional | no_steps |
| 8 | PARTY PUNCH | Beverages | Aunt Example | no_ingredients |
| 9 | CORNMEAL MUFFINS | Breads | Grandma Placeholder | multi_column_ingredients |
| 10 | FRIED EGGS—easiest way | Breakfast Dishes | Uncle Fictional | title_parse_uncertain |
| 11 | LAYERED SALAD | Salads | Cousin Sample | title_parse_uncertain |
| 12 | BROCCOLI BAKE | Vegetables | Aunt Example | group_label_ambiguous |
| older 13 | PEANUT BUTTER FUDGE | Desserts | Aunt Example | only_in_older_edition |
| 13 | SLOW COOKER CHILI | Soups |  |  |
