// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"html"
	"io"
	"strconv"

	"github.com/a-h/templ"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// stepText renders step text with each detected duration wrapped in
// <span class="dur" data-seconds="…">, which cook.js turns into a timer
// button. It is written in Go rather than templ because templ puts a space
// between elements on separate lines, which showed up as "Bake ␣20 minutes␣,".
// All text is HTML-escaped.
func stepText(text string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		for _, sp := range recipe.SplitDurations(text) {
			var err error
			if sp.Seconds > 0 {
				_, err = io.WriteString(w, `<span class="dur" data-seconds="`+strconv.Itoa(sp.Seconds)+`">`+
					html.EscapeString(sp.Text)+`</span>`)
			} else {
				_, err = io.WriteString(w, html.EscapeString(sp.Text))
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// firstStep is the overall number of the first step in group gi, so the
// recipe view numbers steps the same way cook mode does.
func firstStep(r *recipeT, gi int) int {
	n := 1
	for _, g := range r.StepGroups[:gi] {
		n += len(g.Steps)
	}
	return n
}
