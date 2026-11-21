// SPDX-License-Identifier: Apache-2.0

package store

import "context"

// CountLiveRecipes returns the number of recipes not in the trash (for the
// grimoire_recipes metric).
func (s *Store) CountLiveRecipes(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM recipes WHERE deleted_at IS NULL`).Scan(&n)
	return n, err
}
