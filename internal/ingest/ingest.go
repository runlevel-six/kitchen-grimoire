// SPDX-License-Identifier: Apache-2.0

// Package ingest is the one ingest pipeline (DESIGN.md §8): every source
// produces a draft in the interchange shape, and this package normalizes,
// validates, dedupes, and persists it. This file holds the bulk bundle
// importer (§8.4) and the full export (P2.10).
package ingest

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"github.com/runlevel-six/kitchen-grimoire/internal/exportfmt"
	"github.com/runlevel-six/kitchen-grimoire/internal/imagestore"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// MaxImageBytes caps one image inside a bundle, like a phone upload (§8.5).
const MaxImageBytes = 15 << 20

// Repo is the read access the importer needs; internal/store implements
// it. Writes go through recipe.Service so the usual rules apply.
type Repo interface {
	GetRecipe(ctx context.Context, id string) (*recipe.Recipe, error)
	GetRecipeByImportKey(ctx context.Context, key string) (*recipe.Recipe, error)
	GetRecipeByCanonicalURL(ctx context.Context, u string) (*recipe.Recipe, error)
	LiveRecipeIDs(ctx context.Context) ([]string, error)
}

// Importer runs bulk imports and exports.
type Importer struct {
	svc  *recipe.Service
	repo Repo
	// images re-encodes and stores bundle images (Put), or only re-encodes
	// them for a dry run (Process), and serves files to exports (Open).
	images *imagestore.Store

	// mu serializes imports: two at once could both decide to create the
	// same import key.
	mu sync.Mutex
}

// New returns an Importer.
func New(svc *recipe.Service, repo Repo, images *imagestore.Store) *Importer {
	return &Importer{svc: svc, repo: repo, images: images}
}

// Options control an import.
type Options struct {
	// DryRun computes the full report and writes nothing: no recipes, no
	// revisions, no image files.
	DryRun bool
	// EditorID is the user recorded on created revisions ("" for none).
	EditorID string
}

// Outcome is what happened to one bundle item.
type Outcome string

const (
	Created         Outcome = "created"
	Updated         Outcome = "updated"
	Unchanged       Outcome = "unchanged"
	SkippedConflict Outcome = "skipped_conflict"
	Invalid         Outcome = "invalid"
)

// Counts tallies outcomes.
type Counts struct {
	Created         int `json:"created"`
	Updated         int `json:"updated"`
	SkippedConflict int `json:"skipped_conflict"`
	Invalid         int `json:"invalid"`
	Unchanged       int `json:"unchanged"`
}

