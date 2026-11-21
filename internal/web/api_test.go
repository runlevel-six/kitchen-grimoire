// SPDX-License-Identifier: Apache-2.0

package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image/color"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/ingest"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

func (e *env) postBundle(t *testing.T, path, ctype string, body []byte) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Result().Body)
	return rec.Result(), string(b)
}

func decodeReport(t *testing.T, body string) ingest.Report {
	t.Helper()
	var rep ingest.Report
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("report: %v\n%s", err, body)
	}
	return rep
}

func TestBundleAPIAdminOnly(t *testing.T) {
	e := newEnv(t, false)
	res, body := e.get(t, "/api/v1/export")
	wantStatus(t, res, http.StatusForbidden, body)
	res, body = e.postBundle(t, "/api/v1/import/bundle", "application/zip", []byte("PK"))
	wantStatus(t, res, http.StatusForbidden, body)
	wantContains(t, body, `"error": "admin only"`)
}

func TestBundleAPIExportImport(t *testing.T) {
	src := newEnv(t, true)
	src.post(t, "/new", lemonForm())
	const slug = "/r/example-lemon-cream-bars"
	res, body := src.upload(t, slug+"/images", pngBytes(t, 300, 200, color.RGBA{200, 120, 40, 255}))
	wantStatus(t, res, http.StatusSeeOther, body)

	res, zipBody := src.get(t, "/api/v1/export")
	wantStatus(t, res, http.StatusOK, zipBody)
	if ct := res.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content type %q", ct)
	}
	cd := res.Header.Get("Content-Disposition")
	if !regexp.MustCompile(`^attachment; filename="kitchen-grimoire-export-\d{8}\.zip"$`).MatchString(cd) {
		t.Errorf("content disposition %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader([]byte(zipBody)), int64(len(zipBody)))
	if err != nil || len(zr.File) != 2 {
		t.Fatalf("export zip: %v, %d files", err, len(zr.File))
	}

	dst := newEnv(t, true)
	// Wrong content type and a broken zip are client errors.
	res, body = dst.postBundle(t, "/api/v1/import/bundle", "application/json", []byte(zipBody))
	wantStatus(t, res, http.StatusUnsupportedMediaType, body)
	res, body = dst.postBundle(t, "/api/v1/import/bundle", "application/zip", []byte("not a zip"))
	wantStatus(t, res, http.StatusBadRequest, body)
	wantContains(t, body, "invalid bundle")

	// Dry run: a report, and nothing saved.
	res, body = dst.postBundle(t, "/api/v1/import/bundle?dry_run=1", "application/zip", []byte(zipBody))
	wantStatus(t, res, http.StatusOK, body)
	rep := decodeReport(t, body)
	if !rep.DryRun || rep.Counts.Created != 1 || len(rep.Items) != 1 {
		t.Fatalf("dry run report: %s", body)
	}
	if _, n, _ := dst.svc.List(context.Background(), recipe.ListOptions{}); n != 0 {
		t.Fatalf("dry run saved %d recipes", n)
	}

	res, body = dst.postBundle(t, "/api/v1/import/bundle", "application/zip", []byte(zipBody))
	wantStatus(t, res, http.StatusOK, body)
	if rep := decodeReport(t, body); rep.DryRun || rep.Counts.Created != 1 || rep.Items[0].Slug != "example-lemon-cream-bars" {
		t.Fatalf("import report: %s", body)
	}
	res, body = dst.get(t, slug)
	wantStatus(t, res, http.StatusOK, body)
	wantContains(t, body, "Aunt Example", "Example Family Cookbook", `class="hero"`)

	// The same bundle again changes nothing.
	_, body = dst.postBundle(t, "/api/v1/import/bundle", "application/zip", []byte(zipBody))
	if rep := decodeReport(t, body); rep.Counts.Unchanged != 1 {
		t.Fatalf("re-import report: %s", body)
	}
}
