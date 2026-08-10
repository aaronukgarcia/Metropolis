package invariant

// VehicleInvariant checks the vehicle-conservation balance (§19.3, US-3):
// every vehicle instance must be traceable to a spawn (trip origin) and
// despawn (trip completion/parking) event — the invariant that makes
// despawn-masking gridlock (a known failure mode of 'Blue', §19.3)
// structurally impossible, even before engine.traffic (Sprint 5) exists
// to generate real vehicles.
//
// Balance identity: Closing - Opening == TrackedDelta, where
// TrackedDelta = spawns - despawns for the tick. If the vehicle count
// changes by any amount not matched by a tracked spawn/despawn event,
// this invariant reports a Violation naming the imbalance — a vehicle
// that silently stops being counted (the despawn-masking failure mode)
// is exactly what this check exists to catch, before real traffic ever
// lands.
type VehicleInvariant struct {
	stockCheck
}

// NewVehicleInvariant constructs the vehicle-conservation invariant.
func NewVehicleInvariant() VehicleInvariant {
	return VehicleInvariant{stockCheck{name: "vehicles", stock: StockVehicles}}
}
