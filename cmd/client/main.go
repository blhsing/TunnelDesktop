package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/getlantern/systray"

	"tunneldesktop/internal/relaycore"
	"tunneldesktop/internal/tunnel"
)

type config struct {
	ListenAddr string `json:"listen_addr"`
	RelayAddr  string `json:"relay_addr"`
	Proxy      string `json:"proxy"`
	CAFile     string `json:"ca_file"`
	CertFile   string `json:"cert_file"`
	KeyFile    string `json:"key_file"`
	CAPEM      string `json:"ca_pem"`
	CertPEM    string `json:"cert_pem"`
	KeyPEM     string `json:"key_pem"`
	ServerName string `json:"server_name"`
	Token      string `json:"token"`
}

type trayState struct {
	mu       sync.Mutex
	cfg      config
	cancel   context.CancelFunc
	listener net.Listener
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	var bundleFile string
	var configFile string
	var importFile string
	var consoleMode bool
	flag.StringVar(&bundleFile, "bundle", "", "client.tnl bundle file")
	flag.StringVar(&configFile, "config", "", "legacy JSON config file")
	flag.StringVar(&importFile, "import", "", "import client.tnl into the per-user config directory and exit")
	flag.BoolVar(&consoleMode, "console", false, "run in the foreground instead of the system tray")
	flag.Parse()

	if importFile != "" {
		if err := importBundle(importFile); err != nil {
			log.Fatal(err)
		}
		fmt.Println("client.tnl imported")
		return
	}
	if bundleFile == "" && configFile == "" {
		bundleFile = findClientBundle()
	}
	cfg, err := loadConfig(bundleFile, configFile)
	if err != nil {
		log.Fatal(err)
	}
	if consoleMode {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := run(ctx, cfg, false); err != nil && ctx.Err() == nil {
			log.Fatal(err)
		}
		return
	}
	state := &trayState{cfg: cfg}
	systray.Run(func() { state.onReady() }, func() { state.stop() })
}

func loadConfig(bundleFile, configFile string) (config, error) {
	if bundleFile != "" {
		data, err := os.ReadFile(bundleFile)
		if err != nil {
			return config{}, fmt.Errorf("read bundle: %w", err)
		}
		bundle, err := relaycore.DecodeBundle(string(data))
		if err != nil {
			return config{}, err
		}
		if bundle.Role != "client" {
			return config{}, fmt.Errorf("bundle role is %q, want client", bundle.Role)
		}
		cfg := config{
			ListenAddr: bundle.ListenAddr,
			RelayAddr:  bundle.RelayAddr,
			Proxy:      bundle.Proxy,
			CAPEM:      bundle.CAPEM,
			CertPEM:    bundle.CertPEM,
			KeyPEM:     bundle.KeyPEM,
			ServerName: bundle.ServerName,
			Token:      bundle.Token,
		}
		cfg.applyDefaults()
		return cfg, cfg.validate()
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		return config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, fmt.Errorf("decode config: %w", err)
	}
	cfg.resolvePaths(filepath.Dir(configFile))
	cfg.applyDefaults()
	return cfg, cfg.validate()
}

func (s *trayState) onReady() {
	systray.SetTitle("TunnelDesktop")
	systray.SetTooltip("TunnelDesktop RDP client")
	connect := systray.AddMenuItem("Connect", "Start local tunnel and open Remote Desktop")
	disconnect := systray.AddMenuItem("Disconnect", "Stop local tunnel")
	status := systray.AddMenuItem("Status: disconnected", "Current state")
	status.Disable()
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Exit TunnelDesktop client")

	go func() {
		for {
			select {
			case <-connect.ClickedCh:
				if err := s.start(); err != nil {
					log.Printf("connect failed: %v", err)
					status.SetTitle("Status: " + err.Error())
				} else {
					status.SetTitle("Status: connected")
				}
			case <-disconnect.ClickedCh:
				s.stop()
				status.SetTitle("Status: disconnected")
			case <-quit.ClickedCh:
				s.stop()
				systray.Quit()
				return
			}
		}
	}()
}

func (s *trayState) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		launchMSTSC(s.cfg.ListenAddr)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	listener, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		cancel()
		return err
	}
	s.cancel = cancel
	s.listener = listener
	go func() {
		err := serveListener(ctx, s.cfg, listener)
		if err != nil && ctx.Err() == nil {
			log.Printf("client listener stopped: %v", err)
		}
	}()
	launchMSTSC(s.cfg.ListenAddr)
	return nil
}

