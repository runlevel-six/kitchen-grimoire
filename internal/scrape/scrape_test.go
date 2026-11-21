// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestHostileInputs checks that deeply nested markup and JSON and
// reference cycles neither overflow the stack nor take long.
func TestHostileInputs(t *testing.T) {
	u, _ := url.Parse("https://recipes.example.com/x")
	deepHTML := `<title>Deep</title>` + strings.Repeat(`<div itemscope itemtype="https://schema.org/Recipe" itemprop="x">`, 100000)
	deepJSON := `<script type="application/ld+json">{"@type":"Recipe","name":"Deep","recipeInstructions":` +
		strings.Repeat(`[`, 9000) + strings.Repeat(`]`, 9000) + `}</script>`
	cyclic := `<script type="application/ld+json">{"@graph":[{"@id":"a","@type":"Recipe","name":"Cycle","author":{"@id":"b"}},` +
		`{"@id":"b","@type":"Person","name":"B","knows":{"@id":"a"}}]}</script>`
	// html.Parse refuses nesting past 512; Extract then falls back to a
	// token scan, so the JSON-LD in front still counts.
	tooDeep := `<script type="application/ld+json">{"@type":"Recipe","name":"Too deep"}</script>` + strings.Repeat(`<div>`, 2000)
	deepItems := strings.Repeat(`<div itemscope itemtype="https://schema.org/HowToSection" itemprop="recipeInstructions">`, 400)
	deepMicrodata := `<div itemscope itemtype="https://schema.org/Recipe"><h1 itemprop="name">Deep items</h1>` + deepItems + `</div>`
	for name, page := range map[string]string{"deep html": deepHTML, "deep json": deepJSON, "cycle": cyclic,
		"too deep for parser": tooDeep, "deep microdata": deepMicrodata} {
		start := time.Now()
		res, err := Extract([]byte(page), u)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s: took %v", name, d)
		}
		if res.Recipe.Title == "" {
			t.Errorf("%s: no title", name)
		}
	}
}

