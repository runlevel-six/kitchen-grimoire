// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// MarkReviewed clears a recipe's review flag (FR-12). It changes metadata,
// not content, so it doesn't create a revision.
func (s *Store) MarkReviewed(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE recipes SET needs_review = 0, review_reasons = '[]'
		WHERE id = ? AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return s.existsOr(ctx, id, recipe.ErrDeleted)
	}
	return nil
}
