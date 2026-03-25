// Package bridge implements a TCP listener that proxies each connection to a
// WebSocket endpoint. Protocol: raw TCP bytes over binary WebSocket frames.
package bridge

import (
	"io"
	"log"
	"net"
	"net/http"

	"github.com/gorilla/websocket"
)

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

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{})
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
