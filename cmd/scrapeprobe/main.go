// SPDX-License-Identifier: Apache-2.0

// Command scrapeprobe is the throwaway tool for spike S3 (docs/DESIGN.md
// §19): it fetches a list of recipe pages and reports which structured data
// each one publishes, so we know how far JSON-LD alone gets us before
// writing the real extractor (P3.2). It is not part of the image and will be
// removed once internal/scrape exists.
//
//	go run ./cmd/scrapeprobe -in local/fixtures/urls.txt -out local/work/s3/results.csv
//
// Fetching is deliberately polite: an honest User-Agent, one request per
// host at a time, a pause between requests, no cookies, no retries.
package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/runlevel-six/kitchen-grimoire/internal/product"
)

const maxBody = 5 << 20 // §15.1: 5 MiB cap for HTML

// result is one row of the report.
type result struct {
	URL, FinalURL, Host string
	Status              int
	Class               string // jsonld, microdata, opengraph, none, blocked, http_error, error
	Err                 string

	JSONLDBlocks, JSONLDBad int
	Repaired                bool   // a block only parsed after lenient repair
	RecipeWhere             string // top, array, graph, mainEntity
	Name                    bool
	Ingredients             int
	Instructions            string // string, strings, HowToStep, HowToSection, mixed, none
	Image                   string // string, array, ImageObject, none
	Prep, Cook, Total       bool
	Yield                   bool
	Author                  bool
	Category, Keywords      bool
	Microdata, OpenGraph    bool
}

func main() {
	in := flag.String("in", "local/fixtures/urls.txt", "file with one URL per line (# comments allowed)")
	out := flag.String("out", "local/work/s3/results.csv", "CSV output")
	delay := flag.Duration("delay", 2*time.Second, "pause between requests to the same host")
	flag.Parse()

	urls, err := readURLs(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}

	// Group by host: hosts run in parallel, URLs within a host one at a time.
	byHost := map[string][]string{}
	for _, u := range urls {
		pu, err := url.Parse(u)
		if err != nil {
			continue
		}
		byHost[pu.Hostname()] = append(byHost[pu.Hostname()], u)
	}
	var (
		mu      sync.Mutex
		results []result
		wg      sync.WaitGroup
		sem     = make(chan struct{}, 6)
	)
	for _, list := range byHost {
		wg.Add(1)
		go func(list []string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			for i, u := range list {
				if i > 0 {
					time.Sleep(*delay)
				}
				r := probe(context.Background(), client, u)
				mu.Lock()
				results = append(results, r)
				fmt.Fprintf(os.Stderr, "%-10s %3d %s\n", r.Class, r.Status, u)
				mu.Unlock()
			}
		}(list)
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].URL < results[j].URL })

	if err := writeCSV(*out, results); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	summarize(os.Stdout, results)
}

func readURLs(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

func probe(ctx context.Context, client *http.Client, raw string) result {
	r := result{URL: raw}
	if pu, err := url.Parse(raw); err == nil {
		r.Host = strings.TrimPrefix(pu.Hostname(), "www.")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		r.Class, r.Err = "error", err.Error()
		return r
	}
	req.Header.Set("User-Agent", product.UserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil {
		r.Class, r.Err = "error", err.Error()
		return r
	}
	defer resp.Body.Close()
	r.Status = resp.StatusCode
	r.FinalURL = resp.Request.URL.String()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429 || resp.StatusCode == 503:
		r.Class = "blocked"
		return r
	case resp.StatusCode >= 400:
		r.Class = "http_error"
		return r
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		r.Class, r.Err = "error", err.Error()
		return r
	}
	analyze(&r, string(body))
	return r
}

// analyze walks the HTML once, collecting JSON-LD blocks and noting
// Microdata and OpenGraph, then inspects the Recipe node.
func analyze(r *result, page string) {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		r.Class, r.Err = "error", err.Error()
		return
	}
	var blocks []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script":
				if strings.EqualFold(strings.TrimSpace(attr(n, "type")), "application/ld+json") && n.FirstChild != nil {
					blocks = append(blocks, n.FirstChild.Data)
				}
			case "meta":
				if strings.HasPrefix(attr(n, "property"), "og:") {
					r.OpenGraph = true
				}
			}
			if t := attr(n, "itemtype"); strings.Contains(t, "schema.org/Recipe") {
				r.Microdata = true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	r.JSONLDBlocks = len(blocks)
	var recipe map[string]any
	for _, b := range blocks {
		var v any
		if err := json.Unmarshal([]byte(strings.TrimSpace(b)), &v); err != nil {
			r.JSONLDBad++
			// Lenient retry (§8.2 step 2): escape raw control characters
			// inside string literals, the most common publisher mistake.
			if err := json.Unmarshal([]byte(escapeControlInStrings(strings.TrimSpace(b))), &v); err != nil {
				continue
			}
			r.Repaired = true
		}
		if node, where := findRecipe(v, "top"); node != nil {
			recipe, r.RecipeWhere = node, where
			break
		}
	}

	switch {
	case recipe != nil:
		r.Class = "jsonld"
		inspect(r, recipe)
	case r.Microdata:
		r.Class = "microdata"
	case r.OpenGraph:
		r.Class = "opengraph"
	default:
		r.Class = "none"
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// findRecipe looks for a node whose @type is or contains "Recipe" at the
// top level, in arrays, in @graph, or under mainEntity (§8.2 step 3).
func findRecipe(v any, where string) (map[string]any, string) {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			w := where
			if w == "top" {
				w = "array"
			}
			if n, ww := findRecipe(e, w); n != nil {
				return n, ww
			}
		}
	case map[string]any:
		if isType(t["@type"], "Recipe") {
			return t, where
		}
		if g, ok := t["@graph"]; ok {
			if n, _ := findRecipe(g, "graph"); n != nil {
				return n, "graph"
			}
		}
		if m, ok := t["mainEntity"]; ok {
			if n, _ := findRecipe(m, "mainEntity"); n != nil {
				return n, "mainEntity"
			}
		}
	}
	return nil, ""
}

