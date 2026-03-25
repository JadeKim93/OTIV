// otiv-client connects to an OTIV instance with a single command.
// It starts the WebSocket proxy, downloads the OpenVPN config, and runs OpenVPN.
//
// Usage:
//
//	otiv-client -url ws://host:8000/vpn/<guid>
//	otiv-client -url ws://host:8000/vpn/<guid> -config ./custom.ovpn
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/otiv/client/internal/bridge"
)

func main() {
	wsURL := flag.String("url", "", "WebSocket VPN endpoint URL (required)")
	port := flag.String("port", "11194", "local proxy port")
	configPath := flag.String("config", "", "path to .ovpn file (auto-downloaded if not set)")
	flag.Parse()

	if *wsURL == "" {
		log.Fatal("-url is required")
	}

	// Start the TCP↔WSS bridge in the background.
	// proxyReady receives nil (or error) after net.Listen() completes.
	proxyAddr := "127.0.0.1:" + *port
	proxyReady := make(chan error, 1)
	go func() {
		if err := bridge.ListenAndProxy(proxyAddr, *wsURL, proxyReady); err != nil {
			log.Printf("[proxy] stopped: %v", err)
		}
	}()
	if err := <-proxyReady; err != nil {
		log.Fatalf("proxy listen: %v", err)
	}

	// Get .ovpn config
	ovpnFile := *configPath
	if ovpnFile == "" {
		log.Printf("[client] downloading config from server...")
		tmp, err := downloadConfig(*wsURL)
		if err != nil {
			log.Fatalf("download config: %v", err)
		}
		defer os.Remove(tmp)
		ovpnFile = tmp
		log.Printf("[client] config saved to %s", tmp)
	}

	// Launch OpenVPN
	cmd := exec.Command("openvpn", "--config", ovpnFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Fatalf("openvpn: %v — is openvpn installed?", err)
	}
	log.Printf("[client] openvpn started (pid %d)", cmd.Process.Pid)

	// Forward SIGINT/SIGTERM to openvpn for clean shutdown
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

// downloadConfig derives the config download URL from the WebSocket URL,
// fetches the .ovpn file from the server, and writes it to a temp file.
//
//	ws://host:8000/vpn/{id}  →  http://host:8000/api/instances/{id}/client-config
//	wss://host/vpn/{id}      →  https://host/api/instances/{id}/client-config
func downloadConfig(wsURL string) (string, error) {
	cfgURL, err := configURL(wsURL)
	if err != nil {
		return "", err
	}

	resp, err := http.Get(cfgURL)
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

func configURL(wsURL string) (string, error) {
	// wss → https, ws → http
	httpURL := strings.Replace(wsURL, "wss://", "https://", 1)
	httpURL = strings.Replace(httpURL, "ws://", "http://", 1)

	// .../vpn/{id}  →  .../api/instances/{id}/client-config
	parts := strings.SplitN(httpURL, "/vpn/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", fmt.Errorf("invalid URL format: expected .../vpn/<guid>, got %s", wsURL)
	}
	return parts[0] + "/api/instances/" + parts[1] + "/client-config", nil
}
