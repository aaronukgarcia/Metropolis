package citizens

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// citizensByteCopy performs SEC-020's attack — a plain CitizensAPI struct
// copy — via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// internal/foundation/registry/sec020_test.go's registryByteCopy (see that
// file's doc comment for why a literal `cp := *c` is forbidden here: go
// vet's copylocks check flags it, and VERIFY runs go vet).
func citizensByteCopy(c *CitizensAPI) *CitizensAPI {
	cp := new(CitizensAPI)
	*(*[unsafe.Sizeof(CitizensAPI{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(CitizensAPI{})]byte)(unsafe.Pointer(c))
	return cp
}

// TestCitizensAPICopyGuard (SEC-020, AC-1b's command-only surface): a
// command issued against a struct-copied CitizensAPI is rejected with
// ErrAPICopied rather than racing the copy's own mutex over the original's
// aliased maps/shards.
func TestCitizensAPICopyGuard(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	cp := citizensByteCopy(api)

	err = cp.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventDeath, CitizenID: 1})
	if err == nil {
		t.Fatal("expected ErrAPICopied from a struct-copied CitizensAPI, got nil")
	}
	if !errors.Is(err, &errs.E{Code: ErrAPICopied}) {
		t.Fatalf("expected ErrAPICopied (%s), got %v", ErrAPICopied, err)
	}

	// The original is unaffected and still usable.
	if got := api.TotalPopulation("corr"); got != 0 {
		t.Fatalf("original API corrupted by the copy guard test: population %d", got)
	}
}
