//go:build fuzz

package config

// Property tests for limit kinds (REQ-PG-032: limits are checked for the kind
// the rule reads when the configuration loads). Every value goes through the
// real loader. Run with `go test -tags=fuzz ./internal/config`.

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Ranges from "Limit bounds" in docs/push-guard-rules.md (REQ-PG-046).
var countLimit = rapid.SampledFrom([]struct {
	rule, key string
	lo, hi    int64
}{
	{"SIZE-LIMIT", "max_files", 1, 10_000_000}, {"SIZE-LARGE", "max_commits", 1, 10_000_000},
	{"REF-COUNT", "max_refs", 1, 1000}, {"RATE-LIMIT", "max_pushes", 1, 10_000},
	{"META-UNSIGNED", "lookback", 0, 1000}, {"SIZE-MASS-DELETE", "min_lines", 0, 1_000_000},
})

var durationKey = rapid.SampledFrom([]struct {
	rule, key string
	lo, hi    time.Duration
}{
	{"META-FUTURE", "max_skew", 0, 24 * time.Hour}, {"META-BACKDATED", "max_age", time.Minute, 8760 * time.Hour},
	{"RATE-LIMIT", "window", time.Minute, 168 * time.Hour},
})

func loadLimit(t *testing.T, rule, key, value string) (*Config, error) {
	return loadRepo(t, map[string]string{"defaults.yaml": wall,
		"repos/demo/warden.yaml": repoOK + "rules:\n  " + rule + ": {" + key + ": " + value + "}\n"})
}

// Any count inside its range written as a YAML integer loads and reads back
// unchanged; any int64 outside it is refused (REQ-PG-046).
func TestPropCountLimitRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		l := countLimit.Draw(rt, "limit")
		n := rapid.Int64Range(l.lo, l.hi).Draw(rt, "n")
		c, err := loadLimit(t, l.rule, l.key, fmt.Sprint(n))
		if err != nil {
			rt.Fatalf("%s: %d doesn't load: %v", l.key, n, err)
		}
		if got, err := c.Rule(l.rule).Int(l.key); err != nil || got != n {
			rt.Fatalf("%s: %d reads back as %d, %v", l.key, n, got, err)
		}
		out := rapid.OneOf(rapid.Int64Max(l.lo-1), rapid.Int64Min(l.hi+1)).Draw(rt, "out")
		if _, err := loadLimit(t, l.rule, l.key, fmt.Sprint(out)); err == nil {
			rt.Fatalf("%s: %d is outside %d to %d but loads", l.key, out, l.lo, l.hi)
		}
	})
}

// A whole number in float form (2e3, 100.0) is accepted; a fraction is not.
func TestPropCountLimitFloatForms(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		l := countLimit.Draw(rt, "limit")
		n := rapid.Int64Range(l.lo, l.hi).Draw(rt, "n")
		c, err := loadLimit(t, l.rule, l.key, fmt.Sprintf("%d.0", n))
		if err != nil {
			rt.Fatalf("%s: %d.0 doesn't load: %v", l.key, n, err)
		}
		if got, err := c.Rule(l.rule).Int(l.key); err != nil || got != n {
			rt.Fatalf("%s: %d.0 reads back as %d, %v", l.key, n, got, err)
		}
		if _, err := loadLimit(t, l.rule, l.key, fmt.Sprintf("%d.5", n)); err == nil {
			rt.Fatalf("%s: fraction %d.5 loads", l.key, n)
		}
	})
}

// A count that doesn't fit an int64 must be refused when the configuration
// loads, not wrap around to a negative limit.
func TestPropCountLimitOutOfRange(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		l := countLimit.Draw(rt, "limit")
		u := rapid.Uint64Range(math.MaxInt64+1, math.MaxUint64).Draw(rt, "u")
		for _, v := range []string{fmt.Sprint(u), fmt.Sprintf("%g", float64(u)), "1e19", "-1e19"} {
			c, err := loadLimit(t, l.rule, l.key, v)
			if err == nil {
				got, _ := c.Rule(l.rule).Int(l.key)
				rt.Errorf("%s: %s is out of int64 range but loads and reads as %d", l.key, v, got)
			}
		}
	})
}

// Any duration inside its range written the way Go prints it loads and reads back
// unchanged; negatives, a number for a duration limit, and a duration for a
// count limit are refused.
func TestPropDurationLimitKinds(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		d := durationKey.Draw(rt, "duration limit")
		v := time.Duration(rapid.Int64Range(int64(d.lo), int64(d.hi)).Draw(rt, "ns"))
		c, err := loadLimit(t, d.rule, d.key, "'"+v.String()+"'")
		if err != nil {
			rt.Fatalf("%s: %s doesn't load: %v", d.key, v, err)
		}
		if got, err := c.Rule(d.rule).Duration(d.key); err != nil || got != v {
			rt.Fatalf("%s: %s reads back as %s, %v", d.key, v, got, err)
		}
		n := rapid.Int64().Draw(rt, "number")
		if _, err := loadLimit(t, d.rule, d.key, fmt.Sprint(n)); err == nil {
			rt.Fatalf("%s: plain number %d loads as a duration", d.key, n)
		}
		l := countLimit.Draw(rt, "count limit")
		if _, err := loadLimit(t, l.rule, l.key, "'"+v.String()+"'"); err == nil && strings.ContainsAny(v.String(), "hms") {
			rt.Fatalf("%s: duration %s loads as a count", l.key, v)
		}
	})
}

// Negative limits are refused when the configuration loads, in every spelling:
// counts as integers or whole floats, durations however Go or a human writes
// them. Zero loads only where the range starts at zero (REQ-PG-046).
func TestPropNegativeLimitsRefused(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		l := countLimit.Draw(rt, "count limit")
		n := rapid.Int64Max(-1).Draw(rt, "n")
		for _, v := range []string{fmt.Sprint(n), fmt.Sprintf("%d.0", n), fmt.Sprintf("%g", float64(n))} {
			if _, err := loadLimit(t, l.rule, l.key, v); err == nil {
				rt.Errorf("%s: negative count %s loads", l.key, v)
			}
		}

		d := durationKey.Draw(rt, "duration limit")
		neg := time.Duration(rapid.Int64Max(-1).Draw(rt, "ns"))
		unit := rapid.SampledFrom([]string{"ns", "us", "µs", "ms", "s", "m", "h"}).Draw(rt, "unit")
		k := rapid.IntRange(1, 1000).Draw(rt, "k")
		for _, v := range []string{neg.String(), fmt.Sprintf("-%d%s", k, unit), fmt.Sprintf("-%d.5%s", k, unit), fmt.Sprintf("-1h%dm", k)} {
			if _, err := loadLimit(t, d.rule, d.key, "'"+v+"'"); err == nil {
				rt.Errorf("%s: negative duration %s loads", d.key, v)
			}
		}

		for _, z := range []string{"'0s'", "'0'", "'-0s'"} {
			c, err := loadLimit(t, d.rule, d.key, z)
			if (err == nil) != (d.lo == 0) {
				rt.Fatalf("%s: zero duration %s: err %v, range starts at %s", d.key, z, err, d.lo)
			}
			if err == nil {
				if got, _ := c.Rule(d.rule).Duration(d.key); got != 0 {
					rt.Fatalf("%s: %s reads as %s", d.key, z, got)
				}
			}
		}
		if _, err := loadLimit(t, l.rule, l.key, "0"); (err == nil) != (l.lo == 0) {
			rt.Fatalf("%s: zero count: err %v, range starts at %d", l.key, err, l.lo)
		}
	})
}
