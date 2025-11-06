# goportfolio

`goportfolio` is a small, thread-safe helper for tracking balances, open positions, transactions, and mark-to-market values in trading tools or bots. It focuses on readability and a minimal surface area so it can be dropped into prototypes without ceremony.

## Features
- Gorilla-safe data structure: wraps every balance/position mutation with `sync.RWMutex` to keep concurrent readers/writers happy.
- Basic position accounting: create, update, close, and inspect positions with realized/unrealized PnL fields for downstream consumers.
- Transaction ledger: append-only slice that can be copied out for reporting or persistence layers.
- Balance/position DTO snapshots: `Snapshot`, `BalancesSnapshot`, and `PositionsSnapshot` hand you serialization-ready copies without exposing internal maps.
- Snapshot diffs: every `PortfolioEvent` carries a `SnapshotDiff` payload so streaming consumers can apply compact balance/position/transaction patches instead of reloading the full state.
- Configurable pair parsing: pass `WithPairParser` to `NewPortfolioWithOptions` to support venues that do not use the default `BASE/QUOTE` slash format.
- Fee schedules: describe fees per symbol/base/quote (or default) once and let the portfolio apply them for every transaction.
- Persistence hooks: plug in JSON, BoltDB, or SQLite stores via `WithSnapshotStore` to automatically persist and rehydrate state between restarts.
- Streaming events: `Subscribe` yields a channel that receives balance/position/transaction events for real-time UIs or alerting.
- Portfolio valuation helper: converts every asset to a chosen quote asset using caller-provided price data and sums the result.

## Getting Started
```bash
git clone https://github.com/evdnx/goportfolio.git
cd goportfolio
go test ./...
```

`go.mod` targets Go 1.25, but the code is free of 1.25-specific features and should work on any maintained Go release.

## Usage
```go
package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/evdnx/goportfolio"
)

func main() {
	customParser := func(symbol string) (string, string, bool) {
		parts := strings.Split(symbol, "-")
		if len(parts) != 2 {
			return "", "", false
		}
		return parts[0], parts[1], true
	}

	feeSchedule := &goportfolio.FeeSchedule{
		Symbols: map[string]float64{
			"CFX/USDT": 1, // flat fee applied automatically
		},
	}

	store := goportfolio.NewJSONFileStore("portfolio.json")

	p := goportfolio.NewPortfolioWithOptions(
		goportfolio.WithPairParser(customParser),
		goportfolio.WithFeeSchedule(feeSchedule),
		goportfolio.WithSnapshotStore(store),
	)
	if err := p.SetBalance("USDT", 1000); err != nil {
		log.Fatalf("seed balance: %v", err)
	}

	if err := p.AddTransaction(goportfolio.Transaction{
		ID:        "tx1",
		Symbol:    "CFX-USDT",
		Type:      "buy",
		Quantity:  10,
		Price:     2.5,
		Timestamp: time.Now(),
	}); err != nil {
		log.Fatalf("add transaction: %v", err)
	}

	priceBook := map[string]float64{
		"CFX/USDT": 2.7,
	}
	fmt.Printf("Total value in USDT: %.2f\n", p.GetTotalValue("USDT", priceBook))

	fmt.Printf("Balances DTOs: %+v\n", p.BalancesSnapshot())
	fmt.Printf("Positions DTOs: %+v\n", p.PositionsSnapshot())

	if err := p.LoadFromStore(); err != nil {
		log.Fatalf("rehydrate: %v", err)
	}

	events, cancel := p.Subscribe(10)
	defer cancel()

	if err := p.SetBalance("USDT", 750); err != nil {
		log.Fatalf("update balance: %v", err)
	}

	select {
	case evt := <-events:
		fmt.Printf("Event: %s %#v\n", evt.Type, evt.Balance)
		fmt.Printf("Diff: %#v\n", evt.Diff)
	case <-time.After(2 * time.Second):
		fmt.Println("no events received")
	}
}
```

Inspect the `SnapshotDiff` on every event to fan out only the balances, positions, or transaction slices that actually changed and ignore the rest of the snapshot payloads.

## Testing
The project ships with lightweight unit tests that cover transaction handling edge cases. Run them with:
```bash
go test ./...
```

## Roadmap Ideas
These are intentionally out of scope for the current release, but would make natural extensions:
1. Risk controls (max exposure, daily loss limits) baked into `AddTransaction` to stop trades that exceed policy.
2. Built-in persistence migrations to evolve stored snapshots without forcing manual wipes.

## License
MIT-0
