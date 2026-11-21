// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// ErrUserNotFound means no user matched.
var ErrUserNotFound = errors.New("user not found")

// UpsertUser creates the user on first sight (§12) or refreshes their name,
// email, and last-seen time. Empty name/email from the proxy never erase
// stored values.
func (s *Store) UpsertUser(ctx context.Context, remoteUser, displayName, email string) (recipe.User, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return recipe.User{}, err
	}
	now := Now()
	var u recipe.User
	err = s.db.QueryRowContext(ctx, `INSERT INTO users (id, remote_user, display_name, email, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (remote_user) DO UPDATE SET
			display_name = CASE WHEN excluded.display_name <> '' THEN excluded.display_name ELSE users.display_name END,
			email        = CASE WHEN excluded.email <> '' THEN excluded.email ELSE users.email END,
			last_seen_at = excluded.last_seen_at
		RETURNING id, remote_user, display_name, email`,
		id.String(), remoteUser, displayName, email, now, now).
		Scan(&u.ID, &u.RemoteUser, &u.DisplayName, &u.Email)
	return u, err
}

// GetUser loads a user by id.
func (s *Store) GetUser(ctx context.Context, id string) (recipe.User, error) {
	var u recipe.User
	err := s.db.QueryRowContext(ctx, `SELECT id, remote_user, display_name, email FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.RemoteUser, &u.DisplayName, &u.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrUserNotFound
	}
	return u, err
}
