package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type User struct {
	ID        int64
	TelegramID int64
	Username  string
	CreatedAt time.Time
}

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrating database: %w", err)
	}

	return &DB{conn: conn}, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

type QuoteRecord struct {
	ID             int64
	Type           string
	Provider       string
	UserID         int64
	FromAsset      string
	FromChain      string
	ToAsset        string
	Destination    string
	InputAmountUSD float64
	InputAmount    string
	ExpectedOutput string
	Memo           string
	Router         string
	VaultAddress   string
	Expiry         int64
	CreatedAt      time.Time
}

type TopupRecord struct {
	ID        int64
	Type      string
	QuoteID   int64
	UserID    int64
	Provider  string
	FromChain string
	TxHash    string
	Status    string
	CreatedAt time.Time
}

func migrate(conn *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			telegram_id INTEGER UNIQUE NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS quotes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL DEFAULT 'fast',
			provider TEXT NOT NULL,
			user_id INTEGER NOT NULL,
			from_asset TEXT NOT NULL,
			from_chain TEXT NOT NULL,
			to_asset TEXT NOT NULL,
			destination TEXT NOT NULL,
			input_amount_usd REAL NOT NULL,
			input_amount TEXT NOT NULL,
			expected_output TEXT NOT NULL,
			memo TEXT NOT NULL,
			router TEXT NOT NULL,
			vault_address TEXT NOT NULL,
			expiry INTEGER NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS topups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL DEFAULT 'fast',
			quote_id INTEGER NOT NULL REFERENCES quotes(id),
			user_id INTEGER NOT NULL,
			provider TEXT NOT NULL,
			from_chain TEXT NOT NULL,
			tx_hash TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(s); err != nil {
			return fmt.Errorf("executing migration: %w", err)
		}
	}
	return nil
}

func (d *DB) InsertQuote(q *QuoteRecord) (int64, error) {
	result, err := d.conn.Exec(
		`INSERT INTO quotes (type, provider, user_id, from_asset, from_chain, to_asset, destination,
			input_amount_usd, input_amount, expected_output, memo, router, vault_address, expiry)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		q.Type, q.Provider, q.UserID, q.FromAsset, q.FromChain, q.ToAsset, q.Destination,
		q.InputAmountUSD, q.InputAmount, q.ExpectedOutput, q.Memo, q.Router, q.VaultAddress, q.Expiry,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting quote: %w", err)
	}
	return result.LastInsertId()
}

func (d *DB) InsertTopup(t *TopupRecord) (int64, error) {
	result, err := d.conn.Exec(
		`INSERT INTO topups (type, quote_id, user_id, provider, from_chain, tx_hash, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.Type, t.QuoteID, t.UserID, t.Provider, t.FromChain, t.TxHash, t.Status,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting topup: %w", err)
	}
	return result.LastInsertId()
}

func (d *DB) GetTopup(id int64) (*TopupRecord, error) {
	var t TopupRecord
	err := d.conn.QueryRow(
		`SELECT id, type, quote_id, user_id, provider, from_chain, tx_hash, status, created_at
		FROM topups WHERE id = ?`, id,
	).Scan(&t.ID, &t.Type, &t.QuoteID, &t.UserID, &t.Provider, &t.FromChain, &t.TxHash, &t.Status, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("getting topup: %w", err)
	}
	return &t, nil
}

func (d *DB) UpdateTopupStatus(id int64, status string) error {
	_, err := d.conn.Exec("UPDATE topups SET status = ? WHERE id = ?", status, id)
	return err
}

// GetOrCreateUser returns the user for a telegram ID, creating one if it doesn't exist.
// The returned User.ID is the row ID used for wallet derivation index.
func (d *DB) GetOrCreateUser(telegramID int64, username string) (*User, error) {
	var user User
	err := d.conn.QueryRow(
		"SELECT id, telegram_id, username, created_at FROM users WHERE telegram_id = ?",
		telegramID,
	).Scan(&user.ID, &user.TelegramID, &user.Username, &user.CreatedAt)

	if err == sql.ErrNoRows {
		result, err := d.conn.Exec(
			"INSERT INTO users (telegram_id, username) VALUES (?, ?)",
			telegramID, username,
		)
		if err != nil {
			return nil, fmt.Errorf("creating user: %w", err)
		}
		user.ID, _ = result.LastInsertId()
		user.TelegramID = telegramID
		user.Username = username
		user.CreatedAt = time.Now()
		return &user, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying user: %w", err)
	}

	return &user, nil
}
