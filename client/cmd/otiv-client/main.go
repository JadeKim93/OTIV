// otiv-client — OTIV VPN client CLI
//
// Subcommands:
//
//	otiv-client connect <ws-url> [-port 11194] [-config file.ovpn] [-f config.yaml]
//	otiv-client connect <host:port>  [-url ws-url] [-config file.ovpn]  (attach to running proxy)
//	otiv-client proxy   <ws-url> [-port 11194] [-f config.yaml]
//	otiv-client dns list  <ws-url>
//	otiv-client dns apply <ws-url> [-interface tun0] [-dns 10.X.0.1]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/otiv/client/internal/bridge"
)

// ── YAML config ───────────────────────────────────────────────────────────────

type fileConfig struct {
	URL       string `yaml:"url"`
	Port      string `yaml:"port"`
	Config    string `yaml:"config"`
	Interface string `yaml:"interface"`
	DNS       string `yaml:"dns"`
}

func loadFileConfig(path string) (*fileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	return &fc, nil
}

// ── URL helpers ───────────────────────────────────────────────────────────────

// normalizeWSURL ensures the URL has a ws:// or wss:// scheme.
// Accepts: wss://, ws://, https://, http://, or bare host (no scheme).
func normalizeWSURL(raw string) string {
	switch {
	case strings.HasPrefix(raw, "wss://"), strings.HasPrefix(raw, "ws://"):
		return raw
	case strings.HasPrefix(raw, "https://"):
		return "wss://" + strings.TrimPrefix(raw, "https://")
	case strings.HasPrefix(raw, "http://"):
		return "ws://" + strings.TrimPrefix(raw, "http://")
	default:
		// No scheme: assume wss if port 443, otherwise ws.
		// Simplest: default to wss for bare hostnames (production default).
		return "wss://" + raw
	}
}

func wsToHTTP(wsURL string) string {
	s := strings.Replace(wsURL, "wss://", "https://", 1)
	return strings.Replace(s, "ws://", "http://", 1)
}

func instanceID(wsURL string) (string, error) {
	parts := strings.SplitN(wsURL, "/vpn/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", fmt.Errorf("invalid URL: expected .../vpn/<guid>, got %s", wsURL)
	}
	return strings.TrimSuffix(parts[1], "/"), nil
}

func baseURL(wsURL string) (string, error) {
	h := wsToHTTP(wsURL)
	parts := strings.SplitN(h, "/vpn/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid URL: expected .../vpn/<guid>, got %s", wsURL)
	}
	return parts[0], nil
}

// ── API types ─────────────────────────────────────────────────────────────────

type vpnClient struct {
	CommonName  string `json:"common_name"`
	VirtualIP   string `json:"virtual_ip"`
	Hostname    string `json:"hostname"`
	BytesSent   int64  `json:"bytes_sent"`
	BytesRecv   int64  `json:"bytes_recv"`
	ConnectedAt string `json:"connected_at"`
}

type instance struct {
	ID      string `json:"id"`
	Subnet  string `json:"subnet"`
	Status  string `json:"status"`
	Clients []vpnClient
}

func fetchClients(wsURL string) ([]vpnClient, error) {
	id, err := instanceID(wsURL)
	if err != nil {
		return nil, err
	}
	base, err := baseURL(wsURL)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/api/instances/%s/clients", base, id)
	resp, err := bridge.InsecureHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}
	var clients []vpnClient
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return clients, nil
}

func fetchInstance(wsURL string) (*instance, error) {
	id, err := instanceID(wsURL)
	if err != nil {
		return nil, err
	}
	base, err := baseURL(wsURL)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/api/instances", base)
	resp, err := bridge.InsecureHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}
	var instances []instance
	if err := json.NewDecoder(resp.Body).Decode(&instances); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	for i := range instances {
		if instances[i].ID == id {
			return &instances[i], nil
		}
	}
	return nil, fmt.Errorf("instance %s not found", id)
}

func serverDNSIP(subnet string) string {
	// subnet may be "10.8.0.0" or "10.8.0" — always return x.x.x.1
	parts := strings.Split(subnet, ".")
	if len(parts) >= 3 {
		return strings.Join(parts[:3], ".") + ".1"
	}
	return subnet + ".1"
}

// ── connect ───────────────────────────────────────────────────────────────────

