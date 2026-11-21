// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/runlevel-six/kitchen-grimoire/internal/imagestore"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

// MaxUpload is the largest photo accepted from a phone (§8.5).
const MaxUpload = 15 << 20

// ImageRepo is the image metadata storage the handlers need.
type ImageRepo interface {
	AddImage(ctx context.Context, recipeID string, im recipe.Image) (recipe.Image, error)
	RemoveImage(ctx context.Context, recipeID, imageID string) error
	SetPrimaryImage(ctx context.Context, recipeID, imageID string) error
}

// Images bundles the image file store and its metadata repository.
type Images struct {
	Files *imagestore.Store
	Repo  ImageRepo
}

func (h *Handler) registerImages(mux *http.ServeMux) {
	if h.images.Files == nil {
		return
	}
	mux.HandleFunc("GET /img/{file}", h.serveImage)
	mux.HandleFunc("POST /r/{slug}/images", h.uploadImage)
	mux.HandleFunc("POST /r/{slug}/images/{id}/delete", h.deleteImage)
	mux.HandleFunc("POST /r/{slug}/images/{id}/primary", h.primaryImage)
}

// serveImage serves "<sha>.jpg", "<sha>.png", or "<sha>-thumb.jpg". The
// content type comes from our own extension, never from the upload, and the
// files are content-addressed so they can be cached forever.
func (h *Handler) serveImage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	var path, ctype string
	switch {
	case strings.HasSuffix(name, "-thumb.jpg"):
		path, ctype = h.images.Files.ThumbPath(strings.TrimSuffix(name, "-thumb.jpg")), "image/jpeg"
	case strings.HasSuffix(name, ".jpg"):
		path, ctype = h.images.Files.Path(strings.TrimSuffix(name, ".jpg"), "jpg"), "image/jpeg"
	case strings.HasSuffix(name, ".png"):
		path, ctype = h.images.Files.Path(strings.TrimSuffix(name, ".png"), "png"), "image/png"
	}
	if path == "" { // malformed names never reach the filesystem
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
}

func (h *Handler) uploadImage(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if rec.Deleted() {
		h.fail(w, r, recipe.ErrDeleted)
		return
	}
	// Allow a little room for the multipart framing around the file.
	r.Body = http.MaxBytesReader(w, r.Body, MaxUpload+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "expected a file upload", http.StatusBadRequest)
		return
	}
	var stored imagestore.Stored
	found := false
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			h.uploadError(w, r, rec, "The upload was interrupted or too large (max 15 MB).")
			return
		}
		if part.FormName() != "photo" {
			continue
		}
		// Stream straight into the image store: no temp file, and the
		// store enforces the byte cap and re-encodes (stripping EXIF).
		stored, err = h.images.Files.Put(r.Context(), part, MaxUpload)
		switch {
		case errors.Is(err, imagestore.ErrTooLarge):
			h.uploadError(w, r, rec, "That photo is too large (max 15 MB).")
			return
		case errors.Is(err, imagestore.ErrUnsupported):
			h.uploadError(w, r, rec, "That file isn't a photo we can read. Use JPEG, PNG, GIF, or WebP.")
			return
		case err != nil:
			h.fail(w, r, err)
			return
		}
		found = true
		break
	}
	if !found {
		h.uploadError(w, r, rec, "Choose a photo first.")
		return
	}
	if _, err := h.images.Repo.AddImage(r.Context(), rec.ID, recipe.Image{
		SHA256: stored.SHA256, MIME: stored.MIME, Width: stored.Width, Height: stored.Height,
	}); err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/r/"+rec.Slug+"/edit#photos")
}

func (h *Handler) uploadError(w http.ResponseWriter, r *http.Request, rec *recipe.Recipe, msg string) {
	f := formFromRecipe(rec)
	f.Errors = map[string]string{"photo": msg}
	h.render(w, r, http.StatusUnprocessableEntity, editorPage(f))
}

func (h *Handler) deleteImage(w http.ResponseWriter, r *http.Request) {
	h.imageAction(w, r, h.images.Repo.RemoveImage)
}

func (h *Handler) primaryImage(w http.ResponseWriter, r *http.Request) {
	h.imageAction(w, r, h.images.Repo.SetPrimaryImage)
}

func (h *Handler) imageAction(w http.ResponseWriter, r *http.Request, fn func(context.Context, string, string) error) {
	rec, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := fn(r.Context(), rec.ID, r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrImageNotFound) {
			h.render(w, r, http.StatusNotFound, notFoundPage())
			return
		}
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/r/"+rec.Slug+"/edit#photos")
}

// --- view helpers ---

func imgURL(im *recipe.Image) string {
	ext := "jpg"
	if im.MIME == "image/png" {
		ext = "png"
	}
	return "/img/" + im.SHA256 + "." + ext
}

func thumbURL(sha string) string { return "/img/" + sha + "-thumb.jpg" }

func imgSrcset(im *recipe.Image) string {
	return thumbURL(im.SHA256) + " 480w, " + imgURL(im) + " " + strconv.Itoa(im.Width) + "w"
}
