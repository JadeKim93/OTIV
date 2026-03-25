package vpn

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/otiv/backend/internal/config"
)

const (
	ovpnTCPPort = 1194
	mgmtPort    = 7505
)

type Manager struct {
	mu        sync.RWMutex
	instances map[string]*Instance
	docker    *client.Client
	pki       *PKI
	dataDir   string
	cfg       *config.Config
}

func NewManager(cfg *config.Config) (*Manager, error) {
	dc, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	pki, err := NewPKI(filepath.Join(cfg.DataDir, "pki"))
	if err != nil {
		return nil, fmt.Errorf("pki init: %w", err)
	}

	m := &Manager{
		instances: make(map[string]*Instance),
		docker:    dc,
		pki:       pki,
		dataDir:   cfg.DataDir,
		cfg:       cfg,
	}

	if err := m.loadInstances(); err != nil {
		return nil, fmt.Errorf("load instances: %w", err)
	}

	return m, nil
}

func (m *Manager) loadInstances() error {
	stateFile := filepath.Join(m.dataDir, "instances.json")
	data, err := os.ReadFile(stateFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var instances []*Instance
	if err := json.Unmarshal(data, &instances); err != nil {
		return err
	}

	ctx := context.Background()
	for _, inst := range instances {
		inst.MgmtAddr = m.containerMgmtAddr(inst.ID)
		inst.Status = m.reconcileStatus(ctx, inst.ContainerID)
		m.instances[inst.ID] = inst
	}
	return nil
}

// reconcileStatus checks the actual Docker container state and returns the
// correct status string. Called on startup to sync JSON state with reality.
func (m *Manager) reconcileStatus(ctx context.Context, containerID string) string {
	if containerID == "" {
		return "stopped"
	}
	info, err := m.docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return "stopped" // container removed or not found
	}
	if info.State.Running {
		return "running"
	}
	return "stopped"
}

func (m *Manager) saveInstances() error {
	instances := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		instances = append(instances, inst)
	}
	data, err := json.Marshal(instances)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.dataDir, "instances.json"), data, 0644)
}

func (m *Manager) CreateInstance(ctx context.Context, name string) (*Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := uuid.New().String()
	subnet := m.allocateSubnet()
	containerName := m.containerName(id)

	// Generate certs
	serverCert, err := m.pki.GenerateServerCert("server-" + id[:8])
	if err != nil {
		return nil, fmt.Errorf("generate server cert: %w", err)
	}
	caCert, err := m.pki.CACertPEM()
	if err != nil {
		return nil, fmt.Errorf("ca cert: %w", err)
	}

	// Write instance config files
	instanceDir := filepath.Join(m.dataDir, "instances", id)
	if err := os.MkdirAll(instanceDir, 0700); err != nil {
		return nil, err
	}

	files := map[string][]byte{
		"ca.crt":     caCert,
		"server.crt": serverCert.CertPEM,
		"server.key": serverCert.KeyPEM,
		"server.conf": []byte(fmt.Sprintf(ovpnServerConfig, subnet, mgmtPort)),
	}
	for name, data := range files {
		mode := os.FileMode(0644)
		if name == "server.key" {
			mode = 0600
		}
		if err := os.WriteFile(filepath.Join(instanceDir, name), data, mode); err != nil {
			return nil, err
		}
	}

	// Create and start Docker container
	resp, err := m.docker.ContainerCreate(ctx,
		&container.Config{
			Image:  m.cfg.OVPNImage,
			Labels: map[string]string{"com.otiv.instance": "true"},
		},
		&container.HostConfig{
			Binds:   []string{instanceDir + ":/etc/openvpn:ro"},
			CapAdd:  []string{"NET_ADMIN"},
			Sysctls: map[string]string{"net.ipv4.ip_forward": "1"},
			Resources: container.Resources{
				Devices: []container.DeviceMapping{
					{PathOnHost: "/dev/net/tun", PathInContainer: "/dev/net/tun", CgroupPermissions: "rwm"},
				},
			},
			LogConfig: container.LogConfig{
				Type: "json-file",
				Config: map[string]string{
					"max-size": "10m",
					"max-file": "10",
				},
			},
		},
		nil, nil, containerName,
	)
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}

	// OpenVPN 컨테이너는 internal 네트워크에만 연결 — 호스트로 나가는 경로 없음
	if err := m.docker.NetworkConnect(ctx, m.cfg.VPNNetwork, resp.ID, &network.EndpointSettings{
		Aliases: []string{containerName},
	}); err != nil {
		m.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("connect vpn network: %w", err)
	}

	if err := m.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		m.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("start container: %w", err)
	}

	inst := &Instance{
		ID:          id,
		Name:        name,
		Subnet:      subnet,
		ContainerID: resp.ID,
		Status:      "running",
		CreatedAt:   time.Now(),
		MgmtAddr:    m.containerMgmtAddr(id),
	}

	m.instances[id] = inst
	_ = m.saveInstances()

	return inst, nil
}

