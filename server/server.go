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
	mux.HandleFunc("/api/dashboard", s.handleDashboardAPI)
	mux.HandleFunc("/api/charts", s.handleChartsAPI)

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
	var addresses []common.Address

	if s.cfg.Mode == config.ModeSingle {
		addr, err := wallet.DeriveAddress(s.cfg.Mnemonic, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		addresses = []common.Address{addr}
	} else {
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
			addresses = append(addresses, addr)
		}
	}

	balances, err := FetchBalances(ctx, s.rpcClients, addresses)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, balances)
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

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
