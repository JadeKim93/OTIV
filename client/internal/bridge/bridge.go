// Package bridge implements a TCP listener that proxies each connection to a
// WebSocket endpoint. Protocol: raw TCP bytes over binary WebSocket frames.
package bridge

import (
	"bufio"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"
)

// InsecureDialer skips TLS certificate verification.
// OTIV servers commonly use self-signed certificates.
var InsecureDialer = &websocket.Dialer{
	TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	Proxy:           http.ProxyFromEnvironment,
}

// InsecureHTTPClient skips TLS certificate verification for API calls.
var InsecureHTTPClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	},
}

// BridgeConnToWS relays bytes between an already-established net.Conn and
// WebSocket connection. Used for HTTP CONNECT proxying after the 200 response
// has already been written to conn.
func BridgeConnToWS(conn net.Conn, ws *websocket.Conn) error {
	errc := make(chan error, 2)

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					errc <- werr
					return
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()

	go func() {
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			if _, err := conn.Write(msg); err != nil {
				errc <- err
				return
			}
		}
	}()

	if err := <-errc; err != nil {
		log.Printf("[bridge] closed: %v", err)
	}
	return nil
}

// ListenAndProxy starts a TCP listener on addr and proxies every accepted
// connection to wsURL via WebSocket binary frames. Blocks until listener fails.
// If readyCh is non-nil, it receives nil after the listener is bound (or the
// bind error), so callers can wait for the port to be ready before proceeding.
func ListenAndProxy(addr, wsURL string, readyCh chan<- error) error {
	ln, err := net.Listen("tcp", addr)
	if readyCh != nil {
		readyCh <- err // nil on success, error on failure
	}
	if err != nil {
		return err
	}
	log.Printf("[proxy] listening on %s → %s", addr, wsURL)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go pipe(conn, wsURL)
	}
}

func pipe(conn net.Conn, wsURL string) {
	defer conn.Close()

	ws, _, err := InsecureDialer.Dial(wsURL, http.Header{})
	if err != nil {
		log.Printf("[proxy] ws dial: %v", err)
		return
	}
	defer ws.Close()

	log.Printf("[proxy] connected %s → %s", conn.RemoteAddr(), wsURL)

	errc := make(chan error, 2)

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					errc <- werr
					return
				}
			}
			if err != nil {
				if err == io.EOF {
					err = nil
				}
				errc <- err
				return
			}
		}
	}()

	go func() {
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			if _, err := conn.Write(msg); err != nil {
				errc <- err
				return
			}
		}
	}()

	if err := <-errc; err != nil {
		log.Printf("[proxy] closed: %v", err)
	}
}

// ServeHTTPConnect listens on addr as an HTTP CONNECT proxy.
// Each CONNECT request to dest:port is tunneled via a WebSocket to
// wsBase + "/ws-tcp?host=<dest>&port=<port>".
func ServeHTTPConnect(addr, wsBase string, ready chan<- error) error {
	ln, err := net.Listen("tcp", addr)
	if ready != nil {
		ready <- err
	}
	if err != nil {
		return err
	}
	log.Printf("[http-proxy] listening on %s → %s", addr, wsBase)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleConnect(conn, wsBase)
	}
}

func handleConnect(conn net.Conn, wsBase string) {
	defer conn.Close()

	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		log.Printf("[http-proxy] read request: %v", err)
		return
	}

	if req.Method != http.MethodConnect {
		conn.Write([]byte("HTTP/1.1 405 Method Not Allowed\r\n\r\n"))
		return
	}

	// Build relay WS URL: wsBase/ws-tcp?host=H&port=P
	host := req.Host
	// req.Host may be "host:port" or just "host" — normalise
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		// no port — assume 443
		h = host
		p = "443"
	}
	relayURL := wsBase + "/ws-tcp?host=" + url.QueryEscape(h) + "&port=" + url.QueryEscape(p)

	ws, _, err := InsecureDialer.Dial(relayURL, http.Header{})
	if err != nil {
		log.Printf("[http-proxy] ws-tcp dial %s:%s: %v", h, p, err)
		conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer ws.Close()

	conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Prepend any bytes the bufio.Reader buffered beyond the request headers
	var pconn net.Conn = conn
	if n := br.Buffered(); n > 0 {
		peeked, _ := br.Peek(n)
		pconn = &prefixConn{Conn: conn, buf: append([]byte(nil), peeked...)}
	}

	log.Printf("[http-proxy] CONNECT %s:%s", h, p)
	BridgeConnToWS(pconn, ws)
}

// prefixConn wraps a net.Conn, prepending buffered bytes before delegating reads.
type prefixConn struct {
	net.Conn
	buf []byte
	off int
}

func (c *prefixConn) Read(b []byte) (int, error) {
	if c.off < len(c.buf) {
		n := copy(b, c.buf[c.off:])
		c.off += n
		return n, nil
	}
	return c.Conn.Read(b)
}
