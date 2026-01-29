package thorchain

import "github.com/ethereum/go-ethereum/common"

const (
	ThornodeBaseURL = "https://thornode.ninerealms.com"

	// Thorchain asset notation for source USDC on each chain
	AVAXUSDCAsset = "AVAX.USDC-0XB97EF9EF8734C71904D8002F8B6BC66DD9C48A6E"
	BASEUSDCAsset = "BASE.USDC-0X833589FCD6EDB6E08F4C7C32D4F71B54BDA02913"
)

// USDC contract addresses per chain (checksummed)
var USDCContracts = map[string]common.Address{
	"avalanche": common.HexToAddress("0xB97EF9Ef8734C71904D8002F8B6BC66Dd9c48a6E"),
	"base":      common.HexToAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"),
}

// SourceAssets maps RPC chain key to Thorchain USDC asset notation
var SourceAssets = map[string]string{
	"avalanche": AVAXUSDCAsset,
	"base":      BASEUSDCAsset,
}

// ThorchainChainID maps RPC chain key to Thorchain chain identifier
var ThorchainChainID = map[string]string{
	"avalanche": "AVAX",
	"base":      "BASE",
}

// ChainFromThorchain maps Thorchain chain ID back to RPC key
var ChainFromThorchain = map[string]string{
	"AVAX": "avalanche",
	"BASE": "base",
}

// ERC20 ABI for approve function
const ERC20ApproveABI = `[{"inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"name":"approve","outputs":[{"name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"}]`

// Thorchain Router ABI for depositWithExpiry
const RouterDepositABI = `[{"inputs":[{"name":"vault","type":"address"},{"name":"asset","type":"address"},{"name":"amount","type":"uint256"},{"name":"memo","type":"string"},{"name":"expiry","type":"uint256"}],"name":"depositWithExpiry","outputs":[],"stateMutability":"payable","type":"function"}]`
