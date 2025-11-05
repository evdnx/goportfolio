package goportfolio

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"go.etcd.io/bbolt"
)

// PairParser resolves a trading symbol (e.g. "BTC/USDT") into its base
// and quote assets. Implementations should return ok=false if they cannot
// derive a valid pair so callers can surface an error.
type PairParser func(symbol string) (baseAsset, quoteAsset string, ok bool)

// FeeSchedule describes per-symbol/per-asset fee settings.
type FeeSchedule struct {
	Symbols    map[string]float64
	Bases      map[string]float64
	Quotes     map[string]float64
	Default    float64
	HasDefault bool
}

// clone creates a deep copy so callers can mutate their own schedule safely.
func (fs *FeeSchedule) clone() *FeeSchedule {
	if fs == nil {
		return nil
	}
	copyMap := func(src map[string]float64) map[string]float64 {
		if len(src) == 0 {
			return nil
		}
		dst := make(map[string]float64, len(src))
		for k, v := range src {
			dst[k] = v
		}
		return dst
	}
	return &FeeSchedule{
		Symbols:    copyMap(fs.Symbols),
		Bases:      copyMap(fs.Bases),
		Quotes:     copyMap(fs.Quotes),
		Default:    fs.Default,
		HasDefault: fs.HasDefault,
	}
}

// Resolve attempts to find a fee for the provided symbol/base/quote tuple.
func (fs *FeeSchedule) Resolve(symbol, base, quote string) (float64, bool) {
	if fs == nil {
		return 0, false
	}
	if fee, ok := fs.Symbols[symbol]; ok {
		return fee, true
	}
	if base != "" {
		if fee, ok := fs.Bases[base]; ok {
			return fee, true
		}
	}
	if quote != "" {
		if fee, ok := fs.Quotes[quote]; ok {
			return fee, true
		}
	}
	if fs.HasDefault {
		return fs.Default, true
	}
	return 0, false
}

// SnapshotStore persists and loads portfolio snapshots.
type SnapshotStore interface {
	Save(snapshot PortfolioSnapshot) error
	Load() (PortfolioSnapshot, error)
}

// PortfolioSnapshot represents a serializable copy of the portfolio.
type PortfolioSnapshot struct {
	Balances     []BalanceSnapshot  `json:"balances"`
	Positions    []PositionSnapshot `json:"positions"`
	Transactions []Transaction      `json:"transactions"`
}

// EventType identifies the kind of portfolio mutation.
type EventType string

const (
	EventBalanceChanged    EventType = "balance_changed"
	EventPositionChanged   EventType = "position_changed"
	EventTransactionAdded  EventType = "transaction_added"
	EventPortfolioRestored EventType = "portfolio_restored"
)

// PortfolioEvent is sent to subscribers whenever the portfolio mutates.
type PortfolioEvent struct {
	Type        EventType          `json:"type"`
	Balance     *BalanceSnapshot   `json:"balance,omitempty"`
	Position    *PositionSnapshot  `json:"position,omitempty"`
	Transaction *Transaction       `json:"transaction,omitempty"`
	Snapshot    *PortfolioSnapshot `json:"snapshot,omitempty"`
	Timestamp   time.Time          `json:"timestamp"`
	Message     string             `json:"message,omitempty"`
}

// Option configures a Portfolio instance.
type Option func(*Portfolio)

// Portfolio manages the trading portfolio
type Portfolio struct {
	balances         map[string]float64
	positions        map[string]Position
	transactions     []Transaction
	mu               sync.RWMutex
	pairParser       PairParser
	feeSchedule      *FeeSchedule
	snapshotStore    SnapshotStore
	subscribers      map[int]chan PortfolioEvent
	nextSubscriberID int
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
	Fee       float64 // optional; resolved via fee schedule when zero
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
		subscribers:  make(map[int]chan PortfolioEvent),
	}

	for _, opt := range options {
		opt(p)
	}

	if p.pairParser == nil {
		p.pairParser = defaultPairParser
	}
	if p.feeSchedule != nil {
		p.feeSchedule = p.feeSchedule.clone()
	}

	return p
}

// WithPairParser overrides the default BASE/QUOTE parser used for balance updates.
func WithPairParser(parser PairParser) Option {
	return func(p *Portfolio) {
		p.pairParser = parser
	}
}

// WithFeeSchedule configures the portfolio with a fee schedule.
func WithFeeSchedule(schedule *FeeSchedule) Option {
	return func(p *Portfolio) {
		if schedule == nil {
			p.feeSchedule = nil
			return
		}
		p.feeSchedule = schedule.clone()
	}
}

