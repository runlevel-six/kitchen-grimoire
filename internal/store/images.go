// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// ErrImageNotFound means no image matched for that recipe.
var ErrImageNotFound = errors.New("image not found")

// AddImage records an image for a recipe at the end of its list. The first
// image becomes the primary one.
func (s *Store) AddImage(ctx context.Context, recipeID string, im recipe.Image) (recipe.Image, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return im, err
	}
	im.ID = id.String()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var n, maxPos int
		if err := tx.QueryRowContext(ctx, `SELECT count(*), COALESCE(max(position), -1) FROM images WHERE recipe_id = ?`,
			recipeID).Scan(&n, &maxPos); err != nil {
			return err
		}
		im.IsPrimary = n == 0
		_, err := tx.ExecContext(ctx, `INSERT INTO images
			(id, recipe_id, sha256, mime, width, height, position, is_primary, source_url, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			im.ID, recipeID, im.SHA256, im.MIME, im.Width, im.Height, maxPos+1, im.IsPrimary, im.SourceURL, Now())
		if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
			return recipe.ErrNotFound
		}
		return err
	})
	return im, err
}

// RemoveImage deletes an image row. If it was primary, the next image
// becomes primary. The file itself is removed later by GC, because another
// recipe (or a revision) may use the same content.
func (s *Store) RemoveImage(ctx context.Context, recipeID, imageID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var primary bool
		err := tx.QueryRowContext(ctx, `DELETE FROM images WHERE id = ? AND recipe_id = ? RETURNING is_primary`,
			imageID, recipeID).Scan(&primary)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrImageNotFound
		}
		if err != nil || !primary {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE images SET is_primary = 1 WHERE id =
			(SELECT id FROM images WHERE recipe_id = ? ORDER BY position LIMIT 1)`, recipeID)
		return err
	})
}

// SetPrimaryImage makes one image the recipe's primary photo.
func (s *Store) SetPrimaryImage(ctx context.Context, recipeID, imageID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM images WHERE id = ? AND recipe_id = ?`, imageID, recipeID).
			Scan(&one); errors.Is(err, sql.ErrNoRows) {
			return ErrImageNotFound
		} else if err != nil {
			return err
		}
		// Clear first: the partial unique index allows only one primary.
		if _, err := tx.ExecContext(ctx, `UPDATE images SET is_primary = 0 WHERE recipe_id = ?`, recipeID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE images SET is_primary = 1 WHERE id = ?`, imageID)
		return err
	})
}

// ImageInUse reports whether any recipe (live or trashed) references the
// image content. Image GC keeps files that are in use.
func (s *Store) ImageInUse(ctx context.Context, sha string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM images WHERE sha256 = ? LIMIT 1`, sha).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
