package finance

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079941 inc1 — engine.finance is the FIRST engine module to
// implement the save.Participant contract (edge engine.finance→
// int.serializer, registered a6293cb). This file is the serialization
// pilot that proves the whole per-module state-serialization pattern the
// later increments roll out across every engine module.
//
// Serialization here is DATA-ONLY (the pivotal epic finding): the engine
// RNG (internal/foundation/det) is stateless — every consumer builds a
// fresh det.Stream, draws, and discards, so there is no mutable RNG
// cursor to persist — and engine.finance has no RNG at all (no
// foundation/det import). The reproducible-future inputs are (worldSeed,
// month) [in the save-bundle header] plus per-entity IDs [in these
// records]. So a lossless save is exactly the full ledger data.
//
// SaveParticipant does NOT import internal/engine/save: it satisfies
// save.Participant STRUCTURALLY (Kind/Source/Handler), consuming only
// internal/foundation/serialize's Record/RecordSource/RecordHandler
// vocabulary. That keeps this package free of any save→finance /
// finance→save import edge — inc1 stays on the single registered
// engine.finance→int.serializer edge, and live registration into
// save.DefaultParticipants is a deliberately-later increment (AC-6).

// The per-record Kind sub-labels this participant emits. Every record in
// the finance shard carries one of these as serialize.Record.Kind — the
// serializer's own doc names Kind "a caller-defined label that lets a
// reader dispatch to the right decoder", which is exactly how Handler
// routes each record back to the right wire type. (The whole shard is
// routed to this participant by save.Load on the shard-level Kind
// "finance", KindFinance below; these finer sub-labels live one level
// down, inside that shard, and never collide with the shard label.)
const (
	// KindFinance is this participant's stable shard label (AC-1). Must be
	// unique across a participant list; save.Load matches it against the
	// shard header's Kind to route the shard back here.
	KindFinance = "finance"

	recMeta       = "finance.meta"
	recAccount    = "finance.account"
	recTxn        = "finance.txn"
	recTickTxn    = "finance.ticktxn"
	recCreditLine = "finance.creditline"
	recLoan       = "finance.loan"
	recFirm       = "finance.firm"
	recInvestment = "finance.investment"
)

// Wire projections (AC-2). Each serialized domain type gets an explicit
// projection carrying json tags — the domain struct is NEVER marshalled
// directly, so a field added to a domain type without a matching wire
// field is caught by the reflective field-parity drift test
// (participant_test.go), not silently dropped from the save. The wire
// fields reuse the domain's own named types (AccountID, Money, Side, …)
// so the projection can never disagree with the domain about a field's
// underlying kind.

// entryWire is Entry's wire projection.
type entryWire struct {
	Account  AccountID `json:"account"`
	Side     Side      `json:"side"`
	Amount   Money     `json:"amount"`
	Category Category  `json:"category"`
}

// transactionWire is Transaction's wire projection. Entries are projected
// through entryWire (never []Entry marshalled directly).
type transactionWire struct {
	ID          TxID        `json:"id"`
	Month       int64       `json:"month"`
	Description string      `json:"description"`
	Entries     []entryWire `json:"entries"`
}

// accountStateWire is accountState's wire projection (the map VALUE). Its
// map key travels in accountRecordWire.ID alongside it, so accountState's
// own two fields project one-for-one here (keeping the equal-field-count
// drift test's teeth against accountState).
type accountStateWire struct {
	Role    AccountRole `json:"role"`
	Balance Money       `json:"balance"`
}

// accountRecordWire is one ledger account on the wire: the map key (ID)
// plus the accountStateWire value. The role map is not serialized
// separately — it is rebuilt from each account's Role on load (the two
// are maintained in lock-step by OpenAccount/NewFinanceAPI, so this is
// lossless, not a shortcut).
type accountRecordWire struct {
	ID    AccountID        `json:"id"`
	State accountStateWire `json:"state"`
}

// creditLineWire is one entry of the creditLines map on the wire.
type creditLineWire struct {
	Account AccountID `json:"account"`
	Amount  Money     `json:"amount"`
}

