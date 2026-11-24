// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenDataDir(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mapFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range files {
		m["m/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func TestEmbeddedMigrationsLoad(t *testing.T) {
	ms, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].Version != 1 || ms[0].Name != "init" {
		t.Fatalf("migrations = %+v", ms)
	}
}

func TestMigrateFreshAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)

	done, err := Migrate(ctx, db)
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	all, _ := Migrations()
	if len(done) != len(all) {
		t.Fatalf("applied %d, want %d", len(done), len(all))
	}

	done, err = Migrate(ctx, db)
	if err != nil || len(done) != 0 {
		t.Fatalf("second Migrate: applied %d, err %v; want 0, nil", len(done), err)
	}

	st, err := Status(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		if s.AppliedAt == "" {
			t.Errorf("%04d_%s not marked applied", s.Version, s.Name)
		}
	}

	for _, table := range []string{"users", "recipes", "ingredient_groups", "ingredients", "step_groups", "steps",
		"categories", "recipe_categories", "tags", "recipe_tags", "images", "revisions", "favorites", "api_tokens", "meals", "meal_recipes"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (err %v)", table, err)
		}
	}
}

func TestMigrateRules(t *testing.T) {
	ctx := context.Background()
	one := "CREATE TABLE a (x INTEGER) STRICT;"
	two := "CREATE TABLE b (y INTEGER) STRICT;"

	t.Run("applies in order and resumes", func(t *testing.T) {
		db := openTemp(t)
		if _, err := migrate(ctx, db, mapFS(map[string]string{"0001_a.sql": one}), "m"); err != nil {
			t.Fatal(err)
		}
		done, err := migrate(ctx, db, mapFS(map[string]string{"0001_a.sql": one, "0002_b.sql": two}), "m")
		if err != nil || len(done) != 1 || done[0].Version != 2 {
			t.Fatalf("done=%+v err=%v", done, err)
		}
	})

	t.Run("edited migration is refused", func(t *testing.T) {
		db := openTemp(t)
		if _, err := migrate(ctx, db, mapFS(map[string]string{"0001_a.sql": one}), "m"); err != nil {
			t.Fatal(err)
		}
		_, err := migrate(ctx, db, mapFS(map[string]string{"0001_a.sql": one + " -- edited"}), "m")
		if !errors.Is(err, ErrMigrationChanged) {
			t.Fatalf("err = %v, want ErrMigrationChanged", err)
		}
	})

	t.Run("newer database is refused", func(t *testing.T) {
		db := openTemp(t)
		if _, err := migrate(ctx, db, mapFS(map[string]string{"0001_a.sql": one, "0002_b.sql": two}), "m"); err != nil {
			t.Fatal(err)
		}
		_, err := migrate(ctx, db, mapFS(map[string]string{"0001_a.sql": one}), "m")
		if !errors.Is(err, ErrDatabaseNewer) {
			t.Fatalf("err = %v, want ErrDatabaseNewer", err)
		}
	})

	t.Run("failed migration rolls back", func(t *testing.T) {
		db := openTemp(t)
		bad := "CREATE TABLE c (z INTEGER) STRICT; INSERT INTO nope VALUES (1);"
		_, err := migrate(ctx, db, mapFS(map[string]string{"0001_a.sql": one, "0002_c.sql": bad}), "m")
		if err == nil {
			t.Fatal("bad migration succeeded")
		}
		var n int
		_ = db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name = 'c'`).Scan(&n)
		if n != 0 {
			t.Error("table c exists: the failed migration was not rolled back")
		}
		_ = db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n)
		if n != 1 {
			t.Errorf("schema_migrations has %d rows, want 1 (only the good migration)", n)
		}
	})

	t.Run("bad names and gaps are refused", func(t *testing.T) {
		for name, files := range map[string]map[string]string{
			"bad name": {"1_a.sql": one},
			"gap":      {"0001_a.sql": one, "0003_b.sql": two},
		} {
			if _, err := loadMigrations(mapFS(files), "m"); err == nil {
				t.Errorf("%s: loadMigrations succeeded", name)
			}
		}
	})
}

// The schema enforces the rules in docs/DESIGN.md §7 itself, so a bug in
// Go code can't quietly store bad data.
func TestSchemaConstraints(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	if _, err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	now := Now()
	insertRecipe := func(id, kind, raw string) error {
		var rawArg any
		if raw != "" {
			rawArg = raw
		}
		_, err := db.Exec(`INSERT INTO recipes (id, slug, title, source_kind, raw_source, created_at, updated_at)
			VALUES (?, ?, 'Example', ?, ?, ?, ?)`, id, "slug-"+id, kind, rawArg, now, now)
		return err
	}

	if err := insertRecipe("r1", "manual", ""); err != nil {
		t.Fatalf("valid recipe rejected: %v", err)
	}

	mustFail := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
	mustFail("unknown source_kind", insertRecipe("r2", "scraped", ""))
	mustFail("invalid raw_source JSON", insertRecipe("r3", "url", "{not json"))
	_, err := db.Exec(`UPDATE recipes SET review_reasons = '{"a":1}' WHERE id = 'r1'`)
	mustFail("review_reasons that is not an array", err)
	_, err = db.Exec(`UPDATE recipes SET servings = 'lots' WHERE id = 'r1'`)
	mustFail("text in a REAL column (STRICT)", err)
	_, err = db.Exec(`INSERT INTO ingredient_groups (recipe_id, position) VALUES ('missing', 0)`)
	mustFail("group for a missing recipe (foreign key)", err)

	// Purging a recipe removes its children.
	res, err := db.Exec(`INSERT INTO ingredient_groups (recipe_id, position) VALUES ('r1', 0)`)
	if err != nil {
		t.Fatal(err)
	}
	gid, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO ingredients (group_id, position, raw_text) VALUES (?, 0, '1 cup sugar')`, gid); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO ingredients (group_id, position, raw_text, qty_min, qty_max) VALUES (?, 1, 'x', 4, 3)`, gid)
	mustFail("qty_max below qty_min", err)
	if _, err := db.Exec(`DELETE FROM recipes WHERE id = 'r1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM ingredients`).Scan(&n)
	if n != 0 {
		t.Errorf("%d ingredients left after purge, want 0 (cascade)", n)
	}

	// The same category name may exist at two levels; the path is what's unique.
	if _, err := db.Exec(`INSERT INTO categories (id, name, slug, path) VALUES (1, 'Fruits', 'fruits', 'fruits'),
		(2, 'Desserts', 'desserts', 'desserts')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO categories (parent_id, name, slug, path) VALUES (2, 'Fruits', 'fruits', 'desserts/fruits')`); err != nil {
		t.Errorf("same name at another level rejected: %v", err)
	}
	_, err = db.Exec(`INSERT INTO categories (name, slug, path) VALUES ('Fruits', 'fruits', 'fruits')`)
	mustFail("duplicate category path", err)

	// Only one primary image per recipe.
	if err := insertRecipe("r4", "manual", ""); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 64)
	img := `INSERT INTO images (id, recipe_id, sha256, mime, width, height, position, is_primary, created_at)
		VALUES (?, 'r4', ?, 'image/jpeg', 10, 10, ?, 1, ?)`
	if _, err := db.Exec(img, "i1", sha, 0, now); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(img, "i2", sha, 1, now)
	mustFail("second primary image", err)
}

func TestNowFormat(t *testing.T) {
	s := Now()
	if len(s) != len("2006-01-02T15:04:05.000Z") || !strings.HasSuffix(s, "Z") {
		t.Errorf("Now() = %q", s)
	}
}
