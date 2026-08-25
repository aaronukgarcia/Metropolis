package cloud

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/integration"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// memBlob is an in-process object-store double implementing BlobClient with
// byte-preserving semantics (defensive copies in both directions).
type memBlob struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemBlob() *memBlob {
	return &memBlob{data: map[string][]byte{}}
}

func (m *memBlob) Put(key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = append([]byte(nil), data...)
	return nil
}

func (m *memBlob) Get(key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrBlobNotFound, key)
	}
	return append([]byte(nil), b...), nil
}

// failBlob always fails with ErrCloudUnavailable, counting calls.
type failBlob struct{ calls atomic.Int64 }

func (f *failBlob) Put(string, []byte) error {
	f.calls.Add(1)
	return ErrCloudUnavailable
}

func (f *failBlob) Get(string) ([]byte, error) {
	f.calls.Add(1)
	return nil, ErrCloudUnavailable
}

// flakyBlob fails the first n calls then succeeds, to exercise the logical
// retry budget.
type flakyBlob struct {
	n     int
	calls atomic.Int64
}

func (f *flakyBlob) Put(string, []byte) error {
	if f.calls.Add(1) <= int64(f.n) {
		return ErrCloudUnavailable
	}
	return nil
}

func (f *flakyBlob) Get(string) ([]byte, error) {
	if f.calls.Add(1) <= int64(f.n) {
		return nil, ErrCloudUnavailable
	}
	return []byte("ok"), nil
}

// writeSerializerShard produces the exact bytes int.serializer emits for a
// small deterministic record set, plus the serializer's recorded integrity
// metadata. This is the "cloud copy of the same serializer bytes" unit the
// blob tier moves (ICD §3/§4).
func writeSerializerShard(t *testing.T) ([]byte, serialize.ShardMeta) {
	t.Helper()

	recs := []serialize.Record{
		{Kind: "citizen", Data: []byte(`{"id":1,"name":"alice"}`)},
		{Kind: "building", Data: []byte(`{"id":99,"zone":"commercial"}`)},
		{Kind: "citizen", Data: []byte(`{"id":2,"name":"bob"}`)},
	}
	i := 0
	src := func() (serialize.Record, bool, error) {
		if i >= len(recs) {
			return serialize.Record{}, false, nil
		}
		r := recs[i]
		i++
		return r, true, nil
	}

	var buf bytes.Buffer
	meta, err := (serialize.NDJSONSerializer{}).WriteShard(&buf, serialize.ShardMeta{
		Name: "citizens.0001",
		Kind: "citizen",
	}, src)
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	if meta.SHA256 == "" || meta.ByteSize == 0 {
		t.Fatalf("WriteShard returned zero integrity metadata: %+v", meta)
	}
	return buf.Bytes(), meta
}

func registryCode(t *testing.T, err error) string {
	t.Helper()
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not a registry-sourced *errs.E (GR#7)", err)
	}
	return e.Code
}

// TestBlobRoundTripByteIdentical is ICD §11's determinism equivalence test
// for the storage tier: a Blob save -> restore reproduces the exact bytes
// the local serializer produced, validated against the serializer's own
// SHA256 (GR#3 — never a second scheme).
func TestBlobRoundTripByteIdentical(t *testing.T) {
	shard, meta := writeSerializerShard(t)

	store := NewBlobStore(Config{Enabled: true}, newMemBlob())
	if err := store.Save("corr-1", "save/checkpoint/citizens.0001", shard); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Restore("corr-1", "save/checkpoint/citizens.0001")
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !bytes.Equal(got, shard) {
		t.Fatalf("round-tripped bytes differ: got %d bytes, want %d", len(got), len(shard))
	}

	// Validate against the serializer's recorded SHA256/ByteSize — the blob
	// tier moves the serializer's bytes; it must reproduce them exactly.
	sum := sha256.Sum256(got)
	if hex.EncodeToString(sum[:]) != meta.SHA256 {
		t.Fatalf("restored bytes hash %s != serializer SHA256 %s", hex.EncodeToString(sum[:]), meta.SHA256)
	}
	if int64(len(got)) != meta.ByteSize {
		t.Fatalf("restored ByteSize %d != serializer ByteSize %d", len(got), meta.ByteSize)
	}
}

// TestBlobSaveFailureSurfacesRegistryError is ICD §11 / §10 (GR#17): a
// failed save is a registry error, never a silent drop, with a bounded,
// deterministic retry count.
func TestBlobSaveFailureSurfacesRegistryError(t *testing.T) {
	fail := &failBlob{}
	store := NewBlobStore(Config{Enabled: true, MaxRetries: 2}, fail)

	err := store.Save("corr-2", "k", []byte("payload"))
	if err == nil {
		t.Fatal("Save must fail")
	}
	if code := registryCode(t, err); code != integration.ErrRetriesExhausted {
		t.Errorf("surfaced code = %s, want %s", code, integration.ErrRetriesExhausted)
	}
	if !errors.Is(err, ErrCloudUnavailable) {
		t.Errorf("terminal error does not preserve the ErrCloudUnavailable cause: %v", err)
	}
	// MaxRetries=2 -> 1 initial + 2 retries = 3 bounded attempts, then
	// retries exhausted. Never unbounded, never wall-clock (GR#21).
	if got := fail.calls.Load(); got != 3 {
		t.Errorf("transport called %d times, want 3 (bounded logical retries)", got)
	}
	if got := store.Stats().SaveFailures; got != 1 {
		t.Errorf("SaveFailures = %d, want 1", got)
	}
}

