// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// fakeImage returns image metadata with a distinct, well-formed hash. The
// store only records metadata; the files live in the image store.
func fakeImage(c byte) recipe.Image {
	return recipe.Image{SHA256: strings.Repeat(string(c), 64), MIME: "image/jpeg", Width: 10, Height: 8}
}

func imageSHAs(r *recipe.Recipe) []string {
	var out []string
	for _, im := range r.Images {
		out = append(out, im.SHA256[:1])
	}
	return out
}

func TestCreateImportedKeepsDatesImagesAndImportRevision(t *testing.T) {
	ctx := context.Background()
	svc, st := newService(t)
	created := time.Date(2001, 2, 3, 4, 5, 6, 789_000_000, time.UTC)
	modified := created.Add(48 * time.Hour)
	r := sample()
	r.ImportKey = "legacy:example:0001"
	r.SourceKind = recipe.SourceLegacy
	r.CreatedAt, r.UpdatedAt = created, modified
	r.Images = []recipe.Image{fakeImage('a'), fakeImage('b')}
	if err := svc.CreateImported(ctx, r, ""); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRecipeByImportKey(ctx, "legacy:example:0001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 1 || got.ImportRevision != 1 {
		t.Errorf("revision %d / import revision %d, want 1 / 1", got.Revision, got.ImportRevision)
	}
	if !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(modified) {
		t.Errorf("dates %v / %v, want %v / %v", got.CreatedAt, got.UpdatedAt, created, modified)
	}
	if s := imageSHAs(got); !slices.Equal(s, []string{"a", "b"}) || !got.Images[0].IsPrimary || got.Images[1].IsPrimary {
		t.Errorf("images = %v (primary flags %v, %v)", s, got.Images[0].IsPrimary, got.Images[1].IsPrimary)
	}
	// The revision snapshot records the images too.
	old, err := svc.Revision(ctx, got.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Images) != 2 {
		t.Errorf("snapshot has %d images, want 2", len(old.Images))
	}

	// Plain Create never records images or an import revision.
	m := sample()
	m.Title = "Manual"
	m.Images = []recipe.Image{fakeImage('c')}
	m.ImportRevision = 7
	if err := svc.Create(ctx, m, ""); err != nil {
		t.Fatal(err)
	}
	gm, _ := svc.GetByID(ctx, m.ID)
	if len(gm.Images) != 0 || gm.ImportRevision != 0 {
		t.Errorf("Create stored images %v / import revision %d", gm.Images, gm.ImportRevision)
	}
}

func TestUpdateImportedMovesImportRevisionOnlyThere(t *testing.T) {
	ctx := context.Background()
	svc, st := newService(t)
	r := sample()
	r.ImportKey = "k1"
	r.Images = []recipe.Image{fakeImage('a')}
	if err := svc.CreateImported(ctx, r, ""); err != nil {
		t.Fatal(err)
	}

	// Import update without new images keeps the image rows.
	r.Notes = "second import"
	if err := svc.UpdateImported(ctx, r, "", false); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetRecipeByImportKey(ctx, "k1")
	if got.Revision != 2 || got.ImportRevision != 2 || got.Notes != "second import" {
		t.Fatalf("after import update: rev %d, import rev %d, notes %q", got.Revision, got.ImportRevision, got.Notes)
	}
	if s := imageSHAs(got); !slices.Equal(s, []string{"a"}) {
		t.Errorf("images = %v, want [a]", s)
	}

	// Replacing images.
	got.Images = []recipe.Image{fakeImage('c'), fakeImage('b')}
	if err := svc.UpdateImported(ctx, got, "", true); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetRecipeByImportKey(ctx, "k1")
	if s := imageSHAs(got); !slices.Equal(s, []string{"c", "b"}) || !got.Images[0].IsPrimary {
		t.Errorf("images = %v, want [c b] with c primary", s)
	}
	if got.ImportRevision != 3 {
		t.Errorf("import revision %d, want 3", got.ImportRevision)
	}

	// A human edit moves Revision but not ImportRevision, and keeps the key.
	got.Title = "Edited by hand"
	got.ImportKey, got.ImportRevision = "changed", 99 // the form can't change these
	if err := svc.Update(ctx, got, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetRecipeByImportKey(ctx, "k1")
	if got.Revision != 4 || got.ImportRevision != 3 {
		t.Errorf("after edit: rev %d, import rev %d, want 4 / 3", got.Revision, got.ImportRevision)
	}

	// A stale import update is a conflict and writes nothing.
	stale := *got
	stale.Revision = 3
	if err := svc.UpdateImported(ctx, &stale, "", false); !errors.Is(err, recipe.ErrConflict) {
		t.Errorf("stale import update: err = %v, want ErrConflict", err)
	}
}

func TestGetRecipeByCanonicalURLAndLiveIDs(t *testing.T) {
	ctx := context.Background()
	svc, st := newService(t)
	const u = "https://example.com/r/lemon-bars"
	if _, err := st.GetRecipeByCanonicalURL(ctx, u); !errors.Is(err, recipe.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := st.GetRecipeByCanonicalURL(ctx, ""); !errors.Is(err, recipe.ErrNotFound) {
		t.Fatalf("empty url: err = %v, want ErrNotFound", err)
	}

	first := sample()
	first.CanonicalURL = u
	if err := svc.Create(ctx, first, ""); err != nil {
		t.Fatal(err)
	}
	second := sample()
	second.CanonicalURL = u
	if err := svc.Create(ctx, second, ""); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRecipeByCanonicalURL(ctx, u)
	if err != nil || got.ID != first.ID {
		t.Fatalf("got %v, %v; want the oldest (%s)", got, err, first.ID)
	}
	// A trashed match loses to a live one.
	if err := svc.Delete(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetRecipeByCanonicalURL(ctx, u); got.ID != second.ID {
		t.Errorf("got %s, want the live recipe %s", got.ID, second.ID)
	}

	ids, err := st.LiveRecipeIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []string{second.ID}) {
		t.Errorf("live ids = %v, want [%s]", ids, second.ID)
	}
}
