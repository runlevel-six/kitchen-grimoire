// SPDX-License-Identifier: Apache-2.0

package recipe

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrNotFound means no recipe matched.
	ErrNotFound = errors.New("recipe not found")
	// ErrConflict means the recipe changed since it was loaded for editing
	// (someone else saved first). The caller should reload and retry.
	ErrConflict = errors.New("recipe was changed by someone else")
	// ErrDeleted means the operation needs a recipe that is not in the trash.
	ErrDeleted = errors.New("recipe is in the trash")
)

// ListOptions selects and orders recipe summaries.
type ListOptions struct {
	Deleted     bool   // list the trash instead of live recipes
	NeedsReview bool   // only recipes flagged for review
	Order       string // "updated" (default, newest first), "created", "title"
	Limit       int    // default 50
	Offset      int
}

// Repo is the storage the service needs. internal/store implements it.
// Defining the interface here, at the point of use, keeps this package free
// of any database code and makes the service easy to test.
type Repo interface {
	CreateRecipe(ctx context.Context, r *Recipe, editorID string) error
	// UpdateRecipe saves r if the stored revision is still r.Revision, then
	// increments r.Revision. Otherwise it returns ErrConflict.
	UpdateRecipe(ctx context.Context, r *Recipe, editorID string) error
	GetRecipeBySlug(ctx context.Context, slug string) (*Recipe, error)
	GetRecipe(ctx context.Context, id string) (*Recipe, error)
	ListRecipes(ctx context.Context, opts ListOptions) ([]Summary, int, error)
	SoftDeleteRecipe(ctx context.Context, id string, at time.Time) error
	RestoreRecipe(ctx context.Context, id string) error
	PurgeDeleted(ctx context.Context, before time.Time) (int, error)
	ListRevisions(ctx context.Context, id string) ([]RevisionInfo, error)
	GetRevision(ctx context.Context, id string, revision int) (*Recipe, error)
}

// Service applies the recipe rules on top of a Repo.
type Service struct {
	repo Repo
	now  func() time.Time
}

// NewService returns a Service using repo.
func NewService(repo Repo) *Service {
	return &Service{repo: repo, now: time.Now}
}

// Create normalizes, validates, and stores a new recipe, filling in its ID,
// slug, timestamps, and revision 1. editorID may be "" (e.g. imports).
func (s *Service) Create(ctx context.Context, r *Recipe, editorID string) error {
	Normalize(r)
	if err := Validate(r); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	r.ID = id.String()
	r.Slug = Slugify(r.Title)
	if r.Slug == "" {
		r.Slug = "recipe" // a title of only symbols; the store adds a suffix
	}
	r.CreatedBy = editorID
	r.CreatedAt, r.UpdatedAt = now, now
	r.DeletedAt = nil
	r.Revision = 1
	return s.repo.CreateRecipe(ctx, r, editorID)
}

// Update saves an edited recipe. r.Revision must be the revision the edit
// started from; if someone saved in between, it returns ErrConflict and
// nothing is written. The slug stays the same so links keep working.
func (s *Service) Update(ctx context.Context, r *Recipe, editorID string) error {
	cur, err := s.repo.GetRecipe(ctx, r.ID)
	if err != nil {
		return err
	}
	if cur.Deleted() {
		return ErrDeleted
	}
	Normalize(r)
	if err := Validate(r); err != nil {
		return err
	}
	// Identity and history fields come from the stored copy, never the form.
	r.Slug = cur.Slug
	r.CreatedBy, r.CreatedAt = cur.CreatedBy, cur.CreatedAt
	r.ImportKey, r.ImportRevision = cur.ImportKey, cur.ImportRevision
	r.Images = cur.Images
	r.UpdatedAt = s.now().UTC().Truncate(time.Millisecond)
	return s.repo.UpdateRecipe(ctx, r, editorID)
}

// Get returns a live or deleted recipe by slug.
func (s *Service) Get(ctx context.Context, slug string) (*Recipe, error) {
	return s.repo.GetRecipeBySlug(ctx, slug)
}

// GetByID returns a recipe by ID.
func (s *Service) GetByID(ctx context.Context, id string) (*Recipe, error) {
	return s.repo.GetRecipe(ctx, id)
}

// List returns summaries and the total matching count.
func (s *Service) List(ctx context.Context, opts ListOptions) ([]Summary, int, error) {
	if opts.Limit <= 0 || opts.Limit > 500 {
		opts.Limit = 50
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}
	return s.repo.ListRecipes(ctx, opts)
}

// Delete moves a recipe to the trash (FR-01).
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.SoftDeleteRecipe(ctx, id, s.now())
}

// Restore takes a recipe out of the trash.
func (s *Service) Restore(ctx context.Context, id string) error {
	return s.repo.RestoreRecipe(ctx, id)
}

// PurgeTrash permanently removes recipes that have been in the trash for
// longer than days. It returns how many were removed.
func (s *Service) PurgeTrash(ctx context.Context, days int) (int, error) {
	if days < 0 {
		return 0, fmt.Errorf("purge: days must not be negative")
	}
	return s.repo.PurgeDeleted(ctx, s.now().AddDate(0, 0, -days))
}

// EmptyTrash permanently removes everything currently in the trash,
// however recently it was deleted. It returns how many were removed.
func (s *Service) EmptyTrash(ctx context.Context) (int, error) {
	// Any cutoff in the future covers every trashed recipe.
	return s.repo.PurgeDeleted(ctx, s.now().Add(24*time.Hour))
}

// History lists a recipe's revisions, newest first.
func (s *Service) History(ctx context.Context, id string) ([]RevisionInfo, error) {
	return s.repo.ListRevisions(ctx, id)
}

// Revision returns the recipe as it was at a revision.
func (s *Service) Revision(ctx context.Context, id string, rev int) (*Recipe, error) {
	return s.repo.GetRevision(ctx, id, rev)
}

// RestoreRevision makes an old revision current by saving its content as a
// new revision; nothing in the history is lost (FR-10).
func (s *Service) RestoreRevision(ctx context.Context, id string, rev int, editorID string) (*Recipe, error) {
	cur, err := s.repo.GetRecipe(ctx, id)
	if err != nil {
		return nil, err
	}
	old, err := s.repo.GetRevision(ctx, id, rev)
	if err != nil {
		return nil, err
	}
	old.ID = cur.ID
	old.Revision = cur.Revision
	old.DeletedAt = nil
	if err := s.Update(ctx, old, editorID); err != nil {
		return nil, err
	}
	return old, nil
}
