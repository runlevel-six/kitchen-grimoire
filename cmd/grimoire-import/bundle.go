// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/runlevel-six/kitchen-grimoire/internal/exportfmt"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// draftIn is one line of drafts.jsonl (see legacy.WriteDraftsJSONL).
type draftIn struct {
	recipe.Recipe
	Older *recipe.Recipe `json:"older,omitempty"`
}

// bundleCmd turns drafts.jsonl into an importable bundle zip.
func bundleCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	draftsPath := fs.String("drafts", "", "drafts.jsonl written by convert")
	out := fs.String("out", "", "bundle zip to write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *draftsPath == "" || *out == "" {
		return errors.New("--drafts and --out are required")
	}
	f, err := os.Open(*draftsPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var lines [][]byte
	var withOlder int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20) // recipes can be long
	for n := 1; sc.Scan(); n++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var d draftIn
		if err := json.Unmarshal(sc.Bytes(), &d); err != nil {
			return fmt.Errorf("%s line %d: %w", *draftsPath, n, err)
		}
		obj, err := exportfmt.ToJSONLDWithOlder(&d.Recipe, d.Older)
		if err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
		if d.Older != nil {
			withOlder++
		}
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(obj); err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
		lines = append(lines, b.Bytes())
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if err := writeFile(*out, func(w io.Writer) error { return exportfmt.WriteBundleLines(w, lines) }); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d recipes (%d with an older edition) → %s\n", len(lines), withOlder, filepath.Clean(*out))
	return nil
}
