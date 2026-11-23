package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

const (
	version     = "1.0.0"
	configFile  = ".socratex.json"
	defaultPort = 443
)

type Config struct {
	Domain   string `json:"domain"`
	Token    string `json:"token"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	HTTPOnly bool   `json:"-"`
}

type ProxyServer struct {
	cfg      *Config
	mu       sync.RWMutex
	sessions map[string]time.Time // remoteAddr -> lastVisit
}

func main() {
	domain := flag.String("domain", "", "Domain to deploy the proxy (required for HTTPS)")
	httpOnly := flag.Bool("http", false, "Use HTTP-only mode (for testing)")
	user := flag.String("user", "", "Username for Basic Auth")
	password := flag.String("password", "", "Password for Basic Auth")
	token := flag.String("token", "", "Token for Token Auth (auto-generated if empty)")
	port := flag.Int("port", 0, "Listen port (default 443 for HTTPS, 80 for HTTP-only)")
	addr := flag.String("address", "", "Listen address (default all interfaces)")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("socratex (golang) v%s\n", version)
		return
	}

	cfg, err := loadOrCreateConfig(*domain, *user, *password, *token, *httpOnly)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	if cfg.Domain == "" && !cfg.HTTPOnly {
		log.Fatal("please specify --domain=example.com (or use --http for testing)")
	}

	listenPort := *port
	if listenPort == 0 {
		if cfg.HTTPOnly {
			listenPort = 80
		} else {
			listenPort = defaultPort
		}
	}

	ps := &ProxyServer{
		cfg:      cfg,
		sessions: make(map[string]time.Time),
	}

	// print banner
	printBanner(cfg, listenPort)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.HTTPOnly {
		// simple HTTP proxy for testing
		mux := http.NewServeMux()
		mux.HandleFunc("/", ps.handleHTTP)
		server := &http.Server{
			Addr:    fmt.Sprintf("%s:%d", *addr, listenPort),
			Handler: mux,
		}
		go func() {
			log.Printf("[SOCRATEX] HTTP Proxy started at http://%s:%d", cfg.Domain, listenPort)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("listen error: %v", err)
			}
		}()
		<-ctx.Done()
		server.Shutdown(context.Background())
		return
	}

	// Production mode: autocert + dual listeners
	cacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "socratex-certs")
	os.MkdirAll(cacheDir, 0700)

	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.Domain),
		Cache:      autocert.DirCache(cacheDir),
	}

	// HTTP on 80 for ACME challenges + PAC/WPAD
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/", ps.handleWeb)
	httpServer := &http.Server{
		Addr:    ":80",
		Handler: m.HTTPHandler(httpMux),
	}

	// HTTPS proxy on 443 (TLS terminated by autocert)
	httpsMux := http.NewServeMux()
	httpsMux.HandleFunc("/", ps.handleHTTP)

	tlsConfig := m.TLSConfig()
	tlsConfig.NextProtos = []string{"http/1.1", "h2"}

	httpsServer := &http.Server{
		Addr:      fmt.Sprintf("%s:%d", *addr, listenPort),
		Handler:   httpsMux,
		TLSConfig: tlsConfig,
	}

	go func() {
		log.Printf("[SOCRATEX] HTTP Server started at http://%s:80", cfg.Domain)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("http server error: %v", err)
		}
	}()

	go func() {
		log.Printf("[SOCRATEX] Secure Web Proxy started at https://%s:%d", cfg.Domain, listenPort)
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("https server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("[SOCRATEX] shutting down...")
	httpServer.Shutdown(context.Background())
	httpsServer.Shutdown(context.Background())
}

func loadOrCreateConfig(domain, user, password, token string, httpOnly bool) (*Config, error) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, configFile)

	cfg := &Config{HTTPOnly: httpOnly}

	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, cfg)
	}

	if domain != "" {
		cfg.Domain = domain
	}
	if user != "" {
		cfg.User = user
	}
	if password != "" {
		cfg.Password = password
	}
	if token != "" {
		cfg.Token = token
	}

	if cfg.Token == "" {
		cfg.Token = generateToken()
	}

	// persist
	data, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(path, data, 0600)

	return cfg, nil
}

func generateToken() string {
	b := make([]byte, 16)
	// simple random-ish token
	for i := range b {
		b[i] = byte(time.Now().UnixNano() >> (i * 4))
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func printBanner(cfg *Config, port int) {
	scheme := "https"
	if cfg.HTTPOnly {
		scheme = "http"
	}
	base := fmt.Sprintf("%s://%s", scheme, cfg.Domain)
	if (scheme == "https" && port != 443) || (scheme == "http" && port != 80) {
		base = fmt.Sprintf("%s://%s:%d", scheme, cfg.Domain, port)
	}

	fmt.Println()
	fmt.Printf("[SOCRATEX v%s] https://github.com/chuansuoyun/socratex\n", version)
	fmt.Println()

	fmt.Println("* Token authentication:")
	fmt.Printf("  - PAC:  %s/proxy.pac?token=%s\n", base, cfg.Token)
	fmt.Printf("  - WPAD: %s/wpad.dat?token=%s\n", base, cfg.Token)
	fmt.Printf("  - Log:  %s/console?token=%s\n", base, cfg.Token)

	if cfg.User != "" && cfg.Password != "" {
		bsc := fmt.Sprintf("%s://%s:%s@%s", scheme, cfg.User, cfg.Password, cfg.Domain)
		if (scheme == "https" && port != 443) || (scheme == "http" && port != 80) {
			bsc = fmt.Sprintf("%s://%s:%s@%s:%d", scheme, cfg.User, cfg.Password, cfg.Domain, port)
		}
		fmt.Println()
		fmt.Println("* Basic authentication:")
		fmt.Printf("  - PAC:   %s/proxy.pac\n", bsc)
		fmt.Printf("  - WPAD:  %s/wpad.dat\n", bsc)
		fmt.Printf("  - Log:   %s/console\n", bsc)
		fmt.Printf("  - Proxy: %s\n", bsc)
	}
	fmt.Println()
}

// handleWeb serves PAC / WPAD / console on port 80
func (ps *ProxyServer) handleWeb(w http.ResponseWriter, r *http.Request) {
	if !ps.authenticate(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	path := strings.ToLower(r.URL.Path)
	switch path {
	case "/", "/favicon.ico":
		w.Write([]byte("42"))
	case "/proxy.pac", "/wpad.dat":
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		w.Write([]byte(ps.makePAC()))
	case "/console":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		ps.mu.RLock()
		fmt.Fprintf(w, "Socratex (Go) v%s\nActive sessions: %d\n", version, len(ps.sessions))
		for ip, t := range ps.sessions {
			fmt.Fprintf(w, "  %s  last: %s\n", ip, t.Format(time.RFC3339))
		}
		ps.mu.RUnlock()
	default:
		http.NotFound(w, r)
	}
}

func (ps *ProxyServer) makePAC() string {
	proxyLine := fmt.Sprintf("HTTPS %s:443", ps.cfg.Domain)
	if ps.cfg.HTTPOnly {
		proxyLine = fmt.Sprintf("PROXY %s:80", ps.cfg.Domain)
	}

	return fmt.Sprintf(`function FindProxyForURL(url, host) {
    // Socratex (Go) by chuansuoyun
    // https://github.com/chuansuoyun/socratex

    var local = [
        "*.lan", "*.local", "*.internal",
        "10.*.*.*", "127.*.*.*", "172.16.*.*", "192.168.*.*"
    ];

    for (var i = 0; i < local.length; i++) {
        if (shExpMatch(host, local[i])) return "DIRECT";
    }
    if (isPlainHostName(host)) return "DIRECT";
    return "%s";
}
`, proxyLine)
}

func (ps *ProxyServer) authenticate(r *http.Request) bool {
	// Token via query
	if t := r.URL.Query().Get("token"); t != "" {
		if t == ps.cfg.Token {
			ps.touchSession(r.RemoteAddr)
			return true
		}
		return false
	}

	// Basic Auth
	auth := r.Header.Get("Proxy-Authorization")
	if auth == "" {
		auth = r.Header.Get("Authorization")
	}
	if auth != "" && strings.HasPrefix(strings.ToLower(auth), "basic ") {
		payload, err := base64.StdEncoding.DecodeString(auth[6:])
		if err == nil {
			parts := strings.SplitN(string(payload), ":", 2)
			if len(parts) == 2 && parts[0] == ps.cfg.User && parts[1] == ps.cfg.Password {
				ps.touchSession(r.RemoteAddr)
				return true
			}
		}
	}

	// If no auth configured at all, allow (dev mode)
	if ps.cfg.Token == "" && ps.cfg.User == "" {
		return true
	}

	// already authenticated session?
	ps.mu.RLock()
	_, ok := ps.sessions[stripPort(r.RemoteAddr)]
	ps.mu.RUnlock()
	return ok
}

func (ps *ProxyServer) touchSession(remote string) {
	ip := stripPort(remote)
	ps.mu.Lock()
	ps.sessions[ip] = time.Now()
	ps.mu.Unlock()
}

func stripPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// handleHTTP is the main proxy handler (CONNECT + normal HTTP)
func (ps *ProxyServer) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if !ps.authenticate(r) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="Socratex"`)
		http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
		return
	}

	if r.Method == http.MethodConnect {
		ps.handleConnect(w, r)
		return
	}

	// normal HTTP proxy
	ps.handleHTTPProxy(w, r)
}

