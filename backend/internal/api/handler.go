package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/otiv/backend/internal/proxy"
	"github.com/otiv/backend/internal/vpn"
)

type contextKey string

const roleCtxKey contextKey = "role"

type tokenStore struct {
	mu     sync.RWMutex
	tokens map[string]string // token → role ("user" or "admin")
}

func newTokenStore() *tokenStore {
	return &tokenStore{tokens: make(map[string]string)}
}

func (s *tokenStore) create(role string) string {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.tokens[token] = role
	s.mu.Unlock()
	return token
}

func (s *tokenStore) role(token string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.tokens[token]
	return r, ok
}

var upgrader = websocket.Upgrader{
	// Allow non-browser clients (CLI tools don't send Origin).
	// For browser clients, enforce same-origin to prevent CSWSH.
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	},
}

// vpnConn maps the TCP source port (used by the backend to connect to OpenVPN) to
// the real HTTP client IP. OpenVPN's management interface reports real_addr as
// "backendIP:sourcePort", so the port gives us a precise 1:1 mapping.
type vpnConn struct {
	localPort    int
	httpClientIP string
	instanceID   string
	ws           *websocket.Conn
}

type Handler struct {
	manager        *vpn.Manager
	frontendProxy  http.Handler
	accessPassword string
	adminPassword  string
	tokens         *tokenStore
	blocker        *ipBlocker
	globalTimeout  int
	connsMu        sync.RWMutex
	conns          map[int]*vpnConn // localPort → vpnConn
}

func NewHandler(manager *vpn.Manager, frontendURL, accessPassword, adminPassword, dataDir string, globalTimeout int) *Handler {
	target, err := url.Parse(frontendURL)
	if err != nil {
		log.Fatalf("invalid frontend URL: %v", err)
	}
	h := &Handler{
		manager:        manager,
		frontendProxy:  httputil.NewSingleHostReverseProxy(target),
		accessPassword: accessPassword,
		adminPassword:  adminPassword,
		tokens:         newTokenStore(),
		blocker:        newIPBlocker(dataDir),
		globalTimeout:  globalTimeout,
		conns:          make(map[int]*vpnConn),
	}
	manager.SetKickHook(h.closeWSForCN)
	return h
}

// closeWSForCN closes the WebSocket for the given instance+CN by looking up real_addr port.
func (h *Handler) closeWSForCN(instanceID, cn string) {
	inst, ok := h.manager.GetInstance(instanceID)
	if !ok {
		return
	}
	clients, err := inst.GetClients()
	if err != nil {
		return
	}
	for _, c := range clients {
		if c.CommonName != cn {
			continue
		}
		_, portStr, err := net.SplitHostPort(c.RealAddr)
		if err != nil {
			continue
		}
		port := 0
		fmt.Sscanf(portStr, "%d", &port)
		if port == 0 {
			continue
		}
		h.connsMu.RLock()
		conn, ok := h.conns[port]
		h.connsMu.RUnlock()
		if ok {
			conn.ws.Close()
		}
		return
	}
}

// countConns returns the number of active WebSocket connections for the given instance.
// Must NOT be called with connsMu held.
func (h *Handler) countConns(instanceID string) int {
	h.connsMu.RLock()
	defer h.connsMu.RUnlock()
	n := 0
	for _, c := range h.conns {
		if c.instanceID == instanceID {
			n++
		}
	}
	return n
}

// fillHTTPClientIPs replaces RealAddr in each client with the real HTTP client IP.
// OpenVPN's real_addr is "backendContainerIP:sourcePort"; we look up the source port
// in our map to find the original HTTP client IP.
func (h *Handler) fillHTTPClientIPs(clients []vpn.VPNClient) []vpn.VPNClient {
	h.connsMu.RLock()
	defer h.connsMu.RUnlock()
	for i, c := range clients {
		// real_addr format: "ip:port" or "[ipv6]:port"
		_, portStr, err := net.SplitHostPort(c.RealAddr)
		if err != nil {
			continue
		}
		port := 0
		fmt.Sscanf(portStr, "%d", &port)
		if port == 0 {
			continue
		}
		if conn, ok := h.conns[port]; ok {
			clients[i].RealAddr = conn.httpClientIP
		}
	}
	return clients
}

