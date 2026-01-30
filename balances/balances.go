package balances

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
)

var multicallAddr = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")

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

// USDCBalance returns the USDC balance (smallest unit) for a single address on a single chain.
func USDCBalance(ctx context.Context, rpc *ethclient.Client, usdcAddr common.Address, addr common.Address) (*big.Int, error) {
	balOfData, err := erc20ABI.Pack("balanceOf", addr)
	if err != nil {
		return nil, err
	}

	output, err := rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &usdcAddr,
		Data: balOfData,
	}, nil)
	if err != nil {
		return nil, err
	}

	if len(output) < 32 {
		return big.NewInt(0), nil
	}

	bal := new(big.Int).SetBytes(output)
	return bal, nil
}

// FetchBalances retrieves native + USDC balances for the given addresses on all chains.
// usdcContracts maps chain key to USDC contract address.
func FetchBalances(ctx context.Context, rpcClients map[string]*ethclient.Client, addresses []common.Address, usdcContracts map[string]common.Address) ([]AddressBalance, error) {
	var results []AddressBalance

	for chainKey, rpc := range rpcClients {
		usdcAddr, ok := usdcContracts[chainKey]
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

	multicallABI, err := contracts.ContractsMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("parsing multicall ABI: %w", err)
	}

	var calls []contracts.Multicall3Call3
	for _, addr := range addresses {
		ethBalData, err := multicallABI.Pack("getEthBalance", addr)
		if err != nil {
			return nil, fmt.Errorf("packing getEthBalance: %w", err)
		}
		calls = append(calls, contracts.Multicall3Call3{
			Target:       multicallAddr,
			AllowFailure: true,
			CallData:     ethBalData,
		})

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

	callData, err := multicallABI.Pack("aggregate3", calls)
	if err != nil {
		return nil, fmt.Errorf("packing aggregate3: %w", err)
	}

	output, err := rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &multicallAddr,
		Data: callData,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("calling aggregate3: %w", err)
	}

	decoded, err := multicallABI.Unpack("aggregate3", output)
	if err != nil {
		return nil, fmt.Errorf("unpacking aggregate3: %w", err)
	}

	rawResults, ok := decoded[0].([]struct {
		Success    bool   `json:"success"`
		ReturnData []byte `json:"returnData"`
	})
	if !ok {
		return nil, fmt.Errorf("unexpected aggregate3 return type")
	}

	var bals []AddressBalance
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

		bals = append(bals, AddressBalance{
			Address:       addr.Hex(),
			Chain:         chainKey,
			NativeBalance: native.String(),
			USDCBalance:   usdc.String(),
		})
	}

	return bals, nil
}
