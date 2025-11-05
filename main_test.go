package goportfolio

import (
	"strings"
	"testing"
	"time"
)

func TestAddTransactionHandlesVariableLengthSymbols(t *testing.T) {
	p := NewPortfolio()
	p.SetBalance("USDT", 1000)

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
	p.SetBalance("USD", 50)

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

func TestWithPairParserAllowsCustomFormats(t *testing.T) {
	parser := func(symbol string) (string, string, bool) {
		parts := strings.Split(symbol, "-")
		if len(parts) != 2 {
			return "", "", false
		}
		return parts[0], parts[1], true
	}

	p := NewPortfolioWithOptions(WithPairParser(parser))
	p.SetBalance("USDT", 1000)

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
	p.SetBalance("USDT", 100)
	p.SetBalance("BTC", 2)

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
	p.UpdatePosition(pos)

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
