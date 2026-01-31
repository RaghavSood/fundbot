// Package cowswap provides a client for the CoW Protocol (CoWSwap) API.
// Currently used for gas refills (USDC → native token), but designed to
// support general same-chain and cross-chain swaps in the future.
package cowswap

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

const (
	// SettlementContract is the GPv2Settlement address (same on all chains).
	SettlementContract = "0x9008D19f58AAbD9eD0D60971565AA8510560ab41"
	// VaultRelayer is the GPv2VaultRelayer address — sell tokens must be approved to this.
	VaultRelayer = "0xC92E8bdf79f0507f65a392b0ab4667716BFE0110"
	// NativeToken is the placeholder address for the chain's native gas token.
	NativeToken = "0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE"

	appDataJSON = `{"version":"1.3.0","metadata":{}}`
	appDataHash = "0xa872cd1c41362821123e195e2dc6a3f19502a451e1fb2a1f861131526e98fdc7"
)

// ChainConfig holds chain-specific CoW Protocol configuration.
type ChainConfig struct {
	APIBase      string
	ChainID      int64
	USDCAddress  string
	NativeSymbol string
}

// SupportedChains maps RPC chain key to CoW Protocol config.
var SupportedChains = map[string]ChainConfig{
	"base": {
		APIBase:      "https://api.cow.fi/base/api/v1",
		ChainID:      8453,
		USDCAddress:  "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		NativeSymbol: "ETH",
	},
	"avalanche": {
		APIBase:      "https://api.cow.fi/avalanche/api/v1",
		ChainID:      43114,
		USDCAddress:  "0xB97EF9Ef8734C71904D8002F8B6BC66Dd9c48a6E",
		NativeSymbol: "AVAX",
	},
}

// Client handles CoW Protocol API interactions.
type Client struct {
	httpClient *http.Client
	rpcClients map[string]*ethclient.Client
}

// NewClient creates a new CoW Protocol client.
func NewClient(rpcClients map[string]*ethclient.Client) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		rpcClients: rpcClients,
	}
}

// --- API types ---

// QuoteRequest is the POST body for /api/v1/quote.
type QuoteRequest struct {
	SellToken           string `json:"sellToken"`
	BuyToken            string `json:"buyToken"`
	Receiver            string `json:"receiver"`
	SellAmountBeforeFee string `json:"sellAmountBeforeFee"`
	Kind                string `json:"kind"`
	From                string `json:"from"`
	AppData             string `json:"appData"`
	AppDataHash         string `json:"appDataHash"`
	SigningScheme       string `json:"signingScheme"`
}

// QuoteResult is the response from /api/v1/quote.
type QuoteResult struct {
	Quote struct {
		SellToken         string `json:"sellToken"`
		BuyToken          string `json:"buyToken"`
		Receiver          string `json:"receiver"`
		SellAmount        string `json:"sellAmount"`
		BuyAmount         string `json:"buyAmount"`
		ValidTo           uint32 `json:"validTo"`
		AppData           string `json:"appData"`
		AppDataHash       string `json:"appDataHash"`
		FeeAmount         string `json:"feeAmount"`
		Kind              string `json:"kind"`
		PartiallyFillable bool   `json:"partiallyFillable"`
		SellTokenBalance  string `json:"sellTokenBalance"`
		BuyTokenBalance   string `json:"buyTokenBalance"`
	} `json:"quote"`
	From       string `json:"from"`
	Expiration string `json:"expiration"`
	ID         int64  `json:"id"`
}

// OrderSubmission is the POST body for /api/v1/orders.
type OrderSubmission struct {
	SellToken         string `json:"sellToken"`
	BuyToken          string `json:"buyToken"`
	Receiver          string `json:"receiver"`
	SellAmount        string `json:"sellAmount"`
	BuyAmount         string `json:"buyAmount"`
	ValidTo           uint32 `json:"validTo"`
	AppData           string `json:"appData"`
	AppDataHash       string `json:"appDataHash"`
	FeeAmount         string `json:"feeAmount"`
	Kind              string `json:"kind"`
	PartiallyFillable bool   `json:"partiallyFillable"`
	SellTokenBalance  string `json:"sellTokenBalance"`
	BuyTokenBalance   string `json:"buyTokenBalance"`
	SigningScheme     string `json:"signingScheme"`
	Signature         string `json:"signature"`
	From              string `json:"from"`
}

// GasRefillResult holds the result of a gas refill operation.
type GasRefillResult struct {
	Chain    string
	OrderUID string
	Status   string
}

// --- Core API methods (reusable for future swap provider) ---

