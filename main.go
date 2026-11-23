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

	"github.com/phuslu/iploc"
	"golang.org/x/crypto/acme/autocert"
)

const (
	version         = "1.1.0"
	configFile      = ".socratex.json"
	defaultPort     = 443
	maxBypassHosts  = 2000
	bypassHostTTL   = 7 * 24 * time.Hour
	cleanupInterval = 30 * time.Minute
)

type Config struct {
	Domain      string           `json:"domain"`
	Token       string           `json:"token"`
	User        string           `json:"user,omitempty"`
	Password    string           `json:"password,omitempty"`
	BypassList  []string         `json:"bypass,omitempty"`
	BypassHosts map[string]int64 `json:"bypassHosts,omitempty"`
	HTTPOnly    bool             `json:"-"`
}

type ProxyServer struct {
	cfg             *Config
	mu              sync.RWMutex
	sessions        map[string]time.Time
	bypassCountries map[string]bool
	configPath      string
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

	var bypass flagStringSlice
	flag.Var(&bypass, "bypass", "Bypass IPs in these countries (ISO code), can be repeated, e.g. --bypass=CN --bypass=US")

	flag.Parse()

	if *showVersion {
		fmt.Printf("socratex (golang) v%s\n", version)
		return
	}

	cfg, configPath, err := loadOrCreateConfig(*domain, *user, *password, *token, bypass, *httpOnly)
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

	bypassMap := make(map[string]bool)
	for _, c := range cfg.BypassList {
		bypassMap[strings.ToUpper(strings.TrimSpace(c))] = true
	}

	ps := &ProxyServer{
		cfg:             cfg,
		sessions:        make(map[string]time.Time),
		bypassCountries: bypassMap,
		configPath:      configPath,
	}

	printBanner(cfg, listenPort)

	if len(bypassMap) > 0 {
		log.Printf("[SOCRATEX] GeoIP bypass enabled for countries: %s", strings.Join(cfg.BypassList, ", "))
		log.Printf("[SOCRATEX] Current bypass hosts: %d", len(cfg.BypassHosts))
	}

	go ps.cleanupLoop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.HTTPOnly {
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
		ps.saveConfig()
		return
	}

	cacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "socratex-certs")
	os.MkdirAll(cacheDir, 0700)

	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.Domain),
		Cache:      autocert.DirCache(cacheDir),
	}

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/", ps.handleWeb)
	httpServer := &http.Server{
		Addr:    ":80",
		Handler: m.HTTPHandler(httpMux),
	}

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
	ps.saveConfig()
}

type flagStringSlice []string

func (f *flagStringSlice) String() string {
	return strings.Join(*f, ",")
}

func (f *flagStringSlice) Set(value string) error {
	for _, v := range strings.Split(value, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			*f = append(*f, strings.ToUpper(v))
		}
	}
	return nil
}

func loadOrCreateConfig(domain, user, password, token string, bypass []string, httpOnly bool) (*Config, string, error) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, configFile)

	cfg := &Config{
		HTTPOnly:    httpOnly,
		BypassHosts: make(map[string]int64),
	}

	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, cfg)
		if cfg.BypassHosts == nil {
			cfg.BypassHosts = make(map[string]int64)
		}
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
	if len(bypass) > 0 {
		seen := make(map[string]bool)
		var list []string
		for _, c := range append(cfg.BypassList, bypass...) {
			c = strings.ToUpper(strings.TrimSpace(c))
			if c != "" && !seen[c] {
				seen[c] = true
				list = append(list, c)
			}
		}
		cfg.BypassList = list
	}

	if cfg.Token == "" {
		cfg.Token = generateToken()
	}

	data, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(path, data, 0600)

	return cfg, path, nil
}

