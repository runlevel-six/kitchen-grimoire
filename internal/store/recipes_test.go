// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

func newService(t *testing.T) (*recipe.Service, *Store) {
	t.Helper()
	db := openTemp(t)
	if _, err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	st := New(db)
	return recipe.NewService(st), st
}

func sample() *recipe.Recipe {
	return &recipe.Recipe{
		Title:       "  Example   Lemon Bars ",
		Description: "Tangy.\r\n\r\nInvented sample.",
		YieldText:   "24 bars",
		Servings:    24,
		PrepMin:     20,
		IngredientGroups: []recipe.IngredientGroup{
			{Name: "Crust", Ingredients: []recipe.Ingredient{{Raw: "2 cups flour"}, {Raw: "  "}, {Raw: "pinch of salt"}}},
			{Name: "Empty", Ingredients: []recipe.Ingredient{{Raw: ""}}},
			{Name: "Filling", Ingredients: []recipe.Ingredient{{Raw: "4 eggs"}}},
		},
		StepGroups: []recipe.StepGroup{
			{Steps: []string{"Mix.", "", "Bake 20 minutes."}},
		},
		Categories:    [][]string{{"Desserts", "Cookies"}, {"Desserts", "Cookies"}, {"Fruits"}},
		Tags:          []string{"Party", "party", "make-ahead"},
		ContributedBy: "Aunt Example",
		Collection:    "Example Family Cookbook",
	}
}

