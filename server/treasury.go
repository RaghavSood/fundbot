package server

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"

	"github.com/RaghavSood/fundbot/db"
	"github.com/RaghavSood/fundbot/nearintents"
)

func decodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// usdcFormat renders a USDC base-unit amount (6 decimals) as a decimal string.
func usdcFormat(base *big.Int) string {
	if base == nil {
		return "0.000000"
	}
	whole := new(big.Int).Div(base, big.NewInt(1e6))
	frac := new(big.Int).Mod(base, big.NewInt(1e6))
	return fmt.Sprintf("%d.%06d", whole.Int64(), frac.Int64())
}

// usdcFormatStr parses a base-unit string and formats it; "" or bad input -> "0.000000".
func usdcFormatStr(s string) string {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return "0.000000"
	}
	return usdcFormat(v)
}

func (s *Server) handleAdminTreasury(w http.ResponseWriter, r *http.Request) {
	if s.treasury == nil {
		writeJSON(w, map[string]interface{}{"enabled": false})
		return
	}
	ctx := r.Context()

	type chainBalance struct {
		Chain string `json:"chain"`
		USDC  string `json:"usdc"`
	}

	var evm []chainBalance
	evmBals, err := s.treasury.EVMBalances(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("evm balances: %v", err), http.StatusInternalServerError)
		return
	}
	for _, b := range evmBals {
		evm = append(evm, chainBalance{Chain: b.Chain, USDC: usdcFormat(b.Balance)})
	}

	public, confidential, err := s.treasury.IntentsBalances(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("intents balances: %v", err), http.StatusInternalServerError)
		return
	}

	// Present intents balances per source chain by their token IDs.
	type intentsBalance struct {
		Chain        string `json:"chain"`
		Public       string `json:"public"`
		Confidential string `json:"confidential"`
	}
	var intents []intentsBalance
	for _, chain := range nearintents.SupportedSourceChains() {
		tokenID, ok := nearintents.SourceTokenID(chain)
		if !ok {
			continue
		}
		intents = append(intents, intentsBalance{
			Chain:        chain,
			Public:       usdcFormatStr(public[tokenID]),
			Confidential: usdcFormatStr(confidential[tokenID]),
		})
	}

	limit := int64(50)
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	ledger, err := s.store.ListTreasuryLedger(ctx, db.ListTreasuryLedgerParams{Limit: limit, Offset: offset})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, _ := s.store.CountTreasuryLedger(ctx)

	writeJSON(w, map[string]interface{}{
		"enabled": true,
		"account": s.treasury.Address().Hex(),
		"evm":     evm,
		"intents": intents,
		"ledger":  ledger,
		"total":   total,
	})
}

// treasuryOpRequest is the JSON body for deposit/shield actions.
type treasuryOpRequest struct {
	Chain string  `json:"chain"`
	USD   float64 `json:"usd"`
}

func (s *Server) parseTreasuryOp(w http.ResponseWriter, r *http.Request) (string, *big.Int, bool) {
	if s.treasury == nil {
		http.Error(w, "treasury not enabled", http.StatusBadRequest)
		return "", nil, false
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return "", nil, false
	}
	var req treasuryOpRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return "", nil, false
	}
	if _, ok := nearintents.SourceTokenID(req.Chain); !ok {
		http.Error(w, "invalid chain", http.StatusBadRequest)
		return "", nil, false
	}
	if req.USD <= 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return "", nil, false
	}
	amount := new(big.Int).SetInt64(int64(req.USD * 1e6))
	return req.Chain, amount, true
}

func (s *Server) handleAdminTreasuryDeposit(w http.ResponseWriter, r *http.Request) {
	chain, amount, ok := s.parseTreasuryOp(w, r)
	if !ok {
		return
	}
	id, txHash, err := s.treasury.DepositToIntents(r.Context(), chain, amount)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ledger_id": id, "tx_hash": txHash})
}

func (s *Server) handleAdminTreasuryShield(w http.ResponseWriter, r *http.Request) {
	chain, amount, ok := s.parseTreasuryOp(w, r)
	if !ok {
		return
	}
	id, depositAddr, err := s.treasury.Shield(r.Context(), chain, amount)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ledger_id": id, "deposit_address": depositAddr})
}
