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
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"

	"tunneldesktop/internal/relaycore"
	"tunneldesktop/internal/tunnel"
)

const serviceName = "TunnelDesktopAgent"

type config struct {
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
	RDPAddr    string `json:"rdp_addr"`
	MinBackoff string `json:"min_backoff"`
	MaxBackoff string `json:"max_backoff"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	var bundleFile string
	var configFile string
	var consoleMode bool
	var serviceMode bool
	var installMode bool
	var uninstallMode bool
	var statusMode bool
	var selfTestMode bool
	flag.StringVar(&bundleFile, "bundle", "", "agent.tnl bundle file")
	flag.StringVar(&configFile, "config", "", "legacy JSON config file")
	flag.BoolVar(&consoleMode, "console", false, "run in the foreground for debugging")
	flag.BoolVar(&serviceMode, "service", false, "run under the Windows service control manager")
	flag.BoolVar(&installMode, "install", false, "install and start the Windows service")
	flag.BoolVar(&uninstallMode, "uninstall", false, "stop and remove the Windows service")
	flag.BoolVar(&statusMode, "status", false, "print Windows service status")
	flag.BoolVar(&selfTestMode, "self-test", false, "test local RDP, proxy CONNECT, TLS, and token auth")
	flag.Parse()

	if bundleFile == "" && configFile == "" {
		bundleFile = defaultBundlePath("agent.tnl")
	}
	runningAsService := serviceMode
	if !runningAsService {
		var err error
		runningAsService, err = svc.IsWindowsService()
		if err != nil {
			log.Printf("could not determine Windows service context: %v", err)
		}
	}
	if runningAsService {
		if err := runService(bundleFile, configFile); err != nil {
			log.Fatal(err)
		}
		return
	}
	if uninstallMode {
		if err := uninstallService(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if statusMode {
		if err := printStatus(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if selfTestMode {
		cfg, err := loadConfig(bundleFile, configFile)
		if err != nil {
			log.Fatal(err)
		}
		if err := selfTest(context.Background(), cfg); err != nil {
			log.Fatal(err)
		}
		fmt.Println("self-test OK")
		return
	}
	if installMode || !consoleMode {
		if err := installService(bundleFile, configFile); err != nil {
			log.Fatal(err)
		}
		return
	}

	cfg, err := loadConfig(bundleFile, configFile)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
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
		if bundle.Role != "agent" {
			return config{}, fmt.Errorf("bundle role is %q, want agent", bundle.Role)
		}
		cfg := config{
			RelayAddr:  bundle.RelayAddr,
			Proxy:      bundle.Proxy,
			CAPEM:      bundle.CAPEM,
			CertPEM:    bundle.CertPEM,
			KeyPEM:     bundle.KeyPEM,
			ServerName: bundle.ServerName,
			Token:      bundle.Token,
			RDPAddr:    bundle.RDPAddr,
			MinBackoff: bundle.MinBackoff,
			MaxBackoff: bundle.MaxBackoff,
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

func (c *config) applyDefaults() {
	if c.RDPAddr == "" {
		c.RDPAddr = "127.0.0.1:3389"
	}
	if c.MinBackoff == "" {
		c.MinBackoff = "1s"
	}
	if c.MaxBackoff == "" {
		c.MaxBackoff = "60s"
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
	minBackoff, err := time.ParseDuration(c.MinBackoff)
	if err != nil || minBackoff <= 0 {
		return fmt.Errorf("min_backoff must be a positive duration")
	}
	maxBackoff, err := time.ParseDuration(c.MaxBackoff)
	if err != nil || maxBackoff < minBackoff {
		return fmt.Errorf("max_backoff must be a duration >= min_backoff")
	}
	return nil
}

func (c *config) resolvePaths(base string) {
	c.CAFile = resolvePath(base, c.CAFile)
	c.CertFile = resolvePath(base, c.CertFile)
	c.KeyFile = resolvePath(base, c.KeyFile)
}

func installService(bundleFile, configFile string) error {
	if bundleFile == "" && configFile == "" {
		return fmt.Errorf("agent.tnl was not found next to agent.exe; pass -bundle or place agent.tnl beside the executable")
	}
	if bundleFile != "" {
		if _, err := os.Stat(bundleFile); err != nil {
			return fmt.Errorf("agent bundle %q is not readable: %w", bundleFile, err)
		}
	}
	if configFile != "" {
		if _, err := os.Stat(configFile); err != nil {
			return fmt.Errorf("agent config %q is not readable: %w", configFile, err)
		}
	}
	if !isElevated() {
		return relaunchElevated(bundleFile, configFile)
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	args := serviceArgs(bundleFile, configFile)
	serviceConfig := serviceInstallConfig(exePath, args)
	s, err := m.CreateService(serviceName, exePath, serviceConfig, args...)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_EXISTS) || strings.Contains(strings.ToLower(err.Error()), "exists") {
			s, err = m.OpenService(serviceName)
			if err != nil {
				return err
			}
			if err := s.UpdateConfig(serviceConfig); err != nil {
				_ = s.Close()
				return fmt.Errorf("update existing service: %w", err)
			}
		} else if isAccessDenied(err) {
			return installScheduledTask(bundleFile, configFile)
		} else {
			return fmt.Errorf("create service: %w", err)
		}
	}
	defer s.Close()
	_ = eventlog.InstallAsEventCreate(serviceName, eventlog.Info|eventlog.Warning|eventlog.Error)
	if err := s.Start(); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already") {
		return fmt.Errorf("start service: %w", err)
	}
	fmt.Println("TunnelDesktop Agent service installed and started")
	return nil
}

func serviceArgs(bundleFile, configFile string) []string {
	args := []string{"-service"}
	if bundleFile != "" {
		args = append(args, "-bundle", bundleFile)
	}
	if configFile != "" {
		args = append(args, "-config", configFile)
	}
	return args
}

func serviceInstallConfig(exePath string, args []string) mgr.Config {
	return mgr.Config{
		ServiceType:    windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:      mgr.StartAutomatic,
		ErrorControl:   mgr.ErrorNormal,
		BinaryPathName: serviceBinaryPath(exePath, args),
		DisplayName:    "TunnelDesktop Agent",
		Description:    "Work-side RDP backend for TunnelDesktop.",
	}
}

func serviceBinaryPath(exePath string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, syscall.EscapeArg(exePath))
	for _, arg := range args {
		parts = append(parts, syscall.EscapeArg(arg))
	}
	return strings.Join(parts, " ")
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return err
	}
	defer s.Close()
	_, _ = s.Control(svc.Stop)
	if err := s.Delete(); err != nil {
		return err
	}
	_ = eventlog.Remove(serviceName)
	fmt.Println("TunnelDesktop Agent service removed")
	return nil
}

func printStatus() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return err
	}
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return err
	}
	fmt.Printf("service=%s state=%s accepts=%d\n", serviceName, serviceState(status.State), status.Accepts)
	return nil
}

func runService(bundleFile, configFile string) error {
	return svc.Run(serviceName, &agentService{bundleFile: bundleFile, configFile: configFile})
}

type agentService struct {
	bundleFile string
	configFile string
}

func (s *agentService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	cfg, err := loadConfig(s.bundleFile, s.configFile)
	if err != nil {
		logEvent(eventlog.Error, "load config failed: %v", err)
		return false, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, cfg)
	}()
	changes <- svc.Status{State: svc.Running, Accepts: accepts}
	for {
		select {
		case req := <-requests:
			switch req.Cmd {
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				<-errCh
				return false, 0
			case svc.Interrogate:
				changes <- req.CurrentStatus
			}
		case err := <-errCh:
			cancel()
			if err != nil {
				logEvent(eventlog.Error, "agent stopped: %v", err)
				return false, 1
			}
			return false, 0
		}
	}
}

func run(ctx context.Context, cfg config) error {
	minBackoff, _ := time.ParseDuration(cfg.MinBackoff)
	maxBackoff, _ := time.ParseDuration(cfg.MaxBackoff)
	backoff := minBackoff
	for ctx.Err() == nil {
		err := runOnce(ctx, cfg)
		if ctx.Err() != nil {
			return nil
		}
		log.Printf("agent disconnected: %v; reconnecting in %s", err, backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return nil
}

func runOnce(ctx context.Context, cfg config) error {
	tlsConfig, err := cfg.tlsConfig()
	if err != nil {
		return err
	}
	rawConn, err := tunnel.DialContext(ctx, cfg.RelayAddr, cfg.Proxy)
	if err != nil {
		return err
	}
	tlsConn := tls.Client(rawConn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return fmt.Errorf("TLS handshake: %w", err)
	}
	if err := tunnel.SendAuth(ctx, tlsConn, cfg.Token, tunnel.RoleAgent); err != nil {
		_ = tlsConn.Close()
		return err
	}
	session, err := yamux.Server(tlsConn, tunnel.YamuxConfig())
	if err != nil {
		_ = tlsConn.Close()
		return fmt.Errorf("create yamux server: %w", err)
	}
	defer session.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-done:
		}
	}()

	log.Printf("agent connected to relay %s; forwarding streams to %s", cfg.RelayAddr, cfg.RDPAddr)
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			return err
		}
		go handleStream(ctx, stream, cfg.RDPAddr)
	}
}

func selfTest(parent context.Context, cfg config) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	var d net.Dialer
	log.Printf("self-test local RDP target: %s", cfg.RDPAddr)
	rdpConn, err := d.DialContext(ctx, "tcp", cfg.RDPAddr)
	if err != nil {
		return fmt.Errorf("local RDP target %s is not reachable: %w", cfg.RDPAddr, err)
	}
	_ = rdpConn.Close()
	tlsConfig, err := cfg.tlsConfig()
	if err != nil {
		return err
	}
	log.Printf("self-test relay target: %s via %s", cfg.RelayAddr, tunnel.ProxySpecForLog(cfg.Proxy))
	rawConn, err := tunnel.DialContext(ctx, cfg.RelayAddr, cfg.Proxy)
	if err != nil {
		return fmt.Errorf("relay connection test failed: %w. %s", err, relayDialHint(err, cfg))
	}
	tlsConn := tls.Client(rawConn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return fmt.Errorf("TLS handshake failed: %w", err)
	}
	defer tlsConn.Close()
	if err := tunnel.SendAuth(ctx, tlsConn, cfg.Token, tunnel.RoleAgent); err != nil {
		return fmt.Errorf("token auth failed: %w", err)
	}
	return nil
}

func relayDialHint(err error, cfg config) string {
	errText := strings.ToLower(err.Error())
	if strings.Contains(errText, "proxy connect") {
		return fmt.Sprintf(
			"The proxy returned an HTTP error before TLS started. Confirm that the proxy allows CONNECT to %s, that it can reach the relay host or IPv6 address, and that port %s is permitted. If the work network allows direct outbound connections, regenerate the agent bundle with Work agent HTTP proxy left blank.",
			cfg.RelayAddr,
			relayPortForHint(cfg.RelayAddr),
		)
	}
	if strings.Contains(errText, "dial proxy") {
		return fmt.Sprintf("Confirm that the configured proxy %s is reachable from this PC.", tunnel.ProxySpecForLog(cfg.Proxy))
	}
	return "Confirm that the Android relay is running, the relay address is current, and the selected relay port is reachable from this PC."
}

func relayPortForHint(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "the configured relay port"
	}
	return port
}

func handleStream(ctx context.Context, stream net.Conn, rdpAddr string) {
	var d net.Dialer
	rdpConn, err := d.DialContext(ctx, "tcp", rdpAddr)
	if err != nil {
		log.Printf("RDP dial %s failed: %v", rdpAddr, err)
		_ = stream.Close()
		return
	}
	log.Printf("opened RDP stream to %s", rdpAddr)
	tunnel.Pipe(stream, rdpConn)
	log.Printf("closed RDP stream to %s", rdpAddr)
}

func (c config) tlsConfig() (*tls.Config, error) {
	if c.CAPEM != "" || c.CertPEM != "" || c.KeyPEM != "" {
		return tunnel.ClientTLSConfigFromPEM(c.CAPEM, c.CertPEM, c.KeyPEM, c.ServerName)
	}
	return tunnel.ClientTLSConfig(c.CAFile, c.CertFile, c.KeyFile, c.ServerName)
}

func installScheduledTask(bundleFile, configFile string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{strconv.Quote(exePath), "-console"}
	if bundleFile != "" {
		args = append(args, "-bundle", strconv.Quote(bundleFile))
	}
	if configFile != "" {
		args = append(args, "-config", strconv.Quote(configFile))
	}
	out, err := exec.Command("schtasks", "/Create", "/TN", "TunnelDesktop Agent", "/SC", "ONLOGON", "/TR", strings.Join(args, " "), "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("service install requires admin and Scheduled Task fallback failed: %v: %s", err, out)
	}
	fmt.Println("Installed Scheduled Task fallback for current-user logon")
	return nil
}

func relaunchElevated(bundleFile, configFile string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"-install"}
	if bundleFile != "" {
		args = append(args, "-bundle", bundleFile)
	}
	if configFile != "" {
		args = append(args, "-config", configFile)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	exe, _ := windows.UTF16PtrFromString(exePath)
	params, _ := windows.UTF16PtrFromString(joinWindowsArgs(args))
	if err := windows.ShellExecute(0, verb, exe, params, nil, windows.SW_NORMAL); err != nil {
		if isAccessDenied(err) {
			return installScheduledTask(bundleFile, configFile)
		}
		return err
	}
	fmt.Println("Elevation requested; continue in the UAC-launched installer window")
	return nil
}

func isElevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

func isAccessDenied(err error) bool {
	return err == windows.ERROR_ACCESS_DENIED || strings.Contains(strings.ToLower(err.Error()), "access is denied")
}

func defaultBundlePath(name string) string {
	exePath, err := os.Executable()
	if err != nil {
		return name
	}
	return filepath.Join(filepath.Dir(exePath), name)
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

func joinWindowsArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, strconv.Quote(arg))
	}
	return strings.Join(quoted, " ")
}

func serviceState(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start_pending"
	case svc.StopPending:
		return "stop_pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue_pending"
	case svc.PausePending:
		return "pause_pending"
	case svc.Paused:
		return "paused"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}

func logEvent(kind uint16, format string, args ...any) {
	elog, err := eventlog.Open(serviceName)
	if err != nil {
		log.Printf(format, args...)
		return
	}
	defer elog.Close()
	msg := fmt.Sprintf(format, args...)
	switch kind {
	case eventlog.Error:
		_ = elog.Error(1, msg)
	case eventlog.Warning:
		_ = elog.Warning(1, msg)
	default:
		_ = elog.Info(1, msg)
	}
}