func TestCreateAndGet(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	r := sample()
	if err := svc.Create(ctx, r, ""); err != nil {
		t.Fatal(err)
	}
	if r.ID == "" || r.Slug != "example-lemon-bars" || r.Revision != 1 || r.SourceKind != recipe.SourceManual {
		t.Fatalf("after create: id=%q slug=%q rev=%d kind=%q", r.ID, r.Slug, r.Revision, r.SourceKind)
	}

	got, err := svc.Get(ctx, r.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Example Lemon Bars" {
		t.Errorf("title = %q (whitespace should be collapsed)", got.Title)
	}
	if got.Description != "Tangy.\n\nInvented sample." {
		t.Errorf("description = %q", got.Description)
	}
	if len(got.IngredientGroups) != 2 || got.IngredientGroups[0].Name != "Crust" ||
		len(got.IngredientGroups[0].Ingredients) != 2 || got.IngredientGroups[1].Name != "Filling" {
		t.Errorf("ingredient groups = %+v (blank lines and empty groups dropped)", got.IngredientGroups)
	}
	if len(got.StepGroups) != 1 || len(got.StepGroups[0].Steps) != 2 {
		t.Errorf("steps = %+v", got.StepGroups)
	}
	if len(got.Categories) != 2 || len(got.Categories[0]) != 2 || got.Categories[0][0] != "Desserts" ||
		got.Categories[0][1] != "Cookies" || got.Categories[1][0] != "Fruits" {
		t.Errorf("categories = %v", got.Categories)
	}
	if len(got.Tags) != 2 {
		t.Errorf("tags = %v (case-insensitive duplicates dropped)", got.Tags)
	}
	if got.ContributedBy != "Aunt Example" || got.Collection != "Example Family Cookbook" || got.Servings != 24 || got.PrepMin != 20 {
		t.Errorf("fields lost: %+v", got)
	}
	if got.CreatedAt.IsZero() || !got.CreatedAt.Equal(r.CreatedAt) {
		t.Errorf("created_at = %v, want %v", got.CreatedAt, r.CreatedAt)
	}

	// A second recipe with the same title gets a distinct slug.
	r2 := sample()
	if err := svc.Create(ctx, r2, ""); err != nil {
		t.Fatal(err)
	}
	if r2.Slug != "example-lemon-bars-2" {
		t.Errorf("second slug = %q", r2.Slug)
	}

	if _, err := svc.Get(ctx, "nope"); !errors.Is(err, recipe.ErrNotFound) {
		t.Errorf("missing slug: err = %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	svc, _ := newService(t)
	err := svc.Create(context.Background(), &recipe.Recipe{Title: "   ", SourceURL: "ftp://x"}, "")
	var ve *recipe.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want *ValidationError", err)
	}
	if ve.Fields["title"] == "" || ve.Fields["sourceUrl"] == "" {
		t.Errorf("fields = %v", ve.Fields)
	}
}

func TestUpdateRevisionsAndConflicts(t *testing.T) {
	ctx := context.Background()
	svc, st := newService(t)
	user, err := st.UpsertUser(ctx, "alice", "Alice Example", "")
	if err != nil {
		t.Fatal(err)
	}
	r := sample()
	if err := svc.Create(ctx, r, user.ID); err != nil {
		t.Fatal(err)
	}

	edit, _ := svc.Get(ctx, r.Slug)
	edit.Title = "Example Lemon Bars, Revised"
	edit.Tags = []string{"dessert"}
	edit.IngredientGroups = []recipe.IngredientGroup{{Ingredients: []recipe.Ingredient{{Raw: "1 cup sugar"}}}}
	if err := svc.Update(ctx, edit, user.ID); err != nil {
		t.Fatal(err)
	}
	if edit.Revision != 2 || edit.Slug != r.Slug {
		t.Errorf("after update: rev=%d slug=%q (slug must not change)", edit.Revision, edit.Slug)
	}
	got, _ := svc.Get(ctx, r.Slug)
	if got.Title != "Example Lemon Bars, Revised" || len(got.Tags) != 1 || got.IngredientCount() != 1 {
		t.Errorf("update not applied: %+v", got)
	}

	// A save based on revision 1 now conflicts and changes nothing.
	stale := sample()
	stale.ID, stale.Revision = r.ID, 1
	if err := svc.Update(ctx, stale, user.ID); !errors.Is(err, recipe.ErrConflict) {
		t.Fatalf("stale update err = %v, want ErrConflict", err)
	}
	if again, _ := svc.Get(ctx, r.Slug); again.Revision != 2 {
		t.Errorf("revision after conflict = %d, want 2", again.Revision)
	}

	hist, err := svc.History(ctx, r.ID)
	if err != nil || len(hist) != 2 || hist[0].Revision != 2 || hist[0].EditedBy != "Alice Example" {
		t.Fatalf("history = %+v, err %v", hist, err)
	}
	old, err := svc.Revision(ctx, r.ID, 1)
	if err != nil || old.Title != "Example Lemon Bars" || old.IngredientCount() != 3 {
		t.Fatalf("revision 1 = %+v, err %v", old, err)
	}

	restored, err := svc.RestoreRevision(ctx, r.ID, 1, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision != 3 {
		t.Errorf("restore made revision %d, want 3", restored.Revision)
	}
	cur, _ := svc.Get(ctx, r.Slug)
	if cur.Title != "Example Lemon Bars" || cur.IngredientCount() != 3 || len(cur.Tags) != 2 {
		t.Errorf("restored content wrong: %+v", cur)
	}
	if h, _ := svc.History(ctx, r.ID); len(h) != 3 {
		t.Errorf("history after restore has %d entries, want 3 (nothing lost)", len(h))
	}
}

// Two people saving the same revision at once: exactly one wins.
func TestConcurrentUpdates(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	r := sample()
	if err := svc.Create(ctx, r, ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e, err := svc.Get(ctx, r.Slug)
			if err != nil {
				errs[i] = err
				return
			}
			e.Revision = 1 // everyone edits from revision 1
			e.Notes = "edit"
			errs[i] = svc.Update(ctx, e, "")
		}(i)
	}
	wg.Wait()
	var ok, conflict int
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, recipe.ErrConflict):
			conflict++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || conflict != len(errs)-1 {
		t.Errorf("ok=%d conflict=%d, want 1 and %d", ok, conflict, len(errs)-1)
	}
}

