package api

import (
	"encoding/json"
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

type Handler struct {
	manager         *vpn.Manager
	frontendProxy   http.Handler
}

func NewHandler(manager *vpn.Manager, frontendURL string) *Handler {
	target, err := url.Parse(frontendURL)
	if err != nil {
		log.Fatalf("invalid frontend URL: %v", err)
	}
	return &Handler{
		manager:       manager,
		frontendProxy: httputil.NewSingleHostReverseProxy(target),
	}
}

func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()

	r.Route("/api/instances", func(r chi.Router) {
		r.Get("/", h.listInstances)
		r.Post("/", h.createInstance)
		r.Delete("/{id}", h.deleteInstance)
		r.Post("/{id}/stop", h.stopInstance)
		r.Post("/{id}/start", h.startInstance)
		r.Get("/{id}/clients", h.getClients)
		r.Post("/{id}/clients/{cn}/kick", h.kickClient)
		r.Put("/{id}/hostnames/{cn}", h.setHostname)
		r.Get("/{id}/client-config", h.downloadClientConfig)
	})

	// WebSocket VPN proxy endpoint
	r.Get("/vpn/{id}", h.vpnProxy)

	// Generic TCP relay over WebSocket (used by otiv-client proxy HTTP CONNECT)
	r.Get("/ws-tcp", h.wsTCPRelay)

	// Client binary downloads
	r.Get("/download/{file}", h.serveDownload)

	// Frontend reverse proxy (catch-all)
	r.Handle("/*", h.frontendProxy)

	return r
}

type instanceResponse struct {
	*vpn.Instance
	Clients []vpn.VPNClient `json:"clients"`
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

	resp := make([]instanceResponse, len(instances))
	for i, inst := range instances {
		resp[i] = instanceResponse{Instance: inst, Clients: results[i].clients}
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

	writeJSON(w, http.StatusCreated, instanceResponse{Instance: inst, Clients: []vpn.VPNClient{}})
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
	writeJSON(w, http.StatusOK, clients)
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
	if err := h.manager.KickClient(id, cn); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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

	log.Printf("proxy: %s → %s", id[:8], vpnAddr)
	if err := proxy.BridgeWSToTCP(ws, vpnAddr); err != nil {
		log.Printf("proxy done: %s: %v", id[:8], err)
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
	if err := proxy.BridgeWSToTCP(ws, target); err != nil {
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
