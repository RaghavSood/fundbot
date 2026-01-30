package server

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/RaghavSood/fundbot/contracts"
	"github.com/RaghavSood/fundbot/thorchain"
)

var multicallAddr = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")

// balanceOf(address) selector = 0x70a08231
var erc20ABI abi.ABI

func init() {
	var err error
	erc20ABI, err = abi.JSON(strings.NewReader(`[{"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`))
	if err != nil {
		panic(err)
	}
}

// AddressBalance holds balance info for a single address on a single chain.
type AddressBalance struct {
	Address       string `json:"address"`
	Chain         string `json:"chain"`
	NativeBalance string `json:"native_balance"` // wei string
	USDCBalance   string `json:"usdc_balance"`   // smallest unit string
}

// FetchBalances retrieves native + USDC balances for the given addresses on all chains.
func FetchBalances(ctx context.Context, rpcClients map[string]*ethclient.Client, addresses []common.Address) ([]AddressBalance, error) {
	var results []AddressBalance

	for chainKey, rpc := range rpcClients {
		usdcAddr, ok := thorchain.USDCContracts[chainKey]
		if !ok {
			continue
		}

		balances, err := fetchChainBalances(ctx, rpc, chainKey, usdcAddr, addresses)
		if err != nil {
			return nil, fmt.Errorf("fetching %s balances: %w", chainKey, err)
		}
		results = append(results, balances...)
	}

	return results, nil
}

func fetchChainBalances(ctx context.Context, rpc *ethclient.Client, chainKey string, usdcAddr common.Address, addresses []common.Address) ([]AddressBalance, error) {
	if len(addresses) == 0 {
		return nil, nil
	}

	// Build multicall calls: for each address, getEthBalance + balanceOf(USDC)
	multicallABI, err := contracts.ContractsMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("parsing multicall ABI: %w", err)
	}

	var calls []contracts.Multicall3Call3
	for _, addr := range addresses {
		// Native balance via multicall getEthBalance
		ethBalData, err := multicallABI.Pack("getEthBalance", addr)
		if err != nil {
			return nil, fmt.Errorf("packing getEthBalance: %w", err)
		}
		calls = append(calls, contracts.Multicall3Call3{
			Target:       multicallAddr,
			AllowFailure: true,
			CallData:     ethBalData,
		})

		// USDC balance via ERC20 balanceOf
		balOfData, err := erc20ABI.Pack("balanceOf", addr)
		if err != nil {
			return nil, fmt.Errorf("packing balanceOf: %w", err)
		}
		calls = append(calls, contracts.Multicall3Call3{
			Target:       usdcAddr,
			AllowFailure: true,
			CallData:     balOfData,
		})
	}

	// Encode aggregate3 call
	callData, err := multicallABI.Pack("aggregate3", calls)
	if err != nil {
		return nil, fmt.Errorf("packing aggregate3: %w", err)
	}

	// Execute as eth_call (read-only, even though aggregate3 is payable)
	output, err := rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &multicallAddr,
		Data: callData,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("calling aggregate3: %w", err)
	}

	// Decode results
	decoded, err := multicallABI.Unpack("aggregate3", output)
	if err != nil {
		return nil, fmt.Errorf("unpacking aggregate3: %w", err)
	}

	// aggregate3 returns []Multicall3Result
	rawResults, ok := decoded[0].([]struct {
		Success    bool   `json:"success"`
		ReturnData []byte `json:"returnData"`
	})
	if !ok {
		return nil, fmt.Errorf("unexpected aggregate3 return type")
	}

	var balances []AddressBalance
	for i, addr := range addresses {
		native := big.NewInt(0)
		usdc := big.NewInt(0)

		ethIdx := i * 2
		usdcIdx := i*2 + 1

		if ethIdx < len(rawResults) && rawResults[ethIdx].Success && len(rawResults[ethIdx].ReturnData) >= 32 {
			native.SetBytes(rawResults[ethIdx].ReturnData)
		}
		if usdcIdx < len(rawResults) && rawResults[usdcIdx].Success && len(rawResults[usdcIdx].ReturnData) >= 32 {
			usdc.SetBytes(rawResults[usdcIdx].ReturnData)
		}

		balances = append(balances, AddressBalance{
			Address:       addr.Hex(),
			Chain:         chainKey,
			NativeBalance: native.String(),
			USDCBalance:   usdc.String(),
		})
	}

	return balances, nil
}