func TestTrash(t *testing.T) {
	ctx := context.Background()
	svc, st := newService(t)
	a, b := sample(), sample()
	b.Title = "Example Chili"
	for _, r := range []*recipe.Recipe{a, b} {
		if err := svc.Create(ctx, r, ""); err != nil {
			t.Fatal(err)
		}
	}

	if err := svc.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, a.ID); !errors.Is(err, recipe.ErrDeleted) {
		t.Errorf("double delete err = %v", err)
	}
	live, n, _ := svc.List(ctx, recipe.ListOptions{})
	trash, tn, _ := svc.List(ctx, recipe.ListOptions{Deleted: true})
	if n != 1 || live[0].ID != b.ID || tn != 1 || trash[0].ID != a.ID || trash[0].DeletedAt == nil {
		t.Fatalf("live=%v trash=%v", live, trash)
	}

	// A trashed recipe can't be edited.
	e, _ := svc.GetByID(ctx, a.ID)
	if err := svc.Update(ctx, e, ""); !errors.Is(err, recipe.ErrDeleted) {
		t.Errorf("update in trash err = %v", err)
	}

	if err := svc.Restore(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, n, _ := svc.List(ctx, recipe.ListOptions{}); n != 2 {
		t.Errorf("after restore live = %d", n)
	}

	// Purge only removes recipes trashed before the cutoff.
	if err := svc.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.PurgeDeleted(ctx, time.Now().Add(-time.Hour)); n != 0 {
		t.Errorf("purged %d recent recipes, want 0", n)
	}
	if n, _ := st.PurgeDeleted(ctx, time.Now().Add(time.Hour)); n != 1 {
		t.Errorf("purged %d, want 1", n)
	}
	if _, err := svc.GetByID(ctx, a.ID); !errors.Is(err, recipe.ErrNotFound) {
		t.Errorf("purged recipe still found: %v", err)
	}
	if _, err := svc.History(ctx, a.ID); !errors.Is(err, recipe.ErrNotFound) {
		t.Errorf("purged recipe history err = %v", err)
	}
	if err := svc.Delete(ctx, "missing"); !errors.Is(err, recipe.ErrNotFound) {
		t.Errorf("delete missing err = %v", err)
	}
}

func TestListOrderingAndFilters(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	for _, title := range []string{"Banana Bread", "apple pie", "Chili"} {
		r := &recipe.Recipe{Title: title}
		if title == "Chili" {
			r.ReviewReasons = []string{"no_ingredients"}
		}
		if err := svc.Create(ctx, r, ""); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct timestamps
	}
	byTitle, total, _ := svc.List(ctx, recipe.ListOptions{Order: "title"})
	if total != 3 || byTitle[0].Title != "apple pie" || byTitle[2].Title != "Chili" {
		t.Errorf("by title: %v", byTitle)
	}
	recent, _, _ := svc.List(ctx, recipe.ListOptions{})
	if recent[0].Title != "Chili" {
		t.Errorf("most recent first: %v", recent)
	}
	review, n, _ := svc.List(ctx, recipe.ListOptions{NeedsReview: true})
	if n != 1 || review[0].Title != "Chili" || !review[0].NeedsReview {
		t.Errorf("needs review: %v", review)
	}
	page, total, _ := svc.List(ctx, recipe.ListOptions{Order: "title", Limit: 1, Offset: 1})
	if total != 3 || len(page) != 1 || page[0].Title != "Banana Bread" {
		t.Errorf("paging: %v", page)
	}
}

func TestUpsertUser(t *testing.T) {
	ctx := context.Background()
	_, st := newService(t)
	u1, err := st.UpsertUser(ctx, "bob", "Bob", "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	u2, err := st.UpsertUser(ctx, "bob", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if u1.ID != u2.ID || u2.DisplayName != "Bob" || u2.Email != "bob@example.com" {
		t.Errorf("second upsert = %+v, want same id and kept name/email", u2)
	}
	u3, _ := st.UpsertUser(ctx, "bob", "Robert", "")
	if u3.DisplayName != "Robert" {
		t.Errorf("name not updated: %+v", u3)
	}
}
