// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/imagestore"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

// adminServer starts the real admin handler over a small synthetic library.
func adminServer(t *testing.T) (*httptest.Server, *appMetrics) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.OpenDataDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	r := &recipe.Recipe{Title: "Invented Test Stew",
		IngredientGroups: []recipe.IngredientGroup{{Ingredients: []recipe.Ingredient{{Raw: "1 cup water"}}}},
		StepGroups:       []recipe.StepGroup{{Steps: []string{"Simmer."}}}}
	if err := recipe.NewService(st).Create(ctx, r, ""); err != nil {
		t.Fatal(err)
	}
	files, err := imagestore.New(filepath.Join(dir, "images"))
	if err != nil {
		t.Fatal(err)
	}
	am := newAppMetrics(st)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(adminHandler(log, am, db, files))
	t.Cleanup(srv.Close)
	return srv, am
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func TestBackupPullAndRestore(t *testing.T) {
	srv, _ := adminServer(t)
	out := t.TempDir()

	// Metrics: the backup gauge appears only after a successful backup.
	scrape := func() string {
		resp, err := http.Get(srv.URL + "/metrics")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	m := scrape()
	for _, want := range []string{"\ngrimoire_recipes 1\n", "grimoire_build_info{version=", `,go="go`} {
		if !strings.Contains(m, want) {
			t.Errorf("metrics missing %q:\n%s", want, m)
		}
	}
	if strings.Contains(m, "\ngrimoire_backup_last_success_timestamp ") {
		t.Error("backup gauge set before any backup")
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"backup-pull", "--from", srv.URL, "--out", out, "--keep", "2"}, &stdout, &stderr, noEnv); code != 0 {
		t.Fatalf("backup-pull exit %d: %s", code, stderr.String())
	}
	names := dirNames(t, out)
	if len(names) != 1 || !strings.HasPrefix(names[0], "grimoire-") || !strings.HasSuffix(names[0], ".tar.gz") {
		t.Fatalf("out dir = %v", names)
	}
	if !strings.Contains(stdout.String(), "1 recipes") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if !strings.Contains(scrape(), "\ngrimoire_backup_last_success_timestamp ") {
		t.Error("backup gauge not set after a backup")
	}

	// Restore it, then refuse a second restore without --force.
	data := filepath.Join(t.TempDir(), "data")
	file := filepath.Join(out, names[0])
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"restore", "--from", file, "--data-dir", data}, &stdout, &stderr, noEnv); code != 0 {
		t.Fatalf("restore exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 recipes (including trash)") {
		t.Errorf("restore stdout = %q", stdout.String())
	}
	if code := run([]string{"restore", "--from", file, "--data-dir", data}, &stdout, &stderr, noEnv); code != 1 ||
		!strings.Contains(stderr.String(), "not empty") {
		t.Errorf("second restore: exit %d, stderr %q", code, stderr.String())
	}
}

func TestBackupPullFailuresLeaveNoFile(t *testing.T) {
	good, _ := adminServer(t)
	resp, err := http.Get(good.URL + "/admin/backup")
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	handlers := map[string]http.HandlerFunc{
		"500": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		},
		"truncated": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(archive[:len(archive)/2])
		},
		"not a backup": func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "<html>hello</html>")
		},
	}
	for name, h := range handlers {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			out := t.TempDir()
			var stdout, stderr bytes.Buffer
			if code := run([]string{"backup-pull", "--from", srv.URL, "--out", out}, &stdout, &stderr, noEnv); code == 0 {
				t.Fatal("backup-pull succeeded")
			}
			if names := dirNames(t, out); len(names) != 0 {
				t.Errorf("files left behind: %v", names)
			}
		})
	}
}
