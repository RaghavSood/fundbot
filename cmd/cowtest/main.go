// Standalone test script for CoW Protocol USDC → AVAX swap with EIP-2612 permit.
// Usage: go run ./cmd/cowtest
package main

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
	avaxRPC        = "https://avalanche-c-chain-rpc.publicnode.com"
	cowAPIBase     = "https://api.cow.fi/avalanche/api/v1"
	chainID        = 43114
	usdcAddr       = "0xB97EF9Ef8734C71904D8002F8B6BC66Dd9c48a6E"
	nativeToken    = "0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE"
	settlement     = "0x9008D19f58AAbD9eD0D60971565AA8510560ab41"
	vaultRelayer   = "0xC92E8bdf79f0507f65a392b0ab4667716BFE0110"
	privateKeyHex  = "008597d2c7c6c1b8ef14c4fb7719578676cf6845ab3e9f03e34d6384bf088be7"
	sellAmountUSDC = 1_000_000 // 1 USDC
)

var (
	httpClient = &http.Client{Timeout: 30 * time.Second}
	erc20ABI   abi.ABI
	permitABI  abi.ABI
)

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

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)

	// Load key
	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		log.Fatalf("bad private key: %v", err)
	}
	addr := crypto.PubkeyToAddress(privateKey.PublicKey)
	log.Printf("Address: %s", addr.Hex())

	// Connect RPC
	rpc, err := ethclient.Dial(avaxRPC)
	if err != nil {
		log.Fatalf("RPC dial: %v", err)
	}

	ctx := context.Background()
	sellAmount := big.NewInt(sellAmountUSDC)

	// Step 1: Check allowance
	log.Println("=== Step 1: Check allowance ===")
	allowance := checkAllowance(ctx, rpc, addr)
	log.Printf("Current allowance: %s", allowance.String())

	// Step 2: Build permit if needed
	var appDataJSON, appDataHashHex string
	if allowance.Cmp(sellAmount) < 0 {
		log.Println("=== Step 2: Build permit (allowance insufficient) ===")
		appDataJSON, appDataHashHex = buildPermit(ctx, rpc, addr, privateKey)
		log.Printf("appData JSON: %s", appDataJSON)
		log.Printf("appData hash: %s", appDataHashHex)
	} else {
		log.Println("=== Step 2: Skip permit (allowance sufficient) ===")
		appDataJSON = `{"version":"1.3.0","metadata":{}}`
		appDataHashHex = "0x" + hex.EncodeToString(crypto.Keccak256([]byte(appDataJSON)))
		log.Printf("Using default appData hash: %s", appDataHashHex)
	}

	// Step 2b: Simulate permit call via eth_call to verify it works
	log.Println("=== Step 2b: Simulate permit via eth_call ===")
	{
		callDataHex := appDataJSON // extract callData from appData
		// Parse the appData to get the callData
		var appDoc struct {
			Metadata struct {
				Hooks struct {
					Pre []struct {
						Target   string `json:"target"`
						CallData string `json:"callData"`
					} `json:"pre"`
				} `json:"hooks"`
			} `json:"metadata"`
		}
		json.Unmarshal([]byte(callDataHex), &appDoc)
		if len(appDoc.Metadata.Hooks.Pre) > 0 {
			hook := appDoc.Metadata.Hooks.Pre[0]
			cd, _ := hex.DecodeString(strings.TrimPrefix(hook.CallData, "0x"))
			target := common.HexToAddress(hook.Target)
			output, err := rpc.CallContract(ctx, ethereum.CallMsg{
				To:   &target,
				Data: cd,
			}, nil)
			log.Printf("Permit simulation result: output=%x, err=%v", output, err)
		}
	}

	// Step 3: Get quote
	log.Println("=== Step 3: Get quote ===")
	qr := getQuote(addr, sellAmount, appDataJSON, appDataHashHex)
	qrJSON, _ := json.MarshalIndent(qr, "", "  ")
	log.Printf("Quote response:\n%s", string(qrJSON))

	// Step 4: Sign order
	log.Println("=== Step 4: Sign order ===")
	sig := signOrder(qr, privateKey)
	log.Printf("Signature: %s", sig)

	// Step 5: Submit order (with full appData JSON)
	log.Println("=== Step 5: Submit order ===")
	orderUID := submitOrder(qr, sig, addr, appDataJSON)
	log.Printf("Order UID: %s", orderUID)

	// Step 6: Check status
	log.Println("=== Step 6: Check status ===")
	status := checkStatus(orderUID)
	log.Printf("Order status: %s", status)

	log.Println("=== Done! ===")
}

