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

var countLimit = rapid.SampledFrom([]struct{ rule, key string }{
	{"SIZE-LIMIT", "max_files"}, {"SIZE-LARGE", "max_commits"}, {"REF-COUNT", "max_refs"}, {"RATE-LIMIT", "max_pushes"},
})

var durationKey = rapid.SampledFrom([]struct{ rule, key string }{
	{"META-FUTURE", "max_skew"}, {"META-BACKDATED", "max_age"}, {"RATE-LIMIT", "window"},
})

func loadLimit(t *testing.T, rule, key, value string) (*Config, error) {
	return loadRepo(t, map[string]string{"defaults.yaml": wall,
		"repos/demo/warden.yaml": repoOK + "rules:\n  " + rule + ": {" + key + ": " + value + "}\n"})
}

// Any int64 written as a YAML integer loads and reads back unchanged.
func TestPropCountLimitRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		l := countLimit.Draw(rt, "limit")
		n := rapid.Int64Min(0).Draw(rt, "n")
		c, err := loadLimit(t, l.rule, l.key, fmt.Sprint(n))
		if err != nil {
			rt.Fatalf("%s: %d doesn't load: %v", l.key, n, err)
		}
		if got, err := c.Rule(l.rule).Int(l.key); err != nil || got != n {
			rt.Fatalf("%s: %d reads back as %d, %v", l.key, n, got, err)
		}
	})
}

// A whole number in float form (2e3, 100.0) is accepted; a fraction is not.
func TestPropCountLimitFloatForms(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		l := countLimit.Draw(rt, "limit")
		n := rapid.Int64Range(0, 1<<53).Draw(rt, "n")
		c, err := loadLimit(t, l.rule, l.key, fmt.Sprintf("%d.0", n))
		if err != nil {
			rt.Fatalf("%s: %d.0 doesn't load: %v", l.key, n, err)
		}
		if got, err := c.Rule(l.rule).Int(l.key); err != nil || got != n {
			rt.Fatalf("%s: %d.0 reads back as %d, %v", l.key, n, got, err)
		}
		if n < 1<<51 { // above that, x.5 isn't a float64 and rounds to a whole number
			if _, err := loadLimit(t, l.rule, l.key, fmt.Sprintf("%d.5", n)); err == nil {
				rt.Fatalf("%s: fraction %d.5 loads", l.key, n)
			}
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

// Any non-negative duration written the way Go prints it loads and reads back
// unchanged; negatives, a number for a duration limit, and a duration for a
// count limit are refused.
func TestPropDurationLimitKinds(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		d := durationKey.Draw(rt, "duration limit")
		v := time.Duration(rapid.Int64Min(0).Draw(rt, "ns"))
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
// them. Zero stays allowed.
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
			if err != nil {
				rt.Fatalf("%s: zero duration %s refused: %v", d.key, z, err)
			}
			if got, _ := c.Rule(d.rule).Duration(d.key); got != 0 {
				rt.Fatalf("%s: %s reads as %s", d.key, z, got)
			}
		}
		if _, err := loadLimit(t, l.rule, l.key, "0"); err != nil {
			rt.Fatalf("%s: zero count refused: %v", l.key, err)
		}
	})
}
