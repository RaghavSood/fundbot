package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/RaghavSood/fundbot/config"
	"github.com/RaghavSood/fundbot/db"
	"github.com/RaghavSood/fundbot/thorchain"
	"github.com/RaghavSood/fundbot/wallet"
)

//go:embed static
var staticFiles embed.FS

// session tokens (in-memory)
var (
	sessionMu     sync.RWMutex
	adminSessions = map[string]bool{}
	dashSessions  = map[string]bool{}
)

type Server struct {
	cfg        *config.Config
	store      *db.Store
	rpcClients map[string]*ethclient.Client
}

func New(cfg *config.Config, store *db.Store, rpcClients map[string]*ethclient.Client) *Server {
	return &Server{
		cfg:        cfg,
		store:      store,
		rpcClients: rpcClients,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Static files
	staticSub, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(staticSub))

	// Home page and static files
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFileFS(w, r, staticSub, "index.html")
	})
	mux.HandleFunc("/docs", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticSub, "docs.html")
	})
	mux.HandleFunc("/api/dashboard", s.withDashAuth(s.handleDashboardAPI))
	mux.HandleFunc("/api/charts", s.withDashAuth(s.handleChartsAPI))

	// Dashboard login
	mux.HandleFunc("/login", s.handleDashLogin)

	// Admin routes
	mux.HandleFunc("/admin", s.withAdminAuth(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticSub, "admin.html")
	}))
	mux.HandleFunc("/admin/login", s.handleAdminLogin)
	mux.HandleFunc("/api/admin/topups", s.withAdminAuth(s.handleAdminTopups))
	mux.HandleFunc("/api/admin/users", s.withAdminAuth(s.handleAdminUsers))
	mux.HandleFunc("/api/admin/user/", s.withAdminAuth(s.handleAdminUserDetail))
	mux.HandleFunc("/api/admin/balances", s.withAdminAuth(s.handleAdminBalances))
	mux.HandleFunc("/api/admin/export-key", s.withAdminAuth(s.handleExportKey))
	mux.HandleFunc("/api/admin/api-logs", s.withAdminAuth(s.handleAdminAPILogs))
	mux.HandleFunc("/api/admin/api-log/", s.withAdminAuth(s.handleAdminAPILogDetail))
	mux.HandleFunc("/api/explorers", s.withDashAuth(s.handleExplorers))

	addr := fmt.Sprintf(":%d", s.cfg.Port)
	log.Printf("HTTP server listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}

// --- Auth helpers ---

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashPassword(pw string) [32]byte {
	return sha256.Sum256([]byte(pw))
}

func (s *Server) withDashAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.DashboardPassword == "" {
			next(w, r)
			return
		}
		cookie, err := r.Cookie("dash_session")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		sessionMu.RLock()
		valid := dashSessions[cookie.Value]
		sessionMu.RUnlock()
		if !valid {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) withAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("admin_session")
		if err != nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		sessionMu.RLock()
		valid := adminSessions[cookie.Value]
		sessionMu.RUnlock()
		if !valid {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleDashLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		staticSub, _ := fs.Sub(staticFiles, "static")
		http.ServeFileFS(w, r, staticSub, "login.html")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseForm()
	pw := r.FormValue("password")
	expected := hashPassword(s.cfg.DashboardPassword)
	got := hashPassword(pw)
	if subtle.ConstantTimeCompare(expected[:], got[:]) != 1 {
		http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
		return
	}
	token := generateToken()
	sessionMu.Lock()
	dashSessions[token] = true
	sessionMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "dash_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		staticSub, _ := fs.Sub(staticFiles, "static")
		http.ServeFileFS(w, r, staticSub, "login.html")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseForm()
	pw := r.FormValue("password")
	expected := hashPassword(s.cfg.AdminPassword)
	got := hashPassword(pw)
	if subtle.ConstantTimeCompare(expected[:], got[:]) != 1 {
		http.Redirect(w, r, "/admin/login?error=1", http.StatusSeeOther)
		return
	}
	token := generateToken()
	sessionMu.Lock()
	adminSessions[token] = true
	sessionMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "admin_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// --- API handlers ---

func (s *Server) handleDashboardAPI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, _ := s.store.CountUsers(ctx)
	topups, _ := s.store.CountTopups(ctx)
	volume, _ := s.store.TotalVolumeUSD(ctx)
	pairs, _ := s.store.CountDistinctPairs(ctx)
	providers, _ := s.store.CountDistinctProviders(ctx)

	writeJSON(w, map[string]interface{}{
		"users":     users,
		"topups":    topups,
		"volume":    volume,
		"pairs":     pairs,
		"providers": providers,
	})
}

func (s *Server) handleAdminTopups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	topups, err := s.store.ListRecentTopups(ctx, db.ListRecentTopupsParams{Limit: limit, Offset: offset})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, topups)
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type userWithAddr struct {
		db.User
		Address string `json:"address"`
		Index   uint32 `json:"index"`
	}

	// Build lookup maps for users and chats
	userMap := make(map[int64]db.User)
	for _, u := range users {
		userMap[u.ID] = u
	}
	chatMap := make(map[int64]db.Chat)
	if s.cfg.Mode == config.ModeMulti {
		chats, err := s.store.ListChats(ctx)
		if err == nil {
			for _, c := range chats {
				chatMap[c.ID] = c
			}
		}
	}

	var result []userWithAddr
	if s.cfg.Mode == config.ModeSingle {
		addr, _ := wallet.DeriveAddress(s.cfg.Mnemonic, 0)
		result = append(result, userWithAddr{
			User:    db.User{ID: 0, Username: "(shared wallet)"},
			Address: addr.Hex(),
			Index:   0,
		})
		for _, u := range users {
			result = append(result, userWithAddr{User: u, Address: addr.Hex(), Index: 0})
		}
	} else {
		assignments, err := s.store.ListAddressAssignments(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, a := range assignments {
			idx := uint32(a.ID)
			addr, _ := wallet.DeriveAddress(s.cfg.Mnemonic, idx)
			var user db.User
			switch a.AssignedToType {
			case "user":
				if u, ok := userMap[a.AssignedToID]; ok {
					user = u
				} else {
					user = db.User{ID: a.AssignedToID, Username: "(unknown user)"}
				}
			case "chat":
				if c, ok := chatMap[a.AssignedToID]; ok {
					user = db.User{ID: c.ID, Username: fmt.Sprintf("(group: %s)", c.Title)}
				} else {
					user = db.User{ID: a.AssignedToID, Username: "(unknown chat)"}
				}
			}
			result = append(result, userWithAddr{User: user, Address: addr.Hex(), Index: idx})
		}
	}

	writeJSON(w, result)
}

