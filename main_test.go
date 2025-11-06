package goportfolio

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAddTransactionHandlesVariableLengthSymbols(t *testing.T) {
	p := NewPortfolio()
	if err := p.SetBalance("USDT", 1000); err != nil {
		t.Fatalf("set balance failed: %v", err)
	}

	tx := Transaction{
		ID:        "tx1",
		Symbol:    "CFX/USDT",
		Type:      "buy",
		Quantity:  10,
		Price:     2.5,
		Fee:       1,
		Timestamp: time.Now(),
	}

	if err := p.AddTransaction(tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := p.GetBalance("CFX"); got != tx.Quantity {
		t.Fatalf("balance for CFX = %v, want %v", got, tx.Quantity)
	}

	wantUSDT := 1000 - tx.Quantity*tx.Price - tx.Fee
	if got := p.GetBalance("USDT"); got != wantUSDT {
		t.Fatalf("balance for USDT = %v, want %v", got, wantUSDT)
	}
}

func TestAddTransactionWithInvalidSymbolDoesNotUpdateBalances(t *testing.T) {
	p := NewPortfolio()
	if err := p.SetBalance("USD", 50); err != nil {
		t.Fatalf("set balance failed: %v", err)
	}

	tx := Transaction{
		ID:        "tx2",
		Symbol:    "DOGEUSD", // missing separator
		Type:      "buy",
		Quantity:  5,
		Price:     2,
		Fee:       0.1,
		Timestamp: time.Now(),
	}

	err := p.AddTransaction(tx)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "DOGEUSD") {
		t.Fatalf("error message missing symbol context: %v", err)
	}

	if got := p.GetBalance("USD"); got != 50 {
		t.Fatalf("balance for USD changed unexpectedly: got %v want %v", got, 50)
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if got := len(p.balances); got != 1 {
		t.Fatalf("unexpected balances entries, got %d want 1", got)
	}
}

func TestAddTransactionRespectsMaxExposureLimit(t *testing.T) {
	policy := &RiskPolicy{
		MaxExposureBySymbol: map[string]float64{
			"BTC/USDT": 5,
		},
	}
	p := NewPortfolioWithOptions(WithRiskPolicy(policy))
	if err := p.SetBalance("USDT", 100000); err != nil {
		t.Fatalf("set balance failed: %v", err)
	}

	allowed := Transaction{
		ID:        "btc-1",
		Symbol:    "BTC/USDT",
		Type:      "buy",
		Quantity:  3,
		Price:     25000,
		Timestamp: time.Now(),
	}
	if err := p.AddTransaction(allowed); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rejected := Transaction{
		ID:        "btc-2",
		Symbol:    "BTC/USDT",
		Type:      "buy",
		Quantity:  3,
		Price:     26000,
		Timestamp: time.Now().Add(time.Minute),
	}
	err := p.AddTransaction(rejected)
	if err == nil {
		t.Fatalf("expected exposure error, got nil")
	}
	if !strings.Contains(err.Error(), "exposure") {
		t.Fatalf("expected exposure context in error: %v", err)
	}
	if got := len(p.GetTransactions()); got != 1 {
		t.Fatalf("exposure rejection should not record transaction, got %d transactions", got)
	}
}

func TestAddTransactionRespectsDailyLossLimit(t *testing.T) {
	policy := &RiskPolicy{
		DailyLossLimit: 50,
	}
	p := NewPortfolioWithOptions(WithRiskPolicy(policy))
	if err := p.SetBalance("USD", 1000); err != nil {
		t.Fatalf("set balance failed: %v", err)
	}

	now := time.Now()
	open := Transaction{
		ID:        "eth-open",
		Symbol:    "ETH/USD",
		Type:      "buy",
		Quantity:  4,
		Price:     100,
		Timestamp: now,
	}
	if err := p.AddTransaction(open); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	firstLoss := Transaction{
		ID:        "eth-loss-1",
		Symbol:    "ETH/USD",
		Type:      "sell",
		Quantity:  1,
		Price:     80,
		Timestamp: now.Add(time.Minute),
	}
	if err := p.AddTransaction(firstLoss); err != nil {
		t.Fatalf("unexpected error closing small loss: %v", err)
	}

	secondLoss := Transaction{
		ID:        "eth-loss-2",
		Symbol:    "ETH/USD",
		Type:      "sell",
		Quantity:  1,
		Price:     30,
		Timestamp: now.Add(2 * time.Minute),
	}
	err := p.AddTransaction(secondLoss)
	if err == nil {
		t.Fatalf("expected daily loss limit error")
	}
	if !strings.Contains(err.Error(), "daily losses") {
		t.Fatalf("expected daily loss context in error: %v", err)
	}
	pos, ok := p.GetPosition("ETH/USD")
	if !ok {
		t.Fatalf("position should remain after rejected trade")
	}
	if pos.Quantity != 3 {
		t.Fatalf("position quantity changed after rejection: got %v want 3", pos.Quantity)
	}
}

func TestWithPairParserAllowsCustomFormats(t *testing.T) {
	parser := func(symbol string) (string, string, bool) {
		parts := strings.Split(symbol, "-")
		if len(parts) != 2 {
			return "", "", false
		}
		return parts[0], parts[1], true
	}

	p := NewPortfolioWithOptions(WithPairParser(parser))
	if err := p.SetBalance("USDT", 1000); err != nil {
		t.Fatalf("set balance failed: %v", err)
	}

	tx := Transaction{
		ID:        "tx-custom",
		Symbol:    "BTC-USDT",
		Type:      "buy",
		Quantity:  1.5,
		Price:     20000,
		Fee:       10,
		Timestamp: time.Now(),
	}

	if err := p.AddTransaction(tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := p.GetBalance("BTC"); got != tx.Quantity {
		t.Fatalf("balance for BTC = %v, want %v", got, tx.Quantity)
	}
}

func TestBalancesSnapshotReturnsDTOs(t *testing.T) {
	p := NewPortfolio()
	if err := p.SetBalance("USDT", 100); err != nil {
		t.Fatalf("set balance failed: %v", err)
	}
	if err := p.SetBalance("BTC", 2); err != nil {
		t.Fatalf("set balance failed: %v", err)
	}

	snapshots := p.BalancesSnapshot()
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 balances, got %d", len(snapshots))
	}

	m := make(map[string]float64)
	for _, snap := range snapshots {
		m[snap.Asset] = snap.Amount
	}

	if m["USDT"] != 100 || m["BTC"] != 2 {
		t.Fatalf("unexpected snapshot values: %#v", snapshots)
	}

	snapshots[0].Amount = 0
	if p.GetBalance(snapshots[0].Asset) == 0 {
		t.Fatalf("mutation of snapshot should not affect internal state")
	}
}

func TestPositionsSnapshotReturnsDTOs(t *testing.T) {
	p := NewPortfolio()
	now := time.Now()
	pos := Position{
		Symbol:        "ETH/USDT",
		EntryPrice:    1500,
		CurrentPrice:  1600,
		Quantity:      3,
		OpenTime:      now,
		LastUpdated:   now,
		RealizedPnL:   50,
		UnrealizedPnL: 100,
	}
	if err := p.UpdatePosition(pos); err != nil {
		t.Fatalf("update position failed: %v", err)
	}

	snapshots := p.PositionsSnapshot()
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 position, got %d", len(snapshots))
	}

	snap := snapshots[0]
	if snap.Symbol != pos.Symbol || snap.Quantity != pos.Quantity || snap.CurrentPrice != pos.CurrentPrice {
		t.Fatalf("snapshot mismatch: %+v vs %+v", snap, pos)
	}

	snapshots[0].Quantity = 0
	stored, _ := p.GetPosition(pos.Symbol)
	if stored.Quantity == 0 {
		t.Fatalf("mutation of snapshot should not affect stored positions")
	}
}

