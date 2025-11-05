package goportfolio

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// PairParser resolves a trading symbol (e.g. "BTC/USDT") into its base
// and quote assets. Implementations should return ok=false if they cannot
// derive a valid pair so callers can surface an error.
type PairParser func(symbol string) (baseAsset, quoteAsset string, ok bool)

// Option configures a Portfolio instance.
type Option func(*Portfolio)

// Portfolio manages the trading portfolio
type Portfolio struct {
	balances     map[string]float64
	positions    map[string]Position
	transactions []Transaction
	mu           sync.RWMutex
	pairParser   PairParser
}

// Position represents a trading position
type Position struct {
	Symbol        string
	EntryPrice    float64
	CurrentPrice  float64
	Quantity      float64
	OpenTime      time.Time
	LastUpdated   time.Time
	RealizedPnL   float64
	UnrealizedPnL float64
}

// Transaction represents a portfolio transaction
type Transaction struct {
	ID        string
	Symbol    string
	Type      string // "buy", "sell"
	Quantity  float64
	Price     float64
	Fee       float64
	Timestamp time.Time
}

// NewPortfolio creates a new portfolio
func NewPortfolio() *Portfolio {
	return NewPortfolioWithOptions()
}

// NewPortfolioWithOptions creates a portfolio configured by the supplied options.
func NewPortfolioWithOptions(options ...Option) *Portfolio {
	p := &Portfolio{
		balances:     make(map[string]float64),
		positions:    make(map[string]Position),
		transactions: []Transaction{},
		pairParser:   defaultPairParser,
	}

	for _, opt := range options {
		opt(p)
	}

	if p.pairParser == nil {
		p.pairParser = defaultPairParser
	}

	return p
}

// WithPairParser overrides the default BASE/QUOTE parser used for balance updates.
func WithPairParser(parser PairParser) Option {
	return func(p *Portfolio) {
		p.pairParser = parser
	}
}

// BalanceSnapshot is a DTO representing a copy of an asset balance.
type BalanceSnapshot struct {
	Asset  string  `json:"asset"`
	Amount float64 `json:"amount"`
}

// PositionSnapshot is a DTO mirroring Position but intended for serialization.
type PositionSnapshot struct {
	Symbol        string    `json:"symbol"`
	EntryPrice    float64   `json:"entryPrice"`
	CurrentPrice  float64   `json:"currentPrice"`
	Quantity      float64   `json:"quantity"`
	OpenTime      time.Time `json:"openTime"`
	LastUpdated   time.Time `json:"lastUpdated"`
	RealizedPnL   float64   `json:"realizedPnl"`
	UnrealizedPnL float64   `json:"unrealizedPnl"`
}

// GetBalance returns the balance of an asset
func (p *Portfolio) GetBalance(asset string) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.balances[asset]
}

// SetBalance sets the balance of an asset
func (p *Portfolio) SetBalance(asset string, amount float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.balances[asset] = amount
}

// GetPosition returns a position by symbol
func (p *Portfolio) GetPosition(symbol string) (Position, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	position, exists := p.positions[symbol]
	return position, exists
}

// UpdatePosition updates a position
func (p *Portfolio) UpdatePosition(position Position) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.positions[position.Symbol] = position
}

// ClosePosition closes a position
func (p *Portfolio) ClosePosition(symbol string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.positions[symbol]; !exists {
		return fmt.Errorf("position for %s does not exist", symbol)
	}

	delete(p.positions, symbol)
	return nil
}