func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()

	// IP 차단 미들웨어 — 최상위에 적용
	r.Use(h.blockMiddleware)

	// Public: auth endpoint (no token required)
	r.Post("/api/auth", h.login)

	// client-config 는 CLI 툴이 토큰 없이 호출 — UUID 가 접근 제어 역할
	r.Get("/api/instances/{id}/client-config", h.downloadClientConfig)

	// Protected API routes
	r.Group(func(r chi.Router) {
		r.Use(h.authMiddleware)

		r.Route("/api/instances", func(r chi.Router) {
			r.Get("/", h.listInstances)
			r.With(h.adminMiddleware).Post("/", h.createInstance) // admin only
			r.Delete("/{id}", h.deleteInstance)
			r.Post("/{id}/stop", h.stopInstance)
			r.Post("/{id}/start", h.startInstance)
			r.Get("/{id}/clients", h.getClients)
			r.With(h.adminMiddleware).Post("/{id}/clients/{cn}/kick", h.kickClient)            // admin only
			r.With(h.adminMiddleware).Put("/{id}/clients/{cn}/timeout", h.setClientTimeout)  // admin only
			r.With(h.adminMiddleware).Put("/{id}/max-clients", h.setMaxClients)              // admin only
			r.Put("/{id}/hostnames/{cn}", h.setHostname)
		})

		// 차단 IP 관리 — admin only
		r.Route("/api/blocked", func(r chi.Router) {
			r.Use(h.adminMiddleware)
			r.Get("/", h.listBlocked)
			r.Post("/", h.blockIP)
			r.Delete("/{ip}", h.unblockIP)
		})
	})

	// WebSocket VPN proxy endpoint (non-browser clients, no auth header possible)
	r.Get("/vpn/{id}", h.vpnProxy)

	// Generic TCP relay over WebSocket (used by otiv-client proxy HTTP CONNECT)
	r.Get("/ws-tcp", h.wsTCPRelay)

	// Client binary downloads
	r.Get("/download/{file}", h.serveDownload)

	// Frontend reverse proxy (catch-all)
	r.Handle("/*", h.frontendProxy)

	return r
}

func extractBearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if strings.HasPrefix(v, "Bearer ") {
		return strings.TrimPrefix(v, "Bearer ")
	}
	return ""
}

func (h *Handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearer(r)
		if token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		role, ok := h.tokens.role(token)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), roleCtxKey, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handler) adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, _ := r.Context().Value(roleCtxKey).(string)
		if role != "admin" {
			http.Error(w, "forbidden: admin required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) blockMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractClientIP(r)
		if h.blocker.isBlocked(ip) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password == "" {
		http.Error(w, "password required", http.StatusBadRequest)
		return
	}

	ip := extractClientIP(r)

	var role string
	switch body.Password {
	case h.adminPassword:
		role = "admin"
	case h.accessPassword:
		role = "user"
	default:
		if autoBlocked := h.blocker.recordFailure(ip); autoBlocked {
			log.Printf("IP 자동 차단: %s (로그인 실패 10회)", ip)
		}
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}

	h.blocker.resetFailures(ip)
	token := h.tokens.create(role)
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "role": role})
}

func (h *Handler) listBlocked(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.blocker.list())
}

