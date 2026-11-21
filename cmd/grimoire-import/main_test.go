// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtures = "../../testdata/legacy"

func runCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestTokens(t *testing.T) {
	code, out, errs := runCmd(t, "tokens", "--in", filepath.Join(fixtures, "sample-older.txt"),
		"--map", filepath.Join(fixtures, "attribution-map.csv"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{
		"token,count,class,canonical\n",
		"Aunt Example,3,contributor,Aunt Example\n",
		"Neighbor Invented,1,UNKNOWN,\n",
		"Microwave/Cousin Sample,1,compound,tag=Microwave; contributor=Cousin Sample\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestConvertMatchesGolden runs the CLI end to end and compares its files
// with the library's golden output.
func TestConvertMatchesGolden(t *testing.T) {
	dir := t.TempDir()
	code, out, errs := runCmd(t, "convert",
		"--in", filepath.Join(fixtures, "sample-newer.txt"),
		"--older", filepath.Join(fixtures, "sample-older.txt"), "--older-format", "tabbed",
		"--map", filepath.Join(fixtures, "attribution-map.csv"),
		"--normalize", filepath.Join(fixtures, "normalize.csv"),
		"--collection", "Example Family Cookbook", "--out", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "14 recipes") {
		t.Errorf("stdout = %q", out)
	}
	for file, golden := range map[string]string{
		"drafts.jsonl": "merged.drafts.jsonl",
		"report.csv":   "merged.report.csv",
		"report.md":    "merged.report.md",
	} {
		got, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(fixtures, "golden", golden))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from golden %s", file, golden)
		}
	}

	// A second run over a source whose recipe 2 changed title warns that
	// its import key changed.
	src, err := os.ReadFile(filepath.Join(fixtures, "sample-newer.txt"))
	if err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(t.TempDir(), "edited.txt")
	if err := os.WriteFile(edited, bytes.Replace(src, []byte("CORNBREAD Breads"), []byte("SKILLET CORNBREAD Breads"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs = runCmd(t, "convert", "--in", edited, "--map", filepath.Join(fixtures, "attribution-map.csv"),
		"--collection", "Example Family Cookbook", "--out", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "warning: 1 ordinals changed title") {
		t.Errorf("stdout = %q", out)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "report.md"))
	if !strings.Contains(string(md), "legacy:example-family-cookbook:0002:skillet-cornbread (was legacy:example-family-cookbook:0002:cornbread)") {
		t.Errorf("report.md lacks the title change:\n%s", md)
	}
}

func TestErrors(t *testing.T) {
	notUTF8 := filepath.Join(t.TempDir(), "latin1.txt")
	if err := os.WriteFile(notUTF8, []byte("CR\xc8ME BR\xdbL\xc9E\tDesserts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		code int
		msg  string
	}{
		{nil, 2, "usage"},
		{[]string{"bogus"}, 2, "unknown command"},
		{[]string{"tokens"}, 1, "--in is required"},
		{[]string{"tokens", "--in", notUTF8}, 1, "not UTF-8"},
		{[]string{"convert", "--in", "x"}, 1, "--map is required"},
		{[]string{"convert", "--in", "x", "--map", "m", "--out", "o"}, 1, "--collection is required"},
		{[]string{"convert", "--in", "x", "--map", "m", "--collection", "C", "--out", "o", "--format", "docx"}, 1, "unknown format"},
	}
	for _, c := range cases {
		code, _, errs := runCmd(t, c.args...)
		if code != c.code || !strings.Contains(errs, c.msg) {
			t.Errorf("%v: exit %d, stderr %q; want %d and %q", c.args, code, errs, c.code, c.msg)
		}
	}
}
