package wallet

import (
	"crypto/ecdsa"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
)

// DeriveKey derives an ECDSA private key from a mnemonic at the given account index.
// Path: m/44'/60'/0'/0/{index}
func DeriveKey(mnemonic string, index uint32) (*ecdsa.PrivateKey, error) {
	seed := bip39.NewSeed(mnemonic, "")

	masterKey, err := bip32.NewMasterKey(seed)
	if err != nil {
		return nil, fmt.Errorf("creating master key: %w", err)
	}

	// m/44'
	purpose, err := masterKey.NewChildKey(bip32.FirstHardenedChild + 44)
	if err != nil {
		return nil, fmt.Errorf("deriving purpose: %w", err)
	}

	// m/44'/60'
	coinType, err := purpose.NewChildKey(bip32.FirstHardenedChild + 60)
	if err != nil {
		return nil, fmt.Errorf("deriving coin type: %w", err)
	}

	// m/44'/60'/0'
	account, err := coinType.NewChildKey(bip32.FirstHardenedChild + 0)
	if err != nil {
		return nil, fmt.Errorf("deriving account: %w", err)
	}

	// m/44'/60'/0'/0
	change, err := account.NewChildKey(0)
	if err != nil {
		return nil, fmt.Errorf("deriving change: %w", err)
	}

	// m/44'/60'/0'/0/{index}
	child, err := change.NewChildKey(index)
	if err != nil {
		return nil, fmt.Errorf("deriving child %d: %w", index, err)
	}

	privateKey, err := crypto.ToECDSA(child.Key)
	if err != nil {
		return nil, fmt.Errorf("converting to ECDSA: %w", err)
	}

	return privateKey, nil
}

// DeriveAddress derives an Ethereum address from a mnemonic at the given account index.
func DeriveAddress(mnemonic string, index uint32) (common.Address, error) {
	key, err := DeriveKey(mnemonic, index)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(key.PublicKey), nil
}