func cmdConnect(args []string, fc *fileConfig) {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	port := fs.String("port", "11194", "local proxy port")
	configPath := fs.String("config", "", "path to .ovpn file (auto-downloaded if not set)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: otiv-client connect <ws-url> [-port 11194] [-config file.ovpn]")
		fs.PrintDefaults()
	}
	fs.Parse(reorderArgs(args))

	wsURL := fs.Arg(0)

	if fc != nil {
		explicitly := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { explicitly[f.Name] = true })
		if !explicitly["port"] && fc.Port != "" {
			*port = fc.Port
		}
		if !explicitly["config"] && fc.Config != "" {
			*configPath = fc.Config
		}
		if wsURL == "" && fc.URL != "" {
			wsURL = fc.URL
		}
	}

	if wsURL == "" {
		log.Fatal("usage: otiv-client connect <ws-url>")
	}
	wsURL = normalizeWSURL(wsURL)

	proxyAddr := "127.0.0.1:" + *port
	proxyReady := make(chan error, 1)
	go func() {
		if err := bridge.ListenAndProxy(proxyAddr, wsURL, proxyReady); err != nil {
			log.Printf("[proxy] stopped: %v", err)
		}
	}()
	if err := <-proxyReady; err != nil {
		log.Fatalf("proxy listen: %v", err)
	}
	log.Printf("[proxy] listening on %s → %s", proxyAddr, wsURL)

	ovpnFile := *configPath
	if ovpnFile == "" {
		log.Printf("[client] downloading config from server...")
		tmp, err := downloadConfig(wsURL)
		if err != nil {
			log.Fatalf("download config: %v", err)
		}
		defer os.Remove(tmp)
		ovpnFile = tmp
		log.Printf("[client] config saved to %s", tmp)
	}

	ovpnBin := findOpenVPN()
	cmd := exec.Command(ovpnBin, "--config", ovpnFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatalf("openvpn: %v — is openvpn installed?", err)
	}
	log.Printf("[client] openvpn started (pid %d)", cmd.Process.Pid)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sig
		log.Printf("[client] signal %s — stopping openvpn", s)
		cmd.Process.Signal(syscall.SIGTERM)
	}()

	if err := cmd.Wait(); err != nil {
		log.Printf("[client] openvpn exited: %v", err)
	}
}

// ── proxy ─────────────────────────────────────────────────────────────────────
//
// Starts two local servers:
//   - VPN bridge    (--port,      default 11194): WS→TCP for OpenVPN
//   - HTTP CONNECT  (--http-port, default 8080):  HTTP CONNECT proxy via server's WS TCP relay
//
// The HTTP CONNECT proxy routes connections through ws://<server>/ws-tcp?host=H&port=P.

func cmdProxy(args []string, fc *fileConfig) {
	fs := flag.NewFlagSet("proxy", flag.ExitOnError)
	port := fs.String("port", "11194", "local VPN bridge port (for OpenVPN)")
	httpPort := fs.String("http-port", "8080", "local HTTP CONNECT proxy port (0 to disable)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: otiv-client proxy <ws-url> [-port 11194] [-http-port 8080]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "OpenVPN remote:  127.0.0.1 <port>  proto tcp-client")
		fmt.Fprintln(os.Stderr, "HTTP proxy:      http://127.0.0.1:<http-port>  (CONNECT only)")
		fs.PrintDefaults()
	}
	fs.Parse(reorderArgs(args))

	wsURL := fs.Arg(0)

	if fc != nil {
		explicitly := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { explicitly[f.Name] = true })
		if wsURL == "" && fc.URL != "" {
			wsURL = fc.URL
		}
		if !explicitly["port"] && fc.Port != "" {
			*port = fc.Port
		}
	}

	if wsURL == "" {
		log.Fatal("usage: otiv-client proxy <ws-url>")
	}
	wsURL = normalizeWSURL(wsURL)

	// Derive the server's WS base URL: ws://host:port  (strip /vpn/<guid>)
	wsBase := wsBaseURL(wsURL)

	// Start VPN bridge (for OpenVPN direct connection)
	vpnAddr := "127.0.0.1:" + *port
	vpnReady := make(chan error, 1)
	go func() {
		if err := bridge.ListenAndProxy(vpnAddr, wsURL, vpnReady); err != nil {
			log.Printf("[proxy] stopped: %v", err)
		}
	}()
	if err := <-vpnReady; err != nil {
		log.Fatalf("vpn bridge listen: %v", err)
	}
	log.Printf("[proxy] VPN bridge  %s → %s", vpnAddr, wsURL)

	// Start HTTP CONNECT proxy
	if *httpPort != "0" {
		httpAddr := "127.0.0.1:" + *httpPort
		httpReady := make(chan error, 1)
		go func() {
			if err := bridge.ServeHTTPConnect(httpAddr, wsBase, httpReady); err != nil {
				log.Printf("[http-proxy] stopped: %v", err)
			}
		}()
		if err := <-httpReady; err != nil {
			log.Fatalf("http proxy listen: %v", err)
		}
	}

	// Block forever (both servers run as goroutines)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	s := <-sig
	log.Printf("[proxy] signal %s — shutting down", s)
}

