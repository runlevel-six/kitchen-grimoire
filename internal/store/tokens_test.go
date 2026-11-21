// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTokens(t *testing.T) {
	ctx := context.Background()
	_, st := newService(t)
	alice, _ := st.UpsertUser(ctx, "alice", "Alice", "")
	bob, _ := st.UpsertUser(ctx, "bob", "Bob", "")

	tok, secret, err := st.CreateToken(ctx, alice.ID, "iPhone")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, TokenPrefix) || len(secret) < 40 {
		t.Fatalf("secret = %q", secret)
	}
	var stored string
	_ = st.db.QueryRow(`SELECT hash FROM api_tokens WHERE id = ?`, tok.ID).Scan(&stored)
	if strings.Contains(stored, secret) || stored == secret {
		t.Fatal("the secret itself was stored")
	}

	u, err := st.AuthenticateToken(ctx, secret, "capture")
	if err != nil || u.ID != alice.ID {
		t.Fatalf("authenticate = %+v, %v", u, err)
	}
	if _, err := st.AuthenticateToken(ctx, secret, "admin"); !errors.Is(err, ErrBadToken) {
		t.Errorf("wrong scope: %v", err)
	}
	if _, err := st.AuthenticateToken(ctx, secret+"x", "capture"); !errors.Is(err, ErrBadToken) {
		t.Errorf("wrong secret: %v", err)
	}
	list, _ := st.Tokens(ctx, alice.ID)
	if len(list) != 1 || list[0].LastUsedAt == nil || list[0].UserName != "Alice" {
		t.Errorf("tokens = %+v", list)
	}

	// Bob can't revoke Alice's token; Alice can; an admin ("") can revoke any.
	if err := st.RevokeToken(ctx, bob.ID, tok.ID); !errors.Is(err, ErrBadToken) {
		t.Errorf("bob revoked alice's token: %v", err)
	}
	if err := st.RevokeToken(ctx, alice.ID, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AuthenticateToken(ctx, secret, "capture"); !errors.Is(err, ErrBadToken) {
		t.Errorf("revoked token still works: %v", err)
	}
	_, s2, _ := st.CreateToken(ctx, bob.ID, "laptop")
	all, _ := st.Tokens(ctx, "")
	if len(all) != 2 {
		t.Errorf("admin list = %d tokens", len(all))
	}
	var bobTok string
	for _, tk := range all {
		if tk.Name == "laptop" {
			bobTok = tk.ID
		}
	}
	if err := st.RevokeToken(ctx, "", bobTok); err != nil {
		t.Errorf("admin revoke: %v", err)
	}
	if _, err := st.AuthenticateToken(ctx, s2, "capture"); !errors.Is(err, ErrBadToken) {
		t.Errorf("admin-revoked token still works")
	}
}
