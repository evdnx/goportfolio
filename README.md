# goportfolio

`goportfolio` is a small, thread-safe helper for tracking balances, open positions, transactions, and mark-to-market values in trading tools or bots. It focuses on readability and a minimal surface area so it can be dropped into prototypes without ceremony.

## Features
- Gorilla-safe data structure: wraps every balance/position mutation with `sync.RWMutex` to keep concurrent readers/writers happy.
- Basic position accounting: create, update, close, and inspect positions with realized/unrealized PnL fields for downstream consumers.
- Transaction ledger: append-only slice that can be copied out for reporting or persistence layers.
- Balance/position DTO snapshots: `BalancesSnapshot` and `PositionsSnapshot` hand you serialization-ready slices without exposing internal maps.
- Configurable pair parsing: pass `WithPairParser` to `NewPortfolioWithOptions` to support venues that do not use the default `BASE/QUOTE` slash format.
- Context-aware accounting: `AddTransaction` returns useful errors when a transaction cannot be reconciled into balances.
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

	p := goportfolio.NewPortfolioWithOptions(
		goportfolio.WithPairParser(customParser),
	)
	p.SetBalance("USDT", 1000)

	if err := p.AddTransaction(goportfolio.Transaction{
		ID:        "tx1",
		Symbol:    "CFX-USDT",
		Type:      "buy",
		Quantity:  10,
		Price:     2.5,
		Fee:       1,
		Timestamp: time.Now(),
	}); err != nil {
		panic(err)
	}

	priceBook := map[string]float64{
		"CFX/USDT": 2.7,
	}
	fmt.Printf("Total value in USDT: %.2f\n", p.GetTotalValue("USDT", priceBook))

	fmt.Printf("Balances DTOs: %+v\n", p.BalancesSnapshot())
	fmt.Printf("Positions DTOs: %+v\n", p.PositionsSnapshot())
}
```

## Testing
The project ships with lightweight unit tests that cover transaction handling edge cases. Run them with:
```bash
go test ./...
```

## Roadmap Ideas
These are intentionally out of scope for the current release, but would make natural extensions:
1. Optional persistence hooks (JSON, BoltDB, sqlite) fed by the DTO snapshots.
2. Fee configuration per asset/venue instead of per-transaction overrides.
3. Streaming callbacks or channels that notify listeners on portfolio mutations.

## License
MIT-0