// GetQuote requests a quote from the CoW Protocol API.
func (c *Client) GetQuote(chain string, sellToken, buyToken string, sellAmount *big.Int, from common.Address, receiver common.Address) (*QuoteResult, error) {
	cc, ok := SupportedChains[chain]
	if !ok {
		return nil, fmt.Errorf("chain %q not supported by CoW Protocol", chain)
	}

	req := QuoteRequest{
		SellToken:           sellToken,
		BuyToken:            buyToken,
		Receiver:            receiver.Hex(),
		SellAmountBeforeFee: sellAmount.String(),
		Kind:                "sell",
		From:                from.Hex(),
		AppData:             appDataJSON,
		AppDataHash:         appDataHash,
		SigningScheme:       "eip712",
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Post(cc.APIBase+"/quote", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("quote API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var qr QuoteResult
	if err := json.Unmarshal(respBody, &qr); err != nil {
		return nil, fmt.Errorf("decoding quote: %w", err)
	}

	return &qr, nil
}

// SignOrder signs a CoW Protocol order using EIP-712 and returns the signature hex.
func (c *Client) SignOrder(cc ChainConfig, qr *QuoteResult, privateKey *ecdsa.PrivateKey) (string, error) {
	q := qr.Quote

	typedData := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"Order": {
				{Name: "sellToken", Type: "address"},
				{Name: "buyToken", Type: "address"},
				{Name: "receiver", Type: "address"},
				{Name: "sellAmount", Type: "uint256"},
				{Name: "buyAmount", Type: "uint256"},
				{Name: "validTo", Type: "uint32"},
				{Name: "appData", Type: "bytes32"},
				{Name: "feeAmount", Type: "uint256"},
				{Name: "kind", Type: "string"},
				{Name: "partiallyFillable", Type: "bool"},
				{Name: "sellTokenBalance", Type: "string"},
				{Name: "buyTokenBalance", Type: "string"},
			},
		},
		PrimaryType: "Order",
		Domain: apitypes.TypedDataDomain{
			Name:              "Gnosis Protocol",
			Version:           "v2",
			ChainId:           math.NewHexOrDecimal256(cc.ChainID),
			VerifyingContract: SettlementContract,
		},
		Message: apitypes.TypedDataMessage{
			"sellToken":         q.SellToken,
			"buyToken":          q.BuyToken,
			"receiver":          q.Receiver,
			"sellAmount":        q.SellAmount,
			"buyAmount":         q.BuyAmount,
			"validTo":           fmt.Sprintf("%d", q.ValidTo),
			"appData":           q.AppDataHash,
			"feeAmount":         q.FeeAmount,
			"kind":              q.Kind,
			"partiallyFillable": q.PartiallyFillable,
			"sellTokenBalance":  q.SellTokenBalance,
			"buyTokenBalance":   q.BuyTokenBalance,
		},
	}

	domainSep, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return "", fmt.Errorf("hashing domain: %w", err)
	}

	msgHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return "", fmt.Errorf("hashing message: %w", err)
	}

	rawData := fmt.Sprintf("\x19\x01%s%s", string(domainSep), string(msgHash))
	digest := crypto.Keccak256Hash([]byte(rawData))

	sig, err := crypto.Sign(digest.Bytes(), privateKey)
	if err != nil {
		return "", fmt.Errorf("signing order: %w", err)
	}

	// Ethereum signature convention: v = 27 or 28
	if sig[64] < 27 {
		sig[64] += 27
	}

	return fmt.Sprintf("0x%x", sig), nil
}

