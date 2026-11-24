// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/runlevel-six/kitchen-grimoire/internal/ingredient"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// Meals and shopping lists (FR-16, FR-17, D-22). Every change is a plain
// form POST that redirects back, so the pages work without JavaScript; with
// htmx, the forms swap just the part of the page that changed (hx-select
// on the redirected page).

type (
	mealT        = recipe.Meal
	mealSummaryT = recipe.MealSummary
	shopItemT    = ingredient.ShopItem
)

// MealStore is the meal storage the handlers need.
type MealStore interface {
	CreateMeal(ctx context.Context, name, userID string) (string, error)
	ListMeals(ctx context.Context) ([]recipe.MealSummary, error)
	MealsForRecipe(ctx context.Context, recipeID string) ([]recipe.MealSummary, error)
	GetMeal(ctx context.Context, id string) (*recipe.Meal, error)
	UpdateMeal(ctx context.Context, id, name, notes string) error
	DeleteMeal(ctx context.Context, id string) error
	RestoreMeal(ctx context.Context, id string) error
	AddToMeal(ctx context.Context, mealID, recipeID string, factor float64) error
	SetMealFactor(ctx context.Context, mealID, recipeID string, factor float64) error
	RemoveFromMeal(ctx context.Context, mealID, recipeID string) error
	MoveInMeal(ctx context.Context, mealID, recipeID string, delta int) error
}

const (
	maxMealName  = 120
	maxMealNotes = 4000
)

func (h *Handler) registerMeals(mux *http.ServeMux) {
	if h.opts.Meals == nil {
		return
	}
	mux.HandleFunc("GET /meals", h.meals)
	mux.HandleFunc("POST /meals", h.createMeal)
	mux.HandleFunc("GET /meals/{id}", h.meal)
	mux.HandleFunc("POST /meals/{id}", h.updateMeal)
	mux.HandleFunc("POST /meals/{id}/delete", h.deleteMeal)
	mux.HandleFunc("POST /meals/{id}/undelete", h.undeleteMeal)
	mux.HandleFunc("POST /meals/{id}/recipes", h.addToMeal)
	mux.HandleFunc("POST /meals/{id}/recipes/{rid}", h.changeMealEntry)
	mux.HandleFunc("GET /meals/{id}/list", h.shoppingList)
	mux.HandleFunc("GET /r/{slug}/meals", h.mealPicker)
}

func (h *Handler) meals(w http.ResponseWriter, r *http.Request) {
	list, err := h.opts.Meals.ListMeals(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, mealsPage(list, "", flashFrom(r)))
}

// mealName trims and checks a submitted name. The error is the message to
// show; it never echoes the input.
func mealName(s string) (string, string) {
	s = strings.Join(strings.Fields(s), " ")
	switch {
	case s == "":
		return "", "Give the meal a name."
	case utf8.RuneCountInString(s) > maxMealName:
		return "", "That name is too long."
	}
	return s, ""
}

func (h *Handler) createMeal(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// From the "Add to meal" picker: create the meal and add the recipe.
	var rec *recipe.Recipe
	if slug := r.PostForm.Get("recipe"); slug != "" {
		var err error
		if rec, err = h.liveRecipe(r, slug); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	factor := parseFactor(r.PostForm.Get("x"))
	name, msg := mealName(r.PostForm.Get("name"))
	if msg != "" {
		if rec != nil {
			h.renderPicker(w, r, rec, factor, msg)
			return
		}
		list, err := h.opts.Meals.ListMeals(r.Context())
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.render(w, r, http.StatusUnprocessableEntity, mealsPage(list, msg, flash{}))
		return
	}
	id, err := h.opts.Meals.CreateMeal(r.Context(), name, userID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if rec != nil {
		if err := h.opts.Meals.AddToMeal(r.Context(), id, rec.ID, factor); err != nil {
			h.fail(w, r, err)
			return
		}
		redirect(w, r, pickerURL(rec.Slug, factor))
		return
	}
	redirect(w, r, "/meals/"+id)
}

// getMeal loads the meal named in the path; a malformed ID is a 404.
func (h *Handler) getMeal(r *http.Request) (*recipe.Meal, error) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		return nil, recipe.ErrNotFound
	}
	return h.opts.Meals.GetMeal(r.Context(), id)
}

