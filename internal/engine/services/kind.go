package services

// ServiceKind is the extensible identity of one service category. The
// underlying string is the stable lookup key both the kind registry and
// data/services.json's staffingPools[].members reference, so a kind's Go
// identity and its data identity are the same value — there is no
// separate name-mapping table to drift out of sync (GR#3).
//
// A kind is NOT a hardcoded enum: the built-in §10 kinds below are
// registered into the same runtime registry a caller uses for a synthetic
// kind (see [ServicesAPI.RegisterKind]), so adding a new service category
// is a registration call, never a code change to this package (AC-2).
type ServiceKind string

// The §10 Service & Feature Inventory surface, as the sixteen categories
// engine.services.md AC-2 names (plus home-care, which §26's shared-pool
// example names alongside elder care). These are the built-in defaults
// [ServicesAPI.New]/[ServicesAPI.Load] register; a caller may register any
// further kind through [ServicesAPI.RegisterKind].
const (
	ServiceRoadsMaintenance ServiceKind = "roads-maintenance"
	ServiceElectricity      ServiceKind = "electricity"
	ServiceWaterSewage      ServiceKind = "water-sewage"
	ServiceHealthcare       ServiceKind = "healthcare"
	ServiceDeathcare        ServiceKind = "deathcare"
	ServiceGarbage          ServiceKind = "garbage"
	ServiceEducation        ServiceKind = "education"
	ServiceFire             ServiceKind = "fire"
	ServicePoliceJail       ServiceKind = "police-jail"
	ServiceElderCare        ServiceKind = "elder-care"
	ServiceHomeCare         ServiceKind = "home-care"
	ServiceChildBenefit     ServiceKind = "child-benefit"
	ServicePublicTransport  ServiceKind = "public-transport"
	ServiceParksLeisure     ServiceKind = "parks-leisure"
	ServiceCommunications   ServiceKind = "communications"
	ServiceDistrictsPolicy  ServiceKind = "districts-policies"
	ServiceDisastersLite    ServiceKind = "disasters-lite"
)

// builtinKinds is the ordered set of §10 service kinds every freshly
// constructed ServicesAPI registers by default. Ordered (a slice, not a
// map) so registration runs in a deterministic order (GR#21) and any
// caller ranging over it gets that same order.
var builtinKinds = []ServiceKind{
	ServiceRoadsMaintenance,
	ServiceElectricity,
	ServiceWaterSewage,
	ServiceHealthcare,
	ServiceDeathcare,
	ServiceGarbage,
	ServiceEducation,
	ServiceFire,
	ServicePoliceJail,
	ServiceElderCare,
	ServiceHomeCare,
	ServiceChildBenefit,
	ServicePublicTransport,
	ServiceParksLeisure,
	ServiceCommunications,
	ServiceDistrictsPolicy,
	ServiceDisastersLite,
}

// KindDef is the registered definition of one service kind. It carries the
// human-facing name and the benchmark staffing category this kind's
// staffing need derives from (a data/services.json pie.benchmarks id,
// AC-5). The shared-staffing-pool membership is NOT here — that lives in
// data/services.json's staffingPools[].members, so "which services share a
// pool" is a data edit, not a code change (AC-4).
type KindDef struct {
	// Name is the display name (e.g. "Healthcare").
	Name string
	// Benchmark is the pie.benchmarks id this kind's staffing need draws
	// from, or empty when the kind has no Pie benchmark category.
	Benchmark string
}

// defaultKindDefs maps each built-in §10 kind to its registered
// definition. The Benchmark column wires each service to its §54 Pie
// category: healthcare/elder-care/home-care draw their staffing from the
// nurses & GPs benchmark (and from the shared "nursing" pool via
// data/services.json), fire from firefighters, police-jail from police,
// education from teachers, garbage from refuse crews, districts-policies
// from council officers, and the remaining kinds carry no Pie category at
// this generic-model level (they are registrable categories, not
// per-1k-staffed services).
var defaultKindDefs = map[ServiceKind]KindDef{
	ServiceRoadsMaintenance: {Name: "Roads & road maintenance"},
	ServiceElectricity:      {Name: "Electricity"},
	ServiceWaterSewage:      {Name: "Water & sewage"},
	ServiceHealthcare:       {Name: "Healthcare", Benchmark: "nursesGps"},
	ServiceDeathcare:        {Name: "Deathcare"},
	ServiceGarbage:          {Name: "Garbage", Benchmark: "refuseCrews"},
	ServiceEducation:        {Name: "Education", Benchmark: "teachers"},
	ServiceFire:             {Name: "Fire", Benchmark: "firefighters"},
	ServicePoliceJail:       {Name: "Police & jail", Benchmark: "police"},
	ServiceElderCare:        {Name: "Elder care", Benchmark: "nursesGps"},
	ServiceHomeCare:         {Name: "Home care", Benchmark: "nursesGps"},
	ServiceChildBenefit:     {Name: "Child benefit"},
	ServicePublicTransport:  {Name: "Public transport"},
	ServiceParksLeisure:     {Name: "Parks & leisure"},
	ServiceCommunications:   {Name: "Communications"},
	ServiceDistrictsPolicy:  {Name: "Districts & policies", Benchmark: "councilOfficers"},
	ServiceDisastersLite:    {Name: "Disasters-lite"},
}
