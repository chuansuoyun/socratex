package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chuansuoyun/socratex/internal/config"
)

type Client struct {
	cfg        *config.ClientConfig
	serverHost string
	serverName string
	serverURL  string
	authHeader string
}

func New(cfg *config.ClientConfig) (*Client, error) {
	c := &Client{cfg: cfg}
	raw := cfg.Server
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse server url: %w", err)
	}
	host := u.Host
	if u.Port() == "" {
		if u.Scheme == "http" {
			host = net.JoinHostPort(u.Hostname(), "80")
		} else {
			host = net.JoinHostPort(u.Hostname(), "443")
		}
	}
	c.serverHost = host
	c.serverName = u.Hostname()
	c.serverURL = u.Scheme + "://" + host

	if cfg.Username != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(cfg.Username + ":" + cfg.Password))
		c.authHeader = "Basic " + cred
	} else if cfg.Token != "" {
		cred := base64.StdEncoding.EncodeToString([]byte("token:" + cfg.Token))
		c.authHeader = "Basic " + cred
	}
	return c, nil
}

func (c *Client) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", c.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", c.cfg.Listen, err)
	}
	log.Printf("[client] local %s (%s) -> remote %s", c.cfg.Listen, c.cfg.Type, c.serverURL)
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				log.Printf("[client] accept: %v", err)
				continue
			}
		}
		go c.handleConn(conn)
	}
}

func (c *Client) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if c.cfg.Type == "http" {
		c.handleHTTP(conn)
		return
	}
	c.handleSOCKS5(conn)
}

func (c *Client) handleSOCKS5(conn net.Conn) {
	buf := make([]byte, 258)
	n, err := conn.Read(buf)
	if err != nil || n < 2 || buf[0] != 0x05 {
		return
	}
	_, _ = conn.Write([]byte{0x05, 0x00})

	n, err = conn.Read(buf)
	if err != nil || n < 7 || buf[0] != 0x05 || buf[1] != 0x01 {
		_, _ = conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	var host string
	var port uint16
	switch buf[3] {
	case 0x01:
		if n < 10 {
			return
		}
		host = net.IP(buf[4:8]).String()
		port = uint16(buf[8])<<8 | uint16(buf[9])
	case 0x03:
		l := int(buf[4])
		if n < 5+l+2 {
			return
		}
		host = string(buf[5 : 5+l])
		port = uint16(buf[5+l])<<8 | uint16(buf[5+l+1])
	case 0x04:
		if n < 22 {
			return
		}
		host = net.IP(buf[4:20]).String()
		port = uint16(buf[20])<<8 | uint16(buf[21])
	default:
		_, _ = conn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	dest := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	remote, err := c.tunnel(dest)
	if err != nil {
		log.Printf("[client] tunnel %s: %v", dest, err)
		_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()
	_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	_ = conn.SetDeadline(time.Time{})
	relay(conn, remote)
}

func (c *Client) handleHTTP(conn net.Conn) {
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method == http.MethodConnect {
		dest := req.Host
		if !strings.Contains(dest, ":") {
			dest += ":443"
		}
		remote, err := c.tunnel(dest)
		if err != nil {
			log.Printf("[client] tunnel %s: %v", dest, err)
			_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			return
		}
		defer remote.Close()
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		_ = conn.SetDeadline(time.Time{})
		relay(conn, remote)
		return
	}
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	remote, err := c.tunnel(host)
	if err != nil {
		log.Printf("[client] tunnel %s: %v", host, err)
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer remote.Close()
	req.RequestURI = ""
	req.Header.Del("Proxy-Connection")
	req.Header.Del("Proxy-Authorization")
	if err := req.Write(remote); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	relay(conn, remote)
}

func (c *Client) dialRemote() (net.Conn, error) {
	d := &net.Dialer{Timeout: 15 * time.Second}
	tlsCfg := &tls.Config{
		ServerName: c.serverName,
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
	}
	if conn, err := d.Dial("tcp4", c.serverHost); err == nil {
		return tls.Client(conn, tlsCfg), nil
	}
	raw, err := tls.DialWithDialer(d, "tcp", c.serverHost, tlsCfg)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) tunnel(dest string) (net.Conn, error) {
	raw, err := c.dialRemote()
	if err != nil {
		return nil, fmt.Errorf("dial remote: %w", err)
	}

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", dest, dest)
	if c.authHeader != "" {
		req += "Proxy-Authorization: " + c.authHeader + "\r\n"
	}
	req += "Proxy-Connection: Keep-Alive\r\n"
	req += "\r\n"

	if _, err := raw.Write([]byte(req)); err != nil {
		raw.Close()
		return nil, err
	}

	br := bufio.NewReader(raw)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	if resp.StatusCode != 200 {
		raw.Close()
		return nil, fmt.Errorf("remote CONNECT status %d", resp.StatusCode)
	}
	return &bufferedConn{Conn: raw, r: br}, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

func relay(a, b net.Conn) {
	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(a, b)
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(b, a)
		errCh <- err
	}()
	<-errCh
	_ = a.Close()
	_ = b.Close()
}