func TestFeeScheduleAppliedToTransactions(t *testing.T) {
	schedule := &FeeSchedule{
		Symbols: map[string]float64{
			"BTC/USDT": 15,
		},
	}
	p := NewPortfolioWithOptions(WithFeeSchedule(schedule))
	if err := p.SetBalance("USDT", 50000); err != nil {
		t.Fatalf("set balance failed: %v", err)
	}

	tx := Transaction{
		ID:        "fee-test",
		Symbol:    "BTC/USDT",
		Type:      "buy",
		Quantity:  2,
		Price:     10000,
		Timestamp: time.Now(),
	}

	if err := p.AddTransaction(tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	recorded := p.GetTransactions()
	if len(recorded) != 1 {
		t.Fatalf("expected 1 tx, got %d", len(recorded))
	}
	if recorded[0].Fee != 15 {
		t.Fatalf("expected fee 15, got %v", recorded[0].Fee)
	}
}

func TestJSONStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONFileStore(filepath.Join(dir, "snapshot.json"))

	schedule := &FeeSchedule{
		Symbols: map[string]float64{
			"CFX/USDT": 1,
		},
	}

	writer := NewPortfolioWithOptions(
		WithSnapshotStore(store),
		WithFeeSchedule(schedule),
	)
	if err := writer.SetBalance("USDT", 1000); err != nil {
		t.Fatalf("set balance failed: %v", err)
	}
	tx := Transaction{
		ID:        "persist",
		Symbol:    "CFX/USDT",
		Type:      "buy",
		Quantity:  10,
		Price:     5,
		Timestamp: time.Now(),
	}
	if err := writer.AddTransaction(tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reader := NewPortfolioWithOptions(WithSnapshotStore(store))
	if err := reader.LoadFromStore(); err != nil {
		t.Fatalf("load from store failed: %v", err)
	}

	if got := reader.GetBalance("CFX"); got != 10 {
		t.Fatalf("CFX balance mismatch: got %v want 10", got)
	}
	if got := len(reader.GetTransactions()); got != 1 {
		t.Fatalf("expected 1 transaction, got %d", got)
	}
}

func TestSubscribeReceivesEvents(t *testing.T) {
	p := NewPortfolio()
	events, cancel := p.Subscribe(1)

	if err := p.SetBalance("USDT", 25); err != nil {
		t.Fatalf("set balance failed: %v", err)
	}

	select {
	case evt := <-events:
		if evt.Type != EventBalanceChanged {
			t.Fatalf("unexpected event type %s", evt.Type)
		}
		if evt.Balance == nil || evt.Balance.Asset != "USDT" {
			t.Fatalf("balance payload missing or incorrect: %+v", evt.Balance)
		}
	case <-time.After(time.Second):
		t.Fatalf("did not receive balance event")
	}

	cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatalf("subscriber channel should be closed after cancel")
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for subscriber channel to close")
	}
}

