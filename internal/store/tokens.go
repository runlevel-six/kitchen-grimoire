// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// TokenPrefix marks Kitchen Grimoire tokens, so they are recognisable in
// a leak scanner or a pasted log line.
const TokenPrefix = "kg_"

// ErrBadToken means the token is unknown, malformed, or revoked.
var ErrBadToken = errors.New("invalid or revoked token")

// Token describes an API token. The secret itself is never stored: only
// its SHA-256 hash (§8.3).
type Token struct {
	ID         string
	UserID     string
	UserName   string
	Name       string
	Scope      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// hashToken returns the stored form of a token. A plain SHA-256 is enough:
// tokens are 256 random bits, so there is nothing to brute-force.
func hashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// CreateToken makes a capture-scoped token for a user and returns the
// secret. It is shown once and can't be recovered later.
func (s *Store) CreateToken(ctx context.Context, userID, name string) (Token, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Token{}, "", err
	}
	secret := TokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
	id, err := uuid.NewV7()
	if err != nil {
		return Token{}, "", err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	t := Token{ID: id.String(), UserID: userID, Name: name, Scope: "capture", CreatedAt: now}
	_, err = s.db.ExecContext(ctx, `INSERT INTO api_tokens (id, user_id, name, hash, scope, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, t.ID, userID, name, hashToken(secret), t.Scope, FormatTime(now))
	if err != nil {
		return Token{}, "", err
	}
	return t, secret, nil
}

// Tokens lists tokens, newest first: one user's, or everyone's if userID
// is "" (for admins).
func (s *Store) Tokens(ctx context.Context, userID string) ([]Token, error) {
	q := `SELECT t.id, t.user_id, COALESCE(NULLIF(u.display_name, ''), u.remote_user), t.name, t.scope,
		t.created_at, t.last_used_at, t.revoked_at
		FROM api_tokens t JOIN users u ON u.id = t.user_id`
	var args []any
	if userID != "" {
		q += ` WHERE t.user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY t.created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		var t Token
		var created string
		var used, revoked sql.NullString
		if err := rows.Scan(&t.ID, &t.UserID, &t.UserName, &t.Name, &t.Scope, &created, &used, &revoked); err != nil {
			return nil, err
		}
		t.CreatedAt = parseTime(created)
		t.LastUsedAt, t.RevokedAt = parseNullTime(used), parseNullTime(revoked)
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken revokes a token. A non-admin may only revoke their own
// (userID non-empty restricts it).
func (s *Store) RevokeToken(ctx context.Context, userID, tokenID string) error {
	q := `UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`
	args := []any{Now(), tokenID}
	if userID != "" {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBadToken
	}
	return nil
}

// AuthenticateToken returns the user a live token belongs to and records
// its use. Lookup is by hash, so the comparison never touches the secret.
func (s *Store) AuthenticateToken(ctx context.Context, secret, scope string) (recipe.User, error) {
	if !strings.HasPrefix(secret, TokenPrefix) || len(secret) > 100 {
		return recipe.User{}, ErrBadToken
	}
	var u recipe.User
	var tokenID string
	err := s.db.QueryRowContext(ctx, `SELECT t.id, u.id, u.remote_user, u.display_name, u.email
		FROM api_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.hash = ? AND t.scope = ? AND t.revoked_at IS NULL`, hashToken(secret), scope).
		Scan(&tokenID, &u.ID, &u.RemoteUser, &u.DisplayName, &u.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return recipe.User{}, ErrBadToken
	}
	if err != nil {
		return recipe.User{}, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, Now(), tokenID)
	return u, nil
}
