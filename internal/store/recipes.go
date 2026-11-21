// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// Store implements recipe.Repo and the other persistence the app needs on
// top of one SQLite database. Queries are hand-written database/sql (D-19).
type Store struct {
	db *sql.DB
}

// New wraps an open, migrated database.
func New(db *sql.DB) *Store { return &Store{db: db} }

// DB exposes the handle for health checks and backups.
func (s *Store) DB() *sql.DB { return s.db }

// Compile-time check that *Store satisfies the service's interface.
var _ recipe.Repo = (*Store)(nil)

// --- helpers for nullable columns ---

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullFloat(f float64) any {
	if f == 0 {
		return nil
	}
	return f
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return FormatTime(*t)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func parseNullTime(ns sql.NullString) *time.Time {
	if !ns.Valid {
		return nil
	}
	t := parseTime(ns.String)
	return &t
}

// withTx runs fn in a write transaction, committing if it returns nil and
// rolling back otherwise.
func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// --- create / update ---

// CreateRecipe inserts r with its children and revision 1. If r.Slug is
// taken it appends -2, -3, ... and updates r.Slug.
func (s *Store) CreateRecipe(ctx context.Context, r *recipe.Recipe, editorID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		slug, err := uniqueSlug(ctx, tx, r.Slug)
		if err != nil {
			return err
		}
		r.Slug = slug
		reasons, _ := json.Marshal(nonNil(r.ReviewReasons))
		_, err = tx.ExecContext(ctx, `INSERT INTO recipes (
			id, slug, title, description, yield_text, servings, prep_min, cook_min, total_min, notes,
			source_kind, source_url, canonical_url, source_site, author, contributed_by, collection,
			import_key, import_revision, original_text, raw_source, needs_review, review_reasons,
			created_by, created_at, updated_at, deleted_at, revision)
			VALUES (?,?,?,?,?,?,?,?,?,?, ?,?,?,?,?,?,?, ?,?,?,?,?,?, ?,?,?,?,?)`,
			r.ID, r.Slug, r.Title, r.Description, r.YieldText, nullFloat(r.Servings),
			nullInt(r.PrepMin), nullInt(r.CookMin), nullInt(r.TotalMin), r.Notes,
			string(r.SourceKind), r.SourceURL, nullStr(r.CanonicalURL), r.SourceSite, r.Author,
			r.ContributedBy, r.Collection,
			nullStr(r.ImportKey), nullInt(r.ImportRevision), nullStr(r.OriginalText), rawJSON(r.RawSource),
			r.NeedsReview, string(reasons),
			nullStr(editorID), FormatTime(r.CreatedAt), FormatTime(r.UpdatedAt), nullTime(r.DeletedAt), r.Revision)
		if err != nil {
			return fmt.Errorf("insert recipe: %w", err)
		}
		if err := writeChildren(ctx, tx, r); err != nil {
			return err
		}
		if err := indexRecipe(ctx, tx, r); err != nil {
			return fmt.Errorf("index recipe: %w", err)
		}
		return writeSnapshot(ctx, tx, r, editorID)
	})
}

// UpdateRecipe saves r if the stored revision still equals r.Revision
// (optimistic concurrency), increments it, and records a snapshot.
func (s *Store) UpdateRecipe(ctx context.Context, r *recipe.Recipe, editorID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		reasons, _ := json.Marshal(nonNil(r.ReviewReasons))
		res, err := tx.ExecContext(ctx, `UPDATE recipes SET
			title = ?, description = ?, yield_text = ?, servings = ?, prep_min = ?, cook_min = ?, total_min = ?,
			notes = ?, source_kind = ?, source_url = ?, canonical_url = ?, source_site = ?, author = ?,
			contributed_by = ?, collection = ?, import_revision = ?, original_text = ?, raw_source = ?,
			needs_review = ?, review_reasons = ?, updated_at = ?, revision = revision + 1
			WHERE id = ? AND revision = ? AND deleted_at IS NULL`,
			r.Title, r.Description, r.YieldText, nullFloat(r.Servings),
			nullInt(r.PrepMin), nullInt(r.CookMin), nullInt(r.TotalMin),
			r.Notes, string(r.SourceKind), r.SourceURL, nullStr(r.CanonicalURL), r.SourceSite, r.Author,
			r.ContributedBy, r.Collection, nullInt(r.ImportRevision), nullStr(r.OriginalText), rawJSON(r.RawSource),
			r.NeedsReview, string(reasons), FormatTime(r.UpdatedAt),
			r.ID, r.Revision)
		if err != nil {
			return fmt.Errorf("update recipe: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// Tell "gone" apart from "changed under us".
			var rev int
			var deleted sql.NullString
			err := tx.QueryRowContext(ctx, `SELECT revision, deleted_at FROM recipes WHERE id = ?`, r.ID).Scan(&rev, &deleted)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				return recipe.ErrNotFound
			case err != nil:
				return err
			case deleted.Valid:
				return recipe.ErrDeleted
			default:
				return recipe.ErrConflict
			}
		}
		r.Revision++
		if err := writeChildren(ctx, tx, r); err != nil {
			return err
		}
		if err := indexRecipe(ctx, tx, r); err != nil {
			return fmt.Errorf("index recipe: %w", err)
		}
		return writeSnapshot(ctx, tx, r, editorID)
	})
}

