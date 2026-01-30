package server

import (
	"github.com/RaghavSood/fundbot/balances"
)

// Re-export for use in server handlers.
type AddressBalance = balances.AddressBalance

var FetchBalances = balances.FetchBalances