// AddTransaction adds a transaction to the portfolio and updates balances/positions.
func (p *Portfolio) AddTransaction(transaction Transaction) error {
	parser := p.pairParser
	if parser == nil {
		parser = defaultPairParser
		p.pairParser = parser
	}

	baseAsset, quoteAsset, hasPair := parser(transaction.Symbol)
	requiresBalances := transaction.Type == "buy" || transaction.Type == "sell"
	if requiresBalances && !hasPair {
		return fmt.Errorf("cannot update balances for %s: unrecognized symbol format", transaction.Symbol)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.transactions = append(p.transactions, transaction)

	// Update balances and positions based on the transaction
	// This is a simplified implementation
	switch transaction.Type {
	case "buy":
		// Update position
		position, exists := p.positions[transaction.Symbol]
		if !exists {
			position = Position{
				Symbol:      transaction.Symbol,
				EntryPrice:  transaction.Price,
				Quantity:    transaction.Quantity,
				OpenTime:    transaction.Timestamp,
				LastUpdated: transaction.Timestamp,
			}
		} else {
			// Update existing position (average down/up)
			totalValue := position.EntryPrice*position.Quantity + transaction.Price*transaction.Quantity
			totalQuantity := position.Quantity + transaction.Quantity
			position.EntryPrice = totalValue / totalQuantity
			position.Quantity = totalQuantity
			position.LastUpdated = transaction.Timestamp
		}
		p.positions[transaction.Symbol] = position

		if hasPair {
			p.balances[baseAsset] += transaction.Quantity
			p.balances[quoteAsset] -= transaction.Quantity*transaction.Price + transaction.Fee
		}

	case "sell":
		// Update position
		position, exists := p.positions[transaction.Symbol]
		if !exists {
			// Selling without a position - this could be a short position
			// For simplicity, we'll just create a negative position
			position = Position{
				Symbol:      transaction.Symbol,
				EntryPrice:  transaction.Price,
				Quantity:    -transaction.Quantity,
				OpenTime:    transaction.Timestamp,
				LastUpdated: transaction.Timestamp,
			}
		} else {
			// Update existing position
			if position.Quantity <= transaction.Quantity {
				// Position is fully closed
				realizedPnL := (transaction.Price - position.EntryPrice) * position.Quantity
				position.RealizedPnL += realizedPnL
				position.Quantity = 0
				// We'll keep the position in the map with zero quantity for PnL tracking
			} else {
				// Position is partially closed
				realizedPnL := (transaction.Price - position.EntryPrice) * transaction.Quantity
				position.RealizedPnL += realizedPnL
				position.Quantity -= transaction.Quantity
			}
			position.LastUpdated = transaction.Timestamp
		}
		p.positions[transaction.Symbol] = position

		if hasPair {
			p.balances[baseAsset] -= transaction.Quantity
			p.balances[quoteAsset] += transaction.Quantity*transaction.Price - transaction.Fee
		}
	}

	return nil
}

// GetTransactions returns all transactions
func (p *Portfolio) GetTransactions() []Transaction {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Return a copy to avoid race conditions
	transactions := make([]Transaction, len(p.transactions))
	copy(transactions, p.transactions)

	return transactions
}

// GetTotalValue returns the total portfolio value in the specified quote asset
func (p *Portfolio) GetTotalValue(quoteAsset string, prices map[string]float64) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	totalValue := p.balances[quoteAsset]

	for asset, balance := range p.balances {
		if asset == quoteAsset {
			continue
		}

		// Get the price of the asset in the quote asset
		symbol := fmt.Sprintf("%s/%s", asset, quoteAsset)
		price, exists := prices[symbol]
		if !exists {
			// Try reverse symbol
			symbol = fmt.Sprintf("%s/%s", quoteAsset, asset)
			price, exists = prices[symbol]
			if !exists {
				continue
			}
			price = 1 / price
		}

		totalValue += balance * price
	}

	return totalValue
}

// GetAllPositions returns all positions
func (p *Portfolio) GetAllPositions() map[string]Position {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Return a copy to avoid race conditions
	positions := make(map[string]Position)
	for symbol, position := range p.positions {
		positions[symbol] = position
	}

	return positions
}

// BalancesSnapshot returns a slice of balances safe for serialization.
func (p *Portfolio) BalancesSnapshot() []BalanceSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()

	snapshots := make([]BalanceSnapshot, 0, len(p.balances))
	for asset, amount := range p.balances {
		snapshots = append(snapshots, BalanceSnapshot{
			Asset:  asset,
			Amount: amount,
		})
	}

	return snapshots
}

// PositionsSnapshot returns a slice of Position DTOs safe for serialization.
func (p *Portfolio) PositionsSnapshot() []PositionSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()

	snapshots := make([]PositionSnapshot, 0, len(p.positions))
	for _, position := range p.positions {
		snapshots = append(snapshots, PositionSnapshot{
			Symbol:        position.Symbol,
			EntryPrice:    position.EntryPrice,
			CurrentPrice:  position.CurrentPrice,
			Quantity:      position.Quantity,
			OpenTime:      position.OpenTime,
			LastUpdated:   position.LastUpdated,
			RealizedPnL:   position.RealizedPnL,
			UnrealizedPnL: position.UnrealizedPnL,
		})
	}

	return snapshots
}

// defaultPairParser extracts base and quote assets from a symbol formatted as BASE/QUOTE.
func defaultPairParser(symbol string) (string, string, bool) {
	base, quote, ok := strings.Cut(symbol, "/")
	if !ok || base == "" || quote == "" {
		return "", "", false
	}
	return base, quote, true
}
