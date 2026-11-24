// SPDX-License-Identifier: Apache-2.0

package web

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// mealIDFrom pulls the meal ID out of a /meals/{id}... location.
var mealIDRe = regexp.MustCompile(`/meals/([0-9a-f-]{36})`)

func TestMealsFlow(t *testing.T) {
	e := newEnv(t, false)
	e.post(t, "/new", lemonForm())
	pie := lemonForm()
	pie.Set("title", "Example Pumpkin Pie")
	pie["ig_name"] = []string{""}
	pie["ig_lines"] = []string{"1 cup sugar\n2 eggs\n1 8-oz pkg cream cheese\nButter for the pan\nSalt to taste"}
	e.post(t, "/new", pie)

	// The recipe view offers "Add to meal" at the selected scale.
	_, body := e.get(t, "/r/example-lemon-cream-bars?x=2")
	wantContains(t, body, `href="/meals"`, "+ Add to meal", `/r/example-lemon-cream-bars/meals?x=2`, "4×")

	// Picker: create a meal and add the recipe at 2× in one step.
	_, body = e.get(t, "/r/example-lemon-cream-bars/meals?x=2")
	wantContains(t, body, "Add to a meal", "<strong>2×</strong>", "Create and add")
	res, body := e.post(t, "/meals", url.Values{"name": {"  Thanksgiving  "}, "recipe": {"example-lemon-cream-bars"}, "x": {"2"}})
	wantStatus(t, res, http.StatusSeeOther, body)
	if loc := res.Header.Get("Location"); loc != "/r/example-lemon-cream-bars/meals?x=2" {
		t.Fatalf("create-and-add redirect = %q", loc)
	}
	_, body = e.get(t, "/r/example-lemon-cream-bars/meals?x=2")
	wantContains(t, body, "Thanksgiving", "In this meal at 2×")
	id := mealIDRe.FindStringSubmatch(body)[1]

	// Picker at another scale offers to change it.
	_, body = e.get(t, "/r/example-lemon-cream-bars/meals?x=3")
	wantContains(t, body, "Change to 3×")

	// Add the pie from its own picker at 1×.
	res, body = e.post(t, "/meals/"+id+"/recipes", url.Values{"recipe": {"example-pumpkin-pie"}, "x": {"1"}, "back": {"picker"}})
	wantStatus(t, res, http.StatusSeeOther, body)
	// Adding again changes the factor instead of adding twice.
	e.post(t, "/meals/"+id+"/recipes", url.Values{"recipe": {"example-pumpkin-pie"}, "x": {"2"}})

	_, body = e.get(t, "/meals")
	wantContains(t, body, "Thanksgiving", "2 recipes")

	// Meal page: both recipes in order, links carry each one's scale.
	_, body = e.get(t, "/meals/"+id)
	wantContains(t, body, "Example Lemon Cream Bars", "Example Pumpkin Pie", "Shopping list",
		`href="/r/example-lemon-cream-bars?x=2"`, `href="/r/example-pumpkin-pie?x=2"`)
	if strings.Index(body, "Example Lemon Cream Bars") > strings.Index(body, "Example Pumpkin Pie") {
		t.Error("meal order: bars were added first")
	}

	// Rescale the pie back to 1× and move it up.
	rid := recipeID(t, e, "example-pumpkin-pie")
	e.post(t, "/meals/"+id+"/recipes/"+rid, url.Values{"action": {"scale"}, "x": {"1"}})
	e.post(t, "/meals/"+id+"/recipes/"+rid, url.Values{"action": {"up"}})
	_, body = e.get(t, "/meals/"+id)
	if strings.Index(body, "Example Pumpkin Pie") > strings.Index(body, "Example Lemon Cream Bars") {
		t.Error("move up didn't reorder")
	}
	wantContains(t, body, `href="/r/example-pumpkin-pie"`)

	// Shopping list: bars at 2×, pie at 1×; cream cheese combined.
	res, body = e.get(t, "/meals/"+id+"/list")
	wantStatus(t, res, 200, body)
	text := html.UnescapeString(body)
	wantContains(t, text,
		"<strong>cream cheese</strong> 3 8-oz pkg",  // 2 (bars ×2) + 1 (pie)
		"<strong>all-purpose flour</strong> 4 cups", // 2 cups ×2
		"<strong>lemon juice</strong> 6–8 Tbsp",     // 3–4 ×2
		"<strong>sugar</strong> 1 cup",              // pie only
		"Check these", "Butter for the pan",         // unparsed, kept verbatim
		"Probably on hand (2)", "pinch of salt", "Salt to taste",
		`data-recipe="meal-`+id+`"`, "Copy as text",
	)

	// Rename with notes; an empty name is refused without losing the page.
	res, body = e.post(t, "/meals/"+id, url.Values{"name": {""}, "notes": {"x"}})
	wantStatus(t, res, http.StatusUnprocessableEntity, body)
	wantContains(t, body, "Give the meal a name.")
	res, _ = e.post(t, "/meals/"+id, url.Values{"name": {"Thanksgiving 2026"}, "notes": {"Dinner at 3"}})
	wantStatus(t, res, http.StatusSeeOther, "")
	_, body = e.get(t, "/meals/"+id+"?saved=1")
	wantContains(t, body, "Thanksgiving 2026", "Dinner at 3", "Saved.")

	// A trashed recipe stays on the meal page but is left off the list.
	e.post(t, "/r/example-pumpkin-pie/delete", nil)
	_, body = e.get(t, "/meals/"+id)
	wantContains(t, body, "In the trash: left off the shopping list")
	_, body = e.get(t, "/meals/"+id+"/list")
	wantContains(t, html.UnescapeString(body), "Left off because they're in the trash: Example Pumpkin Pie")
	if strings.Contains(body, "Butter for the pan") {
		t.Error("trashed recipe's lines are on the list")
	}

	// Remove, then delete the meal with undo.
	barsID := recipeID(t, e, "example-lemon-cream-bars")
	e.post(t, "/meals/"+id+"/recipes/"+barsID, url.Values{"action": {"remove"}})
	res, _ = e.post(t, "/meals/"+id+"/recipes/"+barsID, url.Values{"action": {"remove"}}) // double tap
	wantStatus(t, res, http.StatusSeeOther, "")
	res, _ = e.post(t, "/meals/"+id+"/delete", nil)
	loc := res.Header.Get("Location")
	_, body = e.get(t, loc)
	wantContains(t, body, "Meal deleted.", "/meals/"+id+"/undelete")
	if strings.Contains(body, "Thanksgiving 2026") {
		t.Error("deleted meal still listed")
	}
	res, body = e.post(t, "/meals/"+id+"/recipes", url.Values{"recipe": {"example-lemon-cream-bars"}})
	wantStatus(t, res, http.StatusConflict, body)
	e.post(t, "/meals/"+id+"/undelete", nil)
	_, body = e.get(t, "/meals")
	wantContains(t, body, "Thanksgiving 2026")
}

