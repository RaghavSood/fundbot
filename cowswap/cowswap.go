// Package cowswap provides a client for the CoW Protocol (CoWSwap) API.
// Currently used for gas refills (USDC → native token), but designed to
// support general same-chain and cross-chain swaps in the future.
//
// Approvals use EIP-2612 permit signatures (gasless) embedded as CoW pre-hooks,
// so orders can be placed even with zero native token balance.
package cowswap

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
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

	// Default appData (no hooks) and its keccak256 hash.
	defaultAppDataJSON = `{"version":"1.3.0","metadata":{}}`
	defaultAppDataHash = "0xa872cd1c41362821123e195e2dc6a3f19502a451e1fb2a1f861131526e98fdc7"

	// permitGasLimit is the gas limit for the permit pre-hook.
	permitGasLimit = "80000"
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
// appData/appDataHash can be empty to use defaults (no hooks).
func (c *Client) GetQuote(chain string, sellToken, buyToken string, sellAmount *big.Int, from common.Address, receiver common.Address, appData, appDataHashHex string) (*QuoteResult, error) {
	cc, ok := SupportedChains[chain]
	if !ok {
		return nil, fmt.Errorf("chain %q not supported by CoW Protocol", chain)
	}

	if appData == "" {
		appData = defaultAppDataJSON
		appDataHashHex = defaultAppDataHash
	}

	req := QuoteRequest{
		SellToken:           sellToken,
		BuyToken:            buyToken,
		Receiver:            receiver.Hex(),
		SellAmountBeforeFee: sellAmount.String(),
		Kind:                "sell",
		From:                from.Hex(),
		AppData:             appData,
		AppDataHash:         appDataHashHex,
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

// --- EIP-2612 permit (gasless approval) ---

var erc20ABI abi.ABI
var permitABI abi.ABI

func init() {
	var err error
	erc20ABI, err = abi.JSON(strings.NewReader(`[{"inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"name":"allowance","outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"name":"owner","type":"address"}],"name":"nonces","outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`))
	if err != nil {
		panic(err)
	}
	permitABI, err = abi.JSON(strings.NewReader(`[{"inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"},{"name":"value","type":"uint256"},{"name":"deadline","type":"uint256"},{"name":"v","type":"uint8"},{"name":"r","type":"bytes32"},{"name":"s","type":"bytes32"}],"name":"permit","outputs":[],"stateMutability":"nonpayable","type":"function"}]`))
	if err != nil {
		panic(err)
	}
}

// permitHook represents a CoW pre-hook for an EIP-2612 permit call.
type permitHook struct {
	Target   string `json:"target"`
	CallData string `json:"callData"`
	GasLimit string `json:"gasLimit"`
}

// appDataDoc is the appData JSON document structure.
type appDataDoc struct {
	Version  string          `json:"version"`
	Metadata appDataMetadata `json:"metadata"`
}

type appDataMetadata struct {
	Hooks *appDataHooks `json:"hooks,omitempty"`
}

type appDataHooks struct {
	Pre  []permitHook `json:"pre"`
	Post []struct{}   `json:"post,omitempty"`
}

// buildAppDataHash computes keccak256 of the appData JSON string.
func buildAppDataHash(appDataJSON string) string {
	hash := crypto.Keccak256Hash([]byte(appDataJSON))
	return "0x" + hex.EncodeToString(hash.Bytes())
}

// needsPermit checks if the vault relayer allowance is insufficient for the given amount.
func (c *Client) needsPermit(ctx context.Context, chain string, sellToken common.Address, owner common.Address, amount *big.Int) (bool, error) {
	rpc, ok := c.rpcClients[chain]
	if !ok {
		return false, fmt.Errorf("no RPC client for chain %s", chain)
	}

	data, err := erc20ABI.Pack("allowance", owner, common.HexToAddress(VaultRelayer))
	if err != nil {
		return false, err
	}

	output, err := rpc.CallContract(ctx, ethereum.CallMsg{To: &sellToken, Data: data}, nil)
	if err != nil {
		return false, fmt.Errorf("checking allowance: %w", err)
	}

	allowance := new(big.Int).SetBytes(output)
	return allowance.Cmp(amount) < 0, nil
}

// getNonce reads the current EIP-2612 nonce for the owner on the token.
func (c *Client) getNonce(ctx context.Context, chain string, token common.Address, owner common.Address) (*big.Int, error) {
	rpc, ok := c.rpcClients[chain]
	if !ok {
		return nil, fmt.Errorf("no RPC client for chain %s", chain)
	}

	data, err := erc20ABI.Pack("nonces", owner)
	if err != nil {
		return nil, err
	}

	output, err := rpc.CallContract(ctx, ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil {
		return nil, fmt.Errorf("reading nonce: %w", err)
	}

	if len(output) < 32 {
		return big.NewInt(0), nil
	}

	return new(big.Int).SetBytes(output), nil
}

// signPermit signs an EIP-2612 permit for USDC and returns the permit callData
// to be used as a CoW pre-hook, plus the appData JSON and its hash.
//
// USDC uses EIP-2612 with domain: name="USDC", version="2".
func (c *Client) signPermit(ctx context.Context, chain string, cc ChainConfig, owner common.Address, privateKey *ecdsa.PrivateKey, amount *big.Int) (string, string, error) {
	token := common.HexToAddress(cc.USDCAddress)
	spender := common.HexToAddress(VaultRelayer)

	nonce, err := c.getNonce(ctx, chain, token, owner)
	if err != nil {
		return "", "", fmt.Errorf("getting permit nonce: %w", err)
	}

	// Deadline: 30 minutes from now
	deadline := big.NewInt(time.Now().Unix() + 1800)

	// Sign EIP-712 permit
	typedData := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"Permit": {
				{Name: "owner", Type: "address"},
				{Name: "spender", Type: "address"},
				{Name: "value", Type: "uint256"},
				{Name: "nonce", Type: "uint256"},
				{Name: "deadline", Type: "uint256"},
			},
		},
		PrimaryType: "Permit",
		Domain: apitypes.TypedDataDomain{
			Name:              "USDC",
			Version:           "2",
			ChainId:           math.NewHexOrDecimal256(cc.ChainID),
			VerifyingContract: cc.USDCAddress,
		},
		Message: apitypes.TypedDataMessage{
			"owner":    owner.Hex(),
			"spender":  spender.Hex(),
			"value":    amount.String(),
			"nonce":    nonce.String(),
			"deadline": deadline.String(),
		},
	}

	domainSep, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return "", "", fmt.Errorf("hashing permit domain: %w", err)
	}

	msgHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return "", "", fmt.Errorf("hashing permit message: %w", err)
	}

	rawData := fmt.Sprintf("\x19\x01%s%s", string(domainSep), string(msgHash))
	digest := crypto.Keccak256Hash([]byte(rawData))

	sig, err := crypto.Sign(digest.Bytes(), privateKey)
	if err != nil {
		return "", "", fmt.Errorf("signing permit: %w", err)
	}

	// Extract r, s, v
	r := [32]byte{}
	s := [32]byte{}
	copy(r[:], sig[:32])
	copy(s[:], sig[32:64])
	v := sig[64]
	if v < 27 {
		v += 27
	}

	// ABI-encode the permit() call
	callData, err := permitABI.Pack("permit", owner, spender, amount, deadline, v, r, s)
	if err != nil {
		return "", "", fmt.Errorf("encoding permit callData: %w", err)
	}

	// Build appData with permit pre-hook
	doc := appDataDoc{
		Version: "1.3.0",
		Metadata: appDataMetadata{
			Hooks: &appDataHooks{
				Pre: []permitHook{
					{
						Target:   cc.USDCAddress,
						CallData: "0x" + hex.EncodeToString(callData),
						GasLimit: permitGasLimit,
					},
				},
			},
		},
	}

	appJSON, err := json.Marshal(doc)
	if err != nil {
		return "", "", fmt.Errorf("marshaling appData: %w", err)
	}

	appJSONStr := string(appJSON)
	appHash := buildAppDataHash(appJSONStr)

	log.Printf("Built permit pre-hook for %s on %s (nonce=%s, deadline=%s)",
		owner.Hex(), cc.NativeSymbol, nonce.String(), deadline.String())

	return appJSONStr, appHash, nil
}