func (ps *ProxyServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	dest := r.Host
	if !strings.Contains(dest, ":") {
		dest += ":443"
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer clientConn.Close()

	targetConn, err := net.DialTimeout("tcp", dest, 15*time.Second)
	if err != nil {
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer targetConn.Close()

	// reply 200 Connection Established
	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// bidirectional copy
	errCh := make(chan error, 2)
	go func() { _, err := io.Copy(targetConn, clientConn); errCh <- err }()
	go func() { _, err := io.Copy(clientConn, targetConn); errCh <- err }()
	<-errCh
}

func (ps *ProxyServer) handleHTTPProxy(w http.ResponseWriter, r *http.Request) {
	// remove hop-by-hop headers
	r.RequestURI = ""
	r.Header.Del("Proxy-Connection")
	r.Header.Del("Proxy-Authorization")
	r.Header.Del("TE")
	r.Header.Del("Trailers")
	r.Header.Del("Transfer-Encoding")
	r.Header.Del("Upgrade")

	if r.URL.Scheme == "" {
		r.URL.Scheme = "http"
	}
	if r.URL.Host == "" {
		r.URL.Host = r.Host
	}

	resp, err := http.DefaultTransport.RoundTrip(r)
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

// ensure we can use TLS client if needed in future
var _ = tls.Config{}