func rawJSON(m json.RawMessage) any {
	if len(m) == 0 {
		return nil
	}
	return string(m)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// uniqueSlug returns base, or base-N for the smallest free N >= 2.
func uniqueSlug(ctx context.Context, tx *sql.Tx, base string) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT slug FROM recipes WHERE slug = ? OR slug LIKE ? ESCAPE '\'`,
		base, likeEscape(base)+"-%")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	taken := map[string]bool{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", err
		}
		taken[s] = true
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if !taken[base] {
		return base, nil
	}
	for n := 2; ; n++ {
		if c := base + "-" + strconv.Itoa(n); !taken[c] {
			return c, nil
		}
	}
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// writeChildren replaces a recipe's groups, lines, steps, categories, and
// tags with the ones in r. Deleting the groups cascades to their rows.
func writeChildren(ctx context.Context, tx *sql.Tx, r *recipe.Recipe) error {
	for _, q := range []string{
		`DELETE FROM ingredient_groups WHERE recipe_id = ?`,
		`DELETE FROM step_groups WHERE recipe_id = ?`,
		`DELETE FROM recipe_categories WHERE recipe_id = ?`,
		`DELETE FROM recipe_tags WHERE recipe_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, r.ID); err != nil {
			return err
		}
	}
	for gi, g := range r.IngredientGroups {
		res, err := tx.ExecContext(ctx, `INSERT INTO ingredient_groups (recipe_id, position, name) VALUES (?, ?, ?)`,
			r.ID, gi, g.Name)
		if err != nil {
			return fmt.Errorf("insert ingredient group: %w", err)
		}
		gid, _ := res.LastInsertId()
		for li, in := range g.Ingredients {
			var qmin, qmax, unit, size, item, note any
			if in.ParseOK {
				qmin, qmax = in.QtyMin, in.QtyMax
				unit, size, item, note = nullStr(in.Unit), nullStr(in.Size), nullStr(in.Item), nullStr(in.Note)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO ingredients
				(group_id, position, raw_text, qty_min, qty_max, unit, size_text, item, note, parse_ok)
				VALUES (?,?,?,?,?,?,?,?,?,?)`,
				gid, li, in.Raw, qmin, qmax, unit, size, item, note, in.ParseOK); err != nil {
				return fmt.Errorf("insert ingredient: %w", err)
			}
		}
	}
	for gi, g := range r.StepGroups {
		res, err := tx.ExecContext(ctx, `INSERT INTO step_groups (recipe_id, position, name) VALUES (?, ?, ?)`,
			r.ID, gi, g.Name)
		if err != nil {
			return fmt.Errorf("insert step group: %w", err)
		}
		gid, _ := res.LastInsertId()
		for si, text := range g.Steps {
			if _, err := tx.ExecContext(ctx, `INSERT INTO steps (group_id, position, text) VALUES (?, ?, ?)`,
				gid, si, text); err != nil {
				return fmt.Errorf("insert step: %w", err)
			}
		}
	}
	for _, path := range r.Categories {
		cid, err := ensureCategoryPath(ctx, tx, path)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO recipe_categories (recipe_id, category_id) VALUES (?, ?)`,
			r.ID, cid); err != nil {
			return err
		}
	}
	for _, name := range r.Tags {
		tid, err := ensureTag(ctx, tx, name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO recipe_tags (recipe_id, tag_id) VALUES (?, ?)`,
			r.ID, tid); err != nil {
			return err
		}
	}
	return nil
}

