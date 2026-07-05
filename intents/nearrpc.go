package intents

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const nearRPCURL = "https://rpc.mainnet.near.org"

// PublicBalances returns the service account's public (Main) balances on the
// intents.near verifier contract for the given token IDs, via a NEAR RPC
// view call (mt_batch_balance_of). Amounts are base-unit strings, index-aligned
// with tokenIDs.
func (c *Client) PublicBalances(ctx context.Context, tokenIDs []string) ([]string, error) {
	args, err := json.Marshal(map[string]interface{}{
		"account_id": c.signerID,
		"token_ids":  tokenIDs,
	})
	if err != nil {
		return nil, err
	}

	rpcReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "fundbot",
		"method":  "query",
		"params": map[string]interface{}{
			"request_type": "call_function",
			"finality":     "final",
			"account_id":   VerifyingContract,
			"method_name":  "mt_batch_balance_of",
			"args_base64":  base64.StdEncoding.EncodeToString(args),
		},
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, nearRPCURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("near rpc: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var rpcResp struct {
		Result struct {
			Result []byte `json:"result"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
			Data    string `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("near rpc decode: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("near rpc error: %s %s", rpcResp.Error.Message, rpcResp.Error.Data)
	}

	var balances []string
	if err := json.Unmarshal(rpcResp.Result.Result, &balances); err != nil {
		return nil, fmt.Errorf("near rpc result decode: %w", err)
	}
	if len(balances) != len(tokenIDs) {
		return nil, fmt.Errorf("near rpc: expected %d balances, got %d", len(tokenIDs), len(balances))
	}
	return balances, nil
}
