package config

import (
	"os"

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
	ListenAddr    string    `yaml:"listen_addr"`
	DataDir       string    `yaml:"data_dir"`
	HostDataDir   string    `yaml:"host_data_dir"` // 호스트에서 본 data_dir 경로 (사이드카 컨테이너 bind mount용)
	DockerNetwork string    `yaml:"docker_network"`
	VPNNetwork    string    `yaml:"vpn_network"`
	OVPNImage     string    `yaml:"ovpn_image"`
	FrontendURL   string    `yaml:"frontend_url"`
	TLS           TLSConfig `yaml:"tls"`
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

	// Re-apply defaults for fields that were not set in the file
	applyDefaults(cfg)
	return cfg, nil
}

func defaults() *Config {
	return &Config{
		ListenAddr:    ":8080",
		DataDir:       "/data",
		DockerNetwork: "otiv_network",
		VPNNetwork:    "otiv_vpn_net",
		OVPNImage:     "otiv-openvpn",
		FrontendURL:   "http://frontend:5173",
	}
}

func applyDefaults(cfg *Config) {
	d := defaults()
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = d.ListenAddr
	}
	if cfg.DataDir == "" {
		cfg.DataDir = d.DataDir
	}
	if cfg.HostDataDir == "" {
		cfg.HostDataDir = cfg.DataDir
	}
	if cfg.DockerNetwork == "" {
		cfg.DockerNetwork = d.DockerNetwork
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
}
