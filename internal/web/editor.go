// SPDX-License-Identifier: Apache-2.0

package web

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// maxForm bounds an editor submission. A very long recipe is a few tens of
// kilobytes.
const maxForm = 1 << 20

// editForm is the editor's view model: what the user typed, as text.
type editForm struct {
	IsNew    bool
	Slug     string
	Title    string
	Revision int

	Description, YieldText, Servings  string
	PrepMin, CookMin, TotalMin        string
	Ingredients                       []formGroup
	Steps                             []formGroup
	Notes                             string
	ContributedBy, Collection, Author string
	SourceURL                         string
	Categories, Tags                  string

	Errors   map[string]string
	Conflict bool
}

type formGroup struct {
	Name     string
	Text     string
	Unparsed []string
}

func (f *editForm) Action() string {
	if f.IsNew {
		return "/new"
	}
	return "/r/" + f.Slug
}

func (f *editForm) CancelURL() string {
	if f.IsNew {
		return "/"
	}
	return "/r/" + f.Slug
}

func editorTitle(f *editForm) string {
	if f.IsNew {
		return "New recipe"
	}
	return "Edit: " + f.Title
}

func rowsFor(text string, min int) int {
	return max(min, strings.Count(text, "\n")+2)
}

// formFromRecipe fills the editor from a stored recipe.
func formFromRecipe(r *recipe.Recipe) *editForm {
	f := &editForm{
		Slug: r.Slug, Title: r.Title, Revision: r.Revision,
		Description: r.Description, YieldText: r.YieldText,
		Servings: numOrEmpty(r.Servings),
		PrepMin:  intOrEmpty(r.PrepMin), CookMin: intOrEmpty(r.CookMin), TotalMin: intOrEmpty(r.TotalMin),
		Notes:         r.Notes,
		ContributedBy: r.ContributedBy, Collection: r.Collection, Author: r.Author, SourceURL: r.SourceURL,
		Tags: strings.Join(r.Tags, ", "),
	}
	var cats []string
	for _, c := range r.Categories {
		cats = append(cats, strings.Join(c, " > "))
	}
	f.Categories = strings.Join(cats, "; ")
	for _, g := range r.IngredientGroups {
		var lines, unparsed []string
		for _, in := range g.Ingredients {
			lines = append(lines, in.Raw)
			if !in.ParseOK {
				unparsed = append(unparsed, in.Raw)
			}
		}
		f.Ingredients = append(f.Ingredients, formGroup{Name: g.Name, Text: strings.Join(lines, "\n"), Unparsed: unparsed})
	}
	for _, g := range r.StepGroups {
		f.Steps = append(f.Steps, formGroup{Name: g.Name, Text: strings.Join(g.Steps, "\n\n")})
	}
	f.ensureGroups()
	return f
}

// ensureGroups guarantees at least one (unnamed) group of each kind.
func (f *editForm) ensureGroups() {
	if len(f.Ingredients) == 0 {
		f.Ingredients = []formGroup{{}}
	}
	if len(f.Steps) == 0 {
		f.Steps = []formGroup{{}}
	}
}