func (s *Server) handleAdminUserDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// extract user ID from path /api/admin/user/{id}
	idStr := r.URL.Path[len("/api/admin/user/"):]
	userID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid user ID", http.StatusBadRequest)
		return
	}

	topups, err := s.store.GetTopupsByUserID(ctx, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, topups)
}

func (s *Server) handleAdminBalances(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type addrInfo struct {
		addr  common.Address
		owner string
	}
	var infos []addrInfo

	if s.cfg.Mode == config.ModeSingle {
		addr, err := wallet.DeriveAddress(s.cfg.Mnemonic, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		infos = append(infos, addrInfo{addr: addr, owner: "Shared Wallet"})
	} else {
		users, _ := s.store.ListUsers(ctx)
		userMap := make(map[int64]db.User)
		for _, u := range users {
			userMap[u.ID] = u
		}
		chats, _ := s.store.ListChats(ctx)
		chatMap := make(map[int64]db.Chat)
		for _, c := range chats {
			chatMap[c.ID] = c
		}

		assignments, err := s.store.ListAddressAssignments(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, a := range assignments {
			addr, err := wallet.DeriveAddress(s.cfg.Mnemonic, uint32(a.ID))
			if err != nil {
				continue
			}
			owner := "Unknown"
			switch a.AssignedToType {
			case "user":
				if u, ok := userMap[a.AssignedToID]; ok {
					if u.Username != "" {
						owner = u.Username
					} else {
						owner = fmt.Sprintf("User #%d", u.TelegramID)
					}
				}
			case "chat":
				if c, ok := chatMap[a.AssignedToID]; ok {
					owner = c.Title
				}
			}
			infos = append(infos, addrInfo{addr: addr, owner: owner})
		}
	}

	addresses := make([]common.Address, len(infos))
	for i, info := range infos {
		addresses[i] = info.addr
	}

	balances, err := FetchBalances(ctx, s.rpcClients, addresses, thorchain.USDCContracts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Build owner lookup
	ownerByAddr := make(map[string]string)
	for _, info := range infos {
		ownerByAddr[info.addr.Hex()] = info.owner
	}

	// Group balances by address
	type groupedBalance struct {
		Address       string `json:"address"`
		Owner         string `json:"owner"`
		AvaxNative    string `json:"avax_native"`
		AvaxUSDC      string `json:"avax_usdc"`
		BaseNative    string `json:"base_native"`
		BaseUSDC      string `json:"base_usdc"`
	}
	grouped := make(map[string]*groupedBalance)
	// Ensure order matches input
	var orderedAddrs []string
	for _, info := range infos {
		hex := info.addr.Hex()
		if _, ok := grouped[hex]; !ok {
			orderedAddrs = append(orderedAddrs, hex)
			grouped[hex] = &groupedBalance{Address: hex, Owner: ownerByAddr[hex], AvaxNative: "0", AvaxUSDC: "0", BaseNative: "0", BaseUSDC: "0"}
		}
	}
	for _, b := range balances {
		g, ok := grouped[b.Address]
		if !ok {
			continue
		}
		switch b.Chain {
		case "avalanche":
			g.AvaxNative = b.NativeBalance
			g.AvaxUSDC = b.USDCBalance
		case "base":
			g.BaseNative = b.NativeBalance
			g.BaseUSDC = b.USDCBalance
		}
	}

	result := make([]groupedBalance, 0, len(orderedAddrs))
	for _, addr := range orderedAddrs {
		result = append(result, *grouped[addr])
	}

	writeJSON(w, result)
}

func (s *Server) handleExportKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Index uint32 `json:"index"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	key, err := wallet.DeriveKey(s.cfg.Mnemonic, req.Index)
	if err != nil {
		http.Error(w, fmt.Sprintf("error deriving key: %v", err), http.StatusInternalServerError)
		return
	}

	addr := crypto.PubkeyToAddress(key.PublicKey)
	privHex := hex.EncodeToString(crypto.FromECDSA(key))

	writeJSON(w, map[string]string{
		"index":       fmt.Sprintf("%d", req.Index),
		"address":     addr.Hex(),
		"private_key": privHex,
	})
}

func (s *Server) handleExplorers(w http.ResponseWriter, r *http.Request) {
	// Return explorer base URLs for all known chains
	explorers := make(map[string]string)
	for _, chain := range []string{"base", "avalanche", "ethereum", "arbitrum", "polygon", "optimism", "bsc"} {
		if u := s.cfg.ExplorerBaseURL(chain); u != "" {
			explorers[chain] = u
		}
	}
	writeJSON(w, explorers)
}

func (s *Server) handleChartsAPI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	byAsset, _ := s.store.VolumeByToAsset(ctx)
	byChain, _ := s.store.VolumeByFromChain(ctx)
	byDay, _ := s.store.VolumeByDay(ctx)
	byProvider, _ := s.store.VolumeByProvider(ctx)

	writeJSON(w, map[string]interface{}{
		"volume_by_asset":    byAsset,
		"volume_by_chain":    byChain,
		"volume_by_day":      byDay,
		"volume_by_provider": byProvider,
	})
}

func (s *Server) handleAdminAPILogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	search := r.URL.Query().Get("q")
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rows, err := s.store.SearchAPIRequests(ctx, db.SearchAPIRequestsParams{
		Search: search,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	total, _ := s.store.CountAPIRequests(ctx, search)

	writeJSON(w, map[string]interface{}{
		"rows":  rows,
		"total": total,
	})
}

func (s *Server) handleAdminAPILogDetail(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/api/admin/api-log/"):]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid ID", http.StatusBadRequest)
		return
	}

	row, err := s.store.GetAPIRequest(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, row)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