func TestSubscribeEmitsSnapshotDiffs(t *testing.T) {
	p := NewPortfolio()
	events, cancel := p.Subscribe(4)
	t.Cleanup(cancel)

	wait := func() PortfolioEvent {
		select {
		case evt := <-events:
			return evt
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event")
		}
		return PortfolioEvent{}
	}

	if err := p.SetBalance("USDT", 250); err != nil {
		t.Fatalf("set balance failed: %v", err)
	}
	balEvt := wait()
	if balEvt.Diff == nil {
		t.Fatalf("expected diff on balance event")
	}
	if len(balEvt.Diff.Balances) != 1 {
		t.Fatalf("expected single balance diff, got %d", len(balEvt.Diff.Balances))
	}
	change := balEvt.Diff.Balances[0]
	if change.Asset != "USDT" || change.Deleted || change.Amount != 250 {
		t.Fatalf("unexpected balance diff payload: %+v", change)
	}

	tx := Transaction{
		ID:        "diff-tx",
		Symbol:    "BTC/USDT",
		Type:      "buy",
		Quantity:  1,
		Price:     100,
		Timestamp: time.Now(),
	}
	if err := p.AddTransaction(tx); err != nil {
		t.Fatalf("add transaction failed: %v", err)
	}
	txEvt := wait()
	if txEvt.Type != EventTransactionAdded {
		t.Fatalf("unexpected event type %s", txEvt.Type)
	}
	if txEvt.Diff == nil {
		t.Fatalf("expected diff on transaction event")
	}
	if txEvt.Diff.TransactionsReset {
		t.Fatalf("transactions should not be marked as reset for append-only update")
	}
	if len(txEvt.Diff.TransactionsAppended) != 1 {
		t.Fatalf("expected appended transaction, got %d", len(txEvt.Diff.TransactionsAppended))
	}
	if txEvt.Diff.TransactionsAppended[0].ID != tx.ID {
		t.Fatalf("appended transaction mismatch: %+v", txEvt.Diff.TransactionsAppended[0])
	}
	if len(txEvt.Diff.Balances) == 0 {
		t.Fatalf("transaction diff should include balance changes")
	}

	if err := p.ClosePosition(tx.Symbol); err != nil {
		t.Fatalf("close position failed: %v", err)
	}
	posEvt := wait()
	if posEvt.Type != EventPositionChanged {
		t.Fatalf("unexpected event type %s", posEvt.Type)
	}
	if posEvt.Diff == nil || len(posEvt.Diff.Positions) != 1 {
		t.Fatalf("expected single position diff on close, got %#v", posEvt.Diff)
	}
	posChange := posEvt.Diff.Positions[0]
	if posChange.Symbol != tx.Symbol || !posChange.Deleted {
		t.Fatalf("position diff should mark deletion, got %+v", posChange)
	}
}