func isType(v any, want string) bool {
	switch t := v.(type) {
	case string:
		return t == want || strings.HasSuffix(t, "/"+want)
	case []any:
		for _, e := range t {
			if isType(e, want) {
				return true
			}
		}
	}
	return false
}

func present(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(t) != ""
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}

func inspect(r *result, n map[string]any) {
	r.Name = present(n["name"])
	if ing, ok := n["recipeIngredient"].([]any); ok {
		r.Ingredients = len(ing)
	} else if present(n["ingredients"]) { // old schema name
		r.Ingredients = -1
	}
	r.Instructions = instructionShape(n["recipeInstructions"])
	switch im := n["image"].(type) {
	case nil:
		r.Image = "none"
	case string:
		r.Image = "string"
	case []any:
		r.Image = "array"
	case map[string]any:
		r.Image = "ImageObject"
		_ = im
	}
	r.Prep, r.Cook, r.Total = present(n["prepTime"]), present(n["cookTime"]), present(n["totalTime"])
	r.Yield = present(n["recipeYield"])
	r.Author = present(n["author"])
	r.Category = present(n["recipeCategory"])
	r.Keywords = present(n["keywords"])
}

func instructionShape(v any) string {
	switch t := v.(type) {
	case nil:
		return "none"
	case string:
		return "string"
	case []any:
		kinds := map[string]bool{}
		for _, e := range t {
			switch x := e.(type) {
			case string:
				kinds["strings"] = true
			case map[string]any:
				switch {
				case isType(x["@type"], "HowToSection"):
					kinds["HowToSection"] = true
				case isType(x["@type"], "HowToStep"):
					kinds["HowToStep"] = true
				default:
					kinds["object"] = true
				}
			}
		}
		if len(kinds) == 1 {
			for k := range kinds {
				return k
			}
		}
		if len(kinds) == 0 {
			return "none"
		}
		return "mixed"
	case map[string]any:
		return "object"
	}
	return "other"
}

func writeCSV(path string, rs []result) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"url", "final_url", "host", "status", "class", "err", "jsonld_blocks", "jsonld_bad", "repaired",
		"recipe_where", "name", "ingredients", "instructions", "image", "prep", "cook", "total",
		"yield", "author", "category", "keywords", "microdata", "opengraph"})
	b := strconv.FormatBool
	for _, r := range rs {
		_ = w.Write([]string{r.URL, r.FinalURL, r.Host, strconv.Itoa(r.Status), r.Class, r.Err,
			strconv.Itoa(r.JSONLDBlocks), strconv.Itoa(r.JSONLDBad), b(r.Repaired), r.RecipeWhere, b(r.Name),
			strconv.Itoa(r.Ingredients), r.Instructions, r.Image, b(r.Prep), b(r.Cook), b(r.Total),
			b(r.Yield), b(r.Author), b(r.Category), b(r.Keywords), b(r.Microdata), b(r.OpenGraph)})
	}
	w.Flush()
	return errors.Join(w.Error(), f.Close())
}

// escapeControlInStrings escapes raw control characters (newlines, tabs)
// that appear inside JSON string literals, leaving structure untouched.
func escapeControlInStrings(s string) string {
	var b strings.Builder
	inStr, esc := false, false
	for _, c := range s {
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr && c < 0x20:
			fmt.Fprintf(&b, `\u%04x`, c)
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

func pct(n, d int) string {
	if d == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%3.0f%% (%d/%d)", 100*float64(n)/float64(d), n, d)
}

func summarize(w io.Writer, rs []result) {
	classes := map[string]int{}
	shapes := map[string]int{}
	where := map[string]int{}
	var reachable, jsonld, core, name, ing, steps, img, total, yield, author int
	for _, r := range rs {
		classes[r.Class]++
		if r.Class == "jsonld" || r.Class == "microdata" || r.Class == "opengraph" || r.Class == "none" {
			reachable++
		}
		if r.Class != "jsonld" {
			continue
		}
		jsonld++
		where[r.RecipeWhere]++
		shapes[r.Instructions]++
		if r.Name {
			name++
		}
		if r.Ingredients > 0 {
			ing++
		}
		if r.Instructions != "none" {
			steps++
		}
		if r.Name && r.Ingredients > 0 && r.Instructions != "none" {
			core++
		}
		if r.Image != "none" {
			img++
		}
		if r.Total || r.Cook || r.Prep {
			total++
		}
		if r.Yield {
			yield++
		}
		if r.Author {
			author++
		}
	}
	fmt.Fprintf(w, "URLs: %d   classes: %v\n", len(rs), classes)
	fmt.Fprintf(w, "JSON-LD Recipe among reachable pages: %s\n", pct(jsonld, reachable))
	fmt.Fprintf(w, "Of JSON-LD recipes:\n")
	fmt.Fprintf(w, "  title+ingredients+steps: %s\n", pct(core, jsonld))
	fmt.Fprintf(w, "  name %s  ingredients %s  instructions %s\n", pct(name, jsonld), pct(ing, jsonld), pct(steps, jsonld))
	fmt.Fprintf(w, "  image %s  any time %s  yield %s  author %s\n", pct(img, jsonld), pct(total, jsonld), pct(yield, jsonld), pct(author, jsonld))
	fmt.Fprintf(w, "  recipe node location: %v\n", where)
	fmt.Fprintf(w, "  instruction shapes: %v\n", shapes)
}
