// SPDX-License-Identifier: Apache-2.0

// Package exportfmt reads and writes the interchange format (DESIGN.md
// §7.3): schema.org Recipe JSON-LD objects with an "x-grimoire" extension,
// packed into a bundle zip of recipes.jsonl plus images/<sha256>.<ext>.
//
// It is pure data mapping: no database, no HTTP. internal/ingest runs the
// mapped recipes through the one ingest pipeline (§8), and builds exports.
package exportfmt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	"github.com/runlevel-six/kitchen-grimoire/internal/scrape"
)

// Object is one JSON-LD object. Marshalling a map sorts its keys, which
// keeps the output byte-stable (§16 round-trip test).
type Object map[string]any

// ExtensionKey is the property that holds the fields schema.org lacks.
const ExtensionKey = "x-grimoire"

// ExtensionVersion is the x-grimoire version this package writes. Readers
// accept older versions and warn on newer ones.
const ExtensionVersion = 1

// SelfKeyPrefix starts the import key synthesized for a recipe that has
// none (one entered by hand, or saved from a URL): "recipe:<id>". Giving
// every exported recipe a key makes re-importing an export idempotent.
const SelfKeyPrefix = "recipe:"

// Extension is the x-grimoire object. Field order here is the output
// order. The list fields are always written (as [] when empty) so a reader
// can tell "x-grimoire says none" from "x-grimoire doesn't say": a nil
// slice after decoding means the key was absent, and the reader then falls
// back to the plain schema.org field.
type Extension struct {
	Version          int             `json:"version"`
	ImportKey        string          `json:"importKey,omitempty"`
	SourceKind       string          `json:"sourceKind,omitempty"`
	CanonicalURL     string          `json:"canonicalUrl,omitempty"`
	SourceSite       string          `json:"sourceSite,omitempty"`
	ContributedBy    string          `json:"contributedBy,omitempty"`
	Collection       string          `json:"collection,omitempty"`
	Servings         float64         `json:"servings,omitempty"`
	CategoryPath     [][]string      `json:"categoryPath"`
	Tags             []string        `json:"tags"`
	IngredientGroups []LineGroup     `json:"ingredientGroups"`
	StepGroups       []LineGroup     `json:"stepGroups"`
	Notes            string          `json:"notes,omitempty"`
	OriginalText     string          `json:"originalText,omitempty"`
	NeedsReview      bool            `json:"needsReview"`
	ReviewReasons    []string        `json:"reviewReasons"`
	RawSource        json.RawMessage `json:"rawSource,omitempty"`
	// OlderEdition is the same recipe as an older edition of its source
	// had it (D-13). On first import it is stored as revision 1, with
	// this object's content as revision 2, so the older wording is kept
	// in the recipe's history. Exports never include it.
	OlderEdition json.RawMessage `json:"olderEdition,omitempty"`
}

// LineGroup is an ingredient or step group. An unnamed group has a null
// name, as in the §7.3 example.
type LineGroup struct {
	Name  *string  `json:"name"`
	Lines []string `json:"lines"`
}

// timeLayout matches the database's timestamps: UTC, milliseconds.
const timeLayout = "2006-01-02T15:04:05.000Z"

// ExportKey is the import key a recipe is exported under: its own, or
// "recipe:<id>" when it has none. It is "" for a recipe with neither.
func ExportKey(r *recipe.Recipe) string {
	if r.ImportKey != "" {
		return r.ImportKey
	}
	if r.ID != "" {
		return SelfKeyPrefix + r.ID
	}
	return ""
}

// ImagePath is the bundle path of an image: images/<sha256>.<jpg|png>.
func ImagePath(im recipe.Image) string {
	return "images/" + im.SHA256 + "." + imageExt(im.MIME)
}

func imageExt(mime string) string {
	if mime == "image/png" {
		return "png"
	}
	return "jpg"
}

func extMIME(ext string) string {
	if ext == "png" {
		return "image/png"
	}
	return "image/jpeg"
}

// ParseImagePath splits a bundle image path into its hash and extension.
// It accepts only images/<64 lowercase hex>.<jpg|png>.
func ParseImagePath(p string) (sha, ext string, ok bool) {
	rest, found := strings.CutPrefix(p, "images/")
	if !found {
		return "", "", false
	}
	sha, ext, found = strings.Cut(rest, ".")
	if !found || !validSHA(sha) || (ext != "jpg" && ext != "png") {
		return "", "", false
	}
	return sha, ext, true
}

func validSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// OrderedImages returns the recipe's images with the primary one first and
// the rest in their stored order. Imports attach images in this order and
// make the first primary, so the order survives a round trip.
func OrderedImages(r *recipe.Recipe) []recipe.Image {
	if len(r.Images) == 0 {
		return nil
	}
	p := 0
	for i, im := range r.Images {
		if im.IsPrimary {
			p = i
			break
		}
	}
	out := make([]recipe.Image, 0, len(r.Images))
	out = append(out, r.Images[p])
	for i, im := range r.Images {
		if i != p {
			out = append(out, im)
		}
	}
	return out
}

// ToJSONLD renders a recipe as a schema.org Recipe object with the
// x-grimoire extension (§7.3). Images are referenced by bundle path.
// Parsed ingredient fields are not written: raw lines are canonical and
// the parse is redone on import (D-06). The rendering is deterministic.
func ToJSONLD(r *recipe.Recipe) Object {
	o := Object{
		"@context": "https://schema.org",
		"@type":    "Recipe",
		"name":     r.Title,
	}
	if r.Description != "" {
		o["description"] = r.Description
	}
	if r.YieldText != "" {
		o["recipeYield"] = r.YieldText
	}
	for key, m := range map[string]int{"prepTime": r.PrepMin, "cookTime": r.CookMin, "totalTime": r.TotalMin} {
		if m > 0 {
			o[key] = FormatDuration(m)
		}
	}

	ext := Extension{
		Version:       ExtensionVersion,
		ImportKey:     ExportKey(r),
		SourceKind:    string(r.SourceKind),
		CanonicalURL:  r.CanonicalURL,
		SourceSite:    r.SourceSite,
		ContributedBy: r.ContributedBy,
		Collection:    r.Collection,
		Servings:      r.Servings,
		CategoryPath:  nonNilPaths(r.Categories),
		Tags:          nonNil(r.Tags),
		Notes:         r.Notes,
		OriginalText:  r.OriginalText,
		NeedsReview:   r.NeedsReview,
		ReviewReasons: nonNil(r.ReviewReasons),
		RawSource:     compactJSON(r.RawSource),
	}

	lines := []string{}
	ext.IngredientGroups = []LineGroup{}
	for _, g := range r.IngredientGroups {
		lg := LineGroup{Name: namePtr(g.Name), Lines: []string{}}
		for _, in := range g.Ingredients {
			lines = append(lines, in.Raw)
			lg.Lines = append(lg.Lines, in.Raw)
		}
		ext.IngredientGroups = append(ext.IngredientGroups, lg)
	}
	o["recipeIngredient"] = lines

	// Named step groups become HowToSections; unnamed ones contribute bare
	// HowToSteps, which is how most sites publish ungrouped steps.
	instructions := []any{}
	ext.StepGroups = []LineGroup{}
	for _, g := range r.StepGroups {
		steps := []any{}
		for _, s := range g.Steps {
			steps = append(steps, Object{"@type": "HowToStep", "text": s})
		}
		if g.Name != "" {
			instructions = append(instructions, Object{"@type": "HowToSection", "name": g.Name, "itemListElement": steps})
		} else {
			instructions = append(instructions, steps...)
		}
		ext.StepGroups = append(ext.StepGroups, LineGroup{Name: namePtr(g.Name), Lines: nonNil(g.Steps)})
	}
	o["recipeInstructions"] = instructions

	// recipeCategory holds leaf names only; x-grimoire keeps full paths.
	var leaves []string
	for _, p := range r.Categories {
		if len(p) == 0 {
			continue
		}
		if leaf := p[len(p)-1]; !contains(leaves, leaf) {
			leaves = append(leaves, leaf)
		}
	}
	if len(leaves) > 0 {
		o["recipeCategory"] = leaves
	}
	if len(r.Tags) > 0 {
		o["keywords"] = strings.Join(r.Tags, ", ")
	}
	if r.Author != "" {
		o["author"] = Object{"@type": "Person", "name": r.Author}
	}
	if r.SourceURL != "" {
		o["url"] = r.SourceURL
	}
	if imgs := OrderedImages(r); len(imgs) > 0 {
		paths := make([]string, len(imgs))
		for i, im := range imgs {
			paths[i] = ImagePath(im)
		}
		o["image"] = paths
	}
	if !r.CreatedAt.IsZero() {
		o["datePublished"] = r.CreatedAt.UTC().Format(timeLayout)
	}
	if !r.UpdatedAt.IsZero() {
		o["dateModified"] = r.UpdatedAt.UTC().Format(timeLayout)
	}
	o[ExtensionKey] = ext
	return o
}

