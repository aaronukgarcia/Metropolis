package capexport

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestSurplusBookSourcedFromServicesAPI (AC-1): the per-service surplus book is
// capacity − internal demand, sourced live from engine.services' ServicesAPI
// through the bound ServiceID — never re-derived locally. Changing the service's
// demand through ServicesAPI.UpdateDemand changes the reported surplus.
func TestSurplusBookSourcedFromServicesAPI(t *testing.T) {
	a, svc, _, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 100)
	setDemand(t, svc, id, 20)
	bindLine(t, a, ExportHospitalBeds, id)

	book, err := a.SurplusBook(ExportHospitalBeds)
	if err != nil {
		t.Fatalf("SurplusBook: %v", err)
	}
	if book.Capacity != 100 || book.Demand != 20 || book.Surplus != 80 {
		t.Fatalf("SurplusBook = %+v, want capacity 100 demand 20 surplus 80", book)
	}

	// Drive demand up through the ServicesAPI seam; the surplus must follow.
	setDemand(t, svc, id, 50)
	book, err = a.SurplusBook(ExportHospitalBeds)
	if err != nil {
		t.Fatalf("SurplusBook after demand growth: %v", err)
	}
	if book.Surplus != 50 {
		t.Fatalf("Surplus = %v after demand 50, want 50 (capacity 100 − demand 50)", book.Surplus)
	}
}

// TestCommittedCapacityAccessor (AC-6): the per-service committed-capacity
// accessor is callable for the prison-places line specifically, so §43's
// prison-overcrowding edge can read capexport's sold-places figure the moment
// it is registered — without this package reaching into engine.prison.
func TestCommittedCapacityAccessor(t *testing.T) {
	a, svc, _, _ := newTestAPI(t)
	id := registerService(t, svc, "prison", 200)
	bindLine(t, a, ExportPrisonPlaces, id)

	if c, err := a.Committed(ExportPrisonPlaces); err != nil || c != 0 {
		t.Fatalf("Committed before any contract = %v, %v; want 0, nil", c, err)
	}

	if _, err := a.IssueContract(IssueRequest{Line: ExportPrisonPlaces, Quantity: 40, TermMonths: 12, RateMicropounds: 1_000_000}); err != nil {
		t.Fatalf("IssueContract prison-places: %v", err)
	}
	if c, err := a.Committed(ExportPrisonPlaces); err != nil || c != 40 {
		t.Fatalf("Committed after contract = %v, %v; want 40, nil", c, err)
	}
}

// TestOversellRejected (AC-8, BUG-100): issuing a contract for more than the
// line's exportable slack returns a registry-sourced error (ErrInsufficientSurplus)
// AND creates no contract record — never a contract that silently oversells
// and only fails later at the crossing.
func TestOversellRejected(t *testing.T) {
	a, svc, _, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 100)
	setDemand(t, svc, id, 20) // surplus 80
	bindLine(t, a, ExportHospitalBeds, id)

	_, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 100, TermMonths: 12, RateMicropounds: 1_000_000})
	if err == nil {
		t.Fatalf("IssueContract oversell returned nil, want ErrInsufficientSurplus")
	}
	if !errors.Is(err, &errs.E{Code: ErrInsufficientSurplus}) {
		t.Fatalf("oversell error = %v, want code %s (BUG-100: assert the registry code, not just that a test exists)", err, ErrInsufficientSurplus)
	}
	if got := a.Contracts(); len(got) != 0 {
		t.Fatalf("oversell created %d contract records, want 0 (BUG-100: no record on failure)", len(got))
	}
}

