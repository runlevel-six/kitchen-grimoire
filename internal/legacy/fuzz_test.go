// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzSplit checks that splitting and converting never panic, and that
// every input byte is accounted for: the ignored prefix and the blocks'
// Raw text concatenate back to the input exactly (docs/DESIGN.md §15.2).
func FuzzSplit(f *testing.F) {
	m, err := LoadMap(strings.NewReader(splitMapCSV))
	if err != nil {
		f.Fatal(err)
	}
	for _, name := range []string{"sample-older.txt", "sample-newer.txt"} {
		if b, err := os.ReadFile(filepath.Join(fixtureDir, name)); err == nil {
			f.Add(string(b))
		}
	}
	for _, seed := range []string{
		"",
		"CHEESE BALL\tAppetizers\n1 cup cheese\nMix.\n",
		"intro\r\nLAYERED SALADSalads\rFRIED EGGS—easy Breakfast Dishes\n",
		"\f[[PAGE 1]]\nCAKES AND BREADS\nSPECIAL SAUCE:\n\f",
		"PIE Desserts\nFor 2:\tfor 1:\n1 cup a\t2 cup\nTopping:\n",
		"\xff\xfeAB Salads\n\x00",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		for _, format := range []Format{FormatAuto, FormatTabbed, FormatPDFText} {
			d := Split(in, format, m)
			var b strings.Builder
			b.WriteString(d.Prefix)
			for i, bl := range d.Blocks {
				if bl.Ordinal != i+1 {
					t.Fatalf("block %d has ordinal %d", i, bl.Ordinal)
				}
				if bl.Raw == "" {
					t.Fatalf("block %d is empty", i)
				}
				b.WriteString(bl.Raw)
			}
			if b.String() != in {
				t.Fatalf("prefix + blocks = %q, want %q", b.String(), in)
			}
			res := Convert(in, Options{Format: format, Map: m, Collection: "Fuzz"})
			if len(res.Drafts) != len(d.Blocks) {
				t.Fatalf("%d drafts for %d blocks", len(res.Drafts), len(d.Blocks))
			}
			Merge(res.Drafts, res.Drafts)
		}
		Tokens(in, FormatAuto, m)
	})
}
