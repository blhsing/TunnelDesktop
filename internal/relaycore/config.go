package relaycore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	ListenAddr          string   `json:"listen_addr"`
	RawRDPAddr          string   `json:"raw_rdp_addr"`
	RawAllowlist        []string `json:"raw_allowlist"`
	DisableRawAllowlist bool     `json:"disable_raw_allowlist"`
	CAFile              string   `json:"ca_file"`
	CertFile            string   `json:"cert_file"`
	KeyFile             string   `json:"key_file"`
	CAPEM               string   `json:"ca_pem"`
	CertPEM             string   `json:"cert_pem"`
	KeyPEM              string   `json:"key_pem"`
	Token               string   `json:"token"`
	MaxHomeStreams      int      `json:"max_home_streams"`
	MaxTLSConnections   int      `json:"max_tls_connections"`
	AgentWaitTimeout    string   `json:"agent_wait_timeout"`
	GeneratedRelayAddr  string   `json:"generated_relay_addr,omitempty"`
	GeneratedServerName string   `json:"generated_server_name,omitempty"`
	GeneratedAt         string   `json:"generated_at,omitempty"`
}

func LoadConfigFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	cfg.ResolvePaths(filepath.Dir(path))
	return cfg, nil
}

func (c *Config) ApplyDefaults() {
	if c.ListenAddr == "" {
		c.ListenAddr = ":443"
	}
	if c.MaxHomeStreams <= 0 {
		c.MaxHomeStreams = 32
	}
	if c.MaxTLSConnections <= 0 {
		c.MaxTLSConnections = 128
	}
	if c.AgentWaitTimeout == "" {
		c.AgentWaitTimeout = "30s"
	}
}

func (c Config) Validate() error {
	if c.CAFile == "" && c.CAPEM == "" {
		return fmt.Errorf("ca_file or ca_pem is required")
	}
	if c.CertFile == "" && c.CertPEM == "" {
		return fmt.Errorf("cert_file or cert_pem is required")
	}
	if c.KeyFile == "" && c.KeyPEM == "" {
		return fmt.Errorf("key_file or key_pem is required")
	}
	if c.Token == "" {
		return fmt.Errorf("token is required")
	}
	if _, err := c.AgentWaitDuration(); err != nil {
		return err
	}
	return nil
}

func (c Config) AgentWaitDuration() (time.Duration, error) {
	timeout, err := time.ParseDuration(c.AgentWaitTimeout)
	if err != nil {
		return 0, fmt.Errorf("parse agent_wait_timeout: %w", err)
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("agent_wait_timeout must be positive")
	}
	return timeout, nil
}

func (c *Config) ResolvePaths(baseDir string) {
	c.CAFile = resolvePath(baseDir, c.CAFile)
	c.CertFile = resolvePath(baseDir, c.CertFile)
	c.KeyFile = resolvePath(baseDir, c.KeyFile)
}

func resolvePath(baseDir, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Clean(filepath.Join(baseDir, value))
}