// wsBaseURL returns the WS base (scheme + host) from a full ws-vpn URL.
// e.g. ws://host:8000/vpn/<guid>  →  ws://host:8000
func wsBaseURL(wsURL string) string {
	// Strip path starting at /vpn/
	if idx := strings.Index(wsURL, "/vpn/"); idx != -1 {
		return wsURL[:idx]
	}
	// Fallback: strip everything after host
	parts := strings.SplitN(wsURL, "/", 4)
	if len(parts) >= 3 {
		return parts[0] + "//" + parts[2]
	}
	return wsURL
}

// ── dns list ──────────────────────────────────────────────────────────────────

func cmdDNSList(args []string, fc *fileConfig) {
	fs := flag.NewFlagSet("dns list", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: otiv-client dns list <ws-url>")
	}
	fs.Parse(reorderArgs(args))

	wsURL := fs.Arg(0)
	if wsURL == "" && fc != nil {
		wsURL = fc.URL
	}
	if wsURL == "" {
		log.Fatal("usage: otiv-client dns list <ws-url>")
	}
	wsURL = normalizeWSURL(wsURL)

	clients, err := fetchClients(wsURL)
	if err != nil {
		log.Fatalf("fetch clients: %v", err)
	}

	if len(clients) == 0 {
		fmt.Println("No clients connected.")
		return
	}

	fmt.Printf("%-20s  %-15s  %s\n", "HOSTNAME", "VIRTUAL IP", "COMMON NAME")
	fmt.Println(strings.Repeat("-", 60))
	for _, c := range clients {
		hostname := c.Hostname
		if hostname == "" {
			hostname = "(unnamed)"
		}
		fmt.Printf("%-20s  %-15s  %s\n", hostname, c.VirtualIP, c.CommonName)
	}
}

// ── dns apply ─────────────────────────────────────────────────────────────────

func cmdDNSApply(args []string, fc *fileConfig) {
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "error: dns apply is not supported on Windows.")
		fmt.Fprintln(os.Stderr, "DNS를 수동으로 설정하세요: 네트워크 어댑터 설정 → OpenVPN TAP 어댑터 → DNS 서버 주소 입력")
		os.Exit(1)
	}

	fs := flag.NewFlagSet("dns apply", flag.ExitOnError)
	iface := fs.String("interface", "", "tunnel interface (auto-detected if empty)")
	dnsIP := fs.String("dns", "", "DNS server IP (auto-derived from server if empty)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: otiv-client dns apply <ws-url> [-interface tun0] [-dns 10.X.0.1]")
		fs.PrintDefaults()
	}
	fs.Parse(reorderArgs(args))

	wsURL := fs.Arg(0)

	if fc != nil {
		explicitly := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { explicitly[f.Name] = true })
		if wsURL == "" && fc.URL != "" {
			wsURL = fc.URL
		}
		if !explicitly["interface"] && fc.Interface != "" {
			*iface = fc.Interface
		}
		if !explicitly["dns"] && fc.DNS != "" {
			*dnsIP = fc.DNS
		}
	}

	if wsURL == "" {
		log.Fatal("usage: otiv-client dns apply <ws-url>")
	}
	wsURL = normalizeWSURL(wsURL)

	if *dnsIP == "" {
		inst, err := fetchInstance(wsURL)
		if err != nil {
			log.Fatalf("fetch instance: %v", err)
		}
		*dnsIP = serverDNSIP(inst.Subnet)
		log.Printf("[dns] server DNS IP: %s (subnet %s)", *dnsIP, inst.Subnet)
	}

	if *iface == "" {
		detected, err := detectTunInterface()
		if err != nil {
			log.Fatalf("detect tun interface: %v\n  → pass -interface <tun0> explicitly", err)
		}
		*iface = detected
		log.Printf("[dns] detected tunnel interface: %s", *iface)
	}

	if err := applyDNS(*iface, *dnsIP); err != nil {
		log.Fatalf("apply DNS: %v", err)
	}
	fmt.Printf("DNS %s applied to interface %s\n", *dnsIP, *iface)
}