func (ps *ProxyServer) saveConfig() {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	data, err := json.MarshalIndent(ps.cfg, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(ps.configPath, data, 0600)
}

func generateToken() string {
	b := make([]byte, 16)
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

	if len(cfg.BypassList) > 0 {
		fmt.Println()
		fmt.Printf("* GeoIP Bypass countries: %s\n", strings.Join(cfg.BypassList, ", "))
	}
	fmt.Println()
}

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
		fmt.Fprintf(w, "Socratex (Go) v%s\n", version)
		fmt.Fprintf(w, "Active sessions: %d\n", len(ps.sessions))
		fmt.Fprintf(w, "Bypass countries: %v\n", ps.cfg.BypassList)
		fmt.Fprintf(w, "Bypass hosts: %d\n\n", len(ps.cfg.BypassHosts))
		for ip, t := range ps.sessions {
			fmt.Fprintf(w, "  client %s  last: %s\n", ip, t.Format(time.RFC3339))
		}
		fmt.Fprintln(w)
		for host, ts := range ps.cfg.BypassHosts {
			fmt.Fprintf(w, "  bypass %s  last: %s\n", host, time.Unix(ts, 0).Format(time.RFC3339))
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

	ps.mu.RLock()
	var bypassEntries []string
	for host := range ps.cfg.BypassHosts {
		h := strings.ReplaceAll(host, "'", "\\'")
		bypassEntries = append(bypassEntries, fmt.Sprintf("'%s': 1", h))
	}
	ps.mu.RUnlock()

	bypassObj := strings.Join(bypassEntries, ",\n        ")

	return fmt.Sprintf(`function FindProxyForURL(url, host) {
    // Socratex (Go) by chuansuoyun
    // https://github.com/chuansuoyun/socratex

    var local = [
        "*.lan", "*.local", "*.internal",
        "10.*.*.*", "127.*.*.*", "172.16.*.*", "192.168.*.*"
    ];

    var bypass = {
        %s
    };

    for (var i = 0; i < local.length; i++) {
        if (shExpMatch(host, local[i])) return "DIRECT";
    }
    if (isPlainHostName(host) || bypass[host]) return "DIRECT";
    return "%s";
}
`, bypassObj, proxyLine)
}

func (ps *ProxyServer) authenticate(r *http.Request) bool {
	if t := r.URL.Query().Get("token"); t != "" {
		if t == ps.cfg.Token {
			ps.touchSession(r.RemoteAddr)
			return true
		}
		return false
	}

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

	if ps.cfg.Token == "" && ps.cfg.User == "" {
		return true
	}

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

	ps.handleHTTPProxy(w, r)
}

func (ps *ProxyServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	dest := r.Host
	if !strings.Contains(dest, ":") {
		dest += ":443"
	}

	hostOnly, _, _ := net.SplitHostPort(dest)
	if hostOnly == "" {
		hostOnly = dest
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

	if len(ps.bypassCountries) > 0 {
		remoteAddr := targetConn.RemoteAddr().String()
		ipStr := stripPort(remoteAddr)
		ip := net.ParseIP(ipStr)
		if ip != nil {
			country := string(iploc.Country(net.ParseIP(ipStr)))
			country = strings.ToUpper(country)
			if country != "" && ps.bypassCountries[country] {
				ps.addBypassHost(hostOnly)
				log.Printf("[BYPASS] %s => [%s] %s (added to PAC DIRECT)", dest, country, ipStr)
			} else {
				log.Printf("[PROXY]  %s => [%s] %s", dest, country, ipStr)
			}
		}
	}

	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	errCh := make(chan error, 2)
	go func() { _, err := io.Copy(targetConn, clientConn); errCh <- err }()
	go func() { _, err := io.Copy(clientConn, targetConn); errCh <- err }()
	<-errCh
}

func (ps *ProxyServer) addBypassHost(host string) {
	if host == "" {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.cfg.BypassHosts == nil {
		ps.cfg.BypassHosts = make(map[string]int64)
	}
	ps.cfg.BypassHosts[host] = time.Now().Unix()

	if len(ps.cfg.BypassHosts) > maxBypassHosts {
		var oldestHost string
		var oldestTS int64 = time.Now().Unix()
		for h, ts := range ps.cfg.BypassHosts {
			if ts < oldestTS {
				oldestTS = ts
				oldestHost = h
			}
		}
		if oldestHost != "" {
			delete(ps.cfg.BypassHosts, oldestHost)
		}
	}
}

func (ps *ProxyServer) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		ps.mu.Lock()
		now := time.Now().Unix()
		for host, ts := range ps.cfg.BypassHosts {
			if now-ts > int64(bypassHostTTL.Seconds()) {
				delete(ps.cfg.BypassHosts, host)
			}
		}
		ps.mu.Unlock()
		ps.saveConfig()
	}
}

func (ps *ProxyServer) handleHTTPProxy(w http.ResponseWriter, r *http.Request) {
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

	if len(ps.bypassCountries) > 0 {
		host := r.URL.Hostname()
		if host != "" {
			go func(h string) {
				ips, err := net.LookupIP(h)
				if err != nil || len(ips) == 0 {
					return
				}
				country := string(iploc.Country(net.ParseIP(ips[0].String())))
				country = strings.ToUpper(country)
				if country != "" && ps.bypassCountries[country] {
					ps.addBypassHost(h)
				}
			}(host)
		}
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

var _ = tls.Config{}
