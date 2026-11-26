package upstream

import (
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/chuansuoyun/socratex/internal/config"
	"golang.org/x/net/proxy"
)

// Dialer dials through an optional upstream HTTP or SOCKS5 proxy.
// If cfg is nil or disabled, it dials directly.
type Dialer struct {
	cfg    *config.UpstreamConfig
	dialer proxy.Dialer
}

func New(cfg *config.UpstreamConfig) (*Dialer, error) {
	d := &Dialer{cfg: cfg}
	if cfg == nil || !cfg.Enabled {
		d.dialer = &net.Dialer{Timeout: 15 * time.Second}
		return d, nil
	}

	addr := net.JoinHostPort(cfg.Address, fmt.Sprintf("%d", cfg.Port))
	var auth *proxy.Auth
	if cfg.Username != "" {
		auth = &proxy.Auth{
			User:     cfg.Username,
			Password: cfg.Password,
		}
	}

	switch cfg.Type {
	case "socks5":
		socks, err := proxy.SOCKS5("tcp", addr, auth, &net.Dialer{Timeout: 15 * time.Second})
		if err != nil {
			return nil, fmt.Errorf("socks5 dialer: %w", err)
		}
		d.dialer = socks
	case "http":
		u := &url.URL{
			Scheme: "http",
			Host:   addr,
		}
		if auth != nil {
			u.User = url.UserPassword(auth.User, auth.Password)
		}
		httpDialer, err := proxy.FromURL(u, &net.Dialer{Timeout: 15 * time.Second})
		if err != nil {
			return nil, fmt.Errorf("http upstream dialer: %w", err)
		}
		d.dialer = httpDialer
	default:
		return nil, fmt.Errorf("unsupported upstream type: %s", cfg.Type)
	}
	return d, nil
}

func (d *Dialer) Dial(network, address string) (net.Conn, error) {
	return d.dialer.Dial(network, address)
}

func (d *Dialer) Enabled() bool {
	return d.cfg != nil && d.cfg.Enabled
}

func (d *Dialer) String() string {
	if !d.Enabled() {
		return "direct"
	}
	return fmt.Sprintf("%s://%s:%d", d.cfg.Type, d.cfg.Address, d.cfg.Port)
}
