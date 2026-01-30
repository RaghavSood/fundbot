package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Mode string

const (
	ModeSingle Mode = "single"
	ModeMulti  Mode = "multi"
)

type Config struct {
	// Telegram bot token from @BotFather
	TelegramToken string `json:"telegram_token"`

	// Operating mode: "single" or "multi"
	Mode Mode `json:"mode"`

	// BIP39 mnemonic for wallet derivation
	Mnemonic string `json:"mnemonic"`

	// Admin telegram user ID - can approve users in single mode
	AdminUserID int64 `json:"admin_user_id"`

	// Whitelisted telegram user IDs (single mode only)
	WhitelistedUsers []int64 `json:"whitelisted_users"`

	// Path to SQLite database (multi mode only)
	DatabasePath string `json:"database_path"`

	// RPC endpoints for supported chains
	RPCEndpoints map[string]string `json:"rpc_endpoints"`

	// HTTP server port (default 8080)
	Port int `json:"port"`

	// Optional password to protect the dashboard; empty = public
	DashboardPassword string `json:"dashboard_password"`

	// Required password to protect the admin panel
	AdminPassword string `json:"admin_password"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.TelegramToken == "" {
		return fmt.Errorf("telegram_token is required")
	}
	if c.Mnemonic == "" {
		return fmt.Errorf("mnemonic is required")
	}
	if c.Mode != ModeSingle && c.Mode != ModeMulti {
		return fmt.Errorf("mode must be 'single' or 'multi'")
	}
	if c.AdminUserID == 0 {
		return fmt.Errorf("admin_user_id is required")
	}
	if c.DatabasePath == "" {
		return fmt.Errorf("database_path is required")
	}
	if c.AdminPassword == "" {
		return fmt.Errorf("admin_password is required")
	}
	if c.Port == 0 {
		c.Port = 8080
	}
	return nil
}

func (c *Config) IsAuthorized(userID int64) bool {
	if userID == c.AdminUserID {
		return true
	}
	if c.Mode == ModeMulti {
		return true // all users allowed, they get their own wallet
	}
	for _, id := range c.WhitelistedUsers {
		if id == userID {
			return true
		}
	}
	return false
}
