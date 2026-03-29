package proxy

import (
	"io"
	"net"

	"github.com/gorilla/websocket"
)

// BridgeWSToTCP proxies raw bytes between a WebSocket connection and a TCP address.
// Runs until either side closes the connection.
// localPort receives the TCP source port chosen by the OS (0 if unavailable); may be nil.
func BridgeWSToTCP(ws *websocket.Conn, tcpAddr string, localPort chan<- int) error {
	conn, err := net.Dial("tcp", tcpAddr)
	if err != nil {
		if localPort != nil {
			localPort <- 0
		}
		return err
	}
	defer conn.Close()

	if localPort != nil {
		if addr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
			localPort <- addr.Port
		} else {
			localPort <- 0
		}
	}

	errc := make(chan error, 2)

	// WebSocket → TCP
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

	// TCP → WebSocket
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

	<-errc
	return nil
}
