// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// maxDuration bounds what ParseISODuration accepts. Anything longer is a
// publishing error, not a recipe time.
const maxDuration = 366 * 24 * time.Hour

// ParseISODuration parses a recipe time leniently (§8.2 step 4). It accepts
// ISO 8601 durations as publishers actually write them — "PT1H15M",
// "PT90M", "P0DT1H", "PT1H 15M", "pt45m", "PT1.5H", "P1H30M" (missing T) —
// and falls back to plain text like "45 minutes", "1 hr 15 mins", "1h15m",
// "1:15", or a bare number of minutes. It reports false for anything it
// cannot read, negative values, and values over a year.
func ParseISODuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 64 {
		return 0, false
	}
	var secs float64
	var ok bool
	if s[0] == 'P' || s[0] == 'p' {
		secs, ok = parseISO(strings.ToUpper(s[1:]))
	} else {
		secs, ok = parseLoose(strings.ToLower(s))
	}
	if !ok || secs < 0 || math.IsNaN(secs) || secs > maxDuration.Seconds() {
		return 0, false
	}
	return time.Duration(math.Round(secs)) * time.Second, true
}

// parseISO reads the part after "P". Spaces are ignored. "M" means months
// only in the date part after a year ("P1Y2M"); otherwise it is minutes,
// because "P30M" on a recipe means thirty minutes.
func parseISO(s string) (float64, bool) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	var total float64
	inTime, sawYear, found := false, false, false
	for len(s) > 0 {
		if s[0] == 'T' {
			inTime = true
			s = s[1:]
			continue
		}
		i := 0
		for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.' || s[i] == ',') {
			i++
		}
		if i == 0 {
			return 0, false
		}
		n, err := strconv.ParseFloat(strings.ReplaceAll(s[:i], ",", "."), 64)
		if err != nil {
			return 0, false
		}
		s = s[i:]
		if s == "" {
			// A trailing number without a unit ("PT1H15"): minutes.
			total += n * 60
			found = true
			break
		}
		unit := s[0]
		s = s[1:]
		switch {
		case unit == 'Y':
			total += n * 365 * 86400
			sawYear = true
		case unit == 'M' && !inTime && sawYear:
			total += n * 30 * 86400
		case unit == 'W':
			total += n * 7 * 86400
		case unit == 'D':
			total += n * 86400
		case unit == 'H':
			total += n * 3600
		case unit == 'M':
			total += n * 60
		case unit == 'S':
			total += n
		default:
			return 0, false
		}
		found = true
	}
	return total, found
}

// looseUnits maps plain-text unit words to seconds.
var looseUnits = map[string]float64{
	"d": 86400, "day": 86400, "days": 86400,
	"h": 3600, "hr": 3600, "hrs": 3600, "hour": 3600, "hours": 3600,
	"m": 60, "min": 60, "mins": 60, "minute": 60, "minutes": 60,
	"s": 1, "sec": 1, "secs": 1, "second": 1, "seconds": 1,
}

// looseFiller are words that may appear between amounts.
var looseFiller = map[string]bool{"and": true, "about": true, "approx": true, "approximately": true, "plus": true}

// parseLoose reads "1 hour 15 minutes", "1h15m", "1:15", or "45".
func parseLoose(s string) (float64, bool) {
	if h, m, found := strings.Cut(s, ":"); found {
		hn, err1 := strconv.Atoi(strings.TrimSpace(h))
		mn, err2 := strconv.Atoi(strings.TrimSpace(m))
		if err1 != nil || err2 != nil || hn < 0 || mn < 0 || mn >= 60 || hn > 10000 {
			return 0, false
		}
		return float64(hn*3600 + mn*60), true
	}
	var total float64
	amounts, units := 0, 0
	for len(s) > 0 {
		r := s[0]
		switch {
		case r == ' ' || r == ',' || r == '\t' || r == '.':
			s = s[1:]
		case r >= '0' && r <= '9':
			i := 0
			for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
				i++
			}
			n, err := strconv.ParseFloat(strings.TrimSuffix(s[:i], "."), 64)
			if err != nil {
				return 0, false
			}
			s = strings.TrimLeft(s[i:], " ")
			j := 0
			for j < len(s) && s[j] >= 'a' && s[j] <= 'z' {
				j++
			}
			word := s[:j]
			s = s[j:]
			amounts++
			if word == "" {
				// A bare number: minutes, but only if it is the whole input.
				total += n * 60
				continue
			}
			mult, ok := looseUnits[word]
			if !ok {
				return 0, false
			}
			total += n * mult
			units++
		case r >= 'a' && r <= 'z':
			j := 0
			for j < len(s) && s[j] >= 'a' && s[j] <= 'z' {
				j++
			}
			if !looseFiller[s[:j]] {
				return 0, false
			}
			s = s[j:]
		default:
			return 0, false
		}
	}
	if amounts == 0 || (units > 0 && units != amounts) || (units == 0 && amounts > 1) {
		return 0, false
	}
	return total, true
}

// minutes converts a parsed duration to whole minutes, rounding.
func minutes(d time.Duration) int {
	return int(math.Round(d.Minutes()))
}