// loanWire is Loan's wire projection.
type loanWire struct {
	ID            LoanID      `json:"id"`
	Principal     Money       `json:"principal"`
	Outstanding   Money       `json:"outstanding"`
	TermMonths    int         `json:"termMonths"`
	MilestoneTier int         `json:"milestoneTier"`
	RateBp        BasisPoints `json:"rateBp"`
}

// simpleFirmWire is SimpleFirm's wire projection. LossStreak projects
// SimpleFirm's UNEXPORTED lossStreak field: it is genuine state (it
// gates AdvanceMonth's close), so a lossless save must carry it — the
// field-parity drift test knows the lossStreak→LossStreak rename.
type simpleFirmWire struct {
	ID         FirmID `json:"id"`
	Name       string `json:"name"`
	Revenue    Money  `json:"revenue"`
	WageCost   Money  `json:"wageCost"`
	InputCost  Money  `json:"inputCost"`
	Rent       Money  `json:"rent"`
	Open       bool   `json:"open"`
	LossStreak int    `json:"lossStreak"`
}

// investmentWire is InvestmentProgramme's wire projection.
type investmentWire struct {
	ID            InvestmentID `json:"id"`
	Name          string       `json:"name"`
	Capex         Money        `json:"capex"`
	MonthlyReturn Money        `json:"monthlyReturn"`
	PaybackMonths int          `json:"paybackMonths"`
	StartMonth    int64        `json:"startMonth"`
}

// financeMetaWire carries the FinanceAPI's scalar/counter state (the
// fields with no domain-struct of their own): id counters, the money-
// stock triple, the current month, the two maintained running totals,
// and the insolvency/game-over state. Serializing totalCreditLine and
// totalDebt directly (rather than recomputing them on load) keeps the
// running-total fields byte-for-byte equal across a round trip; the
// round-trip test independently proves they still reconcile with the
// ledger via RecomputeMoneyStock.
type financeMetaWire struct {
	NextTxID         TxID         `json:"nextTxID"`
	NextLoanID       LoanID       `json:"nextLoanID"`
	NextFirmID       FirmID       `json:"nextFirmID"`
	NextInvestID     InvestmentID `json:"nextInvestID"`
	MoneyStock       Money        `json:"moneyStock"`
	OpeningStock     Money        `json:"openingStock"`
	TrackedDelta     Money        `json:"trackedDelta"`
	Month            int64        `json:"month"`
	TotalCreditLine  Money        `json:"totalCreditLine"`
	TotalDebt        Money        `json:"totalDebt"`
	MissedPayments   int          `json:"missedPayments"`
	InsolvencyMonths int          `json:"insolvencyMonths"`
	GameOver         bool         `json:"gameOver"`
	// Backlog is FEAT-094's running maintenance-underfunding balance
	// (AC-5) — real simulation state that persists across months, so it
	// must survive a save/load round trip like every other running total
	// here (never re-derived from the ledger on load — the ledger's
	// per-tick transactions don't carry enough history to reconstruct a
	// balance that has been decaying/growing over many prior months).
	Backlog Money `json:"backlog"`
	// CremationShortfall/LastCremationShortfallMonth (BUG-733, GR#17):
	// the running, ACCRUING unpaid-cremation-cost debt and the month it
	// was last added to. Real conservation-relevant state (money the
	// treasury still owes the outside world for a service already
	// delivered), so it persists across a save/load round trip exactly
	// like Backlog — never re-derived on load, and NOT part of the
	// PayrollShortfall-style excluded/transient set (see
	// participant_test.go's TestFinanceAPIFieldsAllClassified).
	CremationShortfall          Money `json:"cremationShortfall"`
	LastCremationShortfallMonth int64 `json:"lastCremationShortfallMonth"`
}

// financeSnapshot is a point-in-time, deterministically-ordered copy of
// the full ledger, taken under the finance lock in one shot. Every
// map-backed collection is flattened to a slice sorted by key (GR#21) so
// the emitted record order — and therefore the saved bytes — is
// deterministic; the already-ordered append-only logs (txns/tickTxns/
// investments) keep their post order.
type financeSnapshot struct {
	meta        financeMetaWire
	accounts    []accountRecordWire // sorted by ID
	txns        []transactionWire   // post order
	tickTxns    []transactionWire   // post order
	creditLines []creditLineWire    // sorted by Account
	loans       []loanWire          // sorted by ID
	firms       []simpleFirmWire    // sorted by ID
	investments []investmentWire    // start order
}

