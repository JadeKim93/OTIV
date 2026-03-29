package vpn

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"
)

type Instance struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Subnet         string            `json:"subnet"`
	ContainerID    string            `json:"container_id,omitempty"`
	Status         string            `json:"status"`
	CreatedAt      time.Time         `json:"created_at"`
	Hostnames      map[string]string `json:"hostnames,omitempty"`       // CN → hostname
	ClientTimeouts map[string]int    `json:"client_timeouts,omitempty"` // CN → 초 (0 이하: 전역 기본값 사용)
	MaxClients     int               `json:"max_clients,omitempty"`     // 0 이하: 전역 기본값 사용
	MgmtAddr       string            `json:"-"`
}

type VPNClient struct {
	CommonName  string    `json:"common_name"`
	RealAddr    string    `json:"real_addr,omitempty"`
	VirtualIP   string    `json:"virtual_ip"`
	ConnectedAt time.Time `json:"connected_at"`
	BytesRecv   int64     `json:"bytes_recv"`
	BytesSent   int64     `json:"bytes_sent"`
	Hostname    string    `json:"hostname,omitempty"`
}

// GetClients queries the OpenVPN management interface for connected clients.
func (i *Instance) GetClients() ([]VPNClient, error) {
	conn, err := net.DialTimeout("tcp", i.MgmtAddr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	scanner := bufio.NewScanner(conn)
	scanner.Scan() // consume greeting line

	fmt.Fprintf(conn, "status 2\n")

	var clients []VPNClient
	for scanner.Scan() {
		line := scanner.Text()
		if line == "END" {
			break
		}
		// CLIENT_LIST,cn,real_addr,virt_addr,virt_ipv6,bytes_recv,bytes_sent,connected_since,...
		if !strings.HasPrefix(line, "CLIENT_LIST,") {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 8 || parts[1] == "Common Name" {
			continue
		}
		c := VPNClient{
			CommonName: parts[1],
			RealAddr:   parts[2],
			VirtualIP:  parts[3],
		}
		fmt.Sscanf(parts[5], "%d", &c.BytesRecv)
		fmt.Sscanf(parts[6], "%d", &c.BytesSent)
		// parts[8] = unix timestamp (timezone-safe)
		if len(parts) > 8 && parts[8] != "" {
			var ts int64
			fmt.Sscanf(parts[8], "%d", &ts)
			if ts > 0 {
				c.ConnectedAt = time.Unix(ts, 0)
			}
		}
		clients = append(clients, c)
	}

	fmt.Fprintf(conn, "quit\n")
	return clients, nil
}

// KickClient sends a management interface 'kill <cn>' command to disconnect
// the client with the given Common Name.
func (i *Instance) KickClient(cn string) error {
	// Validate CN to prevent management interface injection via newlines or
	// other special characters.
	if !isValidCN(cn) {
		return fmt.Errorf("invalid client CN")
	}

	conn, err := net.DialTimeout("tcp", i.MgmtAddr, 3*time.Second)
	if err != nil {
		return fmt.Errorf("management connect: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	scanner := bufio.NewScanner(conn)
	scanner.Scan() // consume greeting

	fmt.Fprintf(conn, "kill %s\n", cn)
	scanner.Scan()
	line := scanner.Text()
	fmt.Fprintf(conn, "quit\n")

	// "not found" means the client already disconnected — CCD file was already
	// written by the caller, so future reconnects are blocked. Treat as success.
	if strings.HasPrefix(line, "ERROR") && !strings.Contains(line, "not found") {
		return fmt.Errorf("kick failed: %s", line)
	}
	return nil
}