func TestParseISODuration(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"PT1H15M", 75 * time.Minute, true},
		{"PT90M", 90 * time.Minute, true},
		{"P0DT1H", time.Hour, true},
		{"PT1H 15M", 75 * time.Minute, true},
		{"pt45m", 45 * time.Minute, true},
		{"PT1.5H", 90 * time.Minute, true},
		{"PT0,5H", 30 * time.Minute, true},
		{"PT30S", 30 * time.Second, true},
		{"P1DT2H", 26 * time.Hour, true},
		{"P1W", 7 * 24 * time.Hour, true},
		{"P1H30M", 90 * time.Minute, true}, // missing T
		{"PT1H15", 75 * time.Minute, true}, // trailing number: minutes
		{"P30M", 30 * time.Minute, true},   // minutes, not months
		{"PT0M", 0, true},
		{" PT10M ", 10 * time.Minute, true},
		{"45 minutes", 45 * time.Minute, true},
		{"1 hour 15 minutes", 75 * time.Minute, true},
		{"1 hr 15 mins", 75 * time.Minute, true},
		{"1h15m", 75 * time.Minute, true},
		{"2 Hours", 2 * time.Hour, true},
		{"1 hour and 5 minutes", 65 * time.Minute, true},
		{"about 20 min", 20 * time.Minute, true},
		{"1:15", 75 * time.Minute, true},
		{"30", 30 * time.Minute, true},
		{"", 0, false},
		{"P", 0, false},
		{"PT", 0, false},
		{"PTXM", 0, false},
		{"PT-5M", 0, false},
		{"P400D", 0, false}, // over a year
		{"4 servings", 0, false},
		{"about an hour", 0, false},
		{"30 45", 0, false},
		{"1 hour 30", 0, false},
		{"1:75", 0, false},
		{"PT99999999999999999999H", 0, false},
	}
	for _, tt := range tests {
		got, ok := ParseISODuration(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseISODuration(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCanonicalURL(t *testing.T) {
	final, _ := url.Parse("https://www.recipes.example.com/r/scones?utm_source=x&id=1#frag")
	tests := []struct {
		name, page, want string
	}{
		{"no link: final URL cleaned", `<p>hi</p>`, "https://www.recipes.example.com/r/scones?id=1"},
		{"absolute canonical", `<link rel="canonical" href="https://recipes.example.com/scones">`,
			"https://recipes.example.com/scones"},
		{"relative canonical", `<link rel="canonical" href="/scones/?ref=home">`,
			"https://www.recipes.example.com/scones/"},
		{"rel with other tokens and case", `<link rel="Alternate CANONICAL" href="/a">`,
			"https://www.recipes.example.com/a"},
		{"non-http canonical ignored", `<link rel="canonical" href="javascript:alert(1)">`,
			"https://www.recipes.example.com/r/scones?id=1"},
		{"hostless canonical ignored", `<link rel="canonical" href="http:">`,
			"https://www.recipes.example.com/r/scones?id=1"},
		{"first canonical wins", `<link rel="canonical" href="/one"><link rel="canonical" href="/two">`,
			"https://www.recipes.example.com/one"},
		{"tracking params stripped, others kept in order",
			`<link rel="canonical" href="https://Recipes.Example.com:443/s?b=2&utm_medium=email&fbclid=x&gclid=y&a=1&mc_cid=1&mc_eid=2&UTM_Campaign=z&ref=r#c">`,
			"https://recipes.example.com/s?b=2&a=1"},
		{"all params tracking", `<link rel="canonical" href="https://recipes.example.com/s?utm_source=a&utm_medium=b">`,
			"https://recipes.example.com/s"},
		{"empty path", `<link rel="canonical" href="http://recipes.example.com">`, "http://recipes.example.com/"},
	}
	for _, tt := range tests {
		if got := CanonicalURL([]byte(tt.page), final); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
	if got := CanonicalURL([]byte(`<p></p>`), nil); got != "" {
		t.Errorf("nil final URL: got %q", got)
	}
}

func TestParseBlockRepairs(t *testing.T) {
	for _, in := range []string{
		`{"a": 1}`,
		`  {"a": 1};  `,
		"{\"a\": \"line\nbreak\"}",
		`{"a": [1, 2,], }`,
		`<!-- {"a": 1} -->`,
		`<![CDATA[{"a": 1}]]>`,
		`{&quot;a&quot;: 1}`,
		`{"a": 1}{"b": 2}`,
	} {
		if _, ok := parseBlock(in); !ok {
			t.Errorf("parseBlock(%q) failed", in)
		}
	}
	for _, in := range []string{``, `{`, `{"a": }`, `<p>hello</p>`} {
		if _, ok := parseBlock(in); ok {
			t.Errorf("parseBlock(%q) succeeded", in)
		}
	}
}

func TestPlainText(t *testing.T) {
	tests := []struct{ in, line, block string }{
		{"<p>Mix &amp; stir</p>", "Mix & stir", "Mix & stir"},
		{"a<br>b", "a b", "a\nb"},
		{"<p>one</p><p>two</p>", "one two", "one\n\ntwo"},
		{"&lt;b&gt;bold&lt;/b&gt; text", "bold text", "bold text"},
		{"it&amp;#8217;s", "it’s", "it’s"},
		{"5 < 10 &amp; 3 > 2", "5 < 10 & 3 > 2", "5 < 10 & 3 > 2"},
		{"x<script>evil()</script>y", "xy", "xy"},
		{"  lots   of\t space here ", "lots of space here", "lots of space here"},
		{"AT&T", "AT&T", "AT&T"},
	}
	for _, tt := range tests {
		if got := plainLine(tt.in); got != tt.line {
			t.Errorf("plainLine(%q) = %q, want %q", tt.in, got, tt.line)
		}
		if got := plainBlock(tt.in); got != tt.block {
			t.Errorf("plainBlock(%q) = %q, want %q", tt.in, got, tt.block)
		}
	}
}
