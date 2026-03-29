package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type TLSConfig struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

func (t TLSConfig) Enabled() bool {
	return t.Cert != "" && t.Key != ""
}

type Config struct {
	Port           string    `yaml:"port"`
	DataDir        string    `yaml:"data_dir"`
	HostDataDir    string    `yaml:"host_data_dir"`
	VPNNetwork     string    `yaml:"vpn_network"`
	OVPNImage      string    `yaml:"ovpn_image"`
	FrontendURL    string    `yaml:"frontend_url"`
	TLS            TLSConfig `yaml:"tls"`
	AccessPassword        string    `yaml:"access_password"`
	AdminPassword         string    `yaml:"admin_password"`
	ConnectionTimeout     int       `yaml:"connection_timeout"`      // 초 단위, 0 이하는 무제한
	MaxClientsPerInstance int       `yaml:"max_clients_per_instance"` // 0 이하는 무제한
}

// ListenAddr 는 Port 값을 ":8080" 형식으로 정규화해 반환한다.
func (c *Config) ListenAddr() string {
	if strings.HasPrefix(c.Port, ":") {
		return c.Port
	}
	return ":" + c.Port
}

// Load reads the YAML config file at path. Missing file is not an error —
// defaults are used instead. Fields left blank in the file also use defaults.
func Load(path string) (*Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(path)
	if !os.IsNotExist(err) {
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	// Environment variable overrides YAML (useful for docker-compose sibling container path)
	if v := os.Getenv("HOST_DATA_DIR"); v != "" {
		cfg.HostDataDir = v
	}

	applyDefaults(cfg)
	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Port:           "8080",
		DataDir:        "/data",
		VPNNetwork:     "otiv_vpn_net",
		OVPNImage:      "otiv-openvpn",
		FrontendURL:    "http://frontend:5173",
		AccessPassword: "changeme",
		AdminPassword:  "adminchangeme",
	}
}

// WriteDefaults 는 기본값 config.yaml 을 path 에 생성한다.
func WriteDefaults(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	const template = `# OTIV Backend Configuration
# 이 파일을 수정하고 docker compose restart backend 로 반영한다.

# 접속 비밀번호
access_password: "changeme"

# 관리자 비밀번호 — 로고 5번 클릭 시 입력 팝업 표시
admin_password: "adminchangeme"

# 리스닝 포트 (숫자 또는 ":8080" 형식 모두 허용)
port: "8080"

# PKI 및 인스턴스 데이터 저장 경로
data_dir: /data

# 호스트에서 본 data_dir 경로 (docker-compose 환경에서는 HOST_DATA_DIR 환경변수로 자동 주입)
host_data_dir: /data

# 프론트엔드 개발 서버 URL
frontend_url: http://frontend:5173

# VPN 접속 시간제한 (초 단위, 0 이하는 무제한)
connection_timeout: 0

# instance 당 최대 동시 접속 클라이언트 수 (0 이하는 무제한)
max_clients_per_instance: 0

# HTTPS 설정
# data_dir/tls/ 폴더에 cert.pem, key.pem 을 넣으면 자동으로 HTTPS 로 동작한다.
# 인증서가 없거나 유효하지 않으면 자동으로 HTTP 로 폴백한다.
# 별도 경로를 사용하려면 아래 주석을 해제한다.
# tls:
#   cert: /data/tls/cert.pem
#   key: /data/tls/key.pem
`
	if err := os.WriteFile(path, []byte(template), 0666); err != nil {
		return err
	}
	return os.Chmod(path, 0666)
}

func applyDefaults(cfg *Config) {
	d := defaults()
	if cfg.Port == "" {
		cfg.Port = d.Port
	}
	if cfg.DataDir == "" {
		cfg.DataDir = d.DataDir
	}
	if cfg.HostDataDir == "" {
		cfg.HostDataDir = cfg.DataDir
	}
	if cfg.VPNNetwork == "" {
		cfg.VPNNetwork = d.VPNNetwork
	}
	if cfg.OVPNImage == "" {
		cfg.OVPNImage = d.OVPNImage
	}
	if cfg.FrontendURL == "" {
		cfg.FrontendURL = d.FrontendURL
	}
	if cfg.AccessPassword == "" {
		cfg.AccessPassword = d.AccessPassword
	}
	if cfg.AdminPassword == "" {
		cfg.AdminPassword = d.AdminPassword
	}
}