func (h *Handler) blockIP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IP string `json:"ip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IP == "" {
		http.Error(w, "ip required", http.StatusBadRequest)
		return
	}
	h.blocker.block(body.IP)
	log.Printf("IP 차단 (관리자): %s", body.IP)
	// 이미 연결된 VPN 클라이언트 즉시 kick (HTTP client IP 기준)
	go h.kickVPNClientsByHTTPIP(body.IP)
	w.WriteHeader(http.StatusNoContent)
}

// kickVPNClientsByHTTPIP kicks all VPN clients whose HTTP client IP matches.
// Uses the source-port → HTTP IP map to find the matching OpenVPN CN via real_addr.
func (h *Handler) kickVPNClientsByHTTPIP(ip string) {
	// Collect the source ports that belong to this IP
	h.connsMu.RLock()
	portSet := make(map[int]bool)
	for port, c := range h.conns {
		if c.httpClientIP == ip {
			portSet[port] = true
		}
	}
	h.connsMu.RUnlock()

	if len(portSet) == 0 {
		return
	}

	instances := h.manager.ListInstances()
	for _, inst := range instances {
		if inst.Status != "running" {
			continue
		}
		clients, err := inst.GetClients()
		if err != nil {
			continue
		}
		for _, c := range clients {
			_, portStr, err := net.SplitHostPort(c.RealAddr)
			if err != nil {
				continue
			}
			port := 0
			fmt.Sscanf(portStr, "%d", &port)
			if portSet[port] {
				log.Printf("ban-kick: %s (IP: %s, src-port: %d, instance: %s)", c.CommonName, ip, port, inst.ID[:8])
				_ = h.manager.DisableCN(inst.ID, c.CommonName)
				h.closeWSForCN(inst.ID, c.CommonName)
				_ = h.manager.KickClient(inst.ID, c.CommonName)
			}
		}
	}
}

func (h *Handler) unblockIP(w http.ResponseWriter, r *http.Request) {
	ip := chi.URLParam(r, "ip")
	h.blocker.unblock(ip)
	log.Printf("IP 차단 해제 (관리자): %s", ip)
	w.WriteHeader(http.StatusNoContent)
}

type instanceResponse struct {
	*vpn.Instance
	Clients          []vpn.VPNClient `json:"clients"`
	GlobalTimeout    int             `json:"global_timeout"`
	GlobalMaxClients int             `json:"global_max_clients"`
	ActiveConns      int             `json:"active_conns"`
}

// sanitizeClients strips sensitive fields from clients for non-admin users.
func sanitizeClients(role string, clients []vpn.VPNClient) []vpn.VPNClient {
	if role == "admin" {
		return clients
	}
	out := make([]vpn.VPNClient, len(clients))
	for i, c := range clients {
		c.RealAddr = ""
		out[i] = c
	}
	return out
}

func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request) {
	instances := h.manager.ListInstances()

	// Fetch clients in parallel
	type result struct {
		idx     int
		clients []vpn.VPNClient
	}
	results := make([]result, len(instances))
	var wg sync.WaitGroup
	for i, inst := range instances {
		wg.Add(1)
		go func(i int, inst *vpn.Instance) {
			defer wg.Done()
			clients, _ := inst.GetClients()
			results[i] = result{i, clients}
		}(i, inst)
	}
	wg.Wait()

	role, _ := r.Context().Value(roleCtxKey).(string)
	resp := make([]instanceResponse, len(instances))
	for i, inst := range instances {
		resp[i] = instanceResponse{Instance: inst, Clients: sanitizeClients(role, h.fillHTTPClientIPs(results[i].clients)), GlobalTimeout: h.globalTimeout, GlobalMaxClients: h.manager.Cfg().MaxClientsPerInstance, ActiveConns: h.countConns(inst.ID)}
		if resp[i].Clients == nil {
			resp[i].Clients = []vpn.VPNClient{}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) createInstance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}

	inst, err := h.manager.CreateInstance(r.Context(), body.Name)
	if err != nil {
		log.Printf("create instance error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, instanceResponse{Instance: inst, Clients: []vpn.VPNClient{}, GlobalTimeout: h.globalTimeout, GlobalMaxClients: h.manager.Cfg().MaxClientsPerInstance, ActiveConns: 0})
}

func (h *Handler) stopInstance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.manager.StopInstance(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) startInstance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.manager.StartInstance(r.Context(), id); err != nil {
		log.Printf("start instance error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.manager.DeleteInstance(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getClients(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	clients, err := h.manager.GetInstanceClients(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	role, _ := r.Context().Value(roleCtxKey).(string)
	writeJSON(w, http.StatusOK, sanitizeClients(role, h.fillHTTPClientIPs(clients)))
}

func (h *Handler) setHostname(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cn := chi.URLParam(r, "cn")
	var body struct {
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Hostname == "" {
		http.Error(w, "hostname required", http.StatusBadRequest)
		return
	}
	if err := h.manager.SetHostname(r.Context(), id, cn, body.Hostname); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) kickClient(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cn := chi.URLParam(r, "cn")
	// 1. CCD disable 파일 먼저 작성 — 재접속 시도가 오더라도 OpenVPN 이 즉시 거부
	if err := h.manager.DisableCN(id, cn); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 2. WebSocket close — 현재 세션 즉시 종료
	h.closeWSForCN(id, cn)
	// 3. OpenVPN management kill (fallback: WS close 로 이미 끊겼으면 "not found" → 무시)
	_ = h.manager.KickClient(id, cn)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) setMaxClients(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Max int `json:"max"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "max required", http.StatusBadRequest)
		return
	}
	if err := h.manager.SetMaxClients(id, body.Max); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) setClientTimeout(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cn := chi.URLParam(r, "cn")
	var body struct {
		Seconds int `json:"seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "seconds required", http.StatusBadRequest)
		return
	}
	if err := h.manager.SetClientTimeout(id, cn, body.Seconds); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) downloadClientConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cfg, err := h.manager.GenerateClientConfig(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/x-openvpn-profile")
	w.Header().Set("Content-Disposition", "attachment; filename=otiv-"+id[:8]+".ovpn")
	w.Write(cfg)
}

func (h *Handler) vpnProxy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	inst, ok := h.manager.GetInstance(id)
	if !ok || inst.Status != "running" {
		http.Error(w, "instance not found", http.StatusNotFound)
		return
	}

	// WebSocket 업그레이드 전에 최대 연결 수 확인
	if max := h.manager.EffectiveMaxClients(inst); max > 0 {
		if h.countConns(id) >= max {
			http.Error(w, "instance full", http.StatusServiceUnavailable)
			return
		}
	}

	vpnAddr, ok := h.manager.VPNAddr(id)
	if !ok {
		http.Error(w, "instance not found", http.StatusNotFound)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}
	defer ws.Close()

	clientIP := extractClientIP(r)

	// BridgeWSToTCP 는 TCP dial 직후 source port 를 portCh 로 전달하고 블록된다.
	// OpenVPN 관리 인터페이스의 real_addr 이 "backendContainerIP:sourcePort" 형태이므로
	// 이 포트로 HTTP 클라이언트 IP 를 정확히 1:1 매핑한다.
	portCh := make(chan int, 1)
	registered := make(chan int, 1) // 등록 완료 후 port 값 전달
	go func() {
		port := <-portCh
		if port != 0 {
			h.connsMu.Lock()
			h.conns[port] = &vpnConn{localPort: port, httpClientIP: clientIP, instanceID: id, ws: ws}
			h.connsMu.Unlock()
			log.Printf("proxy: %s → %s (client: %s, src-port: %d)", id[:8], vpnAddr, clientIP, port)
		}
		registered <- port
	}()

	if err := proxy.BridgeWSToTCP(ws, vpnAddr, portCh); err != nil {
		log.Printf("proxy done: %s: %v", id[:8], err)
	}

	// 연결 종료 후 포트 매핑 제거
	if port := <-registered; port != 0 {
		h.connsMu.Lock()
		delete(h.conns, port)
		h.connsMu.Unlock()
	}
}

// isBlockedRelayAddr returns true for loopback and link-local addresses that
// should not be reachable via the ws-tcp relay to prevent SSRF.
func isBlockedRelayAddr(host string) bool {
	ips, err := net.LookupHost(host)
	if err != nil {
		ip := net.ParseIP(host)
		if ip == nil {
			return true
		}
		ips = []string{ip.String()}
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return true
		}
	}
	return false
}

func (h *Handler) wsTCPRelay(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	port := r.URL.Query().Get("port")
	if host == "" || port == "" {
		http.Error(w, "host and port required", http.StatusBadRequest)
		return
	}

	// Validate port is a number in the valid range.
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}

	// Block loopback and link-local to prevent SSRF against the host itself.
	if isBlockedRelayAddr(host) {
		http.Error(w, "forbidden target", http.StatusForbidden)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws-tcp upgrade error: %v", err)
		return
	}
	defer ws.Close()

	target := net.JoinHostPort(host, port)
	log.Printf("ws-tcp relay: → %s", target)
	if err := proxy.BridgeWSToTCP(ws, target, nil); err != nil {
		log.Printf("ws-tcp relay done: %s: %v", target, err)
	}
}

func (h *Handler) serveDownload(w http.ResponseWriter, r *http.Request) {
	file := chi.URLParam(r, "file")
	// Sanitize: reject any path traversal attempts
	if strings.ContainsAny(file, "/\\") || strings.Contains(file, "..") {
		http.Error(w, "invalid file", http.StatusBadRequest)
		return
	}
	path := filepath.Join("/downloads", file)
	dlName := "otiv-client"
	if strings.HasSuffix(file, ".exe") {
		dlName = "otiv-client.exe"
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+dlName)
	http.ServeFile(w, r, path)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