func checkAllowance(ctx context.Context, rpc *ethclient.Client, owner common.Address) *big.Int {
	token := common.HexToAddress(usdcAddr)
	data, err := erc20ABI.Pack("allowance", owner, common.HexToAddress(vaultRelayer))
	if err != nil {
		log.Fatalf("pack allowance: %v", err)
	}
	output, err := rpc.CallContract(ctx, ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil {
		log.Fatalf("call allowance: %v", err)
	}
	return new(big.Int).SetBytes(output)
}

func getNonce(ctx context.Context, rpc *ethclient.Client, owner common.Address) *big.Int {
	token := common.HexToAddress(usdcAddr)
	data, err := erc20ABI.Pack("nonces", owner)
	if err != nil {
		log.Fatalf("pack nonces: %v", err)
	}
	output, err := rpc.CallContract(ctx, ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil {
		log.Fatalf("call nonces: %v", err)
	}
	if len(output) < 32 {
		return big.NewInt(0)
	}
	return new(big.Int).SetBytes(output)
}

func buildPermit(ctx context.Context, rpc *ethclient.Client, owner common.Address, privateKey *ecdsa.PrivateKey) (string, string) {
	spender := common.HexToAddress(vaultRelayer)
	nonce := getNonce(ctx, rpc, owner)
	log.Printf("Permit nonce: %s", nonce.String())

	// Max uint256 for permit value
	maxValue := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	deadline := big.NewInt(time.Now().Unix() + 1800)
	log.Printf("Permit deadline: %s", deadline.String())

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
			Name:              "USD Coin",
			Version:           "2",
			ChainId:           math.NewHexOrDecimal256(chainID),
			VerifyingContract: usdcAddr,
		},
		Message: apitypes.TypedDataMessage{
			"owner":    owner.Hex(),
			"spender":  spender.Hex(),
			"value":    maxValue.String(),
			"nonce":    nonce.String(),
			"deadline": deadline.String(),
		},
	}

	domainSep, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		log.Fatalf("hash permit domain: %v", err)
	}
	msgHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		log.Fatalf("hash permit message: %v", err)
	}

	rawData := fmt.Sprintf("\x19\x01%s%s", string(domainSep), string(msgHash))
	digest := crypto.Keccak256Hash([]byte(rawData))

	sig, err := crypto.Sign(digest.Bytes(), privateKey)
	if err != nil {
		log.Fatalf("sign permit: %v", err)
	}

	r := [32]byte{}
	s := [32]byte{}
	copy(r[:], sig[:32])
	copy(s[:], sig[32:64])
	v := sig[64]
	if v < 27 {
		v += 27
	}

	callData, err := permitABI.Pack("permit", owner, spender, maxValue, deadline, v, r, s)
	if err != nil {
		log.Fatalf("pack permit: %v", err)
	}

	log.Printf("Permit callData: 0x%s", hex.EncodeToString(callData))

	// Build appData JSON with hook
	type hook struct {
		Target   string `json:"target"`
		CallData string `json:"callData"`
		GasLimit string `json:"gasLimit"`
	}
	type hooks struct {
		Pre []hook `json:"pre"`
	}
	type metadata struct {
		Hooks *hooks `json:"hooks,omitempty"`
	}
	type appData struct {
		Version  string   `json:"version"`
		Metadata metadata `json:"metadata"`
	}

	doc := appData{
		Version: "1.3.0",
		Metadata: metadata{
			Hooks: &hooks{
				Pre: []hook{
					{
						Target:   usdcAddr,
						CallData: "0x" + hex.EncodeToString(callData),
						GasLimit: "80000",
					},
				},
			},
		},
	}

	appJSON, err := json.Marshal(doc)
	if err != nil {
		log.Fatalf("marshal appData: %v", err)
	}

	appJSONStr := string(appJSON)
	hash := "0x" + hex.EncodeToString(crypto.Keccak256([]byte(appJSONStr)))

	return appJSONStr, hash
}

