package intents

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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

// versionedMagicPrefix marks a versioned expirable nonce (first 4 bytes of
// sha256(<versioned_nonce>) per the defuse intents SDK).
var versionedMagicPrefix = []byte{0x56, 0x28, 0xf6, 0xc6}

// buildVersionedNonce builds the 32-byte base64 nonce the 1-Click auth
// endpoint validates. Layout (from defuse-protocol/sdk-monorepo
// expirable-nonce.ts):
//
//	MAGIC(4) | VERSION(1=0) | salt(4) | deadline_ns(u64 LE, 8) |
//	  [ startTime_ns(u64 LE, 8) | random(7) ]   // 15-byte "random" section
//
// deadline and startTime are milliseconds*1e6 (nanoseconds). The auth endpoint
// reads the embedded start timestamp out of the nonce for its "timestamp
// validation"; a plain random nonce fails that check.
func buildVersionedNonce(deadline, startTime time.Time, salt []byte) (string, error) {
	if len(salt) != 4 {
		return "", fmt.Errorf("salt must be 4 bytes, got %d", len(salt))
	}
	random7 := make([]byte, 7)
	if _, err := rand.Read(random7); err != nil {
		return "", fmt.Errorf("generating nonce randomness: %w", err)
	}

	buf := make([]byte, 32)
	copy(buf[0:4], versionedMagicPrefix)
	buf[4] = 0 // version
	copy(buf[5:9], salt)
	binary.LittleEndian.PutUint64(buf[9:17], uint64(deadline.UnixNano()))
	binary.LittleEndian.PutUint64(buf[17:25], uint64(startTime.UnixNano()))
	copy(buf[25:32], random7)

	return base64.StdEncoding.EncodeToString(buf), nil
}

// authSalt is the 4-byte nonce salt. For the standard flow the salt is fetched
// from the verifier contract; the auth challenge only validates the embedded
// timestamp, not the salt, so a zero salt is used here. Overridable via
// INTENTS_NONCE_SALT (8 hex chars) if a specific salt is ever required.
func authSalt() []byte {
	if h := os.Getenv("INTENTS_NONCE_SALT"); len(h) == 8 {
		if b, err := hex.DecodeString(h); err == nil {
			return b
		}
	}
	return make([]byte, 4)
}

// buildAuthPayload constructs the empty-intents payload used to prove key
// ownership to /v0/auth/authenticate. The nonce embeds the current timestamp
// (required by the endpoint's timestamp validation) and the deadline; both are
// signed as part of the ERC-191 message.
func buildAuthPayload(signerID string) (string, error) {
	// Truncate to millisecond precision: the deadline string uses JS
	// Date.toISOString() semantics (always 3-digit millis), and the nonce's
	// embedded timestamps must agree with it.
	now := time.Now().UTC().Truncate(time.Millisecond)
	deadline := now.Add(2 * time.Minute)

	nonce, err := buildVersionedNonce(deadline, now, authSalt())
	if err != nil {
		return "", err
	}

	p := intentsPayload{
		SignerID:          signerID,
		VerifyingContract: VerifyingContract,
		Deadline:          deadline.Format("2006-01-02T15:04:05.000Z07:00"),
		Nonce:             nonce,
		Intents:           []interface{}{},
	}

	// The verifier canonicalizes the ERC-191 message as JSON.stringify(payload,
	// null, 2): 2-space pretty JSON with no HTML escaping. The signed bytes must
	// match exactly, or ecrecover yields the wrong address.
	return marshalCanonical(p)
}

// marshalCanonical serializes v the way JavaScript's JSON.stringify(v, null, 2)
// does: 2-space indentation and no HTML escaping (Go escapes <, >, & by
// default; JSON.stringify does not).
func marshalCanonical(v interface{}) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("marshaling canonical payload: %w", err)
	}
	// Encoder appends a trailing newline; JSON.stringify does not.
	return strings.TrimRight(buf.String(), "\n"), nil
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