func (s *trayState) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
}

func run(ctx context.Context, cfg config, openMSTSC bool) error {
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
	}
	defer listener.Close()
	log.Printf("client listening on %s; mstsc should target this address", listener.Addr())
	if openMSTSC {
		launchMSTSC(cfg.ListenAddr)
	}
	return serveListener(ctx, cfg, listener)
}

func serveListener(ctx context.Context, cfg config, listener net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go handleLocalConn(ctx, cfg, conn)
	}
}

func handleLocalConn(ctx context.Context, cfg config, localConn net.Conn) {
	relayConn, err := dialRelay(ctx, cfg)
	if err != nil {
		log.Printf("relay dial failed for %s: %v", localConn.RemoteAddr(), err)
		_ = localConn.Close()
		return
	}
	log.Printf("bridging local RDP connection from %s", localConn.RemoteAddr())
	tunnel.Pipe(localConn, relayConn)
	log.Printf("closed local RDP connection from %s", localConn.RemoteAddr())
}

func dialRelay(ctx context.Context, cfg config) (net.Conn, error) {
	tlsConfig, err := cfg.tlsConfig()
	if err != nil {
		return nil, err
	}
	rawConn, err := tunnel.DialContext(ctx, cfg.RelayAddr, cfg.Proxy)
	if err != nil {
		return nil, err
	}
	tlsConn := tls.Client(rawConn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("TLS handshake: %w", err)
	}
	if err := tunnel.SendAuth(ctx, tlsConn, cfg.Token, tunnel.RoleClient); err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func (c *config) applyDefaults() {
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:3389"
	}
	if c.ServerName == "" && c.RelayAddr != "" {
		c.ServerName = serverNameFromAddr(c.RelayAddr)
	}
}

func (c config) validate() error {
	if c.RelayAddr == "" {
		return fmt.Errorf("relay_addr is required")
	}
	if c.CAFile == "" && c.CAPEM == "" {
		return fmt.Errorf("ca_file or ca_pem is required")
	}
	if c.CertFile == "" && c.CertPEM == "" {
		return fmt.Errorf("cert_file or cert_pem is required")
	}
	if c.KeyFile == "" && c.KeyPEM == "" {
		return fmt.Errorf("key_file or key_pem is required")
	}
	if c.ServerName == "" {
		return fmt.Errorf("server_name is required")
	}
	if c.Token == "" {
		return fmt.Errorf("token is required")
	}
	return nil
}

func (c *config) resolvePaths(base string) {
	c.CAFile = resolvePath(base, c.CAFile)
	c.CertFile = resolvePath(base, c.CertFile)
	c.KeyFile = resolvePath(base, c.KeyFile)
}

func (c config) tlsConfig() (*tls.Config, error) {
	if c.CAPEM != "" || c.CertPEM != "" || c.KeyPEM != "" {
		return tunnel.ClientTLSConfigFromPEM(c.CAPEM, c.CertPEM, c.KeyPEM, c.ServerName)
	}
	return tunnel.ClientTLSConfig(c.CAFile, c.CertFile, c.KeyFile, c.ServerName)
}

func importBundle(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, err := relaycore.DecodeBundle(string(data)); err != nil {
		return err
	}
	dst := userBundlePath()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

func findClientBundle() string {
	userPath := userBundlePath()
	if _, err := os.Stat(userPath); err == nil {
		return userPath
	}
	return defaultBundlePath("client.tnl")
}

func userBundlePath() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return defaultBundlePath("client.tnl")
	}
	return filepath.Join(base, "TunnelDesktop", "client.tnl")
}

func defaultBundlePath(name string) string {
	exePath, err := os.Executable()
	if err != nil {
		return name
	}
	return filepath.Join(filepath.Dir(exePath), name)
}

func launchMSTSC(listenAddr string) {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		host = "127.0.0.1"
		port = "3389"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	target := net.JoinHostPort(host, port)
	if strings.Contains(host, ":") {
		target = "[" + host + "]:" + port
	}
	_ = exec.Command("mstsc.exe", "/v:"+target).Start()
}

func resolvePath(base, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Clean(filepath.Join(base, value))
}

func serverNameFromAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
