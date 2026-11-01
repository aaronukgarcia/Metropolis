package logistics

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestLogisticsAPIFieldsAllClassified: every LogisticsAPI field is either
// serialized (stocks) or explicitly excluded with a reason.
func TestLogisticsAPIFieldsAllClassified(t *testing.T) {
	excluded := map[string]string{
		"mu":            "lock",
		"correlationID": "per-instance error correlation",
		"cfg":           "config reloaded from data/logistics.json",
		"market":        "config dependency",
		"subs":          "runtime handler wiring registered by Wire, not state",
		"self":          "SEC-020 copy guard",
	}
	covered := map[string]bool{"stocks": true}
	lt := reflect.TypeOf((*LogisticsAPI)(nil)).Elem()
	for i := 0; i < lt.NumField(); i++ {
		n := lt.Field(i).Name
		if _, ex := excluded[n]; !ex && !covered[n] {
			t.Fatalf("LogisticsAPI field %q neither serialized nor excluded", n)
		}
	}
}

func bundleDir(t *testing.T, root string) string {
	t.Helper()
	var found string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == "header.json" {
			found = filepath.Dir(p)
		}
		return nil
	})
	if found == "" {
		t.Fatal("no bundle found")
	}
	return found
}

// TestLogisticsParticipant_RoundTripReplacesShelves: a load REPLACES the
// freshly provisioned shelf set (level, runtime-provisioned shelves,
// buffer policy), and shelves absent from the save do not survive.
func TestLogisticsParticipant_RoundTripReplacesShelves(t *testing.T) {
	cid := errs.NewCorrelationID()
	a, err := LoadDefault(cid)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ProvisionStartingStocks(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision("site-x", market.Waste, 500, 40); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Restock("site-x", market.Waste, 10); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(a)}, cid)
	if err := mgr.SaveManual(save.Context{WorldSeed: 1, CreatedAtTick: 1, AppVersion: "t"}, "det"); err != nil {
		t.Fatal(err)
	}
	b, err := LoadDefault(cid)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.ProvisionStartingStocks(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Provision("phantom", market.Waste, 5, 5); err != nil {
		t.Fatal(err)
	}
	mgr2 := save.NewManager(root, []save.Participant{NewSaveParticipant(b)}, cid)
	if _, _, err := mgr2.Load(bundleDir(t, root)); err != nil {
		t.Fatalf("Load: %v", err)
	}
	sa, _ := a.DigestStocks()
	sb, _ := b.DigestStocks()
	if !reflect.DeepEqual(sa, sb) {
		t.Fatalf("shelves differ after load:\nsaved  %+v\nloaded %+v", sa, sb)
	}
	if _, err := b.Stock("phantom", market.Waste); err == nil {
		t.Fatal("phantom shelf survived the load")
	}
}

// copyGuardWant asserts err is the SEC-020 copy-guard trip (MET-G405) raised
// by the NAMED entry point's own guard (ctx "method"), so an inner guard
// further down the call chain cannot mask a removed outer one.
func copyGuardWant(t *testing.T, err error, method string) {
	t.Helper()
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("%s on copy: err = %v, want *errs.E %s", method, err, ErrCopiedValue)
	}
	if e.Code != ErrCopiedValue {
		t.Fatalf("%s on copy: code = %s, want %s (err %v)", method, e.Code, ErrCopiedValue, err)
	}
	if got, _ := e.Ctx["method"].(string); got != method {
		t.Fatalf("%s on copy: guard fired in %q, want its own guard %q", method, got, method)
	}
}

// TestBUG1142_SaveParticipant_RejectsStructCopy: a byte-copied LogisticsAPI
// must trip the SEC-020 guard on every participant entry point, fail closed.
// Payloads are VALID so only the guard can produce the failure, and each
// error is checked for code AND the entry point's own method name.
// NewSaveParticipant's guard is a deliberately discarded call (the wrapper
// is returned regardless, as build does), so it has no observable effect and
// is covered only structurally by astgate; every method it hands out
// re-checks.
func TestBUG1142_SaveParticipant_RejectsStructCopy(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)
	cp := logisticsAPIByteCopy(api)
	valid := serialize.Record{Kind: recLogisticsMeta, Data: []byte("{}")}

	// Control: the same calls on the ORIGINAL succeed, so a failure on the
	// copy can only come from the guard.
	good := NewSaveParticipant(api)
	if good.Kind() != KindLogistics {
		t.Fatalf("control Kind = %q", good.Kind())
	}
	if err := api.applyLoadRecord(valid); err != nil {
		t.Fatalf("control applyLoadRecord: %v", err)
	}
	if _, err := api.DigestStocks(); err != nil {
		t.Fatalf("control DigestStocks: %v", err)
	}

	p := NewSaveParticipant(cp)
	if p == nil {
		t.Fatal("NewSaveParticipant returned nil for a copy")
	}
	t.Run("Kind", func(t *testing.T) {
		if k := p.Kind(); k != "" {
			t.Fatalf("Kind on copy = %q, want empty", k)
		}
	})
	t.Run("Source", func(t *testing.T) {
		_, _, err := p.Source()()
		copyGuardWant(t, err, "Source")
	})
	t.Run("Handler", func(t *testing.T) {
		copyGuardWant(t, p.Handler()(valid), "Handler")
	})
	t.Run("DigestStocks", func(t *testing.T) {
		_, err := cp.DigestStocks()
		copyGuardWant(t, err, "DigestStocks")
	})
	t.Run("applyLoadRecord", func(t *testing.T) {
		copyGuardWant(t, cp.applyLoadRecord(valid), "applyLoadRecord")
	})
	t.Run("resetForLoad", func(t *testing.T) {
		copyGuardWant(t, cp.resetForLoad(), "resetForLoad")
	})
	t.Run("snapshotForSave", func(t *testing.T) {
		_, err := cp.snapshotForSave()
		copyGuardWant(t, err, "snapshotForSave")
	})
}
