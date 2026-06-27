//go:build darwin

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"deskferry/internal/tunnel"
)

const (
	defaultRelayURL   = "https://test-officialwebsite.azurewebsites.net/relay/workdesk"
	defaultListenAddr = "127.0.0.1:3389"
)

type config struct {
	RelayAddr  string
	ListenAddr string
	Proxy      string
}

type relaySnapshot struct {
	Service string              `json:"service"`
	Time    *time.Time          `json:"time"`
	Rooms   []relayRoomSnapshot `json:"rooms"`
}

type relayRoomSnapshot struct {
	ID                    string     `json:"id"`
	WaitingAgents         int        `json:"waiting_agents"`
	ActivePairs           int        `json:"active_pairs"`
	TotalPairs            int64      `json:"total_pairs"`
	LastAgentRemote       string     `json:"last_agent_remote"`
	LastAgentConnectedAt  *time.Time `json:"last_agent_connected_at"`
	HomeAgentConnected    bool       `json:"home_agent_connected"`
	HomeAgentRemote       string     `json:"home_agent_remote"`
	HomeAgentConnectedAt  *time.Time `json:"home_agent_connected_at"`
	LastClientRemote      string     `json:"last_client_remote"`
	LastClientConnectedAt *time.Time `json:"last_client_connected_at"`
}

type relaySummary struct {
	Room       string
	WorkOnline bool
	HomeOnline bool
	Waiting    int
	Active     int
	Total      int64
	LastClient string
	LastAgent  string
	LastHome   string
	CheckedAt  *time.Time
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	var relayURL string
	var listenAddr string
	var proxyFlag string
	var openRDP bool
	var statusOnly bool
	flag.StringVar(&relayURL, "relay-url", "", "relay room URL")
	flag.StringVar(&listenAddr, "listen", "", "local RDP listen address")
	flag.StringVar(&proxyFlag, "proxy", "", "proxy: env, direct, or http://host:port")
	flag.BoolVar(&openRDP, "open-rdp", false, "open the local RDP profile after the tunnel starts")
	flag.BoolVar(&statusOnly, "status", false, "print relay room status and exit")
	flag.Parse()

	cfg, err := loadConfig(relayURL, listenAddr, proxyFlag)
	if err != nil {
		log.Fatal(err)
	}

	if statusOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		summary, err := queryRelaySummary(ctx, cfg)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(formatRelayDetails(summary, cfg))
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, openRDP); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func loadConfig(relayURL, listenAddr, proxyFlag string) (config, error) {
	cfg := config{
		RelayAddr:  strings.TrimSpace(relayURL),
		ListenAddr: strings.TrimSpace(listenAddr),
		Proxy:      strings.TrimSpace(proxyFlag),
	}
	cfg.applyDefaults()
	normalized, err := normalizeRelayURL(cfg.RelayAddr)
	if err != nil {
		return config{}, err
	}
	cfg.RelayAddr = normalized
	return cfg, cfg.validate()
}

func (c *config) applyDefaults() {
	if c.ListenAddr == "" {
		c.ListenAddr = defaultListenAddr
	}
	if c.RelayAddr == "" {
		c.RelayAddr = defaultRelayURL
	}
	if c.Proxy == "" {
		c.Proxy = "env"
	}
}

func (c config) validate() error {
	if c.RelayAddr == "" {
		return errors.New("relay URL is required")
	}
	if !tunnel.IsWebSocketRelay(c.RelayAddr) {
		return errors.New("relay URL must start with https:// or http://")
	}
	if _, err := url.ParseRequestURI(c.RelayAddr); err != nil {
		return fmt.Errorf("relay URL is invalid: %w", err)
	}
	if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		return fmt.Errorf("local RDP address must be host:port: %w", err)
	}
	return nil
}

func run(ctx context.Context, cfg config, openRDP bool) error {
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
	}
	defer listener.Close()

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	go homePresenceLoop(ctx, cfg)

	log.Printf("DeskFerry Home listening on %s; point your RDP client at %s", listener.Addr(), rdpTarget(cfg.ListenAddr))
	if openRDP {
		if err := launchRDP(cfg); err != nil {
			log.Printf("open RDP profile: %v", err)
		}
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		remote := conn.RemoteAddr().String()
		log.Printf("RDP connection from %s", remote)
		go handleLocalConn(ctx, cfg, conn, remote)
	}
}

func handleLocalConn(ctx context.Context, cfg config, localConn net.Conn, remote string) {
	defer localConn.Close()
	relayConn, err := tunnel.DialWebSocketStream(ctx, cfg.RelayAddr, cfg.Proxy, tunnel.RoleClient, "")
	if err != nil {
		log.Printf("relay dial failed for %s: %v", remote, err)
		return
	}
	log.Printf("bridging local RDP connection from %s", remote)
	tunnel.Pipe(localConn, relayConn)
	log.Printf("closed local RDP connection from %s", remote)
}

func homePresenceLoop(ctx context.Context, cfg config) {
	for {
		conn, err := tunnel.DialWebSocket(ctx, cfg.RelayAddr, cfg.Proxy, tunnel.RoleHomeAgent, "")
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("home status connection failed: %v", err)
		} else {
			log.Printf("home status connected to %s", cfg.RelayAddr)
			_, _, err = conn.Read(ctx)
			tunnel.CloseWebSocket(conn)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				log.Printf("home status disconnected: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func launchRDP(cfg config) error {
	profile, err := writeRDPProfile(cfg)
	if err != nil {
		return err
	}
	return exec.Command("open", profile).Start()
}

func writeRDPProfile(cfg config) (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}
	path := filepath.Join(dir, "home-agent.rdp")
	if err := os.WriteFile(path, []byte(rdpProfileContent(cfg)), 0600); err != nil {
		return "", fmt.Errorf("write RDP profile: %w", err)
	}
	return path, nil
}

func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "DeskFerry"), nil
}

