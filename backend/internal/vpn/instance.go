package vpn

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"
)

type Instance struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Subnet      string    `json:"subnet"`
	ContainerID string    `json:"container_id,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	MgmtAddr    string    `json:"-"`
}

type VPNClient struct {
	CommonName  string    `json:"common_name"`
	RealAddr    string    `json:"real_addr"`
	VirtualIP   string    `json:"virtual_ip"`
	ConnectedAt time.Time `json:"connected_at"`
	BytesRecv   int64     `json:"bytes_recv"`
	BytesSent   int64     `json:"bytes_sent"`
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
		if len(parts) > 7 && parts[7] != "" {
			// parts[7] = "2024-01-01 00:00:00", parts[8] = unix timestamp (숫자)
			t, _ := time.Parse("2006-01-02 15:04:05", parts[7])
			c.ConnectedAt = t
		}
		clients = append(clients, c)
	}

	fmt.Fprintf(conn, "quit\n")
	return clients, nil
}
