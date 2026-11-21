// SPDX-License-Identifier: Apache-2.0

package recipe

import "testing"

func TestSplitDurations(t *testing.T) {
	tests := []struct {
		in    string
		timer string // the duration span's text, "" for none
		secs  int
	}{
		{"Bake 20 minutes.", "20 minutes", 1200},
		{"Bake for 15-20 min until golden.", "15-20 min", 1200},
		{"Simmer 1 to 2 hours.", "1 to 2 hours", 7200},
		{"Rest 1½ hours", "1½ hours", 5400},
		{"Whisk for 45 seconds", "45 seconds", 45},
		{"Chill overnight.", "", 0},
		{"Add 2 cups flour.", "", 0},
		{"Cook 3–4 MINUTES per side", "3–4 MINUTES", 240},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			spans := SplitDurations(tt.in)
			var got Span
			joined := ""
			for _, s := range spans {
				joined += s.Text
				if s.Seconds > 0 {
					got = s
				}
			}
			if joined != tt.in {
				t.Errorf("spans rejoin to %q, want %q", joined, tt.in)
			}
			if got.Text != tt.timer || got.Seconds != tt.secs {
				t.Errorf("timer = %q (%ds), want %q (%ds)", got.Text, got.Seconds, tt.timer, tt.secs)
			}
		})
	}
}

func TestSplitDurationsMultiple(t *testing.T) {
	spans := SplitDurations("Bake 10 minutes, turn, then bake 5 minutes more.")
	var n int
	for _, s := range spans {
		if s.Seconds > 0 {
			n++
		}
	}
	if n != 2 {
		t.Errorf("found %d timers, want 2: %+v", n, spans)
	}
}
