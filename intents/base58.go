package intents

import "math/big"

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// base58Encode encodes data using the Bitcoin base58 alphabet.
func base58Encode(data []byte) string {
	num := new(big.Int).SetBytes(data)
	radix := big.NewInt(58)
	zero := big.NewInt(0)
	mod := new(big.Int)

	var out []byte
	for num.Cmp(zero) > 0 {
		num.DivMod(num, radix, mod)
		out = append(out, base58Alphabet[mod.Int64()])
	}

	// Preserve leading zero bytes
	for _, b := range data {
		if b != 0 {
			break
		}
		out = append(out, base58Alphabet[0])
	}

	// Reverse
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