// ensureCategoryPath returns the id of the last category in path, creating
// any missing levels. Names keep the spelling of whoever created them first.
func ensureCategoryPath(ctx context.Context, tx *sql.Tx, path []string) (int64, error) {
	var parent sql.NullInt64
	var id int64
	for i, name := range path {
		p := recipe.CategoryPathSlug(path[:i+1])
		err := tx.QueryRowContext(ctx, `SELECT id FROM categories WHERE path = ?`, p).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			res, err := tx.ExecContext(ctx, `INSERT INTO categories (parent_id, name, slug, path) VALUES (?, ?, ?, ?)`,
				parent, name, recipe.Slugify(name), p)
			if err != nil {
				return 0, fmt.Errorf("insert category %q: %w", p, err)
			}
			id, _ = res.LastInsertId()
		} else if err != nil {
			return 0, err
		}
		parent = sql.NullInt64{Int64: id, Valid: true}
	}
	return id, nil
}

func ensureTag(ctx context.Context, tx *sql.Tx, name string) (int64, error) {
	slug := recipe.Slugify(name)
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE slug = ?`, slug).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := tx.ExecContext(ctx, `INSERT INTO tags (name, slug) VALUES (?, ?)`, name, slug)
		if err != nil {
			return 0, fmt.Errorf("insert tag %q: %w", name, err)
		}
		return res.LastInsertId()
	}
	return id, err
}

// snapshotVersion lets future code read old snapshots if the format changes.
const snapshotVersion = 1

type snapshot struct {
	V      int            `json:"v"`
	Recipe *recipe.Recipe `json:"recipe"`
}

func writeSnapshot(ctx context.Context, tx *sql.Tx, r *recipe.Recipe, editorID string) error {
	b, err := json.Marshal(snapshot{V: snapshotVersion, Recipe: r})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO revisions (recipe_id, revision, snapshot, edited_by, edited_at)
		VALUES (?, ?, ?, ?, ?)`, r.ID, r.Revision, string(b), nullStr(editorID), FormatTime(r.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert revision: %w", err)
	}
	return nil
}

// --- read ---

const recipeColumns = `id, slug, title, description, yield_text, servings, prep_min, cook_min, total_min, notes,
	source_kind, source_url, canonical_url, source_site, author, contributed_by, collection,
	import_key, import_revision, original_text, raw_source, needs_review, review_reasons,
	created_by, created_at, updated_at, deleted_at, revision`

// GetRecipe loads a recipe (live or deleted) by id.
func (s *Store) GetRecipe(ctx context.Context, id string) (*recipe.Recipe, error) {
	return s.getRecipe(ctx, `id = ?`, id)
}

// GetRecipeBySlug loads a recipe (live or deleted) by slug.
func (s *Store) GetRecipeBySlug(ctx context.Context, slug string) (*recipe.Recipe, error) {
	return s.getRecipe(ctx, `slug = ?`, slug)
}

// GetRecipeByImportKey loads a recipe by its bulk-import key.
func (s *Store) GetRecipeByImportKey(ctx context.Context, key string) (*recipe.Recipe, error) {
	return s.getRecipe(ctx, `import_key = ?`, key)
}

