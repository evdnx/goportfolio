package goportfolio

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Portfolio manages the trading portfolio
type Portfolio struct {
	balances     map[string]float64
	positions    map[string]Position
	transactions []Transaction
	mu           sync.RWMutex
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
	return &Portfolio{
		balances:     make(map[string]float64),
		positions:    make(map[string]Position),
		transactions: []Transaction{},
	}
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

// AddTransaction adds a transaction to the portfolio
func (p *Portfolio) AddTransaction(transaction Transaction) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.transactions = append(p.transactions, transaction)
	baseAsset, quoteAsset, hasPair := splitSymbol(transaction.Symbol)

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

// splitSymbol extracts base and quote assets from a symbol formatted as BASE/QUOTE.
func splitSymbol(symbol string) (string, string, bool) {
	base, quote, ok := strings.Cut(symbol, "/")
	if !ok || base == "" || quote == "" {
		return "", "", false
	}
	return base, quote, true
}
