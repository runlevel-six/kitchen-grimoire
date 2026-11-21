// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func noEnv(string) string { return "" }

func TestRunCommands(t *testing.T) {
	tests := []struct {
		args       []string
		code       int
		wantOut    string
		wantErrOut string
	}{
		{args: nil, code: 2, wantErrOut: "Usage:"},
		{args: []string{"bogus"}, code: 2, wantErrOut: `unknown command "bogus"`},
		{args: []string{"help"}, code: 0, wantOut: "Usage:"},
		{args: []string{"version"}, code: 0, wantOut: "grimoire "},
		{args: []string{"doctor"}, code: 0, wantOut: `"vacuum_into": true`},
		{args: []string{"serve"}, code: 1, wantErrOut: "GRIMOIRE_BASE_URL: is required"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(tt.args, &out, &errOut, noEnv)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.code, errOut.String())
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("stdout %q missing %q", out.String(), tt.wantOut)
			}
			if !strings.Contains(errOut.String(), tt.wantErrOut) {
				t.Errorf("stderr %q missing %q", errOut.String(), tt.wantErrOut)
			}
		})
	}
}

func TestMigrateCommand(t *testing.T) {
	dir := t.TempDir() + "/data" // does not exist yet: migrate creates it
	env := func(k string) string {
		if k == "GRIMOIRE_DATA_DIR" {
			return dir
		}
		return ""
	}
	steps := []struct {
		args []string
		want string
	}{
		{[]string{"migrate", "--status"}, "0001_init                     pending"},
		{[]string{"migrate"}, "applied 0001_init"},
		{[]string{"migrate"}, "database is up to date"},
		{[]string{"migrate", "--status"}, "0001_init                     applied 20"},
		{[]string{"migrate", "--reindex"}, "reindexed 0 recipes"},
	}
	for _, s := range steps {
		var out, errOut bytes.Buffer
		if code := run(s.args, &out, &errOut, env); code != 0 {
			t.Fatalf("%v: exit %d: %s", s.args, code, errOut.String())
		}
		if !strings.Contains(out.String(), s.want) {
			t.Errorf("%v: output %q missing %q", s.args, out.String(), s.want)
		}
	}

	var out, errOut bytes.Buffer
	if code := run([]string{"migrate", "--bogus"}, &out, &errOut, env); code != 2 {
		t.Errorf("bad flag: exit %d, want 2", code)
	}
}

func TestVersionJSON(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"version", "--json"}, &out, &out, noEnv); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	var v map[string]string
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("not JSON: %v: %s", err, out.String())
	}
	if v["version"] == "" || v["go"] == "" {
		t.Errorf("missing fields: %v", v)
	}
}