// FromJSONLD maps an interchange object back to a recipe draft. It does
// not normalize or validate; the ingest pipeline does that.
//
// When x-grimoire is present it is authoritative: its scalar fields are
// taken as they are, the plain schema.org text fields (name, description,
// recipeYield, author) are read verbatim, and its list fields are used
// unless the key is absent. Without it, the object is read like any
// published schema.org Recipe (scrape.MapObject: HTML stripped, every
// common shape accepted), so bundles written by other software work too.
// Unknown fields are ignored.
//
// Images come back in r.Images with SHA256 and MIME taken from their
// bundle paths (images/<sha>.<ext>); the importer re-encodes the files.
// Image references that are not bundle paths are dropped with a warning.
// The error is non-nil only when the object is not a Recipe or its
// x-grimoire is malformed.
func FromJSONLD(in Object) (*recipe.Recipe, []string, error) {
	// Normalize the input to what a JSON decoder produces (map[string]any,
	// []any, json.Number), whether it was decoded or built in memory.
	obj, err := redecode(in)
	if err != nil {
		return nil, nil, err
	}
	if t, ok := obj["@type"]; ok && !isRecipeType(t) {
		return nil, nil, errors.New("not a schema.org Recipe (@type)")
	}
	var warnings []string
	warn := func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }

	r := scrape.MapObject(obj)
	r.SourceURL, _ = obj["url"].(string)
	r.SourceURL = strings.TrimSpace(r.SourceURL)
	r.CreatedAt = parseDate(obj["datePublished"])
	r.UpdatedAt = parseDate(obj["dateModified"])
	r.Images = readImages(obj["image"], warn)

	raw, hasExt := obj[ExtensionKey]
	if !hasExt || raw == nil {
		// Another app's bundle: its url is the best dedupe key we have.
		if u, err := url.Parse(r.SourceURL); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			r.CanonicalURL = r.SourceURL
		}
		r.SourceKind = recipe.SourceBundle
		return r, warnings, nil
	}

	var ext Extension
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &ext); err != nil {
		return nil, warnings, fmt.Errorf("x-grimoire: %v", err)
	}
	if ext.Version > ExtensionVersion {
		warn("x-grimoire version %d is newer than this server understands (%d); unknown fields were ignored", ext.Version, ExtensionVersion)
	}

	// Plain text fields, verbatim: our own exports are already plain text,
	// and HTML-stripping them again could change wording ("&amp;").
	if s, ok := obj["name"].(string); ok {
		r.Title = s
	}
	if s, ok := obj["description"].(string); ok {
		r.Description = s
	}
	if s, ok := obj["recipeYield"].(string); ok {
		r.YieldText = s
	}
	if a, ok := obj["author"].(map[string]any); ok {
		if s, ok := a["name"].(string); ok {
			r.Author = s
		}
	} else if s, ok := obj["author"].(string); ok {
		r.Author = s
	}

	r.ImportKey = ext.ImportKey
	r.CanonicalURL = ext.CanonicalURL
	r.SourceSite = ext.SourceSite
	r.ContributedBy = ext.ContributedBy
	r.Collection = ext.Collection
	r.Servings = ext.Servings
	r.Notes = ext.Notes
	r.OriginalText = ext.OriginalText
	r.NeedsReview = ext.NeedsReview
	r.ReviewReasons = ext.ReviewReasons
	if len(ext.RawSource) > 0 && !bytes.Equal(ext.RawSource, []byte("null")) {
		r.RawSource = ext.RawSource
	}
	switch k := recipe.SourceKind(ext.SourceKind); {
	case k == "":
		r.SourceKind = recipe.SourceBundle
	case k.Valid():
		r.SourceKind = k
	default:
		warn("unknown sourceKind %q; using %q", ext.SourceKind, recipe.SourceBundle)
		r.SourceKind = recipe.SourceBundle
	}

	if ext.CategoryPath != nil {
		r.Categories = ext.CategoryPath
	}
	if ext.Tags != nil {
		r.Tags = ext.Tags
	}
	if ext.IngredientGroups != nil {
		r.IngredientGroups = nil
		for _, g := range ext.IngredientGroups {
			ig := recipe.IngredientGroup{Name: deref(g.Name)}
			for _, l := range g.Lines {
				ig.Ingredients = append(ig.Ingredients, recipe.Ingredient{Raw: l})
			}
			r.IngredientGroups = append(r.IngredientGroups, ig)
		}
	}
	if ext.StepGroups != nil {
		r.StepGroups = nil
		for _, g := range ext.StepGroups {
			r.StepGroups = append(r.StepGroups, recipe.StepGroup{Name: deref(g.Name), Steps: g.Lines})
		}
	}
	return r, warnings, nil
}