// WithSnapshotStore wires a persistence layer that receives snapshots after every mutation.
func WithSnapshotStore(store SnapshotStore) Option {
	return func(p *Portfolio) {
		p.snapshotStore = store
	}
}

// SetFeeSchedule replaces the existing fee schedule at runtime.
func (p *Portfolio) SetFeeSchedule(schedule *FeeSchedule) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if schedule == nil {
		p.feeSchedule = nil
		return
	}
	p.feeSchedule = schedule.clone()
}

// SetDefaultFee updates the default fee applied when no symbol/base/quote match is found.
func (p *Portfolio) SetDefaultFee(fee float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureFeeSchedule()
	p.feeSchedule.Default = fee
	p.feeSchedule.HasDefault = true
}

// SetFeeForSymbol wires a fixed fee for a specific trading symbol.
func (p *Portfolio) SetFeeForSymbol(symbol string, fee float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureFeeSchedule()
	p.feeSchedule.Symbols[symbol] = fee
}

// SetFeeForBase wires a fee that is applied to any symbol with the provided base asset.
func (p *Portfolio) SetFeeForBase(base string, fee float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureFeeSchedule()
	p.feeSchedule.Bases[base] = fee
}

// SetFeeForQuote wires a fee that is applied to any symbol with the provided quote asset.
func (p *Portfolio) SetFeeForQuote(quote string, fee float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureFeeSchedule()
	p.feeSchedule.Quotes[quote] = fee
}