func detectTunInterface() (string, error) {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return "", fmt.Errorf("read /sys/class/net: %w", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "tun") {
			return e.Name(), nil
		}
	}
	return "", fmt.Errorf("no tun interface found (is openvpn running?)")
}

// isValidInterface checks that iface contains only characters legal in Linux
// interface names and is within the IFNAMSIZ-1 (15) character limit.
func isValidInterface(iface string) bool {
	if len(iface) == 0 || len(iface) > 15 {
		return false
	}
	for _, r := range iface {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == ':') {
			return false
		}
	}
	return true
}

func applyDNS(iface, dnsIP string) error {
	if !isValidInterface(iface) {
		return fmt.Errorf("invalid interface name: %q", iface)
	}

	// resolvectl — preferred (systemd >= 239, replaces systemd-resolve)
	if _, err := exec.LookPath("resolvectl"); err == nil {
		if out, err := exec.Command("resolvectl", "dns", iface, dnsIP).CombinedOutput(); err != nil {
			return fmt.Errorf("resolvectl dns: %w\n%s", err, out)
		}
		// ~vpn.local = routing domain (routes *.vpn.local queries to this DNS)
		// vpn.local  = search domain  (ping hostname resolves as hostname.vpn.local)
		if out, err := exec.Command("resolvectl", "domain", iface, "~vpn.local", "vpn.local").CombinedOutput(); err != nil {
			return fmt.Errorf("resolvectl domain: %w\n%s", err, out)
		}
		// Flush cached answers so new DNS takes effect immediately.
		_ = exec.Command("resolvectl", "flush-caches").Run()
		printDNSVerify(iface, dnsIP)
		return nil
	}

	// systemd-resolve — older API fallback
	if _, err := exec.LookPath("systemd-resolve"); err == nil {
		cmd := exec.Command("systemd-resolve",
			"--interface="+iface,
			"--set-dns="+dnsIP,
			"--set-domain=~vpn.local", // routing domain
			"--set-domain=vpn.local",  // search domain
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("systemd-resolve: %w", err)
		}
		_ = exec.Command("resolvectl", "flush-caches").Run()
		printDNSVerify(iface, dnsIP)
		return nil
	}

	// resolvconf fallback
	if _, err := exec.LookPath("resolvconf"); err == nil {
		input := fmt.Sprintf("nameserver %s\nsearch vpn.local\n", dnsIP)
		cmd := exec.Command("resolvconf", "-a", iface+".openvpn", "-m", "0")
		cmd.Stdin = strings.NewReader(input)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("resolvconf: %w", err)
		}
		printDNSVerify(iface, dnsIP)
		return nil
	}

	fmt.Fprintf(os.Stderr, "resolvectl / systemd-resolve / resolvconf not found.\n")
	fmt.Fprintf(os.Stderr, "Apply DNS manually — add to /etc/resolv.conf:\n")
	fmt.Fprintf(os.Stderr, "  nameserver %s\n  search vpn.local\n", dnsIP)
	return fmt.Errorf("no DNS configuration tool available")
}

func printDNSVerify(iface, dnsIP string) {
	fmt.Printf("\nVerify with:\n")
	fmt.Printf("  resolvectl status %s\n", iface)
	fmt.Printf("  dig @%s <hostname>.vpn.local\n", dnsIP)
}

