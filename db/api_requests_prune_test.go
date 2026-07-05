package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneAPIRequests(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	old := time.Now().UTC().Add(-30 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	recent := time.Now().UTC().Add(-1 * time.Hour).Format("2006-01-02 15:04:05")

	// (label, method, url, respBody, createdAt, keepExpected)
	rows := []struct {
		label     string
		method    string
		url       string
		respBody  string
		createdAt string
		keep      bool
	}{
		{"old simpleswap create", "POST", "https://api.simpleswap.io/create_exchange?api_key=x", `{"id":"abc"}`, old, true},
		{"old houdini create", "POST", "https://api-partner.houdiniswap.com/exchange", `{"houdiniId":"h1"}`, old, true},
		{"old intents submit-intent", "POST", "https://1click.chaindefuser.com/v0/submit-intent", `{"hash":"0x1"}`, old, true},
		{"old near deposit notify", "POST", "https://1click.chaindefuser.com/v0/deposit/submit", `{}`, old, true},
		{"old simpleswap finished status", "GET", "https://api.simpleswap.io/get_exchange?id=abc", `{"status":"finished"}`, old, true},
		{"old intents SUCCESS status", "GET", "https://1click.chaindefuser.com/v0/status?depositAddress=0x", `{"status":"SUCCESS"}`, old, true},
		{"old thorchain finalised", "GET", "https://thornode.ninerealms.com/thorchain/tx/status/H", `{"stages":{"swap_finalised":{"completed":true}}}`, old, true},
		{"old houdini completed status", "GET", "https://api-partner.houdiniswap.com/status?id=h1", `{"status":4}`, old, true},

		{"old quote", "POST", "https://1click.chaindefuser.com/v0/quote", `{"quote":{}}`, old, false},
		{"old pending simpleswap status", "GET", "https://api.simpleswap.io/get_exchange?id=abc", `{"status":"waiting"}`, old, false},
		{"old pending intents status", "GET", "https://1click.chaindefuser.com/v0/status?depositAddress=0x", `{"status":"PROCESSING"}`, old, false},
		{"old auth call", "POST", "https://1click.chaindefuser.com/v0/auth/authenticate", `{"accessToken":"j"}`, old, false},
		{"old balance check", "GET", "https://1click.chaindefuser.com/v0/account/balances", `{"balances":[]}`, old, false},

		{"recent quote (too new to prune)", "POST", "https://1click.chaindefuser.com/v0/quote", `{"quote":{}}`, recent, true},
		{"recent pending status (too new)", "GET", "https://1click.chaindefuser.com/v0/status", `{"status":"PROCESSING"}`, recent, true},
	}

	for _, r := range rows {
		_, err := store.conn.ExecContext(ctx,
			`INSERT INTO api_requests (provider, method, url, response_body, created_at) VALUES (?, ?, ?, ?, ?)`,
			"test", r.method, r.url, r.respBody, r.createdAt)
		if err != nil {
			t.Fatalf("insert %q: %v", r.label, err)
		}
	}

	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
	deleted, err := store.PruneAPIRequests(ctx, sql.NullTime{Time: cutoff, Valid: true})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	var wantDeleted int64
	for _, r := range rows {
		if !r.keep {
			wantDeleted++
		}
	}
	if deleted != wantDeleted {
		t.Errorf("deleted %d rows, want %d", deleted, wantDeleted)
	}

	// Verify each expected-keep row survived and each expected-delete row is gone.
	for _, r := range rows {
		var count int
		err := store.conn.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM api_requests WHERE url = ? AND method = ? AND response_body = ? AND created_at = ?`,
			r.url, r.method, r.respBody, r.createdAt).Scan(&count)
		if err != nil {
			t.Fatalf("count %q: %v", r.label, err)
		}
		if r.keep && count == 0 {
			t.Errorf("%q: expected to be kept but was deleted", r.label)
		}
		if !r.keep && count != 0 {
			t.Errorf("%q: expected to be deleted but survived", r.label)
		}
	}
}
