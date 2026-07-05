package intents

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

// VerifyingContract is the NEAR Intents verifier contract account.
const VerifyingContract = "intents.near"

// SignerID returns the NEAR implicit account ID for an EVM key:
// the lowercase 0x-prefixed address.
func SignerID(key *ecdsa.PrivateKey) string {
	return strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
}

// signERC191 signs an arbitrary payload string per ERC-191 (personal_sign)
// and returns the signature in the verifier's expected encoding:
// "secp256k1:" + base58(r || s || v) with v in {0, 1}.
func signERC191(key *ecdsa.PrivateKey, payload string) (string, error) {
	hash := accounts.TextHash([]byte(payload))
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		return "", fmt.Errorf("signing payload: %w", err)
	}
	// crypto.Sign returns v as 0/1 already, which is what the verifier expects.
	return "secp256k1:" + base58Encode(sig), nil
}

// intentsPayload is the JSON structure signed for authentication and
// standalone intents. For 1-Click swap intents the payload string comes
// pre-built from /v0/generate-intent and is signed as-is.
type intentsPayload struct {
	SignerID           string        `json:"signer_id"`
	VerifyingContract  string        `json:"verifying_contract"`
	Deadline           string        `json:"deadline"`
	Nonce              string        `json:"nonce"`
	Intents            []interface{} `json:"intents"`
}

// buildAuthPayload constructs the empty-intents payload used to prove key
// ownership to /v0/auth/authenticate.
func buildAuthPayload(signerID string) (string, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}

	p := intentsPayload{
		SignerID:          signerID,
		VerifyingContract: VerifyingContract,
		Deadline:          time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339),
		Nonce:             base64.StdEncoding.EncodeToString(nonce),
		Intents:           []interface{}{},
	}

	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshaling auth payload: %w", err)
	}
	return string(data), nil
}

// MultiPayload is the signed-data envelope accepted by the 1-Click API
// for authentication and intent submission.
type MultiPayload struct {
	Standard  string `json:"standard"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// signedMultiPayload signs payload with the key and wraps it in the erc191 envelope.
func signedMultiPayload(key *ecdsa.PrivateKey, payload string) (*MultiPayload, error) {
	sig, err := signERC191(key, payload)
	if err != nil {
		return nil, err
	}
	return &MultiPayload{
		Standard:  "erc191",
		Payload:   payload,
		Signature: sig,
	}, nil
}
