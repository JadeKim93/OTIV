package proxy

import (
	"io"
	"net"

	"github.com/gorilla/websocket"
)

// BridgeWSToTCP proxies raw bytes between a WebSocket connection and a TCP address.
// Runs until either side closes the connection.
func BridgeWSToTCP(ws *websocket.Conn, tcpAddr string) error {
	conn, err := net.Dial("tcp", tcpAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

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