func rdpProfileContent(cfg config) string {
	lines := []string{
		"screen mode id:i:2",
		"use multimon:i:0",
		"session bpp:i:32",
		"full address:s:" + sanitizeRDPValue(rdpTarget(cfg.ListenAddr)),
		"prompt for credentials:i:1",
		"authentication level:i:2",
		"redirectclipboard:i:1",
		"redirectprinters:i:0",
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

func sanitizeRDPValue(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

func rdpTarget(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		host = "127.0.0.1"
		port = "3389"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + port
	}
	return net.JoinHostPort(host, port)
}

func queryRelaySummary(ctx context.Context, cfg config) (relaySummary, error) {
	statusURL, room, err := relayStatusURL(cfg.RelayAddr)
	if err != nil {
		return relaySummary{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return relaySummary{}, err
	}
	resp, err := httpClient(cfg).Do(req)
	if err != nil {
		return relaySummary{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return relaySummary{}, fmt.Errorf("relay status returned HTTP %s", resp.Status)
	}
	var snapshot relaySnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return relaySummary{}, err
	}
	summary := relaySummary{Room: room, CheckedAt: snapshot.Time}
	for _, r := range snapshot.Rooms {
		summary.Waiting += r.WaitingAgents
		summary.Active += r.ActivePairs
		summary.Total += r.TotalPairs
		summary.WorkOnline = summary.WorkOnline || r.WaitingAgents+r.ActivePairs > 0
		summary.HomeOnline = summary.HomeOnline || r.HomeAgentConnected
		if summary.Room == "" {
			summary.Room = r.ID
		}
		if r.LastClientRemote != "" {
			summary.LastClient = r.LastClientRemote
		}
		if r.LastAgentRemote != "" {
			summary.LastAgent = r.LastAgentRemote
		}
		if r.HomeAgentRemote != "" {
			summary.LastHome = r.HomeAgentRemote
		}
	}
	return summary, nil
}

func formatRelayDetails(summary relaySummary, cfg config) string {
	lines := []string{
		"Room: " + emptyAs(summary.Room, "default"),
		"Relay URL: " + cfg.RelayAddr,
		fmt.Sprintf("Work agent: %s (%d waiting sockets)", onlineText(summary.WorkOnline), summary.Waiting),
		fmt.Sprintf("Home app: %s", onlineText(summary.HomeOnline)),
		fmt.Sprintf("Active RDP streams: %d (%d total)", summary.Active, summary.Total),
		"Local RDP address: " + rdpTarget(cfg.ListenAddr),
	}
	if summary.LastAgent != "" {
		lines = append(lines, "Last work agent: "+summary.LastAgent)
	}
	if summary.LastHome != "" {
		lines = append(lines, "Home app remote: "+summary.LastHome)
	}
	if summary.LastClient != "" {
		lines = append(lines, "Last home client: "+summary.LastClient)
	}
	if summary.CheckedAt != nil && !summary.CheckedAt.IsZero() {
		lines = append(lines, "Checked: "+summary.CheckedAt.Local().Format("2006-01-02 15:04:05"))
	}
	return strings.Join(lines, "\n")
}

func httpClient(cfg config) *http.Client {
	return &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				ServerName: tunnel.HostFromRelayAddress(cfg.RelayAddr),
			},
			Proxy: httpProxyFunc(cfg.Proxy),
		},
	}
}

func httpProxyFunc(proxySpec string) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		spec := strings.TrimSpace(proxySpec)
		if spec == "" || strings.EqualFold(spec, "direct") {
			return nil, nil
		}
		if strings.EqualFold(spec, "env") || strings.EqualFold(spec, "auto") {
			return http.ProxyFromEnvironment(req)
		}
		return url.Parse(spec)
	}
}

func normalizeRelayURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultRelayURL
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse relay URL: %w", err)
	}
	if parsed.Host == "" {
		return "", errors.New("relay URL must include a host")
	}
	switch parsed.Scheme {
	case "https", "http":
	case "wss":
		parsed.Scheme = "https"
	case "ws":
		parsed.Scheme = "http"
	default:
		return "", errors.New("relay URL must start with https:// or http://")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(parsed.Path, "/ws") {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/ws")
	}
	if parsed.Path == "" {
		parsed.Path = "/relay"
	}
	return parsed.String(), nil
}

func relayStatusURL(relayAddr string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(relayAddr))
	if err != nil {
		return "", "", fmt.Errorf("parse relay URL: %w", err)
	}
	if parsed.Host == "" {
		return "", "", errors.New("relay URL must include a host")
	}
	switch parsed.Scheme {
	case "wss":
		parsed.Scheme = "https"
	case "ws":
		parsed.Scheme = "http"
	}
	room := tunnel.RoomFromRelayPath(parsed.Path)
	if room == "" {
		room = strings.TrimSpace(parsed.Query().Get("room"))
	}
	parsed.Path = "/relay/status"
	q := url.Values{}
	if room != "" {
		q.Set("room", room)
	}
	parsed.RawQuery = q.Encode()
	return parsed.String(), room, nil
}

func onlineText(ok bool) string {
	if ok {
		return "online"
	}
	return "waiting"
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
