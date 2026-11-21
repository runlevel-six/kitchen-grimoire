// SPDX-License-Identifier: Apache-2.0

package exportfmt

import (
	"bytes"
	"io"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// FuzzReadBundle feeds arbitrary bytes to ReadBundle and maps every item
// it returns. Nothing may panic, and an accepted bundle must respect the
// limits. Run longer locally with:
//
//	go test ./internal/exportfmt -run '^$' -fuzz FuzzReadBundle -fuzztime 1m
func FuzzReadBundle(f *testing.F) {
	f.Add(writeBundle(f, fullRecipe(), &recipe.Recipe{ID: "x", Title: "Second"}))
	f.Add(zipOf(f, entry{RecipesFile, `{"@type":"Recipe","name":"a","x-grimoire":{"stepGroups":[{"name":null,"lines":["s"]}]}}` + "\n"}))
	f.Add(zipOf(f, entry{RecipesFile, "not json\n"}, entry{"images/", ""}))
	f.Add(zipOf(f, entry{"../evil", "x"}))
	f.Add([]byte{})
	f.Add([]byte("PK\x05\x06" + string(make([]byte, 18))))

	lim := Limits{MaxEntries: 20, MaxRecipes: 20, MaxJSONL: 64 << 10, MaxLine: 16 << 10, MaxImage: 4 << 10, MaxTotal: 128 << 10}
	f.Fuzz(func(t *testing.T, data []byte) {
		b, err := ReadBundle(bytes.NewReader(data), int64(len(data)), lim)
		if err != nil {
			return
		}
		if len(b.Items) > lim.MaxRecipes || b.ImageCount() > lim.MaxEntries {
			t.Fatalf("limits exceeded: %d items, %d images", len(b.Items), b.ImageCount())
		}
		for _, it := range b.Items {
			if it.Err != nil {
				continue
			}
			r, _, err := FromJSONLD(it.Object)
			if err != nil {
				continue
			}
			recipe.Normalize(r)
			if _, err := MarshalLine(r); err != nil {
				t.Fatalf("re-encode: %v", err)
			}
			for _, im := range r.Images {
				ext := "jpg"
				if im.MIME == "image/png" {
					ext = "png"
				}
				rc, err := b.OpenImage(im.SHA256, ext)
				if err != nil {
					continue
				}
				n, _ := io.Copy(io.Discard, rc)
				rc.Close()
				if n > lim.MaxImage {
					t.Fatalf("read %d image bytes, limit %d", n, lim.MaxImage)
				}
			}
		}
	})
}
