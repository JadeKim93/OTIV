// otiv-proxy bridges a local TCP port to a remote WebSocket VPN endpoint.
// Use this when you want to manage the OpenVPN process yourself, or when
// porting the bridge to another language.
//
// Usage:
//
//	otiv-proxy -url ws://host:8000/vpn/<guid> [-port 11194]
//
// Then connect OpenVPN with:
//
//	remote 127.0.0.1 11194
//	proto tcp-client
package main

import (
	"flag"
	"log"

	"github.com/otiv/client/internal/bridge"
)

func main() {
	wsURL := flag.String("url", "", "WebSocket VPN endpoint URL (required)")
	port := flag.String("port", "11194", "local TCP port to listen on")
	flag.Parse()

	if *wsURL == "" {
		log.Fatal("-url is required")
	}

	if err := bridge.ListenAndProxy("127.0.0.1:"+*port, *wsURL, nil); err != nil {
		log.Fatal(err)
	}
}