func (s *Store) getRecipe(ctx context.Context, where string, arg any) (*recipe.Recipe, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+recipeColumns+` FROM recipes WHERE `+where, arg)
	r, err := scanRecipe(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, recipe.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.loadChildren(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanRecipe(row scanner) (*recipe.Recipe, error) {
	var r recipe.Recipe
	var servings sql.NullFloat64
	var prep, cook, total, importRev sql.NullInt64
	var kind, reasons, created, updated string
	var canonical, importKey, original, raw, createdBy, deleted sql.NullString
	err := row.Scan(&r.ID, &r.Slug, &r.Title, &r.Description, &r.YieldText, &servings, &prep, &cook, &total, &r.Notes,
		&kind, &r.SourceURL, &canonical, &r.SourceSite, &r.Author, &r.ContributedBy, &r.Collection,
		&importKey, &importRev, &original, &raw, &r.NeedsReview, &reasons,
		&createdBy, &created, &updated, &deleted, &r.Revision)
	if err != nil {
		return nil, err
	}
	r.Servings = servings.Float64
	r.PrepMin, r.CookMin, r.TotalMin = int(prep.Int64), int(cook.Int64), int(total.Int64)
	r.SourceKind = recipe.SourceKind(kind)
	r.CanonicalURL, r.ImportKey, r.OriginalText = canonical.String, importKey.String, original.String
	r.ImportRevision = int(importRev.Int64)
	if raw.Valid {
		r.RawSource = json.RawMessage(raw.String)
	}
	_ = json.Unmarshal([]byte(reasons), &r.ReviewReasons)
	r.CreatedBy = createdBy.String
	r.CreatedAt, r.UpdatedAt = parseTime(created), parseTime(updated)
	r.DeletedAt = parseNullTime(deleted)
	return &r, nil
}

func (s *Store) loadChildren(ctx context.Context, r *recipe.Recipe) error {
	// Ingredients: one query joined to groups, in order.
	rows, err := s.db.QueryContext(ctx, `SELECT g.position, g.name, i.raw_text, i.qty_min, i.qty_max, i.unit,
		i.size_text, i.item, i.note, i.parse_ok
		FROM ingredient_groups g LEFT JOIN ingredients i ON i.group_id = g.id
		WHERE g.recipe_id = ? ORDER BY g.position, i.position`, r.ID)
	if err != nil {
		return err
	}
	lastPos := -1
	for rows.Next() {
		var pos int
		var name string
		var raw, unit, size, item, note sql.NullString
		var qmin, qmax sql.NullFloat64
		var ok sql.NullBool
		if err := rows.Scan(&pos, &name, &raw, &qmin, &qmax, &unit, &size, &item, &note, &ok); err != nil {
			rows.Close()
			return err
		}
		if pos != lastPos {
			r.IngredientGroups = append(r.IngredientGroups, recipe.IngredientGroup{Name: name})
			lastPos = pos
		}
		if !raw.Valid {
			continue // a group with no lines (LEFT JOIN)
		}
		g := &r.IngredientGroups[len(r.IngredientGroups)-1]
		g.Ingredients = append(g.Ingredients, recipe.Ingredient{
			Raw: raw.String, ParseOK: ok.Bool, QtyMin: qmin.Float64, QtyMax: qmax.Float64,
			Unit: unit.String, Size: size.String, Item: item.String, Note: note.String,
		})
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT g.position, g.name, st.text
		FROM step_groups g LEFT JOIN steps st ON st.group_id = g.id
		WHERE g.recipe_id = ? ORDER BY g.position, st.position`, r.ID)
	if err != nil {
		return err
	}
	lastPos = -1
	for rows.Next() {
		var pos int
		var name string
		var text sql.NullString
		if err := rows.Scan(&pos, &name, &text); err != nil {
			rows.Close()
			return err
		}
		if pos != lastPos {
			r.StepGroups = append(r.StepGroups, recipe.StepGroup{Name: name})
			lastPos = pos
		}
		if text.Valid {
			g := &r.StepGroups[len(r.StepGroups)-1]
			g.Steps = append(g.Steps, text.String)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}

	r.Categories, err = s.recipeCategoryPaths(ctx, r.ID)
	if err != nil {
		return err
	}
	r.Tags, err = s.stringList(ctx, `SELECT t.name FROM recipe_tags rt JOIN tags t ON t.id = rt.tag_id
		WHERE rt.recipe_id = ? ORDER BY t.name COLLATE NOCASE`, r.ID)
	if err != nil {
		return err
	}
	r.Images, err = s.recipeImages(ctx, r.ID)
	return err
}

// recipeCategoryPaths returns each assigned category as a path of names
// from the root, using a recursive query up the parent chain.
func (s *Store) recipeCategoryPaths(ctx context.Context, recipeID string) ([][]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE up(leaf, id, parent_id, name, depth) AS (
			SELECT c.id, c.id, c.parent_id, c.name, 0
			FROM recipe_categories rc JOIN categories c ON c.id = rc.category_id
			WHERE rc.recipe_id = ?
			UNION ALL
			SELECT up.leaf, p.id, p.parent_id, p.name, up.depth + 1
			FROM up JOIN categories p ON p.id = up.parent_id
		)
		SELECT up.leaf, up.name, (SELECT path FROM categories WHERE id = up.leaf) AS leafpath
		FROM up ORDER BY leafpath, up.leaf, up.depth DESC`, recipeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]string
	last := int64(-1)
	for rows.Next() {
		var leaf int64
		var name, leafPath string
		if err := rows.Scan(&leaf, &name, &leafPath); err != nil {
			return nil, err
		}
		if leaf != last {
			out = append(out, nil)
			last = leaf
		}
		out[len(out)-1] = append(out[len(out)-1], name)
	}
	return out, rows.Err()
}

func (s *Store) recipeImages(ctx context.Context, recipeID string) ([]recipe.Image, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, sha256, mime, width, height, is_primary, source_url
		FROM images WHERE recipe_id = ? ORDER BY position`, recipeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recipe.Image
	for rows.Next() {
		var im recipe.Image
		if err := rows.Scan(&im.ID, &im.SHA256, &im.MIME, &im.Width, &im.Height, &im.IsPrimary, &im.SourceURL); err != nil {
			return nil, err
		}
		out = append(out, im)
	}
	return out, rows.Err()
}

func (s *Store) stringList(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListRecipes returns summaries matching opts and the total count.
func (s *Store) ListRecipes(ctx context.Context, opts recipe.ListOptions) ([]recipe.Summary, int, error) {
	where := []string{"r.deleted_at IS NULL"}
	if opts.Deleted {
		where[0] = "r.deleted_at IS NOT NULL"
	}
	if opts.NeedsReview {
		where = append(where, "r.needs_review = 1")
	}
	w := strings.Join(where, " AND ")
	order := "r.updated_at DESC"
	switch opts.Order {
	case "created":
		order = "r.created_at DESC"
	case "title":
		order = "r.title COLLATE NOCASE ASC"
	case "deleted":
		order = "r.deleted_at DESC"
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM recipes r WHERE `+w).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+summaryColumns+` FROM recipes r WHERE `+w+
		` ORDER BY `+order+`, r.id LIMIT ? OFFSET ?`, opts.Limit, opts.Offset)
	if err != nil {
		return nil, 0, err
	}
	out, err := scanSummaries(rows)
	return out, total, err
}

