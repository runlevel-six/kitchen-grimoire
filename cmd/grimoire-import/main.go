// SPDX-License-Identifier: Apache-2.0

// Command grimoire-import converts a legacy recipe collection (UTF-8
// text, see docs/how-to/import-a-legacy-collection.md) into recipe drafts
// and review reports. It is a separate binary and is never built into the
// server image (D-07).
//
// Usage:
//
//	grimoire-import tokens  --in FILE [--format F] [--map FILE]
//	grimoire-import convert --in FILE [--format F] --map FILE [--normalize FILE]
//	                        --collection NAME [--older FILE --older-format F] --out DIR
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/runlevel-six/kitchen-grimoire/internal/legacy"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is main without process-global side effects, for tests.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	var err error
	switch args[0] {
	case "tokens", "--emit-tokens":
		err = tokensCmd(args[1:], stdout, stderr)
	case "convert":
		err = convertCmd(args[1:], stdout, stderr)
	case "bundle":
		err = bundleCmd(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "grimoire-import: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "grimoire-import %s: %v\n", args[0], err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage:
  grimoire-import tokens  --in FILE [--format tabbed|pdftext|auto] [--map FILE]
      List every distinct title-line token with its count and current class
      (UNKNOWN when the map lacks it), as CSV on stdout.
  grimoire-import convert --in FILE [--format ...] --map FILE [--normalize FILE]
                          --collection NAME [--older FILE --older-format ... --pairs FILE] --out DIR
      Write DIR/drafts.jsonl, DIR/report.md, and DIR/report.csv.
  grimoire-import bundle  --drafts DIR/drafts.jsonl --out FILE.zip
      Pack reviewed drafts into a bundle for POST /api/v1/import/bundle
      (or the admin import page). Matched older editions are carried as
      x-grimoire.olderEdition and become revision 1 on import (D-13).
`)
}

// readText reads a UTF-8 text file.
func readText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("%s is not UTF-8 text; convert it first (see docs/how-to/import-a-legacy-collection.md)", path)
	}
	return strings.TrimPrefix(string(b), "\xef\xbb\xbf"), nil // a byte-order mark
}

func loadMap(path string) (*legacy.Map, error) {
	if path == "" {
		return legacy.NewMap(), nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return legacy.LoadMap(f)
}

func tokensCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tokens", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "", "source text file (UTF-8)")
	format := fs.String("format", "auto", "tabbed, pdftext, or auto")
	mapPath := fs.String("map", "", "attribution mapping CSV (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("--in is required")
	}
	f, err := legacy.ParseFormat(*format)
	if err != nil {
		return err
	}
	text, err := readText(*in)
	if err != nil {
		return err
	}
	m, err := loadMap(*mapPath)
	if err != nil {
		return err
	}
	return legacy.WriteTokensCSV(stdout, legacy.Tokens(text, f, m))
}

func convertCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "", "source text file (UTF-8); the newer edition when --older is given")
	format := fs.String("format", "auto", "tabbed, pdftext, or auto")
	mapPath := fs.String("map", "", "attribution mapping CSV")
	normPath := fs.String("normalize", "", "ingredient name override CSV (from,to)")
	collection := fs.String("collection", "", "collection name, e.g. \"Example Family Cookbook\"")
	olderPath := fs.String("older", "", "older edition of the same collection (optional, D-13)")
	olderFormat := fs.String("older-format", "auto", "format of --older")
	pairsPath := fs.String("pairs", "", "confirmed renames CSV (older,newer) for titles the editions don't match on their own")
	out := fs.String("out", "", "output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *in == "":
		return errors.New("--in is required")
	case *pairsPath != "" && *olderPath == "":
		return errors.New("--pairs needs --older")
	case *mapPath == "":
		return errors.New("--map is required (start from `grimoire-import tokens`)")
	case strings.TrimSpace(*collection) == "":
		return errors.New("--collection is required")
	case *out == "":
		return errors.New("--out is required")
	}
	f, err := legacy.ParseFormat(*format)
	if err != nil {
		return err
	}
	of, err := legacy.ParseFormat(*olderFormat)
	if err != nil {
		return fmt.Errorf("--older-format: %w", err)
	}
	m, err := loadMap(*mapPath)
	if err != nil {
		return err
	}
	var norm *legacy.Normalizer
	if *normPath != "" {
		nf, err := os.Open(*normPath)
		if err != nil {
			return err
		}
		norm, err = legacy.LoadNormalizer(nf)
		nf.Close()
		if err != nil {
			return err
		}
	}
	text, err := readText(*in)
	if err != nil {
		return err
	}

	opt := legacy.Options{Format: f, Map: m, Normalizer: norm, Collection: strings.TrimSpace(*collection)}
	res := legacy.Convert(text, opt)
	rep := &legacy.Report{Collection: opt.Collection, Format: res.Format, PrefixLines: res.PrefixLines}
	drafts := res.Drafts
	if *olderPath != "" {
		otext, err := readText(*olderPath)
		if err != nil {
			return err
		}
		oopt := opt
		oopt.Format, oopt.KeySuffix = of, "older"
		ores := legacy.Convert(otext, oopt)
		var pairs legacy.Pairs
		if *pairsPath != "" {
			pf, err := os.Open(*pairsPath)
			if err != nil {
				return err
			}
			pairs, err = legacy.LoadPairs(pf)
			pf.Close()
			if err != nil {
				return err
			}
		}
		drafts, err = legacy.MergePairs(drafts, ores.Drafts, pairs)
		if err != nil {
			return err
		}
		rep.HasOlder, rep.OlderFormat, rep.OlderPrefixLines = true, ores.Format, ores.PrefixLines
	}
	rep.Drafts = drafts

	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	draftsPath := filepath.Join(*out, "drafts.jsonl")
	rep.TitleChanges = titleChanges(draftsPath, drafts)
	if err := writeFile(draftsPath, func(w io.Writer) error { return legacy.WriteDraftsJSONL(w, drafts) }); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(*out, "report.md"), rep.WriteMarkdown); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(*out, "report.csv"), rep.WriteCSV); err != nil {
		return err
	}
	review := 0
	for _, d := range drafts {
		if d.Recipe.NeedsReview {
			review++
		}
	}
	fmt.Fprintf(stdout, "%d recipes (%d need review) → %s\n", len(drafts), review, *out)
	if len(rep.TitleChanges) > 0 {
		fmt.Fprintf(stdout, "warning: %d ordinals changed title since the previous run; see report.md\n", len(rep.TitleChanges))
	}
	return nil
}

// titleChanges compares the drafts with a previous run's drafts.jsonl, if
// there is one, and lists ordinals whose title (and so import key) changed.
func titleChanges(prevPath string, drafts []legacy.Draft) []string {
	f, err := os.Open(prevPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	prev := map[string]string{} // key without the title slug → full key
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var r struct {
			ImportKey string `json:"importKey"`
		}
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.ImportKey != "" {
			prev[keyStem(r.ImportKey)] = r.ImportKey
		}
	}
	var out []string
	for _, d := range drafts {
		k := d.Recipe.ImportKey
		if old, ok := prev[keyStem(k)]; ok && old != k {
			out = append(out, fmt.Sprintf("%s (was %s)", k, old))
		}
	}
	return out
}

// keyStem drops the title slug: "legacy:c:0042:title" → "legacy:c:0042".
func keyStem(k string) string {
	if i := strings.LastIndexByte(k, ':'); i >= 0 {
		return k[:i]
	}
	return k
}

// writeFile writes through a temporary file and renames it into place.
func writeFile(path string, write func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path))
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	bw := bufio.NewWriter(tmp)
	if err := write(bw); err != nil {
		tmp.Close()
		return err
	}
	if err := bw.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