// reorderArgs moves flag-like arguments (starting with "-") to the front so that
// flag.FlagSet.Parse can handle them even when a positional argument appears first.
// e.g. ["ws://...", "-port", "11195"]  →  ["-port", "11195", "ws://..."]
func reorderArgs(args []string) []string {
	var flagArgs, posArgs []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flagArgs = append(flagArgs, args[i])
			// If it's "-flag value" style (not "-flag=value"), include the value too.
			if !strings.Contains(args[i], "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		} else {
			posArgs = append(posArgs, args[i])
		}
	}
	return append(flagArgs, posArgs...)
}

// ── main ──────────────────────────────────────────────────────────────────────

func usage() {
	fmt.Fprintf(os.Stderr, `otiv-client — OTIV VPN client

Usage:
  otiv-client connect <ws-url> [-port 11194] [-config file.ovpn] [-f config.yaml]
  otiv-client proxy   <ws-url> [-port 11194] [-http-port 8080] [-f config.yaml]
  otiv-client dns list  <ws-url>
  otiv-client dns apply <ws-url> [-interface tun0] [-dns 10.X.0.1]

Global flags:
  -f <path>   YAML config file (url, port, config, interface, dns)

Examples:
  # All-in-one: internal proxy + openvpn
  sudo otiv-client connect ws://host:8000/vpn/<guid>

  # Proxy server: VPN bridge (port 11194) + HTTP CONNECT proxy (port 8080)
  otiv-client proxy ws://host:8000/vpn/<guid>
  # Then configure OpenVPN:  remote 127.0.0.1 11194
  # Then configure HTTP proxy: http://127.0.0.1:8080

  # DNS
  otiv-client dns list  ws://host:8000/vpn/<guid>
  sudo otiv-client dns apply ws://host:8000/vpn/<guid>
`)
}

func main() {
	var filePath string
	var remaining []string
	for i := 0; i < len(os.Args[1:]); i++ {
		arg := os.Args[1+i]
		if arg == "-f" || arg == "--f" {
			if i+1 < len(os.Args[1:]) {
				i++
				filePath = os.Args[1+i]
			}
		} else if strings.HasPrefix(arg, "-f=") {
			filePath = strings.TrimPrefix(arg, "-f=")
		} else if strings.HasPrefix(arg, "--f=") {
			filePath = strings.TrimPrefix(arg, "--f=")
		} else {
			remaining = append(remaining, arg)
		}
	}

	var fc *fileConfig
	if filePath != "" {
		var err error
		fc, err = loadFileConfig(filePath)
		if err != nil {
			log.Fatalf("config file: %v", err)
		}
	}

	if len(remaining) == 0 {
		usage()
		os.Exit(1)
	}

	switch remaining[0] {
	case "connect":
		cmdConnect(remaining[1:], fc)
	case "proxy":
		cmdProxy(remaining[1:], fc)
	case "dns":
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: otiv-client dns <list|apply> ...")
			os.Exit(1)
		}
		switch remaining[1] {
		case "list":
			cmdDNSList(remaining[2:], fc)
		case "apply":
			cmdDNSApply(remaining[2:], fc)
		default:
			fmt.Fprintf(os.Stderr, "unknown dns subcommand: %s\n", remaining[1])
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", remaining[0])
		usage()
		os.Exit(1)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func downloadConfig(wsURL string) (string, error) {
	cfgURL, err := configURL(wsURL)
	if err != nil {
		return "", err
	}
	resp, err := bridge.InsecureHTTPClient.Get(cfgURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned %s", resp.Status)
	}
	f, err := os.CreateTemp("", "otiv-*.ovpn")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// findOpenVPN returns the path to the openvpn binary.
// On Windows it falls back to well-known installation directories when openvpn
// is not on PATH, so the user does not need to configure environment variables.
func findOpenVPN() string {
	if path, err := exec.LookPath("openvpn"); err == nil {
		return path
	}
	candidates := []string{
		`C:\Program Files\OpenVPN\bin\openvpn.exe`,
		`C:\Program Files (x86)\OpenVPN\bin\openvpn.exe`,
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "openvpn" // let exec.Command produce a clear error
}

func configURL(wsURL string) (string, error) {
	httpURL := wsToHTTP(wsURL)
	parts := strings.SplitN(httpURL, "/vpn/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", fmt.Errorf("invalid URL format: expected .../vpn/<guid>, got %s", wsURL)
	}
	return parts[0] + "/api/instances/" + parts[1] + "/client-config", nil
}
