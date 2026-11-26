package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config is the root configuration loaded from config.json
type Config struct {
	Mode   string        `json:"mode"` // "server" | "client"
	Server *ServerConfig `json:"server,omitempty"`
	Client *ClientConfig `json:"client,omitempty"`
}

// ServerConfig controls the remote Secure Web Proxy
type ServerConfig struct {
	Listen     string `json:"listen"`
	HTTPListen string `json:"http_listen"`
	Domain     string `json:"domain"`
	AutoCert   bool   `json:"auto_cert"`
	CertFile   string `json:"cert_file"`
	KeyFile    string `json:"key_file"`
	Auth       AuthConfig `json:"auth"`
	Bypass     []string   `json:"bypass"`
	Upstream   *UpstreamConfig `json:"upstream,omitempty"`
}

// ClientConfig is a local proxy that tunnels through a remote socratex server
type ClientConfig struct {
	Listen   string `json:"listen"`
	Type     string `json:"type"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
}

type AuthConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
}

type UpstreamConfig struct {
	Enabled  bool   `json:"enabled"`
	Type     string `json:"type"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	if cfg.Mode == "" {
		if cfg.Server != nil {
			cfg.Mode = "server"
		} else if cfg.Client != nil {
			cfg.Mode = "client"
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	switch c.Mode {
	case "server":
		if c.Server == nil {
			return fmt.Errorf("mode=server but server section is missing")
		}
		return c.Server.Validate()
	case "client":
		if c.Client == nil {
			return fmt.Errorf("mode=client but client section is missing")
		}
		return c.Client.Validate()
	default:
		return fmt.Errorf("mode must be \"server\" or \"client\", got %q", c.Mode)
	}
}

func (s *ServerConfig) Validate() error {
	if s.Listen == "" {
		s.Listen = ":443"
	}
	if s.HTTPListen == "" {
		s.HTTPListen = ":80"
	}
	if s.Upstream != nil && s.Upstream.Enabled {
		t := strings.ToLower(s.Upstream.Type)
		if t != "socks5" && t != "http" {
			return fmt.Errorf("upstream.type must be socks5 or http")
		}
		s.Upstream.Type = t
		if s.Upstream.Address == "" || s.Upstream.Port <= 0 {
			return fmt.Errorf("upstream.address and upstream.port are required when enabled")
		}
	}
	for i, b := range s.Bypass {
		s.Bypass[i] = strings.ToUpper(strings.TrimSpace(b))
	}
	return nil
}

func (c *ClientConfig) Validate() error {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:1080"
	}
	t := strings.ToLower(c.Type)
	if t == "" {
		t = "socks5"
	}
	if t != "socks5" && t != "http" {
		return fmt.Errorf("client.type must be socks5 or http")
	}
	c.Type = t
	if c.Server == "" {
		return fmt.Errorf("client.server is required")
	}
	return nil
}

func SaveExample(path string) error {
	example := Config{
		Mode: "server",
		Server: &ServerConfig{
			Listen:     ":443",
			HTTPListen: ":80",
			Domain:     "proxy.example.com",
			AutoCert:   true,
			Auth: AuthConfig{
				Username: "alice",
				Password: "change-me",
			},
			Bypass: []string{"CN"},
			Upstream: &UpstreamConfig{
				Enabled: false,
				Type:    "socks5",
				Address: "1.2.3.4",
				Port:    1080,
			},
		},
		Client: &ClientConfig{
			Listen:   "127.0.0.1:1080",
			Type:     "socks5",
			Server:   "https://proxy.example.com:443",
			Username: "alice",
			Password: "change-me",
		},
	}
	data, err := json.MarshalIndent(example, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
