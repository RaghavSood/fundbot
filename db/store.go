package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store wraps sqlc Queries with connection management and helpers.
type Store struct {
	*Queries
	conn *sql.DB
}

func Open(path string) (*Store, error) {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("setting goose dialect: %w", err)
	}
	if err := goose.Up(conn, "migrations"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return &Store{
		Queries: New(conn),
		conn:    conn,
	}, nil
}

func (s *Store) Close() error {
	return s.conn.Close()
}

// GetOrCreateUser returns the user for a telegram ID, creating one if needed.
func (s *Store) GetOrCreateUser(ctx context.Context, telegramID int64, username string) (User, error) {
	user, err := s.GetUserByTelegramID(ctx, telegramID)
	if err == nil {
		return user, nil
	}
	if err != sql.ErrNoRows {
		return User{}, fmt.Errorf("querying user: %w", err)
	}

	return s.CreateUser(ctx, CreateUserParams{
		TelegramID: telegramID,
		Username:   username,
	})
}

// GetOrCreateChat returns the chat for a Telegram chat ID, creating one if needed.
func (s *Store) GetOrCreateChat(ctx context.Context, chatID int64, title string) (Chat, error) {
	chat, err := s.GetChatByChatID(ctx, chatID)
	if err == nil {
		return chat, nil
	}
	if err != sql.ErrNoRows {
		return Chat{}, fmt.Errorf("querying chat: %w", err)
	}

	return s.CreateChat(ctx, CreateChatParams{
		ChatID: chatID,
		Title:  title,
	})
}

// GetOrCreateAddressAssignment returns the address assignment for the given entity, creating one if needed.
func (s *Store) GetOrCreateAddressAssignment(ctx context.Context, assignedToID int64, assignedToType string) (AddressAssignment, error) {
	a, err := s.GetAddressAssignment(ctx, GetAddressAssignmentParams{
		AssignedToID:   assignedToID,
		AssignedToType: assignedToType,
	})
	if err == nil {
		return a, nil
	}
	if err != sql.ErrNoRows {
		return AddressAssignment{}, fmt.Errorf("querying address assignment: %w", err)
	}

	return s.CreateAddressAssignment(ctx, CreateAddressAssignmentParams{
		AssignedToID:   assignedToID,
		AssignedToType: assignedToType,
	})
}

// InsertTopupWithShortID generates a random short ID and inserts the topup.
func (s *Store) InsertTopupWithShortID(ctx context.Context, arg InsertTopupParams) (InsertTopupRow, error) {
	arg.ShortID = generateShortID()
	return s.InsertTopup(ctx, arg)
}

func generateShortID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}
