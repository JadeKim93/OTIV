package vpn

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	dockertypes "github.com/docker/docker/api/types"
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
	timeout := 5
	for _, inst := range instances {
		// Stop and remove any existing container for this instance.
		// On restart we always spin up a fresh container so state is clean.
		if inst.ContainerID != "" {
			_ = m.docker.ContainerStop(ctx, inst.ContainerID, container.StopOptions{Timeout: &timeout})
			_ = m.docker.ContainerRemove(ctx, inst.ContainerID, container.RemoveOptions{Force: true})
			inst.ContainerID = ""
		}

		// Re-create the container using the existing config files on disk.
		if err := m.startContainer(ctx, inst); err != nil {
			log.Printf("loadInstances: failed to restart %s (%s): %v", inst.Name, inst.ID[:8], err)
			inst.Status = "stopped"
		} else {
			inst.Status = "running"
		}

		inst.MgmtAddr = m.containerMgmtAddr(inst.ID)
		m.instances[inst.ID] = inst
	}
	_ = m.saveInstances()
	return nil
}

// serverIPFromSubnet returns the VPN server IP from a subnet (e.g. "10.8.0.0" → "10.8.0.1").
func serverIPFromSubnet(subnet string) string {
	parts := strings.Split(subnet, ".")
	if len(parts) == 4 {
		parts[3] = "1"
		return strings.Join(parts, ".")
	}
	return subnet
}

// startContainer creates and starts a Docker container for inst, using the
// config files already on disk. Updates inst.ContainerID on success.
func (m *Manager) startContainer(ctx context.Context, inst *Instance) error {
	containerName := m.containerName(inst.ID)
	instanceDir := filepath.Join(m.dataDir, "instances", inst.ID)
	hostInstanceDir := filepath.Join(m.cfg.HostDataDir, "instances", inst.ID)

	// Regenerate server.conf so template changes (e.g. DNS push) take effect on restart.
	serverConf := fmt.Sprintf(ovpnServerConfig, inst.Subnet, serverIPFromSubnet(inst.Subnet), mgmtPort)
	_ = os.WriteFile(filepath.Join(instanceDir, "server.conf"), []byte(serverConf), 0644)

	// Ensure dnsmasq.hosts exists so the container can bind-mount it.
	hostsPath := filepath.Join(instanceDir, "dnsmasq.hosts")
	if _, err := os.Stat(hostsPath); os.IsNotExist(err) {
		_ = os.WriteFile(hostsPath, []byte{}, 0644)
	}

	// Ensure ccd directory exists — OpenVPN reads per-CN files here for kick/block.
	_ = os.MkdirAll(filepath.Join(instanceDir, "ccd"), 0755)

	resp, err := m.docker.ContainerCreate(ctx,
		&container.Config{
			Image:  m.cfg.OVPNImage,
			Labels: map[string]string{"com.otiv.instance": "true"},
		},
		&container.HostConfig{
			Binds:   []string{hostInstanceDir + ":/etc/openvpn:ro"},
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
		return fmt.Errorf("create container: %w", err)
	}

	if err := m.docker.NetworkConnect(ctx, m.cfg.VPNNetwork, resp.ID, &network.EndpointSettings{
		Aliases: []string{containerName},
	}); err != nil {
		_ = m.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("connect vpn network: %w", err)
	}

	if err := m.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = m.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("start container: %w", err)
	}

	inst.ContainerID = resp.ID
	return nil
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
		"ca.crt":      caCert,
		"server.crt":  serverCert.CertPEM,
		"server.key":  serverCert.KeyPEM,
		"server.conf": []byte(fmt.Sprintf(ovpnServerConfig, subnet, serverIPFromSubnet(subnet), mgmtPort)),
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

	inst := &Instance{
		ID:        id,
		Name:      name,
		Subnet:    subnet,
		Status:    "stopped",
		CreatedAt: time.Now(),
		MgmtAddr:  m.containerMgmtAddr(id),
	}

	// OpenVPN 컨테이너는 internal 네트워크에만 연결 — 호스트로 나가는 경로 없음
	if err := m.startContainer(ctx, inst); err != nil {
		return nil, err
	}
	inst.Status = "running"

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
// Returns false if the instance doesn't exist or its container is not running.
func (m *Manager) VPNAddr(id string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[id]
	if !ok || inst.Status != "running" {
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

func (m *Manager) KickClient(instanceID, cn string) error {
	m.mu.RLock()
	inst, ok := m.instances[instanceID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("instance not found")
	}

	// Write a CCD disable file so OpenVPN permanently rejects reconnection attempts.
	// The block is cleared when the instance is stopped/restarted or deleted.
	ccdPath := filepath.Join(m.dataDir, "instances", instanceID, "ccd", cn)
	if err := os.WriteFile(ccdPath, []byte("disable\n"), 0644); err != nil {
		log.Printf("kick: failed to write ccd for %s: %v", cn, err)
	}

	return inst.KickClient(cn)
}

// GetInstanceClients returns the connected clients for an instance, auto-assigning
// a friendly hostname to any new CN that doesn't have one yet.
func (m *Manager) GetInstanceClients(ctx context.Context, id string) ([]VPNClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[id]
	if !ok {
		return nil, fmt.Errorf("instance not found")
	}

	clients, _ := inst.GetClients()
	if clients == nil {
		clients = []VPNClient{}
	}

	if inst.Hostnames == nil {
		inst.Hostnames = make(map[string]string)
	}

	changed := false
	for i, c := range clients {
		if _, ok := inst.Hostnames[c.CommonName]; !ok {
			inst.Hostnames[c.CommonName] = m.uniqueFriendlyName(inst)
			changed = true
		}
		clients[i].Hostname = inst.Hostnames[c.CommonName]
	}

	if changed {
		_ = m.writeDNSHosts(inst, clients)
		m.reloadDNS(ctx, inst)
		_ = m.saveInstances()
	}

	return clients, nil
}

// sanitizeHostname replaces whitespace with hyphens and lowercases the result
// so the name is valid as a DNS label.
func sanitizeHostname(name string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' {
			return '-'
		}
		return r
	}, name))
}

