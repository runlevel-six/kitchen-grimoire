# Legacy import report: Example Family Cookbook

- Source format: tabbed
- Recipes: 13
- Needing review: 5
- Lines ignored before the first title: 3

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
| Vegetables | 1 |

## Review reasons

| Reason | Recipes |
|---|---|
| `group_label_ambiguous` | 1 |
| `multi_column_ingredients` | 1 |
| `no_ingredients` | 1 |
| `no_steps` | 1 |
| `unclassified_token` | 1 |

## Unknown tokens

Add these to the mapping file, then convert again.

| Kind | Text | Recipes |
|---|---|---|
| unclassified_token | Neighbor Invented | 1 |

## Recipes with no ingredients

- 8. PARTY PUNCH

## Recipes with no steps

- 7. ROOT BEER FLOAT

## All recipes

| # | Title | Category | Contributor | Flags |
|---|---|---|---|---|
| 1 | CHEESE BALL | Appetizers | Aunt Example |  |
| 2 | CORN BREAD | Breads | Grandma Placeholder |  |
| 3 | CHOCOLATE CHIP COOKIES | Desserts > Cookies | Cousin Sample |  |
| 4 | LEMON POPPY SEED CAKE | Desserts > Cakes |  |  |
| 5 | HAMBURGER CASSEROLE | Main Dishes > Casseroles | Cousin Sample |  |
| 6 | SUGAR-FREE FRUIT SALAD | Salads |  | unclassified_token:Neighbor Invented |
| 7 | ROOT BEER FLOAT | Beverages | Uncle Fictional | no_steps |
| 8 | PARTY PUNCH | Beverages | Aunt Example | no_ingredients |
| 9 | CORNMEAL MUFFINS | Breads | Grandma Placeholder | multi_column_ingredients |
| 10 | FRIED EGGS—easiest way | Breakfast Dishes | Uncle Fictional |  |
| 11 | LAYERED SALAD | Salads | Cousin Sample |  |
| 12 | BROCCOLI BAKE | Vegetables | Aunt Example | group_label_ambiguous |
| 13 | PEANUT BUTTER FUDGE | Desserts | Aunt Example |  |
