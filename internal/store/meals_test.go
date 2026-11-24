// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

func TestMeals(t *testing.T) {
	ctx := context.Background()
	svc, st := newService(t)
	u, err := st.UpsertUser(ctx, "cook", "Cook", "")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, title := range []string{"Example Turkey", "Example Stuffing", "Example Pie"} {
		r := sample()
		r.Title = title
		if err := svc.Create(ctx, r, u.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}

	mealID, err := st.CreateMeal(ctx, "Thanksgiving", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if err := st.AddToMeal(ctx, mealID, id, float64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	// Adding again only changes the factor: once per meal.
	if err := st.AddToMeal(ctx, mealID, ids[0], 0.5); err != nil {
		t.Fatal(err)
	}
	m, err := st.GetMeal(ctx, mealID)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 3 || m.Entries[0].Recipe.Title != "Example Turkey" || m.Entries[0].Factor != 0.5 ||
		m.Entries[2].Factor != 3 {
		t.Fatalf("entries = %+v", m.Entries)
	}

	if err := st.MoveInMeal(ctx, mealID, ids[2], -1); err != nil {
		t.Fatal(err)
	}
	if err := st.MoveInMeal(ctx, mealID, ids[0], -1); err != nil { // already first: no-op
		t.Fatal(err)
	}
	if err := st.SetMealFactor(ctx, mealID, ids[1], 4); err != nil {
		t.Fatal(err)
	}
	m, _ = st.GetMeal(ctx, mealID)
	if got := []string{m.Entries[0].Recipe.Title, m.Entries[1].Recipe.Title, m.Entries[2].Recipe.Title}; got[1] != "Example Pie" || got[2] != "Example Stuffing" {
		t.Errorf("order = %q", got)
	}
	if m.Entries[2].Factor != 4 {
		t.Errorf("stuffing factor = %v", m.Entries[2].Factor)
	}

	// A recipe can be in any number of meals.
	other, _ := st.CreateMeal(ctx, "Christmas", u.ID)
	if err := st.AddToMeal(ctx, other, ids[2], 2); err != nil {
		t.Fatal(err)
	}
	in, err := st.MealsForRecipe(ctx, ids[2])
	if err != nil || len(in) != 2 {
		t.Fatalf("MealsForRecipe = %+v, %v", in, err)
	}
	for _, ms := range in {
		if ms.Factor != map[string]float64{mealID: 3, other: 2}[ms.ID] {
			t.Errorf("%s factor = %v", ms.Name, ms.Factor)
		}
	}
	notIn, _ := st.MealsForRecipe(ctx, ids[0])
	for _, ms := range notIn {
		if ms.ID == other && ms.Factor != 0 {
			t.Errorf("turkey shows as in Christmas: %+v", ms)
		}
	}

	if err := st.RemoveFromMeal(ctx, mealID, ids[1]); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveFromMeal(ctx, mealID, ids[1]); !errors.Is(err, recipe.ErrNotFound) {
		t.Errorf("second remove: %v", err)
	}
	if err := st.UpdateMeal(ctx, mealID, "Thanksgiving 2026", "Dinner at 3"); err != nil {
		t.Fatal(err)
	}

	// A trashed recipe stays in the meal (marked); purging it removes it.
	if err := svc.Delete(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	m, _ = st.GetMeal(ctx, mealID)
	if m.Name != "Thanksgiving 2026" || m.Notes != "Dinner at 3" || len(m.Entries) != 2 || m.Entries[0].Recipe.DeletedAt == nil {
		t.Fatalf("after trash: %+v", m)
	}
	if _, err := st.PurgeDeleted(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	m, _ = st.GetMeal(ctx, mealID)
	if len(m.Entries) != 1 {
		t.Errorf("after purge: %+v", m.Entries)
	}

	// Delete and undo; a deleted meal can't be changed and isn't listed.
	if err := st.DeleteMeal(ctx, mealID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddToMeal(ctx, mealID, ids[1], 1); !errors.Is(err, recipe.ErrNotFound) {
		t.Errorf("add to deleted meal: %v", err)
	}
	list, _ := st.ListMeals(ctx)
	if len(list) != 1 || list[0].ID != other || list[0].Recipes != 1 {
		t.Errorf("ListMeals = %+v", list)
	}
	if err := st.RestoreMeal(ctx, mealID); err != nil {
		t.Fatal(err)
	}
	if list, _ = st.ListMeals(ctx); len(list) != 2 {
		t.Errorf("after restore = %+v", list)
	}

	// Deleted meals are purged with the trash.
	if err := st.DeleteMeal(ctx, mealID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PurgeDeleted(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetMeal(ctx, mealID); !errors.Is(err, recipe.ErrNotFound) {
		t.Errorf("purged meal: %v", err)
	}
	if _, err := st.GetMeal(ctx, "nope"); !errors.Is(err, recipe.ErrNotFound) {
		t.Errorf("missing meal: %v", err)
	}
}