// SetHostname assigns a custom hostname to a client identified by its CN.
func (m *Manager) SetHostname(ctx context.Context, instanceID, cn, hostname string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[instanceID]
	if !ok {
		return fmt.Errorf("instance not found")
	}
	if inst.Hostnames == nil {
		inst.Hostnames = make(map[string]string)
	}
	inst.Hostnames[cn] = sanitizeHostname(hostname)

	clients, _ := inst.GetClients()
	if clients == nil {
		clients = []VPNClient{}
	}
	_ = m.writeDNSHosts(inst, clients)
	m.reloadDNS(ctx, inst)
	return m.saveInstances()
}

func (m *Manager) writeDNSHosts(inst *Instance, clients []VPNClient) error {
	var sb strings.Builder
	for _, c := range clients {
		if name, ok := inst.Hostnames[c.CommonName]; ok && c.VirtualIP != "" {
			safe := sanitizeHostname(name)
			sb.WriteString(fmt.Sprintf("%s %s.vpn.local %s\n", c.VirtualIP, safe, safe))
		}
	}
	hostsPath := filepath.Join(m.dataDir, "instances", inst.ID, "dnsmasq.hosts")
	return os.WriteFile(hostsPath, []byte(sb.String()), 0644)
}

func (m *Manager) reloadDNS(ctx context.Context, inst *Instance) {
	if inst.ContainerID == "" {
		return
	}
	exec, err := m.docker.ContainerExecCreate(ctx, inst.ContainerID, dockertypes.ExecConfig{
		Cmd: []string{"sh", "-c", "[ -f /var/run/dnsmasq.pid ] && kill -HUP $(cat /var/run/dnsmasq.pid) || true"},
	})
	if err != nil {
		return
	}
	_ = m.docker.ContainerExecStart(ctx, exec.ID, dockertypes.ExecStartCheck{})
}

var adjectives = []string{"amber", "bold", "calm", "dark", "eager", "fast", "glad", "hazy", "icy", "jade", "keen", "lazy", "mild", "neat", "odd", "pale", "quick", "rare", "swift", "tame", "warm", "zany"}
var nouns = []string{"bear", "crane", "deer", "eagle", "finch", "goat", "hawk", "ibis", "jay", "kite", "lynx", "mink", "newt", "orca", "puma", "quail", "raven", "seal", "teal", "vole", "wolf", "yak"}

func friendlyName() string {
	return adjectives[rand.Intn(len(adjectives))] + "-" + nouns[rand.Intn(len(nouns))]
}

func (m *Manager) uniqueFriendlyName(inst *Instance) string {
	used := make(map[string]bool, len(inst.Hostnames))
	for _, v := range inst.Hostnames {
		used[v] = true
	}
	for range 50 {
		name := friendlyName()
		if !used[name] {
			return name
		}
	}
	return friendlyName() + "-" + uuid.New().String()[:4]
}

func (m *Manager) StopInstance(ctx context.Context, id string) error {
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
		inst.ContainerID = ""
	}
	inst.Status = "stopped"
	_ = m.saveInstances()
	return nil
}

func (m *Manager) StartInstance(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[id]
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}
	if inst.Status == "running" {
		return nil
	}
	if err := m.startContainer(ctx, inst); err != nil {
		return err
	}
	inst.Status = "running"
	_ = m.saveInstances()
	return nil
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

// ovpnServerConfig args: subnet, serverVPNIP, mgmtPort
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
client-config-dir /etc/openvpn/ccd
push "dhcp-option DNS %s"
push "dhcp-option DOMAIN vpn.local"
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
data-ciphers AES-256-GCM:AES-128-GCM
verb 3
<ca>
%s</ca>
<cert>
%s</cert>
<key>
%s</key>
`
