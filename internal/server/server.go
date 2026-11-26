package server

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chuansuoyun/socratex/internal/config"
	"github.com/chuansuoyun/socratex/internal/upstream"
	"github.com/phuslu/iploc"
	"golang.org/x/crypto/acme/autocert"
)

const (
	maxBypassHosts  = 2000
	bypassHostTTL   = 7 * 24 * time.Hour
	cleanupInterval = 30 * time.Minute
)

type Server struct {
	cfg      *config.ServerConfig
	dialer   *upstream.Dialer
	mu       sync.RWMutex
	sessions map[string]time.Time
	bypass   map[string]bool
	hosts    map[string]int64
}

func New(cfg *config.ServerConfig) (*Server, error) {
	d, err := upstream.New(cfg.Upstream)
	if err != nil {
		return nil, err
	}
	bypass := make(map[string]bool)
	for _, c := range cfg.Bypass {
		code := strings.ToUpper(strings.TrimSpace(c))
		if code != "" {
			bypass[code] = true
		}
	}
	return &Server{
		cfg:      cfg,
		dialer:   d,
		sessions: make(map[string]time.Time),
		bypass:   bypass,
		hosts:    make(map[string]int64),
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	log.Printf("[server] domain=%s listen=%s upstream=%s", s.cfg.Domain, s.cfg.Listen, s.dialer.String())
	if len(s.bypass) > 0 {
		log.Printf("[server] geoip bypass countries: %v", s.cfg.Bypass)
	}
	go s.cleanupLoop(ctx)

	tlsConfig, err := s.buildTLS()
	if err != nil {
		return err
	}

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/", s.handleWeb)
	httpHandler := http.Handler(httpMux)

	if s.cfg.AutoCert && s.cfg.Domain != "" && s.cfg.CertFile == "" {
		cacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "socratex-certs")
		_ = os.MkdirAll(cacheDir, 0700)
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(s.cfg.Domain),
			Cache:      autocert.DirCache(cacheDir),
		}
		tlsConfig = m.TLSConfig()
		tlsConfig.NextProtos = []string{"http/1.1"}
		httpHandler = m.HTTPHandler(httpMux)
	}

	httpServer := &http.Server{
		Addr:    s.cfg.HTTPListen,
		Handler: httpHandler,
	}

	// HTTPS proxy: MUST NOT use ServeMux.
	// CONNECT RequestURI is "host:port" (no leading "/"), ServeMux returns 404.
	httpsServer := &http.Server{
		Addr:      s.cfg.Listen,
		Handler:   http.HandlerFunc(s.handleProxy),
		TLSConfig: tlsConfig,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("[server] HTTP listening on %s", s.cfg.HTTPListen)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("http: %w", err)
		}
	}()
	go func() {
		log.Printf("[server] HTTPS proxy listening on %s", s.cfg.Listen)
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("https: %w", err)
		}
	}()

	s.printBanner()
	select {
	case <-ctx.Done():
		_ = httpServer.Shutdown(context.Background())
		_ = httpsServer.Shutdown(context.Background())
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) buildTLS() (*tls.Config, error) {
	cfg := &tls.Config{NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12}
	if s.cfg.CertFile != "" && s.cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.cfg.CertFile, s.cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load cert: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func (s *Server) printBanner() {
	domain := s.cfg.Domain
	if domain == "" {
		domain = "127.0.0.1"
	}
	base := "https://" + domain
	fmt.Println()
	fmt.Println("[SOCRATEX SERVER]")
	fmt.Printf("  Domain : %s\n", domain)
	fmt.Printf("  Listen : %s (HTTPS) / %s (HTTP)\n", s.cfg.Listen, s.cfg.HTTPListen)
	fmt.Printf("  Exit   : %s\n", s.dialer.String())
	if s.cfg.Auth.Token != "" {
		fmt.Printf("  PAC    : %s/proxy.pac?token=%s\n", base, s.cfg.Auth.Token)
	}
	if s.cfg.Auth.Username != "" {
		fmt.Printf("  Proxy  : https://%s:***@%s\n", s.cfg.Auth.Username, domain)
	}
	fmt.Println()
}

func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Socratex"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	switch strings.ToLower(r.URL.Path) {
	case "/", "/favicon.ico":
		w.Write([]byte("42"))
	case "/proxy.pac", "/wpad.dat":
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		w.Write([]byte(s.makePAC()))
	case "/console":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		s.mu.RLock()
		fmt.Fprintf(w, "Socratex Server\nSessions: %d\nBypass hosts: %d\nUpstream: %s\n\n",
			len(s.sessions), len(s.hosts), s.dialer.String())
		for ip, t := range s.sessions {
			fmt.Fprintf(w, "  client %s  %s\n", ip, t.Format(time.RFC3339))
		}
		for h, ts := range s.hosts {
			fmt.Fprintf(w, "  bypass %s  %s\n", h, time.Unix(ts, 0).Format(time.RFC3339))
		}
		s.mu.RUnlock()
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) makePAC() string {
	proxyLine := fmt.Sprintf("HTTPS %s:443", s.cfg.Domain)
	s.mu.RLock()
	var entries []string
	for host := range s.hosts {
		h := strings.ReplaceAll(host, "'", "\\'")
		entries = append(entries, fmt.Sprintf("'%s':1", h))
	}
	s.mu.RUnlock()
	bypassObj := strings.Join(entries, ",")
	return fmt.Sprintf(`function FindProxyForURL(url, host) {
  var local=["*.lan","*.local","*.internal","10.*.*.*","127.*.*.*","172.16.*.*","192.168.*.*"];
  var bypass={%s};
  for (var i=0;i<local.length;i++) if (shExpMatch(host,local[i])) return "DIRECT";
  if (isPlainHostName(host)||bypass[host]) return "DIRECT";
  return "%s";
}
`, bypassObj, proxyLine)
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(r) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="Socratex"`)
		http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	s.handleHTTP(w, r)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	dest := r.Host
	if dest == "" {
		dest = r.RequestURI
	}
	if !strings.Contains(dest, ":") {
		dest += ":443"
	}
	hostOnly, _, err := net.SplitHostPort(dest)
	if err != nil {
		hostOnly = dest
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	clientConn, bufrw, err := hj.Hijack()
	if err != nil {
		log.Printf("[server] hijack: %v", err)
		return
	}
	defer clientConn.Close()

	targetConn, err := s.dialer.Dial("tcp", dest)
	if err != nil {
		log.Printf("[server] dial %s: %v", dest, err)
		fmt.Fprintf(clientConn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer targetConn.Close()

	if len(s.bypass) > 0 {
		ipStr := stripPort(targetConn.RemoteAddr().String())
		if ip := net.ParseIP(ipStr); ip != nil {
			country := strings.ToUpper(string(iploc.Country(ip)))
			if country != "" && s.bypass[country] {
				s.addBypassHost(hostOnly)
				log.Printf("[BYPASS] %s => [%s] %s", dest, country, ipStr)
			} else {
				log.Printf("[PROXY]  %s => [%s] %s via %s", dest, country, ipStr, s.dialer.String())
			}
		}
	} else {
		log.Printf("[PROXY]  %s", dest)
	}

	if bufrw != nil {
		_ = bufrw.Flush()
	}
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	relay(clientConn, targetConn)
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	r.RequestURI = ""
	for _, h := range []string{"Proxy-Connection", "Proxy-Authorization", "TE", "Trailers", "Transfer-Encoding", "Upgrade"} {
		r.Header.Del(h)
	}
	if r.URL.Scheme == "" {
		r.URL.Scheme = "http"
	}
	if r.URL.Host == "" {
		r.URL.Host = r.Host
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return s.dialer.Dial(network, addr)
		},
	}
	resp, err := tr.RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (s *Server) authenticate(r *http.Request) bool {
	cfg := s.cfg.Auth
	if cfg.Username == "" && cfg.Token == "" {
		return true
	}

	if t := r.URL.Query().Get("token"); t != "" && cfg.Token != "" {
		if t == cfg.Token {
			s.touch(r.RemoteAddr)
			return true
		}
		return false
	}

	auth := r.Header.Get("Proxy-Authorization")
	if auth == "" {
		auth = r.Header.Get("Authorization")
	}
	if strings.HasPrefix(strings.ToLower(auth), "basic ") {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth[6:]))
		if err == nil {
			parts := strings.SplitN(string(raw), ":", 2)
			if len(parts) == 2 {
				user, pass := parts[0], parts[1]
				if cfg.Username != "" && user == cfg.Username && pass == cfg.Password {
					s.touch(r.RemoteAddr)
					return true
				}
				if cfg.Token != "" && user == "token" && pass == cfg.Token {
					s.touch(r.RemoteAddr)
					return true
				}
				if cfg.Token != "" && user == cfg.Token {
					s.touch(r.RemoteAddr)
					return true
				}
			}
		}
	}

	s.mu.RLock()
	_, ok := s.sessions[stripPort(r.RemoteAddr)]
	s.mu.RUnlock()
	return ok
}

func (s *Server) touch(addr string) {
	s.mu.Lock()
	s.sessions[stripPort(addr)] = time.Now()
	s.mu.Unlock()
}

func (s *Server) addBypassHost(host string) {
	if host == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hosts[host] = time.Now().Unix()
	if len(s.hosts) > maxBypassHosts {
		var oldest string
		var oldestTS int64 = time.Now().Unix()
		for h, ts := range s.hosts {
			if ts < oldestTS {
				oldestTS, oldest = ts, h
			}
		}
		delete(s.hosts, oldest)
	}
}

func (s *Server) cleanupLoop(ctx context.Context) {
	t := time.NewTicker(cleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now().Unix()
			s.mu.Lock()
			for h, ts := range s.hosts {
				if now-ts > int64(bypassHostTTL.Seconds()) {
					delete(s.hosts, h)
				}
			}
			for ip, ts := range s.sessions {
				if time.Since(ts) > 24*time.Hour {
					delete(s.sessions, ip)
				}
			}
			s.mu.Unlock()
		}
	}
}

func stripPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
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