// TestInvalidContractRejected (AC-9, BUG-100): cancelling a nonexistent
// contract returns a registry-sourced error distinct from AC-8's oversell
// code, and the state is unchanged.
func TestInvalidContractRejected(t *testing.T) {
	a, _, _, _ := newTestAPI(t)

	_, err := a.PayCancellationPenalty(ContractID(999))
	if err == nil {
		t.Fatalf("PayCancellationPenalty on nonexistent contract returned nil, want ErrInvalidContract")
	}
	if !errors.Is(err, &errs.E{Code: ErrInvalidContract}) {
		t.Fatalf("invalid-contract error = %v, want code %s (BUG-100: assert the registry code)", err, ErrInvalidContract)
	}
	if ErrInvalidContract == ErrInsufficientSurplus {
		t.Fatalf("AC-9 code (%s) must be distinct from AC-8 code (%s)", ErrInvalidContract, ErrInsufficientSurplus)
	}

	// A cancelled contract is equally invalid, and cancelling twice does not
	// re-post a penalty.
	a2, svc, _, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 100)
	bindLine(t, a2, ExportHospitalBeds, id)
	c, err := a2.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 10, TermMonths: 12, RateMicropounds: 1_000_000})
	if err != nil {
		t.Fatalf("IssueContract: %v", err)
	}
	if _, err := a2.PayCancellationPenalty(c.ID); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	if _, err := a2.PayCancellationPenalty(c.ID); err == nil || !errors.Is(err, &errs.E{Code: ErrInvalidContract}) {
		t.Fatalf("second cancel error = %v, want ErrInvalidContract", err)
	}
}

// TestMalformedCatalogueRejected (AC-9): a malformed catalogue entry (zero
// rate) is a registry-sourced load-time failure, distinct from AC-8's code —
// never a zero-valued contract silently accepted.
func TestMalformedCatalogueRejected(t *testing.T) {
	cfg := validTestConfig()
	cfg.Services[0].RateMicropounds = 0 // malformed: non-positive rate

	_, err := New(cfg, "test")
	if err == nil {
		t.Fatalf("New with malformed catalogue returned nil, want ErrCapexportDataInvalid")
	}
	if !errors.Is(err, &errs.E{Code: ErrCapexportDataInvalid}) {
		t.Fatalf("malformed-catalogue error = %v, want code %s", err, ErrCapexportDataInvalid)
	}
	if ErrCapexportDataInvalid == ErrInsufficientSurplus {
		t.Fatalf("AC-9 load code (%s) must be distinct from AC-8 code (%s)", ErrCapexportDataInvalid, ErrInsufficientSurplus)
	}
}

// TestCatalogueDataDriven (AC-5, GR#15): the catalogue is data, not Go
// literals — changing a fixture unit/rate changes what the API reports, so the
// rate is never a hardcoded value.
func TestCatalogueDataDriven(t *testing.T) {
	cfg := validTestConfig()
	for i := range cfg.Services {
		if cfg.Services[i].ID == ExportHospitalBeds {
			cfg.Services[i].RateMicropounds = 123_456_789
			cfg.Services[i].Unit = "beds/night"
		}
	}
	a, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if rate, err := a.DefaultRate(ExportHospitalBeds); err != nil || rate != 123_456_789 {
		t.Fatalf("DefaultRate = %v, %v; want 123456789 (data-driven)", rate, err)
	}
	for _, d := range a.Catalogue() {
		if d.ID == ExportHospitalBeds && d.Unit != "beds/night" {
			t.Fatalf("catalogue unit for hospital-beds = %q, want the fixture's %q (data-driven)", d.Unit, "beds/night")
		}
	}
}

// TestCatalogueMatchesShippedData is the drift test (weakness pattern #2):
// the Go enum (ExportableServices) and the shipped data/capexport.json must
// agree on the ten in-scope lines. If this fails, one side was edited without
// the other — a silent divergence a stranger must never be told about by a
// bare got/want.
func TestCatalogueMatchesShippedData(t *testing.T) {
	a, err := LoadDefault("test")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	got := map[ExportableService]bool{}
	for _, d := range a.Catalogue() {
		got[d.ID] = true
	}
	if len(got) != len(ExportableServices) {
		t.Fatalf("data/capexport.json has %d lines, Go enum has %d — they have drifted (change both together)", len(got), len(ExportableServices))
	}
	for _, line := range ExportableServices {
		if !got[line] {
			t.Errorf("data/capexport.json is missing line %q that the Go enum declares — the enum and the data file must stay in sync", line)
		}
	}
}
