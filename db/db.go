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

func migrate(conn *sql.DB) error {
	_, err := conn.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			telegram_id INTEGER UNIQUE NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
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