// FormatDuration renders minutes as an ISO 8601 duration: PT1H30M, PT45M,
// PT2H. Hours are not folded into days, which every reader handles.
func FormatDuration(minutes int) string {
	h, m := minutes/60, minutes%60
	var b strings.Builder
	b.WriteString("PT")
	if h > 0 {
		b.WriteString(strconv.Itoa(h) + "H")
	}
	if m > 0 || h == 0 {
		b.WriteString(strconv.Itoa(m) + "M")
	}
	return b.String()
}

// ParseDuration reads an ISO 8601 duration (leniently, as the scraper
// does) and returns whole minutes, or 0 if it can't be read.
func ParseDuration(s string) int {
	d, ok := scrape.ParseISODuration(s)
	if !ok {
		return 0
	}
	return int(math.Round(d.Minutes()))
}

// redecode round-trips v through JSON so it has decoder types.
func redecode(in Object) (map[string]any, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("encode object: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("decode object: %w", err)
	}
	if out == nil {
		return nil, errors.New("not a JSON object")
	}
	return out, nil
}

func isRecipeType(t any) bool {
	switch v := t.(type) {
	case string:
		return v == "Recipe" || strings.HasSuffix(v, "/Recipe")
	case []any:
		for _, e := range v {
			if isRecipeType(e) {
				return true
			}
		}
	}
	return false
}

// readImages collects bundle image paths from a string, an array, or
// ImageObjects ({"url": ...} / {"contentUrl": ...}).
func readImages(v any, warn func(string, ...any)) []recipe.Image {
	var out []recipe.Image
	var add func(v any, depth int)
	add = func(v any, depth int) {
		if depth > 3 {
			return
		}
		switch t := v.(type) {
		case string:
			if sha, ext, ok := ParseImagePath(t); ok {
				out = append(out, recipe.Image{SHA256: sha, MIME: extMIME(ext)})
			} else {
				warn("image %q ignored: only bundle paths images/<sha256>.<jpg|png> are imported", clip(t, 120))
			}
		case []any:
			for _, e := range t {
				add(e, depth+1)
			}
		case map[string]any:
			if u, ok := t["url"]; ok {
				add(u, depth+1)
			} else if u, ok := t["contentUrl"]; ok {
				add(u, depth+1)
			}
		}
	}
	add(v, 0)
	return out
}

// parseDate accepts an RFC 3339 timestamp or a plain date.
func parseDate(v any) time.Time {
	s, _ := v.(string)
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Truncate(time.Millisecond)
		}
	}
	return time.Time{}
}

// compactJSON compacts a raw JSON value so the rendering is stable.
func compactJSON(m json.RawMessage) json.RawMessage {
	if len(m) == 0 {
		return nil
	}
	var b bytes.Buffer
	if err := json.Compact(&b, m); err != nil {
		return nil // never write invalid JSON; the store guarantees validity anyway
	}
	return b.Bytes()
}

func namePtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilPaths(p [][]string) [][]string {
	if p == nil {
		return [][]string{}
	}
	return p
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ToJSONLDWithOlder is ToJSONLD plus the older edition of the same recipe
// (x-grimoire.olderEdition, D-13). older may be nil.
func ToJSONLDWithOlder(r, older *recipe.Recipe) (Object, error) {
	o := ToJSONLD(r)
	if older == nil {
		return o, nil
	}
	b, err := json.Marshal(ToJSONLD(older))
	if err != nil {
		return nil, err
	}
	ext := o[ExtensionKey].(Extension)
	ext.OlderEdition = b
	o[ExtensionKey] = ext
	return o, nil
}

// OlderEdition returns the x-grimoire.olderEdition recipe, or nil when the
// object has none.
func OlderEdition(in Object) (*recipe.Recipe, error) {
	obj, err := redecode(in)
	if err != nil {
		return nil, err
	}
	raw, ok := obj[ExtensionKey].(map[string]any)
	if !ok {
		return nil, nil
	}
	older, ok := raw["olderEdition"].(map[string]any)
	if !ok {
		return nil, nil
	}
	r, _, err := FromJSONLD(Object(older))
	if err != nil {
		return nil, fmt.Errorf("olderEdition: %w", err)
	}
	return r, nil
}