// total is the number of records the snapshot emits: one meta record
// plus one per item in every collection.
func (s *financeSnapshot) total() int {
	return 1 + len(s.accounts) + len(s.txns) + len(s.tickTxns) +
		len(s.creditLines) + len(s.loans) + len(s.firms) + len(s.investments)
}

// recordAt marshals exactly the i-th record of the deterministic
// emission sequence (meta, accounts, txns, tickTxns, creditLines, loans,
// firms, investments) — one record's bytes, on demand, so Source never
// materialises the whole encoded shard before its first yield (AC-4).
func (s *financeSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("finance: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

// locate maps a global record index to its (Kind, wire value) without
// encoding anything — the pure index arithmetic behind recordAt.
func (s *financeSnapshot) locate(i int) (string, any) {
	if i == 0 {
		return recMeta, s.meta
	}
	i--
	if i < len(s.accounts) {
		return recAccount, s.accounts[i]
	}
	i -= len(s.accounts)
	if i < len(s.txns) {
		return recTxn, s.txns[i]
	}
	i -= len(s.txns)
	if i < len(s.tickTxns) {
		return recTickTxn, s.tickTxns[i]
	}
	i -= len(s.tickTxns)
	if i < len(s.creditLines) {
		return recCreditLine, s.creditLines[i]
	}
	i -= len(s.creditLines)
	if i < len(s.loans) {
		return recLoan, s.loans[i]
	}
	i -= len(s.loans)
	if i < len(s.firms) {
		return recFirm, s.firms[i]
	}
	i -= len(s.firms)
	return recInvestment, s.investments[i]
}

// toEntryWires projects a Transaction's entries one-for-one.
func toEntryWires(entries []Entry) []entryWire {
	out := make([]entryWire, len(entries))
	for i, e := range entries {
		// Direct struct conversion (identical field set) into the TAGGED
		// wire type — the domain struct is still never json.Marshalled
		// directly (AC-2). Mirrors save/fixture_test.go's widgetWire(w).
		out[i] = entryWire(e)
	}
	return out
}

// toTransactionWire projects a Transaction (never marshalled directly).
func toTransactionWire(tx Transaction) transactionWire {
	return transactionWire{
		ID:          tx.ID,
		Month:       tx.Month,
		Description: tx.Description,
		Entries:     toEntryWires(tx.Entries),
	}
}

// snapshotForSave copies the full ledger into a deterministically-ordered
// financeSnapshot under the read lock (AC-1/AC-3). It reads everything in
// one locked pass so the snapshot is internally consistent, then releases
// the lock — Source encodes from the snapshot, not the live state.
func (f *FinanceAPI) snapshotForSave() (financeSnapshot, error) {
	if err := f.checkNotCopied("snapshotForSave"); err != nil {
		return financeSnapshot{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()

	snap := financeSnapshot{
		meta: financeMetaWire{
			NextTxID:                    f.nextTxID,
			NextLoanID:                  f.nextLoanID,
			NextFirmID:                  f.nextFirmID,
			NextInvestID:                f.nextInvestID,
			MoneyStock:                  f.moneyStock,
			OpeningStock:                f.openingStock,
			TrackedDelta:                f.trackedDelta,
			Month:                       f.month,
			TotalCreditLine:             f.totalCreditLine,
			TotalDebt:                   f.totalDebt,
			MissedPayments:              f.missedPayments,
			InsolvencyMonths:            f.insolvencyMonths,
			GameOver:                    f.gameOver,
			Backlog:                     f.backlog,
			CremationShortfall:          f.cremationShortfall,
			LastCremationShortfallMonth: f.lastCremationShortfallMonth,
		},
	}

	// Accounts — sorted by ID (GR#21).
	acctIDs := make([]AccountID, 0, len(f.accounts))
	for id := range f.accounts {
		acctIDs = append(acctIDs, id)
	}
	sort.Slice(acctIDs, func(i, j int) bool { return acctIDs[i] < acctIDs[j] })
	snap.accounts = make([]accountRecordWire, 0, len(acctIDs))
	for _, id := range acctIDs {
		a := f.accounts[id]
		snap.accounts = append(snap.accounts, accountRecordWire{
			ID:    id,
			State: accountStateWire{Role: a.Role, Balance: a.Balance},
		})
	}

	// Append-only logs — post order (already deterministic).
	snap.txns = make([]transactionWire, len(f.txns))
	for i, tx := range f.txns {
		snap.txns[i] = toTransactionWire(tx)
	}
	snap.tickTxns = make([]transactionWire, len(f.tickTxns))
	for i, tx := range f.tickTxns {
		snap.tickTxns[i] = toTransactionWire(tx)
	}

	// Credit lines — sorted by account (GR#21).
	clIDs := make([]AccountID, 0, len(f.creditLines))
	for id := range f.creditLines {
		clIDs = append(clIDs, id)
	}
	sort.Slice(clIDs, func(i, j int) bool { return clIDs[i] < clIDs[j] })
	snap.creditLines = make([]creditLineWire, 0, len(clIDs))
	for _, id := range clIDs {
		snap.creditLines = append(snap.creditLines, creditLineWire{Account: id, Amount: f.creditLines[id]})
	}

	// Loans — sorted by ID (GR#21).
	loanIDs := make([]LoanID, 0, len(f.loans))
	for id := range f.loans {
		loanIDs = append(loanIDs, id)
	}
	sort.Slice(loanIDs, func(i, j int) bool { return loanIDs[i] < loanIDs[j] })
	snap.loans = make([]loanWire, 0, len(loanIDs))
	for _, id := range loanIDs {
		l := f.loans[id]
		snap.loans = append(snap.loans, loanWire{
			ID:            l.ID,
			Principal:     l.Principal,
			Outstanding:   l.Outstanding,
			TermMonths:    l.TermMonths,
			MilestoneTier: l.MilestoneTier,
			RateBp:        l.RateBp,
		})
	}

	// Firms — sorted by ID (GR#21).
	firmIDs := make([]FirmID, 0, len(f.firms))
	for id := range f.firms {
		firmIDs = append(firmIDs, id)
	}
	sort.Slice(firmIDs, func(i, j int) bool { return firmIDs[i] < firmIDs[j] })
	snap.firms = make([]simpleFirmWire, 0, len(firmIDs))
	for _, id := range firmIDs {
		fm := f.firms[id]
		snap.firms = append(snap.firms, simpleFirmWire{
			ID:         fm.ID,
			Name:       fm.Name,
			Revenue:    fm.Revenue,
			WageCost:   fm.WageCost,
			InputCost:  fm.InputCost,
			Rent:       fm.Rent,
			Open:       fm.Open,
			LossStreak: fm.lossStreak,
		})
	}

	// Investments — start order (append order is deterministic).
	snap.investments = make([]investmentWire, len(f.investments))
	for i, p := range f.investments {
		snap.investments[i] = investmentWire{
			ID:            p.ID,
			Name:          p.Name,
			Capex:         p.Capex,
			MonthlyReturn: p.MonthlyReturn,
			PaybackMonths: p.PaybackMonths,
			StartMonth:    p.StartMonth,
		}
	}

	return snap, nil
}

// resetForLoad clears the ledger to empty under the write lock, before a
// Load streams records in (AC-1). A freshly-constructed FinanceAPI has
// the six well-known accounts pre-opened; a load must REPLACE the ledger
// with the saved one, so every collection is emptied and every scalar
// zeroed here — Handler then rebuilds them one record at a time.
func (f *FinanceAPI) resetForLoad() error {
	if err := f.checkNotCopied("resetForLoad"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts = make(map[AccountID]*accountState)
	f.role = make(map[AccountID]AccountRole)
	f.txns = nil
	f.tickTxns = nil
	f.creditLines = make(map[AccountID]Money)
	f.totalCreditLine = 0
	f.loans = make(map[LoanID]*Loan)
	f.firms = make(map[FirmID]*SimpleFirm)
	f.investments = nil
	f.nextTxID = 0
	f.nextLoanID = 0
	f.nextFirmID = 0
	f.nextInvestID = 0
	f.moneyStock = 0
	f.openingStock = 0
	f.trackedDelta = 0
	f.month = 0
	f.totalDebt = 0
	f.missedPayments = 0
	f.insolvencyMonths = 0
	f.gameOver = false
	f.backlog = 0
	f.cremationShortfall = 0
	f.lastCremationShortfallMonth = 0
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect
// directly into the ledger under the write lock (AC-1/AC-4). Installing
// per record — rather than buffering the whole decoded shard and then
// assigning — keeps the load side O(1) per record and streaming, the
// mirror of Source's one-record-at-a-time emission. The role map is
// rebuilt here from each account's Role. Returns a decode/kind error
// verbatim so ReadShard fails loud and closed rather than loading a
// partial ledger silently.
func (f *FinanceAPI) applyLoadRecord(rec serialize.Record) error {
	if err := f.checkNotCopied("applyLoadRecord"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	switch rec.Kind {
	case recMeta:
		var m financeMetaWire
		if err := json.Unmarshal(rec.Data, &m); err != nil {
			return fmt.Errorf("finance: decoding %s record: %w", rec.Kind, err)
		}
		f.nextTxID = m.NextTxID
		f.nextLoanID = m.NextLoanID
		f.nextFirmID = m.NextFirmID
		f.nextInvestID = m.NextInvestID
		f.moneyStock = m.MoneyStock
		f.openingStock = m.OpeningStock
		f.trackedDelta = m.TrackedDelta
		f.month = m.Month
		f.totalCreditLine = m.TotalCreditLine
		f.totalDebt = m.TotalDebt
		f.missedPayments = m.MissedPayments
		f.insolvencyMonths = m.InsolvencyMonths
		f.gameOver = m.GameOver
		f.backlog = m.Backlog
		f.cremationShortfall = m.CremationShortfall
		f.lastCremationShortfallMonth = m.LastCremationShortfallMonth

	case recAccount:
		var a accountRecordWire
		if err := json.Unmarshal(rec.Data, &a); err != nil {
			return fmt.Errorf("finance: decoding %s record: %w", rec.Kind, err)
		}
		f.accounts[a.ID] = &accountState{Role: a.State.Role, Balance: a.State.Balance}
		f.role[a.ID] = a.State.Role

	case recTxn:
		var w transactionWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("finance: decoding %s record: %w", rec.Kind, err)
		}
		f.txns = append(f.txns, fromTransactionWire(w))

	case recTickTxn:
		var w transactionWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("finance: decoding %s record: %w", rec.Kind, err)
		}
		f.tickTxns = append(f.tickTxns, fromTransactionWire(w))

	case recCreditLine:
		var c creditLineWire
		if err := json.Unmarshal(rec.Data, &c); err != nil {
			return fmt.Errorf("finance: decoding %s record: %w", rec.Kind, err)
		}
		f.creditLines[c.Account] = c.Amount

	case recLoan:
		var l loanWire
		if err := json.Unmarshal(rec.Data, &l); err != nil {
			return fmt.Errorf("finance: decoding %s record: %w", rec.Kind, err)
		}
		f.loans[l.ID] = &Loan{
			ID:            l.ID,
			Principal:     l.Principal,
			Outstanding:   l.Outstanding,
			TermMonths:    l.TermMonths,
			MilestoneTier: l.MilestoneTier,
			RateBp:        l.RateBp,
		}

	case recFirm:
		var fm simpleFirmWire
		if err := json.Unmarshal(rec.Data, &fm); err != nil {
			return fmt.Errorf("finance: decoding %s record: %w", rec.Kind, err)
		}
		f.firms[fm.ID] = &SimpleFirm{
			ID:         fm.ID,
			Name:       fm.Name,
			Revenue:    fm.Revenue,
			WageCost:   fm.WageCost,
			InputCost:  fm.InputCost,
			Rent:       fm.Rent,
			Open:       fm.Open,
			lossStreak: fm.LossStreak,
		}

	case recInvestment:
		var p investmentWire
		if err := json.Unmarshal(rec.Data, &p); err != nil {
			return fmt.Errorf("finance: decoding %s record: %w", rec.Kind, err)
		}
		f.investments = append(f.investments, &InvestmentProgramme{
			ID:            p.ID,
			Name:          p.Name,
			Capex:         p.Capex,
			MonthlyReturn: p.MonthlyReturn,
			PaybackMonths: p.PaybackMonths,
			StartMonth:    p.StartMonth,
		})

	default:
		return fmt.Errorf("finance: unknown finance save record kind %q", rec.Kind)
	}
	return nil
}

// fromTransactionWire rebuilds a Transaction from its wire projection.
func fromTransactionWire(w transactionWire) Transaction {
	entries := make([]Entry, len(w.Entries))
	for i, e := range w.Entries {
		entries[i] = Entry(e)
	}
	return Transaction{
		ID:          w.ID,
		Month:       w.Month,
		Description: w.Description,
		Entries:     entries,
	}
}

// SaveParticipant adapts a *FinanceAPI to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save —
// the interface is satisfied structurally. Construct via
// NewSaveParticipant; the wrapped FinanceAPI is the live state Source
// snapshots on save and the target Handler rebuilds on load.
type SaveParticipant struct {
	f *FinanceAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing
// f's state. On save it snapshots f; on load it resets f and rebuilds it
// from the streamed records — so a load target is typically a FRESH
// NewFinanceAPI whose pre-opened accounts are replaced by the saved ones.
func NewSaveParticipant(f *FinanceAPI) *SaveParticipant {
	// SEC-020 pre-lock guard (astgate live-tree): a copied FinanceAPI is
	// still wrapped so the caller gets a non-nil participant, but every
	// method below re-checks checkNotCopied and fails closed, so a copy can
	// never actually read or mutate the ledger through this participant.
	_ = f.checkNotCopied("NewSaveParticipant")
	return &SaveParticipant{f: f}
}

// Kind returns the finance shard label (AC-1). The SEC-020 guard mirrors
// every other method that reaches the wrapped candidate type (astgate
// live-tree): a copied FinanceAPI yields the empty kind, which save.Load
// and registry validation reject rather than routing a shard to a copy.
func (p *SaveParticipant) Kind() string {
	if err := p.f.checkNotCopied("Kind"); err != nil {
		return ""
	}
	return KindFinance
}

// Source returns a fresh pull-iterator over the finance state (AC-1). It
// snapshots the full ledger under the lock once, up front, then yields
// one record at a time, marshalling each on demand — never buffering the
// whole encoded shard before the first yield (AC-4). A copied-value guard
// failure (SEC-020) surfaces on the first pull.
func (p *SaveParticipant) Source() serialize.RecordSource {
	if err := p.f.checkNotCopied("Source"); err != nil {
		return func() (serialize.Record, bool, error) { return serialize.Record{}, false, err }
	}
	snap, snapErr := p.f.snapshotForSave()
	idx := 0
	return func() (serialize.Record, bool, error) {
		if snapErr != nil {
			err := snapErr
			snapErr = nil
			return serialize.Record{}, false, err
		}
		if idx >= snap.total() {
			return serialize.Record{}, false, nil
		}
		rec, err := snap.recordAt(idx)
		if err != nil {
			return serialize.Record{}, false, err
		}
		idx++
		return rec, true, nil
	}
}

// Handler returns a fresh sink that rebuilds the finance state from the
// streamed records (AC-1). It clears the target ledger on the first
// record, then installs each record's effect directly under the lock —
// one record at a time, never buffering the whole shard (AC-4).
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	if err := p.f.checkNotCopied("Handler"); err != nil {
		return func(serialize.Record) error { return err }
	}
	reset := false
	return func(rec serialize.Record) error {
		if !reset {
			if err := p.f.resetForLoad(); err != nil {
				return err
			}
			reset = true
		}
		return p.f.applyLoadRecord(rec)
	}
}
