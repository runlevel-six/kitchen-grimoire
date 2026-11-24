// SPDX-License-Identifier: Apache-2.0

package recipe

import "time"

// Meal is a named, household-shared group of recipes, each cooked at its
// own scale (FR-16, D-22).
type Meal struct {
	ID        string
	Name      string
	Notes     string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
	Entries   []MealEntry // in meal order
}

// MealEntry is one recipe in a meal.
type MealEntry struct {
	Recipe Summary
	Factor float64
}

// MealSummary is the light row used in lists of meals.
type MealSummary struct {
	ID        string
	Name      string
	Recipes   int
	UpdatedAt time.Time
	// Factor is set by MealsForRecipe: the recipe's scale in this meal,
	// or 0 if the recipe isn't in it.
	Factor float64
}
