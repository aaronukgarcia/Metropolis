package data

import "testing"

// FEAT-199 tests: the zoning.json catalogue schema (zoning.go) follows
// this package's per-file loader precedent (pacing.go's split), so these
// tests mirror pacing/load test conventions: load the REAL shipped file
// for the happy path, hand-built invalid documents for each rejection.

func loadZoningForTest(t *testing.T, dir string) ZoningCatalogue {
	t.Helper()
	c, err := LoadZoningFile(dir, "corr-zoning-test")
	if err != nil {
		t.Fatalf("LoadZoningFile(%s): %v", dir, err)
	}
	return c
}

// TestLoadZoningRealFile loads the real data/zoning.json and asserts the
// FEAT-199 contract on it: six zone families, every one spanning the full
// density range 1-5 with five semantic palette keys that all resolve in
// the colours map.
func TestLoadZoningRealFile(t *testing.T) {
	dir, err := ResolveDataDir("corr-zoning-test")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	c := loadZoningForTest(t, dir)

	const wantZones = 6
	if len(c.Zones) != wantZones {
		t.Fatalf("zones = %d entries, want %d (FEAT-199: residential/commercial/office/industry/farming/mining)", len(c.Zones), wantZones)
	}

	wantIDs := []string{"residential", "commercial", "office", "industry", "farming", "mining"}
	for _, id := range wantIDs {
		z, ok := c.ZoneByID(id)
		if !ok {
			t.Fatalf("ZoneByID(%q): not found in real data/zoning.json", id)
		}
		if z.DensityMin != 1 || z.DensityMax != 5 {
			t.Errorf("zone %q density range [%d,%d], want [1,5]", id, z.DensityMin, z.DensityMax)
		}
		if len(z.PaletteKeys) != 5 {
			t.Errorf("zone %q carries %d palette keys, want 5 (res1..res5 ladder shape)", id, len(z.PaletteKeys))
		}
		for d := z.DensityMin; d <= z.DensityMax; d++ {
			key, ok := c.ColourKeyFor(z, d)
			if !ok {
				t.Errorf("zone %q density %d: no palette key resolved", id, d)
				continue
			}
			if _, ok := c.Colours[key]; !ok {
				t.Errorf("zone %q density %d: palette key %q missing from colours map", id, d, key)
			}
		}
	}
}

// TestZoneByAlias covers the SS34 build-catalogue slug -> FEAT-199 family
// resolution compose's KindZone write-through uses. Every eight-way slug
// must resolve to exactly one family, and an unknown slug must not.
func TestZoneByAlias(t *testing.T) {
	dir, err := ResolveDataDir("corr-zoning-test")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	c := loadZoningForTest(t, dir)

	cases := []struct{ slug, wantFamily string }{
		{"dwelling", "residential"},
		{"shop", "commercial"},
		{"entertainment", "commercial"},
		{"office", "office"},
		{"manufacturing", "industry"},
		{"heavy_industry", "industry"},
		{"farming", "farming"},
		{"mining", "mining"},
	}
	for _, tc := range cases {
		z, ok := c.ZoneByAlias(tc.slug)
		if !ok {
			t.Errorf("ZoneByAlias(%q): not found — the eight-way SS34 slugs must all resolve (compose write-through would strand them)", tc.slug)
			continue
		}
		if z.ID != tc.wantFamily {
			t.Errorf("ZoneByAlias(%q).ID = %q, want %q", tc.slug, z.ID, tc.wantFamily)
		}
	}
	if _, ok := c.ZoneByAlias("not-a-zone"); ok {
		t.Error("ZoneByAlias(\"not-a-zone\") resolved, want miss")
	}
}

// TestColourKeyForBounds checks ColourKeyFor clamps-free behaviour: an
// out-of-range density reports miss rather than indexing out of bounds.
func TestColourKeyForBounds(t *testing.T) {
	dir, err := ResolveDataDir("corr-zoning-test")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	c := loadZoningForTest(t, dir)
	z, _ := c.ZoneByID("residential")

	for _, d := range []int{-1, 0, 6, 100} {
		if key, ok := c.ColourKeyFor(z, d); ok {
			t.Errorf("ColourKeyFor(residential, %d) = (%q, true), want miss", d, key)
		}
	}
}

func TestZoningValidationRejections(t *testing.T) {
	valid := func() ZoningCatalogue {
		return ZoningCatalogue{
			Version: 1,
			Zones: []ZoneDensity{{
				ID:          "residential",
				DensityMin:  1,
				DensityMax:  5,
				PaletteKeys: []string{"res1", "res2", "res3", "res4", "res5"},
				Aliases:     []string{"dwelling"},
			}},
			Colours: map[string]int{
				"res1": 194, "res2": 157, "res3": 120, "res4": 71, "res5": 22,
			},
		}
	}

	cases := []struct {
		name   string
		mutate func(*ZoningCatalogue)
		field  string
	}{
		{
			name:   "missing version",
			mutate: func(c *ZoningCatalogue) { c.Version = 0 },
			field:  "version",
		},
		{
			name:   "no zones",
			mutate: func(c *ZoningCatalogue) { c.Zones = nil },
			field:  "zones",
		},
		{
			name:   "empty zone id",
			mutate: func(c *ZoningCatalogue) { c.Zones[0].ID = "" },
			field:  "zones[0].id",
		},
		{
			name: "duplicate zone id",
			mutate: func(c *ZoningCatalogue) {
				c.Zones = append(c.Zones, c.Zones[0])
			},
			field: "zones[1].id",
		},
		{
			name:   "density min below 1",
			mutate: func(c *ZoningCatalogue) { c.Zones[0].DensityMin = 0 },
			field:  "zones[0].densityMin",
		},
		{
			name:   "density max above 5",
			mutate: func(c *ZoningCatalogue) { c.Zones[0].DensityMax = 6 },
			field:  "zones[0].densityMax",
		},
		{
			name:   "density min above max",
			mutate: func(c *ZoningCatalogue) { c.Zones[0].DensityMin = 3; c.Zones[0].DensityMax = 2 },
			field:  "zones[0].densityMin",
		},
		{
			name:   "palette keys wrong count",
			mutate: func(c *ZoningCatalogue) { c.Zones[0].PaletteKeys = c.Zones[0].PaletteKeys[:4] },
			field:  "zones[0].paletteKeys",
		},
		{
			name:   "palette key missing from colours",
			mutate: func(c *ZoningCatalogue) { delete(c.Colours, "res3") },
			field:  "colours",
		},
		{
			name: "duplicate alias across zones",
			mutate: func(c *ZoningCatalogue) {
				c.Zones = append(c.Zones, ZoneDensity{
					ID: "commercial", DensityMin: 1, DensityMax: 5,
					PaletteKeys: []string{"com1", "com2", "com3", "com4", "com5"},
					Aliases:     []string{"dwelling"},
				})
				c.Colours["com1"], c.Colours["com2"], c.Colours["com3"], c.Colours["com4"], c.Colours["com5"] = 195, 116, 80, 44, 30
			},
			field: "zones[1].aliases",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want a schema error naming %s", tc.field)
			}
			fe, ok := err.(*FieldError)
			if !ok {
				t.Fatalf("Validate() error is %T, want *FieldError", err)
			}
			if fe.Field != tc.field {
				t.Errorf("Validate().field = %q, want %q (err: %v)", fe.Field, tc.field, err)
			}
		})
	}
}