func (m *Manager) ListInstances() []*Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		result = append(result, inst)
	}
	return result
}

func (m *Manager) GetInstance(id string) (*Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[id]
	return inst, ok
}

func (m *Manager) DeleteInstance(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[id]
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}

	if inst.ContainerID != "" {
		timeout := 5
		_ = m.docker.ContainerStop(ctx, inst.ContainerID, container.StopOptions{Timeout: &timeout})
		_ = m.docker.ContainerRemove(ctx, inst.ContainerID, container.RemoveOptions{Force: true})
	}

	_ = os.RemoveAll(filepath.Join(m.dataDir, "instances", id))
	delete(m.instances, id)
	return m.saveInstances()
}

// VPNAddr returns the TCP address of the OpenVPN server for the given instance ID.
func (m *Manager) VPNAddr(id string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.instances[id]; !ok {
		return "", false
	}
	return fmt.Sprintf("%s:%d", m.containerName(id), ovpnTCPPort), true
}

func (m *Manager) GenerateClientConfig(id string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.instances[id]; !ok {
		return nil, fmt.Errorf("instance %s not found", id)
	}

	cert, err := m.pki.GenerateClientCert("client-" + uuid.New().String()[:8])
	if err != nil {
		return nil, err
	}
	ca, err := m.pki.CACertPEM()
	if err != nil {
		return nil, err
	}

	cfg := fmt.Sprintf(ovpnClientConfig, ca, cert.CertPEM, cert.KeyPEM)
	return []byte(cfg), nil
}

func (m *Manager) containerName(id string) string {
	// 전체 UUID 사용 (하이픈 제거) — 앞 8자리 truncation으로 인한 충돌 방지
	return "otiv-" + strings.ReplaceAll(id, "-", "")
}

func (m *Manager) containerMgmtAddr(id string) string {
	return fmt.Sprintf("%s:%d", m.containerName(id), mgmtPort)
}

// Shutdown stops and removes all managed OpenVPN containers. Called on process exit.
func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	timeout := 5
	for _, inst := range m.instances {
		if inst.ContainerID == "" {
			continue
		}
		log.Printf("stopping instance %s (%s)", inst.Name, inst.ID[:8])
		_ = m.docker.ContainerStop(ctx, inst.ContainerID, container.StopOptions{Timeout: &timeout})
		_ = m.docker.ContainerRemove(ctx, inst.ContainerID, container.RemoveOptions{Force: true})
	}
}

func (m *Manager) allocateSubnet() string {
	used := make(map[string]bool)
	for _, inst := range m.instances {
		used[inst.Subnet] = true
	}
	for i := 8; i < 255; i++ {
		s := fmt.Sprintf("10.%d.0.0", i)
		if !used[s] {
			return s
		}
	}
	return "10.8.0.0"
}

const ovpnServerConfig = `mode server
tls-server
proto tcp
dev tun
server %s 255.255.255.0
ca /etc/openvpn/ca.crt
cert /etc/openvpn/server.crt
key /etc/openvpn/server.key
dh none
ecdh-curve prime256v1
keepalive 10 120
persist-key
persist-tun
verb 3
management 0.0.0.0 %d
`

const ovpnClientConfig = `client
proto tcp-client
dev tun
remote 127.0.0.1 11194
nobind
persist-key
persist-tun
remote-cert-tls server
verb 3
<ca>
%s</ca>
<cert>
%s</cert>
<key>
%s</key>
`