const summaryColumns = `r.id, r.slug, r.title, r.yield_text, r.total_min, r.contributed_by, r.collection,
	r.needs_review, (SELECT sha256 FROM images i WHERE i.recipe_id = r.id ORDER BY i.is_primary DESC, i.position LIMIT 1),
	r.updated_at, r.deleted_at`

func scanSummaries(rows *sql.Rows) ([]recipe.Summary, error) {
	defer rows.Close()
	var out []recipe.Summary
	for rows.Next() {
		var sm recipe.Summary
		var total sql.NullInt64
		var img, deleted sql.NullString
		var updated string
		if err := rows.Scan(&sm.ID, &sm.Slug, &sm.Title, &sm.YieldText, &total, &sm.ContributedBy, &sm.Collection,
			&sm.NeedsReview, &img, &updated, &deleted); err != nil {
			return nil, err
		}
		sm.TotalMin = int(total.Int64)
		sm.PrimaryImage = img.String
		sm.UpdatedAt = parseTime(updated)
		sm.DeletedAt = parseNullTime(deleted)
		out = append(out, sm)
	}
	return out, rows.Err()
}

// --- trash ---

// SoftDeleteRecipe moves a recipe to the trash.
func (s *Store) SoftDeleteRecipe(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE recipes SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`,
		FormatTime(at), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return s.existsOr(ctx, id, recipe.ErrDeleted)
	}
	return nil
}

// RestoreRecipe takes a recipe out of the trash.
func (s *Store) RestoreRecipe(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE recipes SET deleted_at = NULL WHERE id = ? AND deleted_at IS NOT NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return s.existsOr(ctx, id, nil) // not in the trash: nothing to do
	}
	return nil
}

// existsOr returns ErrNotFound if id doesn't exist, else orErr.
func (s *Store) existsOr(ctx context.Context, id string, orErr error) error {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM recipes WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return recipe.ErrNotFound
	}
	if err != nil {
		return err
	}
	return orErr
}

// PurgeDeleted permanently deletes recipes trashed before the cutoff. Their
// children, revisions, and favorites go with them (ON DELETE CASCADE).
func (s *Store) PurgeDeleted(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM recipes WHERE deleted_at IS NOT NULL AND deleted_at < ?`,
		FormatTime(before))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// --- revisions ---

// ListRevisions lists a recipe's revisions, newest first.
func (s *Store) ListRevisions(ctx context.Context, id string) ([]recipe.RevisionInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT v.revision, COALESCE(NULLIF(u.display_name, ''), u.remote_user, ''), v.edited_at
		FROM revisions v LEFT JOIN users u ON u.id = v.edited_by
		WHERE v.recipe_id = ? ORDER BY v.revision DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recipe.RevisionInfo
	for rows.Next() {
		var ri recipe.RevisionInfo
		var at string
		if err := rows.Scan(&ri.Revision, &ri.EditedBy, &at); err != nil {
			return nil, err
		}
		ri.EditedAt = parseTime(at)
		out = append(out, ri)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, s.existsOr(ctx, id, nil)
	}
	return out, nil
}

// GetRevision returns the recipe as saved at a revision.
func (s *Store) GetRevision(ctx context.Context, id string, revision int) (*recipe.Recipe, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT snapshot FROM revisions WHERE recipe_id = ? AND revision = ?`,
		id, revision).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, recipe.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var snap snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return nil, fmt.Errorf("revision %d of %s: %w", revision, id, err)
	}
	if snap.Recipe == nil {
		return nil, fmt.Errorf("revision %d of %s: empty snapshot", revision, id)
	}
	return snap.Recipe, nil
}
