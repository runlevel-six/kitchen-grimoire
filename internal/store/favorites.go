// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// SetFavorite marks or unmarks a recipe as one of a user's favorites (FR-13).
func (s *Store) SetFavorite(ctx context.Context, userID, recipeID string, on bool) error {
	if on {
		_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO favorites (user_id, recipe_id, created_at) VALUES (?, ?, ?)`,
			userID, recipeID, Now())
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM favorites WHERE user_id = ? AND recipe_id = ?`, userID, recipeID)
	return err
}

// IsFavorite reports whether a user has favorited a recipe.
func (s *Store) IsFavorite(ctx context.Context, userID, recipeID string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM favorites WHERE user_id = ? AND recipe_id = ?`, userID, recipeID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Favorites lists a user's live favorite recipes, most recently added first.
func (s *Store) Favorites(ctx context.Context, userID string, limit int) ([]recipe.Summary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+summaryColumns+` FROM recipes r
		JOIN favorites f ON f.recipe_id = r.id AND f.user_id = ?
		WHERE r.deleted_at IS NULL ORDER BY f.created_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	return scanSummaries(rows)
}
