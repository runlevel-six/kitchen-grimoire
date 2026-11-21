// SPDX-License-Identifier: Apache-2.0

// Package recipe holds the recipe domain types.
//
// This is the minimal shape needed by the S4 UI prototype; P1.1/P1.2 extend
// it to the full model in docs/DESIGN.md §7.
package recipe

// Recipe is one recipe as shown to a user.
type Recipe struct {
	Slug        string
	Title       string
	Description string

	YieldText string  // as written, e.g. "24 bars"
	Servings  float64 // numeric servings for the scaler; 0 if unknown

	PrepMin, CookMin, TotalMin int

	IngredientGroups []IngredientGroup
	StepGroups       []StepGroup
	Notes            string

	// Provenance (§7.1).
	SourceURL     string
	Author        string // as published
	ContributedBy string // who gave it to the household
	Collection    string

	OriginalText string // verbatim imported text, if any

	Categories []string
	Tags       []string
}

// IngredientGroup is an optionally named list of ingredients ("Filling").
type IngredientGroup struct {
	Name        string
	Ingredients []Ingredient
}

// Ingredient keeps the raw line; the parsed fields are derived (D-06).
type Ingredient struct {
	Raw string

	ParseOK bool
	QtyMin  float64
	QtyMax  float64 // equal to QtyMin unless a range ("3-4")
	Unit    string
	Size    string // "8-oz" in "1 8-oz pkg cream cheese"; never scaled
	Item    string
	Note    string
}

// StepGroup is an optionally named list of steps.
type StepGroup struct {
	Name  string
	Steps []string
}

// StepCount returns the total number of steps across groups.
func (r *Recipe) StepCount() int {
	n := 0
	for _, g := range r.StepGroups {
		n += len(g.Steps)
	}
	return n
}

// FlatStep is a step with its group name and 1-based overall number.
type FlatStep struct {
	Number int
	Group  string
	Text   string
}

// FlatSteps lists every step in order, for cook mode.
func (r *Recipe) FlatSteps() []FlatStep {
	var out []FlatStep
	for _, g := range r.StepGroups {
		for _, s := range g.Steps {
			out = append(out, FlatStep{Number: len(out) + 1, Group: g.Name, Text: s})
		}
	}
	return out
}
