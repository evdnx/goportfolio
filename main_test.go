package goportfolio

import (
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

	p.AddTransaction(tx)

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

	p.AddTransaction(tx)

	if got := p.GetBalance("USD"); got != 50 {
		t.Fatalf("balance for USD changed unexpectedly: got %v want %v", got, 50)
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if got := len(p.balances); got != 1 {
		t.Fatalf("unexpected balances entries, got %d want 1", got)
	}
}
