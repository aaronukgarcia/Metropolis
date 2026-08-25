package cloud

import (
	"errors"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/integration"
)

// BlobClient is the durable-storage transport seam: the fake-able
// stand-in for an Azure Blob SDK client. It moves opaque bytes — the
// output of the local serializer (int.serializer), never re-encoded or
// re-hashed here (ICD §3/§4, AC-3). Implementations MUST be safe for
// concurrent use and MUST NOT read wall-clock time.
type BlobClient interface {
	// Put durably stores data under key. The exact bytes must be
	// retrievable verbatim via Get.
	Put(key string, data []byte) error

	// Get retrieves the bytes stored under key, verbatim. It returns
	// ErrBlobNotFound (wrapped or as-is) when key does not exist.
	Get(key string) ([]byte, error)
}

// BlobStore is the durable Blob-save / restore tier (tier a): a cloud copy
// of the exact bytes the local serializer produced. It is byte-preserving
// by construction — it stores what it is given and returns what it stored
// — and it owns no sim authority: the authoritative state remains the
// local checkpoint, the cloud copy is durability, not a second source of
// truth (GR#3).
//
// A failed save is a returned registry error, never a silent drop
// (GR#17). Resilience is delegated to an integration.Connection: retries
// are logical (attempt-counter, no wall clock), and Reconnect re-runs the
// configured re-auth / name-lookup hooks.
//
// Safe for concurrent use: the client and Connection are immutable after
// NewBlobStore and the counters are atomic.
type BlobStore struct {
	client BlobClient
	conn   *integration.Connection

	saves           atomic.Int64
	restores        atomic.Int64
	saveFailures    atomic.Int64
	restoreFailures atomic.Int64
}

// NewBlobStore constructs a durable Blob store over client. A nil client
// is rejected at first use, not at construction, so the zero-Config path
// (cloud absent) never allocates transport state it does not need.
func NewBlobStore(cfg Config, client BlobClient) *BlobStore {
	return &BlobStore{
		client: client,
		conn:   integration.NewConnection(cfg.connectionConfig()),
	}
}

// Save durably persists data verbatim under key. It retries the transport
// through the Connection's logical retry budget and returns a
// registry-sourced error (never a silent drop) once that budget is spent.
func (b *BlobStore) Save(correlationID, key string, data []byte) error {
	if b.client == nil {
		return cloudError(correlationID, ErrCloudDisabled, map[string]any{"key": key, "op": "save"})
	}
	// Defensive copy so a caller mutating data after Save returns cannot
	// change what was persisted (the store moves the exact bytes it was
	// given at the moment of the call).
	payload := append([]byte(nil), data...)

	err := attemptUntilSettled(b.conn, func() error {
		return b.client.Put(key, payload)
	})
	if err != nil {
		b.saveFailures.Add(1)
		return err
	}
	b.saves.Add(1)
	return nil
}

// Restore retrieves the bytes durably stored under key, verbatim. A missing
// key or transport failure surfaces a registry-sourced error wrapping the
// sentinel cause (errors.Is(err, ErrBlobNotFound) distinguishes the two).
func (b *BlobStore) Restore(correlationID, key string) ([]byte, error) {
	if b.client == nil {
		return nil, cloudError(correlationID, ErrCloudDisabled, map[string]any{"key": key, "op": "restore"})
	}

	var out []byte
	err := attemptUntilSettled(b.conn, func() error {
		got, e := b.client.Get(key)
		if e != nil {
			return e
		}
		out = append([]byte(nil), got...)
		return nil
	})
	if err != nil {
		b.restoreFailures.Add(1)
		return nil, err
	}
	b.restores.Add(1)
	return out, nil
}

// attemptUntilSettled runs op through the Connection's logical retry
// budget: it calls Connection.Attempt until op succeeds (returning nil) or
// the Connection reports a terminal state (returning that registry error).
// A bare op error still within budget loops again immediately — retries are
// bounded and deterministic, never wall-clock (GR#21). Any *errs.E the
// Connection returns (retries exhausted, or a copy-guard rejection) is
// terminal and returned as-is, so a mis-used Connection can never spin here.
func attemptUntilSettled(conn *integration.Connection, op func() error) error {
	for {
		err := conn.Attempt(op)
		if err == nil {
			return nil
		}
		var e *errs.E
		if errors.As(err, &e) {
			return err
		}
		// Bare op error, still within the retry budget: retry.
	}
}

// Reconnect re-establishes the cloud connection (re-auth + name lookup via
// the configured ReconnectHooks) per integration.Connection. The Blob tier
// owns no queue of its own (ICD §9), so the drain is normally a no-op.
func (b *BlobStore) Reconnect(name string) (integration.DrainStats, error) {
	return b.conn.Reconnect(name)
}

// State reports the underlying connection's current state (monitoring, §10).
func (b *BlobStore) State() integration.ConnState { return b.conn.State() }

// Retries reports the underlying connection's current retry counter.
func (b *BlobStore) Retries() int64 { return b.conn.Retries() }

// Stats carries the Blob tier's monitoring counters (§10: the critical
// signal is the save/restore success rate and the failure count).
type Stats struct {
	Saves           int64
	Restores        int64
	SaveFailures    int64
	RestoreFailures int64
}

// Stats reports the Blob tier's success/failure counters.
func (b *BlobStore) Stats() Stats {
	return Stats{
		Saves:           b.saves.Load(),
		Restores:        b.restores.Load(),
		SaveFailures:    b.saveFailures.Load(),
		RestoreFailures: b.restoreFailures.Load(),
	}
}