func (p *Portfolio) ensureFeeSchedule() {
	if p.feeSchedule == nil {
		p.feeSchedule = &FeeSchedule{
			Symbols: make(map[string]float64),
			Bases:   make(map[string]float64),
			Quotes:  make(map[string]float64),
		}
		return
	}
	if p.feeSchedule.Symbols == nil {
		p.feeSchedule.Symbols = make(map[string]float64)
	}
	if p.feeSchedule.Bases == nil {
		p.feeSchedule.Bases = make(map[string]float64)
	}
	if p.feeSchedule.Quotes == nil {
		p.feeSchedule.Quotes = make(map[string]float64)
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

// SetBalance sets the balance of an asset and persists/emits events if configured.
func (p *Portfolio) SetBalance(asset string, amount float64) error {
	p.mu.Lock()
	p.balances[asset] = amount
	balanceSnap := BalanceSnapshot{Asset: asset, Amount: amount}
	snapshot := p.snapshotLocked()
	p.mu.Unlock()

	if err := p.persistSnapshot(snapshot); err != nil {
		return err
	}
	p.publish(PortfolioEvent{
		Type:      EventBalanceChanged,
		Balance:   &balanceSnap,
		Timestamp: time.Now(),
		Message:   fmt.Sprintf("balance for %s set to %f", asset, amount),
	})
	return nil
}

// GetPosition returns a position by symbol
func (p *Portfolio) GetPosition(symbol string) (Position, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	position, exists := p.positions[symbol]
	return position, exists
}

// UpdatePosition upserts a position and notifies subscribers.
func (p *Portfolio) UpdatePosition(position Position) error {
	p.mu.Lock()
	p.positions[position.Symbol] = position
	positionSnap := toPositionSnapshot(position)
	snapshot := p.snapshotLocked()
	p.mu.Unlock()

	if err := p.persistSnapshot(snapshot); err != nil {
		return err
	}
	p.publish(PortfolioEvent{
		Type:      EventPositionChanged,
		Position:  &positionSnap,
		Timestamp: time.Now(),
		Message:   fmt.Sprintf("position %s updated", position.Symbol),
	})
	return nil
}

// ClosePosition closes a position
func (p *Portfolio) ClosePosition(symbol string) error {
	p.mu.Lock()
	position, exists := p.positions[symbol]
	if !exists {
		p.mu.Unlock()
		return fmt.Errorf("position for %s does not exist", symbol)
	}

	delete(p.positions, symbol)
	position.Quantity = 0
	position.LastUpdated = time.Now()
	snapshot := p.snapshotLocked()
	positionSnap := toPositionSnapshot(position)
	p.mu.Unlock()

	if err := p.persistSnapshot(snapshot); err != nil {
		return err
	}
	p.publish(PortfolioEvent{
		Type:      EventPositionChanged,
		Position:  &positionSnap,
		Timestamp: time.Now(),
		Message:   fmt.Sprintf("position %s closed", symbol),
	})
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

	if requiresBalances {
		fee, err := p.resolveFee(transaction, baseAsset, quoteAsset)
		if err != nil {
			return err
		}
		transaction.Fee = fee
	}

	p.mu.Lock()
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

	snapshot := p.snapshotLocked()
	p.mu.Unlock()

	if err := p.persistSnapshot(snapshot); err != nil {
		return err
	}
	txCopy := transaction
	p.publish(PortfolioEvent{
		Type:        EventTransactionAdded,
		Transaction: &txCopy,
		Timestamp:   time.Now(),
		Message:     fmt.Sprintf("transaction %s recorded", transaction.ID),
	})

	return nil
}

func (p *Portfolio) resolveFee(transaction Transaction, baseAsset, quoteAsset string) (float64, error) {
	if transaction.Fee > 0 {
		return transaction.Fee, nil
	}
	if p.feeSchedule == nil {
		return 0, nil
	}
	if fee, ok := p.feeSchedule.Resolve(transaction.Symbol, baseAsset, quoteAsset); ok {
		return fee, nil
	}
	return 0, fmt.Errorf("no fee configured for %s and no fee provided", transaction.Symbol)
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

func toPositionSnapshot(position Position) PositionSnapshot {
	return PositionSnapshot{
		Symbol:        position.Symbol,
		EntryPrice:    position.EntryPrice,
		CurrentPrice:  position.CurrentPrice,
		Quantity:      position.Quantity,
		OpenTime:      position.OpenTime,
		LastUpdated:   position.LastUpdated,
		RealizedPnL:   position.RealizedPnL,
		UnrealizedPnL: position.UnrealizedPnL,
	}
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
		snapshots = append(snapshots, toPositionSnapshot(position))
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

// Snapshot returns a deep copy of the portfolio state.
func (p *Portfolio) Snapshot() PortfolioSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snapshotLocked()
}

// Restore replaces the current in-memory state with the provided snapshot.
func (p *Portfolio) Restore(snapshot PortfolioSnapshot) {
	p.mu.Lock()
	p.restoreLocked(snapshot)
	p.mu.Unlock()

	snapCopy := cloneSnapshot(snapshot)
	p.publish(PortfolioEvent{
		Type:      EventPortfolioRestored,
		Snapshot:  &snapCopy,
		Timestamp: time.Now(),
		Message:   "portfolio restored from snapshot",
	})
}

// LoadFromStore pulls the most recent snapshot from the configured store.
func (p *Portfolio) LoadFromStore() error {
	if p.snapshotStore == nil {
		return errors.New("snapshot store is not configured")
	}
	snapshot, err := p.snapshotStore.Load()
	if err != nil {
		return err
	}
	p.Restore(snapshot)
	return nil
}

// Subscribe returns a channel for streaming portfolio events and an unsubscribe function.
func (p *Portfolio) Subscribe(buffer int) (<-chan PortfolioEvent, func()) {
	if buffer < 0 {
		buffer = 0
	}
	ch := make(chan PortfolioEvent, buffer)

	p.mu.Lock()
	id := p.nextSubscriberID
	p.nextSubscriberID++
	p.subscribers[id] = ch
	p.mu.Unlock()

	unsubscribe := func() {
		p.mu.Lock()
		if subscriber, ok := p.subscribers[id]; ok {
			delete(p.subscribers, id)
			close(subscriber)
		}
		p.mu.Unlock()
	}

	return ch, unsubscribe
}

func (p *Portfolio) publish(event PortfolioEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	p.mu.RLock()
	subs := make([]chan PortfolioEvent, 0, len(p.subscribers))
	for _, sub := range p.subscribers {
		subs = append(subs, sub)
	}
	p.mu.RUnlock()

	for _, sub := range subs {
		select {
		case sub <- event:
		default:
		}
	}
}

func (p *Portfolio) snapshotLocked() PortfolioSnapshot {
	balances := make([]BalanceSnapshot, 0, len(p.balances))
	for asset, amount := range p.balances {
		balances = append(balances, BalanceSnapshot{
			Asset:  asset,
			Amount: amount,
		})
	}

	positions := make([]PositionSnapshot, 0, len(p.positions))
	for _, position := range p.positions {
		positions = append(positions, toPositionSnapshot(position))
	}

	transactions := make([]Transaction, len(p.transactions))
	copy(transactions, p.transactions)

	return PortfolioSnapshot{
		Balances:     balances,
		Positions:    positions,
		Transactions: transactions,
	}
}

func (p *Portfolio) persistSnapshot(snapshot PortfolioSnapshot) error {
	if p.snapshotStore == nil {
		return nil
	}
	return p.snapshotStore.Save(snapshot)
}

func (p *Portfolio) restoreLocked(snapshot PortfolioSnapshot) {
	p.balances = make(map[string]float64, len(snapshot.Balances))
	for _, b := range snapshot.Balances {
		p.balances[b.Asset] = b.Amount
	}

	p.positions = make(map[string]Position, len(snapshot.Positions))
	for _, position := range snapshot.Positions {
		p.positions[position.Symbol] = Position{
			Symbol:        position.Symbol,
			EntryPrice:    position.EntryPrice,
			CurrentPrice:  position.CurrentPrice,
			Quantity:      position.Quantity,
			OpenTime:      position.OpenTime,
			LastUpdated:   position.LastUpdated,
			RealizedPnL:   position.RealizedPnL,
			UnrealizedPnL: position.UnrealizedPnL,
		}
	}

	p.transactions = make([]Transaction, len(snapshot.Transactions))
	copy(p.transactions, snapshot.Transactions)
}

func cloneSnapshot(snapshot PortfolioSnapshot) PortfolioSnapshot {
	clone := PortfolioSnapshot{
		Balances:     make([]BalanceSnapshot, len(snapshot.Balances)),
		Positions:    make([]PositionSnapshot, len(snapshot.Positions)),
		Transactions: make([]Transaction, len(snapshot.Transactions)),
	}
	copy(clone.Balances, snapshot.Balances)
	copy(clone.Positions, snapshot.Positions)
	copy(clone.Transactions, snapshot.Transactions)
	return clone
}

// JSONFileStore persists snapshots to a local JSON file.
type JSONFileStore struct {
	Path string
}

// NewJSONFileStore creates a JSON-backed snapshot store.
func NewJSONFileStore(path string) *JSONFileStore {
	return &JSONFileStore{Path: path}
}

// Save writes the snapshot to disk using an atomic rename.
func (s *JSONFileStore) Save(snapshot PortfolioSnapshot) error {
	if s == nil || s.Path == "" {
		return errors.New("json store path is empty")
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := s.Path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.Path)
}

// Load reads the snapshot from disk. Missing files return the zero snapshot.
func (s *JSONFileStore) Load() (PortfolioSnapshot, error) {
	var snapshot PortfolioSnapshot
	if s == nil || s.Path == "" {
		return snapshot, errors.New("json store path is empty")
	}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

const boltBucket = "goportfolio"
const boltKey = "snapshot"

// BoltDBStore persists snapshots inside a BoltDB bucket.
type BoltDBStore struct {
	db *bbolt.DB
}

// NewBoltDBStore opens a BoltDB database at the supplied path.
func NewBoltDBStore(path string) (*BoltDBStore, error) {
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, err
	}
	return &BoltDBStore{db: db}, nil
}

// Save writes the snapshot bytes into BoltDB.
func (s *BoltDBStore) Save(snapshot PortfolioSnapshot) error {
	if s == nil || s.db == nil {
		return errors.New("boltdb store is not initialized")
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(boltBucket))
		if err != nil {
			return err
		}
		return bucket.Put([]byte(boltKey), data)
	})
}

// Load fetches the snapshot bytes from BoltDB.
func (s *BoltDBStore) Load() (PortfolioSnapshot, error) {
	var snapshot PortfolioSnapshot
	if s == nil || s.db == nil {
		return snapshot, errors.New("boltdb store is not initialized")
	}
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(boltBucket))
		if bucket == nil {
			return nil
		}
		data := bucket.Get([]byte(boltKey))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &snapshot)
	})
	return snapshot, err
}

// Close releases the underlying BoltDB handle.
func (s *BoltDBStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// SQLiteStore persists snapshots to an embedded SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens/creates an on-disk SQLite database at path.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS portfolio_snapshot (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		payload BLOB NOT NULL
	)`); err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// Save writes the snapshot into SQLite using an UPSERT.
func (s *SQLiteStore) Save(snapshot PortfolioSnapshot) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite store is not initialized")
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO portfolio_snapshot (id, payload)
		VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET payload = excluded.payload`, data)
	return err
}

// Load fetches the snapshot from SQLite.
func (s *SQLiteStore) Load() (PortfolioSnapshot, error) {
	var snapshot PortfolioSnapshot
	if s == nil || s.db == nil {
		return snapshot, errors.New("sqlite store is not initialized")
	}
	var payload []byte
	err := s.db.QueryRow(`SELECT payload FROM portfolio_snapshot WHERE id = 1`).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

// Close releases the SQLite database handle.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
