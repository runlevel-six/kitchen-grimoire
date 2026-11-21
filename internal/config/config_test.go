// SPDX-License-Identifier: Apache-2.0

package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

// envMap turns a map into a getenv function.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(envMap(map[string]string{
		"GRIMOIRE_BASE_URL":        "https://recipes.example.com/",
		"GRIMOIRE_TRUSTED_PROXIES": "10.0.0.0/8, 192.0.2.7",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Listen != ":8080" || c.AdminListen != ":9090" || c.DataDir != "/data" {
		t.Errorf("listen/admin/data = %q %q %q", c.Listen, c.AdminListen, c.DataDir)
	}
	if got := c.BaseURL.String(); got != "https://recipes.example.com" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", got)
	}
	if c.AuthMode != AuthTrustedHeader {
		t.Errorf("AuthMode = %q", c.AuthMode)
	}
	if len(c.TrustedProxies) != 2 || c.TrustedProxies[1].String() != "192.0.2.7/32" {
		t.Errorf("TrustedProxies = %v", c.TrustedProxies)
	}
	if c.UserHeader != "Remote-User" {
		t.Errorf("UserHeader = %q", c.UserHeader)
	}
	if c.FetchTimeout != 15*time.Second || c.TrashDays != 30 || c.LogLevel != slog.LevelInfo {
		t.Errorf("timeout/trash/level = %v %d %v", c.FetchTimeout, c.TrashDays, c.LogLevel)
	}
}

func TestLoadDevNoAuth(t *testing.T) {
	c, err := Load(envMap(map[string]string{
		"GRIMOIRE_BASE_URL":  "http://localhost:8080",
		"GRIMOIRE_AUTH_MODE": "none",
		"GRIMOIRE_DEV":       "1",
		"GRIMOIRE_LOG_LEVEL": "debug",
		"GRIMOIRE_ADMINS":    "alice, ,bob",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.Dev || c.AuthMode != AuthNone || c.LogLevel != slog.LevelDebug {
		t.Errorf("dev=%v mode=%q level=%v", c.Dev, c.AuthMode, c.LogLevel)
	}
	if strings.Join(c.Admins, "|") != "alice|bob" {
		t.Errorf("Admins = %q", c.Admins)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string // substrings that must all appear in the error
	}{
		{
			name: "missing base url and proxies",
			env:  map[string]string{},
			want: []string{"GRIMOIRE_BASE_URL: is required", "GRIMOIRE_TRUSTED_PROXIES: is required"},
		},
		{
			name: "relative base url",
			env:  map[string]string{"GRIMOIRE_BASE_URL": "/recipes", "GRIMOIRE_TRUSTED_PROXIES": "10.0.0.1"},
			want: []string{"must be an absolute http(s) URL"},
		},
		{
			name: "auth none without dev",
			env:  map[string]string{"GRIMOIRE_BASE_URL": "http://x", "GRIMOIRE_AUTH_MODE": "none"},
			want: []string{"only allowed with GRIMOIRE_DEV=1"},
		},
		{
			name: "unknown auth mode",
			env:  map[string]string{"GRIMOIRE_BASE_URL": "http://x", "GRIMOIRE_AUTH_MODE": "oidc"},
			want: []string{`unknown mode "oidc"`},
		},
		{
			name: "bad values",
			env: map[string]string{
				"GRIMOIRE_BASE_URL":        "http://x",
				"GRIMOIRE_TRUSTED_PROXIES": "10.0.0.0/33",
				"GRIMOIRE_FETCH_TIMEOUT":   "soon",
				"GRIMOIRE_TRASH_DAYS":      "0",
				"GRIMOIRE_LOG_LEVEL":       "loud",
				"GRIMOIRE_DEV":             "maybe",
			},
			want: []string{"TRUSTED_PROXIES", "FETCH_TIMEOUT", "TRASH_DAYS", "LOG_LEVEL", "GRIMOIRE_DEV"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(envMap(tt.env))
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}
