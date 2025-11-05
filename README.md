# goportfolio

`goportfolio` is a small, thread-safe helper for tracking balances, open positions, transactions, and mark-to-market values in trading tools or bots. It focuses on readability and a minimal surface area so it can be dropped into prototypes without ceremony.

## Features
- Gorilla-safe data structure: wraps every balance/position mutation with `sync.RWMutex` to keep concurrent readers/writers happy.
- Basic position accounting: create, update, close, and inspect positions with realized/unrealized PnL fields for downstream consumers.
- Transaction ledger: append-only slice that can be copied out for reporting or persistence layers.
- Simplified balance logic: uses a slash-delimited pair parser (`BASE/QUOTE`) to update base/quote balances on buys and sells without assuming fixed-length symbols.
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
	"time"

	"github.com/evdnx/goportfolio"
)

func main() {
	p := goportfolio.NewPortfolio()
	p.SetBalance("USDT", 1000)

	p.AddTransaction(goportfolio.Transaction{
		ID:        "tx1",
		Symbol:    "CFX/USDT",
		Type:      "buy",
		Quantity:  10,
		Price:     2.5,
		Fee:       1,
		Timestamp: time.Now(),
	})

	priceBook := map[string]float64{
		"CFX/USDT": 2.7,
	}
	fmt.Printf("Total value in USDT: %.2f\n", p.GetTotalValue("USDT", priceBook))
}
```

## Testing
The project ships with lightweight unit tests that cover transaction handling edge cases. Run them with:
```bash
go test ./...
```

## Roadmap Ideas
These are intentionally out of scope for the initial cut, but would make natural extensions:
1. Expose balance/position snapshots as DTOs for serialization.
2. Allow callers to configure base/quote parsing for venues that do not use `BASE/QUOTE`.
3. Add context-aware errors to `AddTransaction` when balances cannot be updated.

## License
MIT-0
