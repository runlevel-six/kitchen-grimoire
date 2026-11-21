// SPDX-License-Identifier: Apache-2.0

package web

import "github.com/runlevel-six/kitchen-grimoire/internal/recipe"

// SampleRecipes are invented recipes for the S4 prototype (docs/DESIGN.md
// §17: synthetic data only). They exercise the cases the UI must handle:
// named groups, ranges, unscaled sizes, unparsed lines, step timers, and
// provenance. They go away when the store lands in P1.
func SampleRecipes() []*recipe.Recipe {
	ing := func(raw string, qmin, qmax float64, size, unit, item, note string) recipe.Ingredient {
		return recipe.Ingredient{Raw: raw, ParseOK: true, QtyMin: qmin, QtyMax: qmax,
			Size: size, Unit: unit, Item: item, Note: note}
	}
	raw := func(s string) recipe.Ingredient { return recipe.Ingredient{Raw: s} }

	bars := &recipe.Recipe{
		Slug:          "example-lemon-cream-bars",
		Title:         "Example Lemon Cream Bars",
		Description:   "A shortbread crust under a tangy lemon and cream cheese layer. Invented sample recipe.",
		YieldText:     "24 bars",
		Servings:      24,
		PrepMin:       25,
		CookMin:       40,
		TotalMin:      185,
		ContributedBy: "Aunt Example",
		Collection:    "Example Family Cookbook",
		Categories:    []string{"Desserts", "Cookies"},
		Tags:          []string{"make-ahead", "party"},
		IngredientGroups: []recipe.IngredientGroup{
			{Name: "Crust", Ingredients: []recipe.Ingredient{
				ing("2 cups all-purpose flour", 2, 2, "", "cups", "all-purpose flour", ""),
				ing("½ cup powdered sugar", 0.5, 0.5, "", "cup", "powdered sugar", ""),
				ing("1 cup butter, softened", 1, 1, "", "cup", "butter", "softened"),
				raw("pinch of salt"),
			}},
			{Name: "Filling", Ingredients: []recipe.Ingredient{
				ing("1 8-oz pkg cream cheese, softened", 1, 1, "8-oz", "pkg", "cream cheese", "softened"),
				ing("1½ cups sugar", 1.5, 1.5, "", "cups", "sugar", ""),
				ing("4 eggs", 4, 4, "", "", "eggs", ""),
				ing("3-4 Tbsp lemon juice", 3, 4, "", "Tbsp", "lemon juice", ""),
				raw("zest of 2 lemons"),
				ing("¼ cup flour", 0.25, 0.25, "", "cup", "flour", ""),
			}},
		},
		StepGroups: []recipe.StepGroup{
			{Name: "Crust", Steps: []string{
				"Preheat the oven to 350 degrees. Line a 9x13 pan with foil.",
				"Mix flour, powdered sugar, butter, and salt until crumbly. Press firmly into the pan.",
				"Bake 18-20 minutes, until the edges are just golden.",
			}},
			{Name: "Filling", Steps: []string{
				"Beat the cream cheese and sugar until smooth. Beat in the eggs one at a time.",
				"Stir in lemon juice, zest, and flour. Pour over the hot crust.",
				"Bake 20 minutes more, until the center barely jiggles.",
				"Cool 30 minutes, then chill at least 2 hours or overnight before cutting.",
			}},
		},
		Notes: "Dust with powdered sugar just before serving.\nKeeps 4 days in the refrigerator.",
		OriginalText: "EXAMPLE LEMON CREAM BARS   Desserts   Cookies   Aunt Example\n" +
			"2 cups Flour--All Purpose\n1/2 cup Sugar--Powdered\n1 cup Butter\n...\n" +
			"Mix flour, sugar and butter and press in pan. Bake 18-20 minutes. Filling: beat cream cheese...",
	}

	chili := &recipe.Recipe{
		Slug:        "example-weeknight-chili",
		Title:       "Example Weeknight Chili",
		Description: "A one-pot chili for busy evenings. Invented sample recipe.",
		YieldText:   "6 servings",
		Servings:    6,
		PrepMin:     15,
		CookMin:     35,
		TotalMin:    50,
		SourceURL:   "https://recipes.example.com/weeknight-chili",
		Author:      "Example Test Kitchen",
		Categories:  []string{"Main Dishes", "Beef"},
		Tags:        []string{"weeknight"},
		IngredientGroups: []recipe.IngredientGroup{
			{Ingredients: []recipe.Ingredient{
				ing("1 Tbsp oil", 1, 1, "", "Tbsp", "oil", ""),
				ing("1 lb ground beef", 1, 1, "", "lb", "ground beef", ""),
				ing("1 onion, chopped", 1, 1, "", "", "onion", "chopped"),
				ing("2 cloves garlic, minced", 2, 2, "", "cloves", "garlic", "minced"),
				ing("2 Tbsp chili powder", 2, 2, "", "Tbsp", "chili powder", ""),
				ing("1 (15 oz) can kidney beans, drained", 1, 1, "(15 oz)", "can", "kidney beans", "drained"),
				ing("1 (28 oz) can crushed tomatoes", 1, 1, "(28 oz)", "can", "crushed tomatoes", ""),
				raw("salt and pepper to taste"),
			}},
		},
		StepGroups: []recipe.StepGroup{
			{Steps: []string{
				"Heat the oil in a large pot over medium-high heat. Brown the beef with the onion, 8-10 minutes.",
				"Add garlic and chili powder; cook 1 minute.",
				"Add beans and tomatoes. Bring to a boil, then simmer uncovered 25 minutes, stirring now and then.",
				"Season with salt and pepper. Serve with your favorite toppings.",
			}},
		},
	}
	return []*recipe.Recipe{bars, chili}
}
