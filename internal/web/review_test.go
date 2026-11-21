// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

func TestReviewQueue(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()
	r := &recipe.Recipe{
		Title:            "EXAMPLE CHEESE BALL",
		SourceKind:       recipe.SourceLegacy,
		OriginalText:     "EXAMPLE CHEESE BALL\\tAppetizers\\n1 8-oz pkg cream cheese\\nMix well.",
		ReviewReasons:    []string{"no_steps"},
		IngredientGroups: []recipe.IngredientGroup{{Ingredients: []recipe.Ingredient{{Raw: "1 8-oz pkg cream cheese"}}}},
	}
	if err := e.svc.Create(ctx, r, ""); err != nil {
		t.Fatal(err)
	}
	_, body := e.get(t, "/review")
	wantContains(t, body, "Review queue", "1 recipe", "EXAMPLE CHEESE BALL")
	_, body = e.get(t, "/r/"+r.Slug)
	wantContains(t, body, "Flagged for review: no_steps", "Mark reviewed", `class="original-panel"`, "Mix well.")

	res, body := e.post(t, "/r/"+r.Slug+"/reviewed", nil)
	wantStatus(t, res, http.StatusSeeOther, body)
	_, body = e.get(t, "/review")
	wantContains(t, body, "0 recipes")
	got, _ := e.svc.Get(ctx, r.Slug)
	if got.NeedsReview || len(got.ReviewReasons) != 0 || got.Revision != 1 {
		t.Errorf("after mark reviewed: %+v (no new revision expected)", got)
	}
}
