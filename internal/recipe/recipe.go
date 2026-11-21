// SPDX-License-Identifier: Apache-2.0

// Package recipe holds the recipe domain: types, validation, slugs, and the
// service that applies the rules (revisions, soft delete) on top of a
// repository. The repository interface is defined here, where it is used;
// internal/store implements it.
package recipe

import (
	"encoding/json"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/ingredient"
)

// SourceKind says how a recipe entered the library.
type SourceKind string

const (
	SourceManual  SourceKind = "manual"
	SourceURL     SourceKind = "url"
	SourceCapture SourceKind = "capture"
	SourceBundle  SourceKind = "bundle"
	SourceLegacy  SourceKind = "legacy"
)

// Valid reports whether k is one of the known kinds.
func (k SourceKind) Valid() bool {
	switch k {
	case SourceManual, SourceURL, SourceCapture, SourceBundle, SourceLegacy:
		return true
	}
	return false
}

// Recipe is the full recipe. JSON tags define the revision snapshot format.
type Recipe struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`

	YieldText string  `json:"yieldText,omitempty"` // as written, e.g. "24 bars"
	Servings  float64 `json:"servings,omitempty"`  // numeric servings for the scaler; 0 if unknown

	PrepMin  int `json:"prepMin,omitempty"` // 0 means unknown
	CookMin  int `json:"cookMin,omitempty"`
	TotalMin int `json:"totalMin,omitempty"`

	IngredientGroups []IngredientGroup `json:"ingredientGroups"`
	StepGroups       []StepGroup       `json:"stepGroups"`
	Notes            string            `json:"notes,omitempty"`

	// Categories are paths from the root: [["Desserts", "Cookies"]].
	Categories [][]string `json:"categories,omitempty"`
	Tags       []string   `json:"tags,omitempty"`

	// Provenance (§7.1).
	SourceKind    SourceKind `json:"sourceKind"`
	SourceURL     string     `json:"sourceUrl,omitempty"`
	CanonicalURL  string     `json:"canonicalUrl,omitempty"`
	SourceSite    string     `json:"sourceSite,omitempty"`
	Author        string     `json:"author,omitempty"`        // as published
	ContributedBy string     `json:"contributedBy,omitempty"` // who gave it to the household
	Collection    string     `json:"collection,omitempty"`

	ImportKey      string `json:"importKey,omitempty"`
	ImportRevision int    `json:"importRevision,omitempty"`

	OriginalText string          `json:"originalText,omitempty"` // verbatim imported text
	RawSource    json.RawMessage `json:"rawSource,omitempty"`    // extracted JSON-LD

	NeedsReview   bool     `json:"needsReview,omitempty"`
	ReviewReasons []string `json:"reviewReasons,omitempty"`

	Images []Image `json:"images,omitempty"`

	CreatedBy string     `json:"createdBy,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
	Revision  int        `json:"revision"`
}

// IngredientGroup is an optionally named list of ingredients ("Filling").
type IngredientGroup struct {
	Name        string       `json:"name,omitempty"`
	Ingredients []Ingredient `json:"ingredients"`
}

// Ingredient keeps the raw line; the parsed fields are derived (D-06).
type Ingredient = ingredient.Line

// StepGroup is an optionally named list of steps.
type StepGroup struct {
	Name  string   `json:"name,omitempty"`
	Steps []string `json:"steps"`
}

// Image is a stored image's metadata (files live in the image store).
type Image struct {
	ID        string `json:"id"`
	SHA256    string `json:"sha256"`
	MIME      string `json:"mime"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	IsPrimary bool   `json:"isPrimary,omitempty"`
	SourceURL string `json:"sourceUrl,omitempty"`
}

// PrimaryImage returns the primary image, or the first one, or nil.
func (r *Recipe) PrimaryImage() *Image {
	for i := range r.Images {
		if r.Images[i].IsPrimary {
			return &r.Images[i]
		}
	}
	if len(r.Images) > 0 {
		return &r.Images[0]
	}
	return nil
}

// Deleted reports whether the recipe is in the trash.
func (r *Recipe) Deleted() bool { return r.DeletedAt != nil }

// StepCount returns the total number of steps across groups.
func (r *Recipe) StepCount() int {
	n := 0
	for _, g := range r.StepGroups {
		n += len(g.Steps)
	}
	return n
}

// IngredientCount returns the total number of ingredient lines.
func (r *Recipe) IngredientCount() int {
	n := 0
	for _, g := range r.IngredientGroups {
		n += len(g.Ingredients)
	}
	return n
}

// FlatStep is a step with its group name and 1-based overall number.
type FlatStep struct {
	Number int
	Group  string
	Text   string
}

// FlatSteps lists every step in order, for cook mode.
func (r *Recipe) FlatSteps() []FlatStep {
	var out []FlatStep
	for _, g := range r.StepGroups {
		for _, s := range g.Steps {
			out = append(out, FlatStep{Number: len(out) + 1, Group: g.Name, Text: s})
		}
	}
	return out
}

// Summary is the light row used in lists.
type Summary struct {
	ID            string
	Slug          string
	Title         string
	YieldText     string
	TotalMin      int
	ContributedBy string
	Collection    string
	NeedsReview   bool
	PrimaryImage  string // sha256, "" if none
	UpdatedAt     time.Time
	DeletedAt     *time.Time
}

// RevisionInfo describes one saved revision.
type RevisionInfo struct {
	Revision int
	EditedBy string // display name, "" if unknown
	EditedAt time.Time
}

// User is an authenticated household member.
type User struct {
	ID          string
	RemoteUser  string
	DisplayName string
	Email       string
	IsAdmin     bool // from configuration, not stored
}

// Name returns the best human-readable name.
func (u User) Name() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.RemoteUser
}