// --- Gas refill (high-level) ---

// RefillGasIfNeeded checks if the wallet needs gas on a chain and submits a CoW swap if so.
// Uses EIP-2612 permit for gasless approval when the vault relayer allowance is insufficient.
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

	sellToken := common.HexToAddress(cc.USDCAddress)

	// Check if we need a permit (allowance < refillUSDC)
	var appData, appHash string
	needs, err := c.needsPermit(ctx, chain, sellToken, addr, refillUSDC)
	if err != nil {
		return nil, fmt.Errorf("checking permit need: %w", err)
	}

	if needs {
		// Use max uint256 for permit value so we don't need to permit again next time
		maxValue := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
		appData, appHash, err = c.signPermit(ctx, chain, cc, addr, privateKey, maxValue)
		if err != nil {
			return nil, fmt.Errorf("signing permit: %w", err)
		}
	}
	// If no permit needed, appData/appHash are empty strings → GetQuote uses defaults

	// Get quote (with permit hook appData if needed)
	qr, err := c.GetQuote(chain, cc.USDCAddress, NativeToken, refillUSDC, addr, addr, appData, appHash)
	if err != nil {
		return nil, fmt.Errorf("getting quote: %w", err)
	}

	// Sign order
	sig, err := c.SignOrder(cc, qr, privateKey)
	if err != nil {
		return nil, fmt.Errorf("signing order: %w", err)
	}

	// Submit order
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