// SubmitOrder submits a signed order to the CoW Protocol API. Returns the order UID.
func (c *Client) SubmitOrder(chain string, qr *QuoteResult, signature string, from common.Address) (string, error) {
	cc, ok := SupportedChains[chain]
	if !ok {
		return "", fmt.Errorf("chain %q not supported by CoW Protocol", chain)
	}

	q := qr.Quote
	order := OrderSubmission{
		SellToken:         q.SellToken,
		BuyToken:          q.BuyToken,
		Receiver:          q.Receiver,
		SellAmount:        q.SellAmount,
		BuyAmount:         q.BuyAmount,
		ValidTo:           q.ValidTo,
		AppData:           q.AppData,
		AppDataHash:       q.AppDataHash,
		FeeAmount:         q.FeeAmount,
		Kind:              q.Kind,
		PartiallyFillable: q.PartiallyFillable,
		SellTokenBalance:  q.SellTokenBalance,
		BuyTokenBalance:   q.BuyTokenBalance,
		SigningScheme:     "eip712",
		Signature:         signature,
		From:              from.Hex(),
	}

	body, err := json.Marshal(order)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Post(cc.APIBase+"/orders", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("order API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var orderUID string
	if err := json.Unmarshal(respBody, &orderUID); err != nil {
		return "", fmt.Errorf("decoding order UID: %w", err)
	}

	return orderUID, nil
}

// CheckOrderStatus checks the status of a CoW order.
// Returns one of: "presignaturePending", "open", "fulfilled", "cancelled", "expired".
func (c *Client) CheckOrderStatus(chain string, orderUID string) (string, error) {
	cc, ok := SupportedChains[chain]
	if !ok {
		return "", fmt.Errorf("unsupported chain: %s", chain)
	}

	url := fmt.Sprintf("%s/orders/%s", cc.APIBase, orderUID)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetching order status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("order status API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding order status: %w", err)
	}

	return result.Status, nil
}

// --- ERC20 approval ---

var erc20ABI abi.ABI

func init() {
	var err error
	erc20ABI, err = abi.JSON(strings.NewReader(`[{"inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"name":"approve","outputs":[{"name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"name":"allowance","outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`))
	if err != nil {
		panic(err)
	}
}

// EnsureApproval checks the current allowance of sellToken to the vault relayer
// and submits a max-approval transaction if insufficient. Waits for confirmation.
func (c *Client) EnsureApproval(ctx context.Context, chain string, sellToken common.Address, addr common.Address, privateKey *ecdsa.PrivateKey) error {
	cc, ok := SupportedChains[chain]
	if !ok {
		return fmt.Errorf("chain %q not supported", chain)
	}

	rpc, ok := c.rpcClients[chain]
	if !ok {
		return fmt.Errorf("no RPC client for chain %s", chain)
	}

	relayer := common.HexToAddress(VaultRelayer)

	// Check current allowance
	data, err := erc20ABI.Pack("allowance", addr, relayer)
	if err != nil {
		return err
	}

	output, err := rpc.CallContract(ctx, ethereum.CallMsg{To: &sellToken, Data: data}, nil)
	if err != nil {
		return fmt.Errorf("checking allowance: %w", err)
	}

	allowance := new(big.Int).SetBytes(output)
	// Skip if allowance >= 1000 USDC (1e9 smallest units)
	if allowance.Cmp(big.NewInt(1e9)) >= 0 {
		return nil
	}

	log.Printf("Approving %s to CoW vault relayer on %s", sellToken.Hex(), cc.NativeSymbol)

	maxApproval := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	approveData, err := erc20ABI.Pack("approve", relayer, maxApproval)
	if err != nil {
		return err
	}

	chainID := big.NewInt(cc.ChainID)
	nonce, err := rpc.PendingNonceAt(ctx, addr)
	if err != nil {
		return fmt.Errorf("getting nonce: %w", err)
	}

	gasPrice, err := rpc.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("getting gas price: %w", err)
	}

	gas, err := rpc.EstimateGas(ctx, ethereum.CallMsg{
		From: addr,
		To:   &sellToken,
		Data: approveData,
	})
	if err != nil {
		return fmt.Errorf("estimating gas: %w", err)
	}

	tx := types.NewTransaction(nonce, sellToken, big.NewInt(0), gas, gasPrice, approveData)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
	if err != nil {
		return fmt.Errorf("signing approval tx: %w", err)
	}

	if err := rpc.SendTransaction(ctx, signedTx); err != nil {
		return fmt.Errorf("sending approval tx: %w", err)
	}

	log.Printf("Approval tx sent: %s", signedTx.Hash().Hex())

	// Wait for confirmation (up to 60s)
	for i := 0; i < 30; i++ {
		time.Sleep(2 * time.Second)
		receipt, err := rpc.TransactionReceipt(ctx, signedTx.Hash())
		if err != nil {
			continue
		}
		if receipt.Status == 1 {
			log.Printf("Approval confirmed: %s", signedTx.Hash().Hex())
			return nil
		}
		return fmt.Errorf("approval tx reverted: %s", signedTx.Hash().Hex())
	}

	return fmt.Errorf("approval tx not confirmed after 60s: %s", signedTx.Hash().Hex())
}

// --- Gas refill (high-level) ---

// RefillGasIfNeeded checks if the wallet needs gas on a chain and submits a CoW swap if so.
// Returns nil result if no refill was needed or conditions weren't met.
func (c *Client) RefillGasIfNeeded(ctx context.Context, chain string, addr common.Address, privateKey *ecdsa.PrivateKey, nativeBalance *big.Int, usdcBalance *big.Int, minNativeWei *big.Int, refillUSDC *big.Int) (*GasRefillResult, error) {
	cc, ok := SupportedChains[chain]
	if !ok {
		return nil, nil // chain not supported by CoW
	}

	if nativeBalance.Cmp(minNativeWei) >= 0 {
		return nil, nil // sufficient gas
	}

	if usdcBalance.Cmp(refillUSDC) < 0 {
		return nil, nil // insufficient USDC for refill
	}

	log.Printf("Gas refill needed on %s for %s: native=%s, threshold=%s",
		chain, addr.Hex(), nativeBalance.String(), minNativeWei.String())

	usdcAddr := common.HexToAddress(cc.USDCAddress)

	// Ensure approval
	if err := c.EnsureApproval(ctx, chain, usdcAddr, addr, privateKey); err != nil {
		return nil, fmt.Errorf("ensuring approval: %w", err)
	}

	// Get quote
	qr, err := c.GetQuote(chain, cc.USDCAddress, NativeToken, refillUSDC, addr, addr)
	if err != nil {
		return nil, fmt.Errorf("getting quote: %w", err)
	}

	// Sign
	sig, err := c.SignOrder(cc, qr, privateKey)
	if err != nil {
		return nil, fmt.Errorf("signing order: %w", err)
	}

	// Submit
	orderUID, err := c.SubmitOrder(chain, qr, sig, addr)
	if err != nil {
		return nil, fmt.Errorf("submitting order: %w", err)
	}

	log.Printf("CoW gas refill order submitted on %s: %s", cc.NativeSymbol, orderUID)

	return &GasRefillResult{
		Chain:    chain,
		OrderUID: orderUID,
		Status:   "open",
	}, nil
}