// formFromRequest reads the submitted editor form.
func formFromRequest(r *http.Request) (*editForm, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	p := r.PostForm
	f := &editForm{
		Title: p.Get("title"), Description: p.Get("description"),
		YieldText: p.Get("yield_text"), Servings: strings.TrimSpace(p.Get("servings")),
		PrepMin: strings.TrimSpace(p.Get("prep_min")), CookMin: strings.TrimSpace(p.Get("cook_min")),
		TotalMin: strings.TrimSpace(p.Get("total_min")),
		Notes:    p.Get("notes"), ContributedBy: p.Get("contributed_by"), Collection: p.Get("collection"),
		Author: p.Get("author"), SourceURL: p.Get("source_url"),
		Categories: p.Get("categories"), Tags: p.Get("tags"),
		Errors: map[string]string{},
	}
	f.Revision, _ = strconv.Atoi(p.Get("revision"))
	names, lines := p["ig_name"], p["ig_lines"]
	for i := range max(len(names), len(lines)) {
		f.Ingredients = append(f.Ingredients, formGroup{Name: at(names, i), Text: at(lines, i)})
	}
	names, texts := p["sg_name"], p["sg_text"]
	for i := range max(len(names), len(texts)) {
		f.Steps = append(f.Steps, formGroup{Name: at(names, i), Text: at(texts, i)})
	}
	switch p.Get("action") {
	case "add-ingredient-group":
		f.Ingredients = append(f.Ingredients, formGroup{})
	case "add-step-group":
		f.Steps = append(f.Steps, formGroup{})
	}
	f.ensureGroups()
	return f, nil
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

var blankLines = regexp.MustCompile(`\n[ \t]*\n`)

// apply copies the form onto r, recording number-format problems in
// f.Errors. Fields the form doesn't show are left untouched.
func (f *editForm) apply(r *recipe.Recipe) {
	r.Title, r.Description, r.YieldText, r.Notes = f.Title, f.Description, f.YieldText, f.Notes
	r.ContributedBy, r.Collection, r.Author, r.SourceURL = f.ContributedBy, f.Collection, f.Author, f.SourceURL

	r.Servings = 0
	if f.Servings != "" {
		v, err := strconv.ParseFloat(strings.ReplaceAll(f.Servings, ",", "."), 64)
		if err != nil {
			f.Errors["servings"] = "must be a number"
		}
		r.Servings = v
	}
	for _, m := range []struct {
		key, text string
		dst       *int
	}{{"prepMin", f.PrepMin, &r.PrepMin}, {"cookMin", f.CookMin, &r.CookMin}, {"totalMin", f.TotalMin, &r.TotalMin}} {
		*m.dst = 0
		if m.text != "" {
			v, err := strconv.Atoi(m.text)
			if err != nil {
				f.Errors[m.key] = "must be a whole number of minutes"
			}
			*m.dst = v
		}
	}

	r.IngredientGroups = nil
	for _, g := range f.Ingredients {
		var lines []recipe.Ingredient
		for _, l := range strings.Split(strings.ReplaceAll(g.Text, "\r\n", "\n"), "\n") {
			lines = append(lines, recipe.Ingredient{Raw: l})
		}
		r.IngredientGroups = append(r.IngredientGroups, recipe.IngredientGroup{Name: g.Name, Ingredients: lines})
	}
	r.StepGroups = nil
	for _, g := range f.Steps {
		text := strings.ReplaceAll(g.Text, "\r\n", "\n")
		r.StepGroups = append(r.StepGroups, recipe.StepGroup{Name: g.Name, Steps: blankLines.Split(text, -1)})
	}

	r.Tags = nil
	for _, t := range strings.Split(f.Tags, ",") {
		r.Tags = append(r.Tags, t)
	}
	r.Categories = nil
	for _, c := range strings.FieldsFunc(f.Categories, func(r rune) bool { return r == ';' || r == '\n' }) {
		r.Categories = append(r.Categories, strings.FieldsFunc(c, func(r rune) bool { return r == '>' || r == '›' }))
	}
}

func numOrEmpty(v float64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func intOrEmpty(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

// --- handlers ---

func (h *Handler) newForm(w http.ResponseWriter, r *http.Request) {
	f := &editForm{IsNew: true}
	f.ensureGroups()
	h.render(w, r, http.StatusOK, editorPage(f))
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxForm)
	f, err := formFromRequest(r)
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f.IsNew = true
	if r.PostForm.Get("action") != "save" && r.PostForm.Get("action") != "" {
		h.render(w, r, http.StatusOK, editorPage(f)) // "+ group" round trip
		return
	}
	rec := &recipe.Recipe{SourceKind: recipe.SourceManual}
	f.apply(rec)
	if len(f.Errors) > 0 {
		h.render(w, r, http.StatusUnprocessableEntity, editorPage(f))
		return
	}
	if err := h.svc.Create(r.Context(), rec, userID(r)); err != nil {
		var ve *recipe.ValidationError
		if errors.As(err, &ve) {
			f.Errors = ve.Fields
			h.render(w, r, http.StatusUnprocessableEntity, editorPage(f))
			return
		}
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/r/"+rec.Slug+"?created=1")
}

func (h *Handler) editForm(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if rec.Deleted() {
		redirect(w, r, "/r/"+rec.Slug)
		return
	}
	h.render(w, r, http.StatusOK, editorPage(formFromRecipe(rec)))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxForm)
	cur, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	f, err := formFromRequest(r)
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f.Slug = cur.Slug
	if r.PostForm.Get("action") != "save" && r.PostForm.Get("action") != "" {
		h.render(w, r, http.StatusOK, editorPage(f))
		return
	}
	// Apply the form onto the stored recipe so fields the editor doesn't
	// show (import metadata, original text, review flags) are kept.
	rec := *cur
	rec.Revision = f.Revision
	f.apply(&rec)
	if len(f.Errors) > 0 {
		h.render(w, r, http.StatusUnprocessableEntity, editorPage(f))
		return
	}
	err = h.svc.Update(r.Context(), &rec, userID(r))
	var ve *recipe.ValidationError
	switch {
	case err == nil:
		redirect(w, r, "/r/"+cur.Slug+"?saved=1")
	case errors.As(err, &ve):
		f.Errors = ve.Fields
		h.render(w, r, http.StatusUnprocessableEntity, editorPage(f))
	case errors.Is(err, recipe.ErrConflict):
		// Keep their text; base it on the current revision so saving
		// again deliberately overwrites the other edit.
		f.Conflict = true
		f.Revision = cur.Revision
		h.render(w, r, http.StatusConflict, editorPage(f))
	default:
		h.fail(w, r, err)
	}
}