// TestBlobRestoreNotFoundSurfacesRegistryError proves a missing blob is a
// registry error that still lets a caller distinguish the not-found cause.
func TestBlobRestoreNotFoundSurfacesRegistryError(t *testing.T) {
	store := NewBlobStore(Config{Enabled: true, MaxRetries: 2}, newMemBlob())

	_, err := store.Restore("corr-3", "missing")
	if err == nil {
		t.Fatal("Restore of a missing key must fail")
	}
	if code := registryCode(t, err); code != integration.ErrRetriesExhausted {
		t.Errorf("surfaced code = %s, want %s", code, integration.ErrRetriesExhausted)
	}
	if !errors.Is(err, ErrBlobNotFound) {
		t.Errorf("terminal error does not preserve the ErrBlobNotFound cause: %v", err)
	}
}

// TestBlobRetriesWithinBudget proves a transient failure within the logical
// retry budget self-heals without the caller intervening.
func TestBlobRetriesWithinBudget(t *testing.T) {
	flaky := &flakyBlob{n: 1}
	store := NewBlobStore(Config{Enabled: true, MaxRetries: 3}, flaky)

	if err := store.Save("corr-4", "k", []byte("x")); err != nil {
		t.Fatalf("Save should succeed within budget: %v", err)
	}
	if got := flaky.calls.Load(); got != 2 {
		t.Errorf("transport called %d times, want 2 (1 failure + 1 success)", got)
	}
	if got := store.Stats().Saves; got != 1 {
		t.Errorf("Saves = %d, want 1", got)
	}
	if got := store.State(); got != integration.StateConnected {
		t.Errorf("State() = %v, want StateConnected after a recovered attempt", got)
	}
}

// TestBlobSaveDefensiveCopy proves the store moves the bytes it was given
// at call time — a later mutation of the caller's slice cannot change what
// was persisted.
func TestBlobSaveDefensiveCopy(t *testing.T) {
	store := NewBlobStore(Config{Enabled: true}, newMemBlob())
	orig := []byte("immutable-save-bytes")

	if err := store.Save("corr-5", "k", orig); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for i := range orig {
		orig[i] = 0xFF // mutate after the fact
	}

	got, err := store.Restore("corr-5", "k")
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !bytes.Equal(got, []byte("immutable-save-bytes")) {
		t.Fatalf("restored bytes %q were corrupted by caller mutation", got)
	}
}

// TestBlobReconnectRunsReauthAndLookup is ICD §11's reconnect + re-auth test
// for the storage tier.
func TestBlobReconnectRunsReauthAndLookup(t *testing.T) {
	hooks := &recordingHooks{}
	store := NewBlobStore(Config{Enabled: true, Hooks: hooks}, &failBlob{})

	// Force the connection out of Connected.
	if err := store.Save("corr-6", "k", []byte("x")); err == nil {
		t.Fatal("Save must fail against a failing transport")
	}

	if _, err := store.Reconnect("azure.blob"); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if got := store.State(); got != integration.StateConnected {
		t.Errorf("State() after Reconnect = %v, want StateConnected", got)
	}
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if hooks.authCalls != 1 {
		t.Errorf("Authenticate called %d times, want 1", hooks.authCalls)
	}
	if hooks.lookupCalls != 1 {
		t.Errorf("Lookup called %d times, want 1", hooks.lookupCalls)
	}
	if hooks.lookupTarget != "azure.blob" {
		t.Errorf("Lookup target = %q, want %q", hooks.lookupTarget, "azure.blob")
	}
}

// TestBlobNilClientReturnsCloudDisabled proves a BlobStore with no
// transport (cloud absent) fails loudly with a registry error rather than
// silently no-op-ing (GR#17).
func TestBlobNilClientReturnsCloudDisabled(t *testing.T) {
	store := NewBlobStore(Config{Enabled: true}, nil)

	if err := store.Save("corr-7", "k", []byte("x")); err == nil {
		t.Fatal("Save with nil client must fail")
	} else {
		if code := registryCode(t, err); code != integration.ErrRetriesExhausted {
			t.Errorf("surfaced code = %s, want %s", code, integration.ErrRetriesExhausted)
		}
		if !errors.Is(err, ErrCloudDisabled) {
			t.Errorf("nil-client error does not preserve ErrCloudDisabled: %v", err)
		}
	}

	if _, err := store.Restore("corr-7", "k"); err == nil {
		t.Fatal("Restore with nil client must fail")
	} else if !errors.Is(err, ErrCloudDisabled) {
		t.Errorf("nil-client restore error does not preserve ErrCloudDisabled: %v", err)
	}
}