func getQuote(from common.Address, sellAmount *big.Int, appData, appDataHash string) map[string]interface{} {
	req := map[string]interface{}{
		"sellToken":           usdcAddr,
		"buyToken":            nativeToken,
		"receiver":            from.Hex(),
		"sellAmountBeforeFee": sellAmount.String(),
		"kind":                "sell",
		"from":                from.Hex(),
		"appData":             appData,
		"appDataHash":         appDataHash,
		"signingScheme":       "eip712",
	}

	body, _ := json.Marshal(req)
	log.Printf("Quote request:\n%s", string(body))

	resp, err := httpClient.Post(cowAPIBase+"/quote", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("quote request: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		log.Fatalf("quote API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	return result
}

func signOrder(qr map[string]interface{}, privateKey *ecdsa.PrivateKey) string {
	q := qr["quote"].(map[string]interface{})

	// Use appDataHash (bytes32) for EIP-712 signing, NOT appData (which is the full JSON)
	appDataHash := q["appDataHash"].(string)
	log.Printf("Signing with appDataHash (bytes32): %s", appDataHash)

	validToFloat := q["validTo"].(float64)

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
			ChainId:           math.NewHexOrDecimal256(chainID),
			VerifyingContract: settlement,
		},
		Message: apitypes.TypedDataMessage{
			"sellToken":         q["sellToken"].(string),
			"buyToken":          q["buyToken"].(string),
			"receiver":          q["receiver"].(string),
			"sellAmount":        q["sellAmount"].(string),
			"buyAmount":         q["buyAmount"].(string),
			"validTo":           fmt.Sprintf("%d", int64(validToFloat)),
			"appData":           appDataHash,
			"feeAmount":         "0",
			"kind":              q["kind"].(string),
			"partiallyFillable": q["partiallyFillable"].(bool),
			"sellTokenBalance":  q["sellTokenBalance"].(string),
			"buyTokenBalance":   q["buyTokenBalance"].(string),
		},
	}

	domainSep, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		log.Fatalf("hash order domain: %v", err)
	}
	msgHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		log.Fatalf("hash order message: %v", err)
	}

	rawData := fmt.Sprintf("\x19\x01%s%s", string(domainSep), string(msgHash))
	digest := crypto.Keccak256Hash([]byte(rawData))

	sig, err := crypto.Sign(digest.Bytes(), privateKey)
	if err != nil {
		log.Fatalf("sign order: %v", err)
	}
	if sig[64] < 27 {
		sig[64] += 27
	}

	return fmt.Sprintf("0x%x", sig)
}

func submitOrder(qr map[string]interface{}, signature string, from common.Address, fullAppData string) string {
	q := qr["quote"].(map[string]interface{})

	validToFloat := q["validTo"].(float64)
	quoteID := int64(qr["id"].(float64))

	order := map[string]interface{}{
		"sellToken":         q["sellToken"].(string),
		"buyToken":          q["buyToken"].(string),
		"receiver":          q["receiver"].(string),
		"sellAmount":        q["sellAmount"].(string),
		"buyAmount":         q["buyAmount"].(string),
		"validTo":           int64(validToFloat),
		"appData":           fullAppData,
		"feeAmount":         "0",
		"kind":              q["kind"].(string),
		"partiallyFillable": q["partiallyFillable"].(bool),
		"sellTokenBalance":  q["sellTokenBalance"].(string),
		"buyTokenBalance":   q["buyTokenBalance"].(string),
		"signingScheme":     "eip712",
		"signature":         signature,
		"from":              from.Hex(),
		"quoteId":           quoteID,
	}

	body, _ := json.Marshal(order)
	log.Printf("Order submission:\n%s", string(body))

	resp, err := httpClient.Post(cowAPIBase+"/orders", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("submit order: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 201 {
		log.Fatalf("order API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var uid string
	json.Unmarshal(respBody, &uid)
	return uid
}

func checkStatus(orderUID string) string {
	resp, err := httpClient.Get(cowAPIBase + "/orders/" + orderUID)
	if err != nil {
		log.Fatalf("check status: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		log.Fatalf("status API returned %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	json.Unmarshal(body, &result)
	return result["status"].(string)
}