// ItemReport is one bundle item's result. Index is the item's 0-based line
// in recipes.jsonl. Reasons explain invalid and skipped items; Warnings
// note things that did not stop the item (an unreadable image, say).
type ItemReport struct {
	Index     int      `json:"index"`
	ImportKey string   `json:"importKey,omitempty"`
	Title     string   `json:"title,omitempty"`
	Outcome   Outcome  `json:"outcome"`
	Slug      string   `json:"slug,omitempty"`
	Reasons   []string `json:"reasons,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

// Report is the whole import's result (§8.4).
type Report struct {
	DryRun bool         `json:"dryRun"`
	Counts Counts       `json:"counts"`
	Items  []ItemReport `json:"items"`
}

func (rep *Report) add(it ItemReport) {
	switch it.Outcome {
	case Created:
		rep.Counts.Created++
	case Updated:
		rep.Counts.Updated++
	case Unchanged:
		rep.Counts.Unchanged++
	case SkippedConflict:
		rep.Counts.SkippedConflict++
	case Invalid:
		rep.Counts.Invalid++
	}
	rep.Items = append(rep.Items, it)
}

// ImportBundle imports every item of a bundle and reports what happened.
//
// Idempotency (§8.4) is keyed on x-grimoire.importKey, else on the
// canonical URL:
//   - no recipe has the key: create it, with ImportRevision = 1;
//   - the stored recipe has the same content: unchanged, nothing written;
//   - the stored recipe is untouched since its last import (Revision ==
//     ImportRevision): update it in place, as a new revision;
//   - otherwise a person edited it since (or it never came from an
//     import, or it is in the trash): skip it and report a conflict
//     (OQ-09).
//
// Invalid items are reported and skipped; the rest carry on. Each item is
// saved in its own transaction, so an error part-way leaves the earlier
// items imported; the returned error is then non-nil and the report
// covers the items done so far.
//
// Dry run takes exactly the same path, deciding every outcome against the
// live database; only the final writes are skipped, and images are
// re-encoded in memory (imagestore.Process) rather than stored. So a dry
// run's report matches what a real run would do, unless the library
// changes in between.
func (im *Importer) ImportBundle(ctx context.Context, b *exportfmt.Bundle, opts Options) (Report, error) {
	im.mu.Lock()
	defer im.mu.Unlock()

	rep := Report{DryRun: opts.DryRun, Items: []ItemReport{}}
	run := &importRun{im: im, b: b, opts: opts, images: map[string]imageResult{}, keys: map[string]int{}}
	for _, item := range b.Items {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		it, err := run.item(ctx, item)
		if err != nil {
			return rep, fmt.Errorf("item %d: %w", item.Index, err)
		}
		rep.add(it)
	}
	return rep, nil
}

// importRun holds the state of one ImportBundle call.
type importRun struct {
	im     *Importer
	b      *exportfmt.Bundle
	opts   Options
	images map[string]imageResult // bundle path -> stored image, so each is encoded once
	keys   map[string]int         // dedupe key -> first item index in this bundle
}

type imageResult struct {
	img recipe.Image
	err error
}

// item imports one bundle line. Problems with the item itself become the
// report; only infrastructure failures (database, disk) are returned.
func (run *importRun) item(ctx context.Context, item exportfmt.Item) (ItemReport, error) {
	rep := ItemReport{Index: item.Index}
	invalid := func(reasons ...string) (ItemReport, error) {
		rep.Outcome, rep.Reasons = Invalid, reasons
		return rep, nil
	}
	if item.Err != nil {
		return invalid(item.Err.Error())
	}
	r, warnings, err := exportfmt.FromJSONLD(item.Object)
	rep.Warnings = warnings
	if err != nil {
		return invalid(err.Error())
	}

	// normalize → validate
	recipe.Normalize(r)
	rep.Title, rep.ImportKey = r.Title, r.ImportKey
	if err := recipe.Validate(r); err != nil {
		return invalid(validationReasons(err)...)
	}

	// The same key twice in one bundle is ambiguous; keep the first.
	dedupe := dedupeKey(r)
	if dedupe != "" {
		if first, dup := run.keys[dedupe]; dup {
			return invalid(fmt.Sprintf("duplicate of item %d (same import key or canonical URL)", first))
		}
		run.keys[dedupe] = item.Index
	}

	// Images: re-encode each (stripping metadata, §8.5) and swap the
	// bundle references for what the image store holds.
	var imgs []recipe.Image
	for _, ref := range r.Images {
		res, err := run.image(ctx, ref)
		if err != nil {
			rep.Warnings = append(rep.Warnings, err.Error())
			continue
		}
		imgs = append(imgs, res)
	}
	r.Images = imgs

	// dedupe
	existing, err := run.im.lookup(ctx, r)
	if err != nil {
		return rep, err
	}
	if existing == nil {
		return run.create(ctx, r, rep)
	}
	rep.Slug = existing.Slug
	switch {
	case existing.Deleted():
		rep.Outcome = SkippedConflict
		rep.Reasons = []string{"the matching recipe is in the trash; restore or purge it first"}
		return rep, nil
	case sameContent(existing, r):
		rep.Outcome = Unchanged
		return rep, nil
	case existing.ImportRevision == 0:
		rep.Outcome = SkippedConflict
		rep.Reasons = []string{"a recipe with this key exists but did not come from a bulk import; it was left alone"}
		return rep, nil
	case existing.Revision != existing.ImportRevision:
		rep.Outcome = SkippedConflict
		rep.Reasons = []string{fmt.Sprintf("edited since it was imported (revision %d, imported as revision %d); it was left alone",
			existing.Revision, existing.ImportRevision)}
		return rep, nil
	}
	return run.update(ctx, existing, r, rep)
}

func (run *importRun) create(ctx context.Context, r *recipe.Recipe, rep ItemReport) (ItemReport, error) {
	rep.Outcome = Created
	if run.opts.DryRun {
		return rep, nil
	}
	if err := run.im.svc.CreateImported(ctx, r, run.opts.EditorID); err != nil {
		return saveError(rep, err)
	}
	rep.Slug = r.Slug
	return rep, nil
}

func (run *importRun) update(ctx context.Context, existing, r *recipe.Recipe, rep ItemReport) (ItemReport, error) {
	rep.Outcome = Updated
	if run.opts.DryRun {
		return rep, nil
	}
	r.ID = existing.ID
	r.Revision = existing.Revision // optimistic concurrency against what we compared
	replace := !sameImages(exportfmt.OrderedImages(existing), r.Images)
	if err := run.im.svc.UpdateImported(ctx, r, run.opts.EditorID, replace); err != nil {
		return saveError(rep, err)
	}
	return rep, nil
}

// saveError turns errors that are about the item into report outcomes.
func saveError(rep ItemReport, err error) (ItemReport, error) {
	var ve *recipe.ValidationError
	switch {
	case errors.As(err, &ve):
		rep.Outcome, rep.Reasons = Invalid, validationReasons(err)
		return rep, nil
	case errors.Is(err, recipe.ErrConflict), errors.Is(err, recipe.ErrDeleted):
		rep.Outcome, rep.Reasons = SkippedConflict, []string{"the recipe changed while importing: " + err.Error()}
		return rep, nil
	}
	return rep, err
}

// image stores (or, in a dry run, only re-encodes) one referenced bundle
// image. Results are cached by path: recipes often share photos.
func (run *importRun) image(ctx context.Context, ref recipe.Image) (recipe.Image, error) {
	path := exportfmt.ImagePath(ref)
	if res, ok := run.images[path]; ok {
		return res.img, res.err
	}
	res := imageResult{}
	st, err := run.storeImage(ctx, path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		res.err = fmt.Errorf("image %s is missing from the bundle", path)
	case errors.Is(err, imagestore.ErrTooLarge), errors.Is(err, imagestore.ErrUnsupported), errors.Is(err, zip.ErrFormat),
		errors.Is(err, zip.ErrChecksum), errors.Is(err, io.ErrUnexpectedEOF):
		res.err = fmt.Errorf("image %s skipped: %v", path, err)
	case err != nil:
		return recipe.Image{}, err // disk or context failure: stop the import
	default:
		res.img = recipe.Image{SHA256: st.SHA256, MIME: st.MIME, Width: st.Width, Height: st.Height}
	}
	run.images[path] = res
	return res.img, res.err
}

// storeImage reads a bundle image and returns what the image store holds
// for it. Normally that means re-encoding (§8.5), which also strips any
// metadata. But re-encoding is lossy and never reproduces the same bytes,
// so an image that is byte-identical to a file this store already holds
// (its content hashes to its name, and that file exists: typically an
// export of this same library) is reused as is. That keeps "export, then
// re-import" from creating new revisions or degrading photos. Nothing new
// is ever adopted without re-encoding.
func (run *importRun) storeImage(ctx context.Context, path string) (imagestore.Stored, error) {
	sha, ext, _ := exportfmt.ParseImagePath(path)
	rc, err := run.b.OpenImage(sha, ext)
	if err != nil {
		return imagestore.Stored{}, err
	}
	data, err := io.ReadAll(io.LimitReader(rc, MaxImageBytes+1))
	rc.Close()
	if err != nil {
		return imagestore.Stored{}, err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) == sha {
		if st, ok := run.im.images.Lookup(sha, ext); ok {
			return st, nil
		}
	}
	if run.opts.DryRun {
		return run.im.images.Process(ctx, bytes.NewReader(data), MaxImageBytes)
	}
	return run.im.images.Put(ctx, bytes.NewReader(data), MaxImageBytes)
}

// lookup finds the stored recipe an item refers to, or nil.
func (im *Importer) lookup(ctx context.Context, r *recipe.Recipe) (*recipe.Recipe, error) {
	found := func(rec *recipe.Recipe, err error) (*recipe.Recipe, error) {
		if errors.Is(err, recipe.ErrNotFound) {
			return nil, nil
		}
		return rec, err
	}
	if r.ImportKey == "" {
		if r.CanonicalURL == "" {
			return nil, nil // no key at all: always a new recipe
		}
		return found(im.repo.GetRecipeByCanonicalURL(ctx, r.CanonicalURL))
	}
	rec, err := found(im.repo.GetRecipeByImportKey(ctx, r.ImportKey))
	if rec != nil || err != nil {
		return rec, err
	}
	// "recipe:<id>" is the key an export gives a recipe that had none.
	// Re-importing an export into the library it came from must find
	// that recipe, not create a twin.
	if id, ok := strings.CutPrefix(r.ImportKey, exportfmt.SelfKeyPrefix); ok {
		rec, err := found(im.repo.GetRecipe(ctx, id))
		if rec != nil && rec.ImportKey == "" {
			return rec, nil
		}
		return nil, err
	}
	return nil, nil
}

func dedupeKey(r *recipe.Recipe) string {
	if r.ImportKey != "" {
		return "key:" + r.ImportKey
	}
	if r.CanonicalURL != "" {
		return "url:" + r.CanonicalURL
	}
	return ""
}

// sameContent reports whether importing r would change nothing: both
// render to the same interchange JSON, ignoring identity (ID, key) and the
// datePublished/dateModified timestamps.
func sameContent(stored, incoming *recipe.Recipe) bool {
	in := *incoming
	in.ID, in.ImportKey = stored.ID, stored.ImportKey
	return bytes.Equal(contentJSON(stored), contentJSON(&in))
}

func contentJSON(r *recipe.Recipe) []byte {
	cp := *r
	recipe.Normalize(&cp)
	o := exportfmt.ToJSONLD(&cp)
	delete(o, "datePublished")
	delete(o, "dateModified")
	b, _ := json.Marshal(o)
	return b
}

func sameImages(a, b []recipe.Image) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].SHA256 != b[i].SHA256 || a[i].MIME != b[i].MIME {
			return false
		}
	}
	return true
}

// validationReasons lists a ValidationError's fields as "field: problem".
func validationReasons(err error) []string {
	var ve *recipe.ValidationError
	if !errors.As(err, &ve) {
		return []string{err.Error()}
	}
	out := make([]string, 0, len(ve.Fields))
	for f, msg := range ve.Fields {
		out = append(out, f+": "+msg)
	}
	sort.Strings(out)
	return out
}

// Export writes a bundle of every live recipe (not the trash) and its
// images to w (FR-06, §7.3). The output is deterministic, so exporting the
// same library twice gives identical bytes.
func (im *Importer) Export(ctx context.Context, w io.Writer) error {
	ids, err := im.repo.LiveRecipeIDs(ctx)
	if err != nil {
		return err
	}
	recipes := make([]*recipe.Recipe, 0, len(ids))
	for _, id := range ids {
		r, err := im.repo.GetRecipe(ctx, id)
		if errors.Is(err, recipe.ErrNotFound) {
			continue // purged meanwhile
		}
		if err != nil {
			return err
		}
		if !r.Deleted() {
			recipes = append(recipes, r)
		}
	}
	return exportfmt.WriteBundle(w, recipes, func(sha, ext string) (io.ReadCloser, error) {
		return im.images.Open(sha, ext)
	})
}
