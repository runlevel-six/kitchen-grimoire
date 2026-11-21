// SPDX-License-Identifier: Apache-2.0

package recipe

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := map[string]string{
		"Example Lemon Bars":                       "example-lemon-bars",
		"  Crème Brûlée  ":                         "creme-brulee",
		"Mom's Christmas Cookies":                  "moms-christmas-cookies",
		"Aunt X’s Cheese Ball":                     "aunt-xs-cheese-ball",
		"Beef & Mushroom Turnovers":                "beef-and-mushroom-turnovers",
		"Chili -- Quick (30 min!)":                 "chili-quick-30-min",
		"!!!":                                      "",
		"日本の料理":                                    "",
		"Jalapeño Poppers—Baked":                   "jalapeno-poppers-baked",
		"WASSAIL—EXAMPLE":                          "wassail-example",
		"1-2-3 Fudge":                              "1-2-3-fudge",
		"Über Long " + strings.Repeat("Word ", 40): "",
	}
	for in, want := range tests {
		got := Slugify(in)
		if strings.HasPrefix(in, "Über Long") {
			if len(got) > MaxSlug || strings.HasSuffix(got, "-") || !strings.HasPrefix(got, "uber-long-word") {
				t.Errorf("long title slug = %q", got)
			}
			continue
		}
		if got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if got := CategoryPathSlug([]string{"Desserts", "Cookies"}); got != "desserts/cookies" {
		t.Errorf("CategoryPathSlug = %q", got)
	}
}

func TestNormalizeKeepsWording(t *testing.T) {
	r := &Recipe{
		Title: "Test",
		StepGroups: []StepGroup{{Steps: []string{
			"Line one\n\nSecond paragraph   ",
		}}},
		ReviewReasons: []string{"x", "x", " "},
	}
	Normalize(r)
	if r.StepGroups[0].Steps[0] != "Line one\n\nSecond paragraph" {
		t.Errorf("step = %q", r.StepGroups[0].Steps[0])
	}
	if len(r.ReviewReasons) != 1 || !r.NeedsReview {
		t.Errorf("reasons = %v needsReview = %v", r.ReviewReasons, r.NeedsReview)
	}
}
