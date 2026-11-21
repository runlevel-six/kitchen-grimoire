// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func pngBytes(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (e *env) upload(t *testing.T, path string, data []byte) (*http.Response, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("photo", "IMG_0001.HEIC") // the client's name and type are ignored
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Result().Body)
	return rec.Result(), string(b)
}

func TestPhotos(t *testing.T) {
	e := newEnv(t, false)
	e.post(t, "/new", lemonForm())
	const slug = "/r/example-lemon-cream-bars"

	res, body := e.upload(t, slug+"/images", pngBytes(t, 800, 600, color.RGBA{200, 120, 40, 255}))
	wantStatus(t, res, http.StatusSeeOther, body)

	_, body = e.get(t, slug)
	hero := regexp.MustCompile(`class="hero" src="(/img/[0-9a-f]{64}\.jpg)"`).FindStringSubmatch(body)
	if hero == nil {
		t.Fatalf("no hero image in recipe page")
	}
	res, img := e.get(t, hero[1])
	wantStatus(t, res, 200, "")
	if res.Header.Get("Content-Type") != "image/jpeg" || len(img) == 0 {
		t.Errorf("image served as %q (%d bytes)", res.Header.Get("Content-Type"), len(img))
	}
	thumb := hero[1][:len(hero[1])-len(".jpg")] + "-thumb.jpg"
	if res, _ := e.get(t, thumb); res.StatusCode != 200 {
		t.Errorf("thumb status %d", res.StatusCode)
	}
	_, body = e.get(t, "/")
	if !regexp.MustCompile(`<img src="/img/[0-9a-f]{64}-thumb\.jpg"`).MatchString(body) {
		t.Error("home card has no thumbnail")
	}

	// A second photo, then make it the main one, then remove the first.
	e.upload(t, slug+"/images", pngBytes(t, 400, 400, color.RGBA{0, 0, 255, 255}))
	rec, _ := e.svc.Get(context.Background(), "example-lemon-cream-bars")
	if len(rec.Images) != 2 || !rec.Images[0].IsPrimary {
		t.Fatalf("images = %+v", rec.Images)
	}
	res, body = e.post(t, slug+"/images/"+rec.Images[1].ID+"/primary", nil)
	wantStatus(t, res, http.StatusSeeOther, body)
	e.post(t, slug+"/images/"+rec.Images[0].ID+"/delete", nil)
	rec, _ = e.svc.Get(context.Background(), "example-lemon-cream-bars")
	if len(rec.Images) != 1 || !rec.Images[0].IsPrimary || rec.Images[0].Width != 400 {
		t.Errorf("after primary+remove: %+v", rec.Images)
	}
	if res, _ := e.post(t, slug+"/images/nope/delete", nil); res.StatusCode != 404 {
		t.Errorf("remove missing image: %d", res.StatusCode)
	}

	// Not a picture.
	res, body = e.upload(t, slug+"/images", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"))
	wantStatus(t, res, http.StatusUnprocessableEntity, body)
	wantContains(t, body, "a photo we can read")

	// Malformed and traversal names never touch the filesystem.
	for _, p := range []string{"/img/..%2F..%2Fetc%2Fpasswd", "/img/abc.jpg", "/img/" + string(bytes.Repeat([]byte("g"), 64)) + ".jpg"} {
		if res, _ := e.get(t, p); res.StatusCode != 404 {
			t.Errorf("%s: %d, want 404", p, res.StatusCode)
		}
	}
}
