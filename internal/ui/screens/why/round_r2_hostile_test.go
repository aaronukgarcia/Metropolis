package why

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// round r2 (sonnet-round-r2-f5why): every numeric field of the patch, set to
// each hostile literal. Accepted patches must be in range and render sanely;
// refused patches must keep the last good frame.

func r2set(m map[string]any, path []any, val json.Number) {
	var cur any = m
	for i, k := range path {
		last := i == len(path)-1
		switch c := cur.(type) {
		case map[string]any:
			key := k.(string)
			if last {
				c[key] = val
				return
			}
			if _, ok := c[key]; !ok {
				c[key] = map[string]any{}
			}
			cur = c[key]
		case []any:
			idx := k.(int)
			if last {
				c[idx] = val
				return
			}
			cur = c[idx]
		}
	}
}

func r2paths() [][]any {
	var ps [][]any
	for _, f := range []string{"month", "aWorld", "neutral", "score", "net", "reputation", "reputationPoints", "wellbeingFactor", "wellbeingPoints", "wanted", "room", "movedIn", "left", "inflow", "outflow", "schemaVersion"} {
		ps = append(ps, []any{f})
	}
	for _, h := range []string{"places", "lived", "free", "movedIn", "left"} {
		ps = append(ps, []any{"homes", h})
	}
	ps = append(ps, []any{"homeless", "count"})
	for i := 0; i < 10; i++ {
		for _, f := range []string{"value", "points"} {
			ps = append(ps, []any{"levers", i, f})
		}
	}
	for i := 0; i < 2; i++ {
		for _, f := range []string{"value", "points"} {
			ps = append(ps, []any{"modifiers", i, f})
		}
	}
	ps = append(ps, []any{"levers", 0, "parts", 0, "v"}, []any{"modifiers", 0, "parts", 0, "v"})
	return ps
}

func TestRoundR2_Hostile(t *testing.T) {
	lits := []string{"1e300", "-1e300", "-0", "0", "9223372036854775807", "-9223372036854775808", "1e999", "-1e999", "1e9", "1000000001", "-1000000001", "1e4", "10001", "-10001", "100", "101", "-1", "0.04", "-0.04", "1e19"}
	good := fixture(t, "sample.json")
	var accepted []string
	for _, p := range r2paths() {
		for _, l := range lits {
			var m map[string]any
			dec := json.NewDecoder(strings.NewReader(string(good)))
			dec.UseNumber()
			if err := dec.Decode(&m); err != nil {
				t.Fatal(err)
			}
			// make sure the optional parents exist
			r2set(m, p, json.Number(l))
			raw, err := json.Marshal(m)
			if err != nil {
				continue
			}
			s := boundScreen()
			apply(s, good)
			before := joined(frame(s, 80, 24))
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("PANIC %v=%s: %v", p, l, r)
					}
				}()
				apply(s, raw)
				for _, wh := range [][2]int{{80, 24}, {120, 28}, {40, 10}} {
					rows := frame(s, wh[0], wh[1])
					for _, r := range rows {
						if len([]rune(r)) > wh[0] {
							t.Errorf("WIDE %v=%s %v", p, l, wh)
						}
						if r2sign.MatchString(r) {
							t.Errorf("DOUBLE MINUS %v=%s: %q", p, l, r)
						}
					}
				}
			}()
			after := joined(frame(s, 80, 24))
			if after != before {
				accepted = append(accepted, fmt.Sprintf("%v=%s", p, l))
			}
		}
	}
	t.Logf("ACCEPTED (frame changed): %d\n%s", len(accepted), strings.Join(accepted, "\n"))
}

func TestRoundR2_AtBounds(t *testing.T) {
	p := samplePatch(t)
	aw := float64(maxScalar)
	p.AWorld = &aw
	nb := -float64(maxScalar)
	p.Neutral = &nb
	p.Score = -maxScalar
	p.Net = maxCount
	p.Reputation = -100
	p.ReputationPoints = -maxScalar
	p.WellbeingPoints = -maxScalar
	p.WellbeingFactor = maxFactor
	p.Wanted = -maxCount
	p.Room = maxCount
	v := int64(maxCount)
	p.MovedIn, p.Left = &v, &v
	for i := range p.Levers {
		p.Levers[i].Built = true
		p.Levers[i].Value = 100
		p.Levers[i].Points = -100
		p.Levers[i].Parts = []Part{{"a-very-long-label-indeed", maxCount}, {"b", -maxCount}, {"c", 1e9}}
	}
	for i := range p.Modifiers {
		p.Modifiers[i].Built = true
		p.Modifiers[i].Points = 100
		p.Modifiers[i].Parts = []Part{{"x", -maxCount}}
	}
	for _, sign := range []float64{1, -1} {
		q := p
		q.Net = sign * maxCount
		q.Score = sign * maxScalar
		s := boundScreen()
		apply(s, mustMarshal(t, q))
		if !s.HaveData() {
			t.Fatal("bounds patch refused")
		}
		for _, wh := range [][2]int{{80, 24}, {120, 28}} {
			rows := frame(s, wh[0], wh[1])
			t.Logf("== sign %v %v\n%s", sign, wh, joined(nonEmpty(rows)))
			for _, r := range rows {
				if len([]rune(r)) > wh[0] {
					t.Errorf("wide row %q", r)
				}
			}
		}
	}
}

var r2sign = regexp.MustCompile(`--[0-9]|\+-[0-9]|-\+[0-9]|\+\+`)
