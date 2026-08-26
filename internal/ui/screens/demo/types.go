package demo

// AgeBucket is one month-age row of the population pyramid (DEMO-1):
// MonthAge is age in whole months (0 = newborn), Male/Female are citizen
// counts at that exact month-age. Sourced from "f6.population"'s
// ageMonths field — see doc.go's SF-2 table.
type AgeBucket struct {
	MonthAge int
	Male     int
	Female   int
}

// TraitBucket is one bucket of the personality-trait distribution
// (DEMO-7): Trait names the personality trait/category, Count is how
// many citizens fall in it. Sourced from "f6.population"'s personality
// field.
type TraitBucket struct {
	Trait string
	Count int
}

// ActivityHours is one row of the §42 "how your city spends Saturday"
// view (DEMO-4): Activity names the leisure activity, Hours is the
// city-wide total hours spent on it. Sourced from "f6.leisure"'s
// hoursByActivity field.
type ActivityHours struct {
	Activity string
	Hours    float64
}

// TasteBucket is one bucket of the leisure-taste weighting distribution
// (DEMO-7): Taste names the leisure-taste category, Weight is its
// relative weighting across the population. Sourced from
// "f6.leisure"'s leisureTaste field.
type TasteBucket struct {
	Taste  string
	Weight float64
}

// TypologyRow is one housing typology's demand-vs-stock figure (DEMO-5):
// Typology names the housing typology (§HS), Demand/Stock are the
// current demand and stock counts. Retired is true when a typology that
// was previously known has been absent from the most recent full
// "f6.housing" snapshot — SF-7/DEMO-9's "no longer available" state,
// rather than presenting stale Demand/Stock numbers for something the
// engine no longer models. Sourced from "f6.housing"'s typologies field.
type TypologyRow struct {
	Typology string
	Demand   int
	Stock    int
	Retired  bool
}

// CommuteFigures is the §21 in/out-commuting leak view (DEMO-6):
// OutCommuters is residents working off-map, InCommuters is off-map
// workers filling local vacancies — kept as two distinct figures, never
// merged into one undifferentiated "commuting" number. Sourced from
// "f6.commute"'s outCommuters/inCommuters fields.
type CommuteFigures struct {
	OutCommuters int
	InCommuters  int
}