func (h *Handler) meal(w http.ResponseWriter, r *http.Request) {
	m, err := h.getMeal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, mealPage(m, "", flashFrom(r)))
}

func (h *Handler) updateMeal(w http.ResponseWriter, r *http.Request) {
	m, err := h.getMeal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name, msg := mealName(r.PostForm.Get("name"))
	notes := strings.TrimSpace(strings.ReplaceAll(r.PostForm.Get("notes"), "\r\n", "\n"))
	if msg == "" && utf8.RuneCountInString(notes) > maxMealNotes {
		msg = "Those notes are too long."
	}
	if msg != "" {
		h.render(w, r, http.StatusUnprocessableEntity, mealPage(m, msg, flash{}))
		return
	}
	if err := h.opts.Meals.UpdateMeal(r.Context(), m.ID, name, notes); err != nil {
		h.fail(w, r, mealErr(err))
		return
	}
	redirect(w, r, "/meals/"+m.ID+"?saved=1")
}

func (h *Handler) deleteMeal(w http.ResponseWriter, r *http.Request) {
	m, err := h.getMeal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.opts.Meals.DeleteMeal(r.Context(), m.ID); err != nil && !errors.Is(err, recipe.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/meals?mealdeleted="+m.ID)
}

func (h *Handler) undeleteMeal(w http.ResponseWriter, r *http.Request) {
	m, err := h.getMeal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.opts.Meals.RestoreMeal(r.Context(), m.ID); err != nil && !errors.Is(err, recipe.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/meals/"+m.ID+"?restored=1")
}

// addToMeal adds a recipe (or changes its scale if it's already there).
// Form: recipe (slug), x (factor), back ("picker" to return to the recipe's
// picker; otherwise the meal page).
func (h *Handler) addToMeal(w http.ResponseWriter, r *http.Request) {
	m, err := h.getMeal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	rec, err := h.liveRecipe(r, r.PostForm.Get("recipe"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	factor := parseFactor(r.PostForm.Get("x"))
	if err := h.opts.Meals.AddToMeal(r.Context(), m.ID, rec.ID, factor); err != nil {
		h.fail(w, r, mealErr(err))
		return
	}
	if r.PostForm.Get("back") == "picker" {
		redirect(w, r, pickerURL(rec.Slug, factor))
		return
	}
	redirect(w, r, "/meals/"+m.ID)
}

// changeMealEntry handles one entry's controls. Form: action = scale (with
// x), up, down, or remove; back = "picker" (with recipe slug and x) to
// return to the picker.
func (h *Handler) changeMealEntry(w http.ResponseWriter, r *http.Request) {
	m, err := h.getMeal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rid := r.PathValue("rid")
	if _, err := uuid.Parse(rid); err != nil {
		h.fail(w, r, recipe.ErrNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	switch r.PostForm.Get("action") {
	case "scale":
		err = h.opts.Meals.SetMealFactor(ctx, m.ID, rid, parseFactor(r.PostForm.Get("x")))
	case "up":
		err = h.opts.Meals.MoveInMeal(ctx, m.ID, rid, -1)
	case "down":
		err = h.opts.Meals.MoveInMeal(ctx, m.ID, rid, 1)
	case "remove":
		err = h.opts.Meals.RemoveFromMeal(ctx, m.ID, rid)
		if errors.Is(err, recipe.ErrNotFound) {
			err = nil // already gone: a double tap
		}
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err != nil {
		h.fail(w, r, mealErr(err))
		return
	}
	if r.PostForm.Get("back") == "picker" {
		redirect(w, r, pickerURL(recipe.Slugify(r.PostForm.Get("recipe")), parseFactor(r.PostForm.Get("x"))))
		return
	}
	redirect(w, r, "/meals/"+m.ID)
}

// mealErr turns "not found" from a write into a clearer page: the usual
// cause is a meal deleted in another tab.
func mealErr(err error) error {
	if errors.Is(err, recipe.ErrNotFound) {
		return errMealGone
	}
	return err
}

var errMealGone = errors.New("meal deleted or recipe not in it")

func (h *Handler) liveRecipe(r *http.Request, slug string) (*recipe.Recipe, error) {
	rec, err := h.svc.Get(r.Context(), slug)
	if err != nil {
		return nil, err
	}
	if rec.Deleted() {
		return nil, recipe.ErrDeleted
	}
	return rec, nil
}

func pickerURL(slug string, factor float64) string {
	return "/r/" + slug + "/meals?x=" + url.QueryEscape(fmtFactor(factor))
}

// scaleQuery is the recipe view's query for factor: "" at 1×.
func scaleQuery(factor float64) string {
	if factor == 1 || factor == 0 {
		return ""
	}
	return "?x=" + url.QueryEscape(fmtFactor(factor))
}

// scaleNote marks a recipe cooked at other than 1×: " · 2×".
func scaleNote(factor float64) string {
	if factor == 1 || factor == 0 {
		return ""
	}
	return " · " + fmtFactor(factor) + "×"
}

// mealPicker is the "Add to meal" list for one recipe, at the scale
// selected on the recipe view. With htmx it opens in place under the
// scaler; without JavaScript it's a page of its own.
func (h *Handler) mealPicker(w http.ResponseWriter, r *http.Request) {
	rec, err := h.liveRecipe(r, r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPicker(w, r, rec, parseFactor(r.URL.Query().Get("x")), "")
}

func (h *Handler) renderPicker(w http.ResponseWriter, r *http.Request, rec *recipe.Recipe, factor float64, msg string) {
	list, err := h.opts.Meals.MealsForRecipe(r.Context(), rec.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if msg != "" {
		status = http.StatusUnprocessableEntity
	}
	h.render(w, r, status, pickerPage(rec, factor, list, msg))
}

// --- shopping list (§9.4) ---

// shopView is what the shopping-list page shows.
type shopView struct {
	List    ingredient.ShoppingList
	Factors map[string]float64 // recipe title → factor, for unparsed lines
	Trashed []string           // recipe titles left off
	Recipes int
}

func (h *Handler) shoppingList(w http.ResponseWriter, r *http.Request) {
	m, err := h.getMeal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := shopView{Factors: map[string]float64{}}
	var lines []ingredient.ShopLine
	for _, e := range m.Entries {
		if e.Recipe.DeletedAt != nil {
			v.Trashed = append(v.Trashed, e.Recipe.Title)
			continue
		}
		rec, err := h.svc.Get(r.Context(), e.Recipe.Slug)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		v.Recipes++
		v.Factors[rec.Title] = e.Factor
		for _, g := range rec.IngredientGroups {
			for _, in := range g.Ingredients {
				lines = append(lines, ingredient.ShopLine{Line: in, Factor: e.Factor, Recipe: rec.Title})
			}
		}
	}
	v.List = ingredient.Shop(lines)
	h.render(w, r, http.StatusOK, shoppingPage(m, v))
}

// shopAmounts is a row's amounts as one string: "2 cups + 4 Tbsp".
func shopAmounts(it ingredient.ShopItem) string { return strings.Join(it.Amounts, " + ") }

// shopText is one row as plain text, for "Copy as text".
func shopText(it ingredient.ShopItem) string {
	if len(it.Amounts) == 0 {
		return it.Item
	}
	return it.Item + ": " + shopAmounts(it)
}