func TestMealsInputChecks(t *testing.T) {
	e := newEnv(t, false)
	e.post(t, "/new", lemonForm())

	for _, path := range []string{"/meals/nope", "/meals/nope/list", "/meals/01a20e9f-5088-7537-a107-c9ffb153c105"} {
		res, body := e.get(t, path)
		wantStatus(t, res, http.StatusNotFound, body)
	}
	res, body := e.post(t, "/meals", url.Values{"name": {strings.Repeat("a", 121)}})
	wantStatus(t, res, http.StatusUnprocessableEntity, body)
	wantContains(t, body, "That name is too long.")

	// From the picker, a blank name re-renders the picker with the error.
	res, body = e.post(t, "/meals", url.Values{"name": {" "}, "recipe": {"example-lemon-cream-bars"}, "x": {"2"}})
	wantStatus(t, res, http.StatusUnprocessableEntity, body)
	wantContains(t, body, "Add to a meal", "Give the meal a name.")

	// An unknown factor falls back to 1×; an unknown action is refused.
	e.post(t, "/meals", url.Values{"name": {"Picnic"}, "recipe": {"example-lemon-cream-bars"}, "x": {"7"}})
	_, body = e.get(t, "/r/example-lemon-cream-bars/meals")
	wantContains(t, body, "In this meal at 1×")
	id := mealIDRe.FindStringSubmatch(body)[1]
	res, body = e.post(t, "/meals/"+id+"/recipes/"+recipeID(t, e, "example-lemon-cream-bars"), url.Values{"action": {"drop"}})
	wantStatus(t, res, http.StatusBadRequest, body)

	// The meal's name is never echoed into the flash or a URL unescaped.
	e.post(t, "/meals", url.Values{"name": {`<script>alert(1)</script>`}})
	_, body = e.get(t, "/meals")
	if strings.Contains(body, "<script>alert") {
		t.Error("meal name not escaped")
	}
	_, body = e.get(t, "/meals?mealdeleted=../../x")
	if strings.Contains(body, "Meal deleted.") {
		t.Error("flash accepted a non-UUID meal id")
	}

	// htmx: the picker swap returns a page containing #meal-picker.
	req := httptest.NewRequest(http.MethodGet, "/r/example-lemon-cream-bars/meals?x=2", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `id="meal-picker"`) {
		t.Error("picker missing its swap target")
	}
}

func recipeID(t *testing.T, e *env, slug string) string {
	t.Helper()
	rec, err := e.svc.Get(t.Context(), slug)
	if err != nil {
		t.Fatal(err)
	}
	return rec.ID
}

// Cook mode keeps the scale: scaled drawer, and every link carries ?x=.
func TestCookModeScale(t *testing.T) {
	e := newEnv(t, false)
	e.post(t, "/new", lemonForm())

	_, body := e.get(t, "/r/example-lemon-cream-bars?x=2")
	wantContains(t, body, `id="start-cooking" href="/r/example-lemon-cream-bars/cook?x=2"`, `hx-select-oob="#start-cooking"`)

	_, body = e.get(t, "/r/example-lemon-cream-bars/cook?x=2")
	wantContains(t, body, "at 2×", "2 8-oz pkg cream cheese, softened", "6–8 Tbsp lemon juice", "not scaled",
		`data-scale="2"`, `href="/r/example-lemon-cream-bars/cook?step=2&amp;x=2"`, `href="/r/example-lemon-cream-bars?x=2"`)

	_, body = e.get(t, "/r/example-lemon-cream-bars/cook?x=%C2%BD&step=2")
	wantContains(t, body, "at ½×", "½ 8-oz pkg cream cheese")

	// At 1× nothing changes.
	_, body = e.get(t, "/r/example-lemon-cream-bars/cook")
	wantContains(t, body, "1 8-oz pkg cream cheese, softened", `href="/r/example-lemon-cream-bars/cook?step=2"`)
	if strings.Contains(body, "not scaled") || strings.Contains(body, "cook-scale") {
		t.Error("1× cook mode shows scaling marks")
	}
}
