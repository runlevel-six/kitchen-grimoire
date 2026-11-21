// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// RecipeByCanonicalURL returns the most recently updated live recipe that
// was imported from the given canonical URL, or nil if there is none
// (§8.2 step 10: offer "open existing" before saving a duplicate).
func (s *Store) RecipeByCanonicalURL(ctx context.Context, canonical string) (*recipe.Summary, error) {
	if canonical == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+summaryColumns+` FROM recipes r
		WHERE r.canonical_url = ? AND r.deleted_at IS NULL ORDER BY r.updated_at DESC LIMIT 1`, canonical)
	if err != nil {
		return nil, err
	}
	list, err := scanSummaries(rows)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}
