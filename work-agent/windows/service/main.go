package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"

	"deskferry/internal/tunnel"
)

const serviceName = "DeskFerryAgent"
const defaultRelayURL = "https://test-officialwebsite.azurewebsites.net/relay/"

type config struct {
	RelayAddr  string   `json:"relay_addr"`
	RelayAddrs []string `json:"relay_addrs,omitempty"`
	Proxy      string   `json:"proxy"`
	RDPAddr    string   `json:"rdp_addr"`
	MinBackoff string   `json:"min_backoff"`
	MaxBackoff string   `json:"max_backoff"`
}

type relayURLFlag []string

func (f *relayURLFlag) Set(value string) error {
	*f = append(*f, splitRelayURLs(value)...)
	return nil
}

func (f *relayURLFlag) String() string {
	return joinRelayURLs([]string(*f))
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	var relayURLs relayURLFlag
	var proxyFlag string
	var rdpFlag string
	var consoleMode bool
	var serviceMode bool
	var installMode bool
	var uninstallMode bool
	var statusMode bool
	var selfTestMode bool
	flag.Var(&relayURLs, "relay-url", "relay service URL; repeat to add more relay URLs")
	flag.StringVar(&proxyFlag, "proxy", "", "HTTP proxy for Azure relay WebSocket, or direct/env/auto")
	flag.StringVar(&rdpFlag, "rdp", "", "local RDP target")
	flag.BoolVar(&consoleMode, "console", false, "run in the foreground for debugging")
	flag.BoolVar(&serviceMode, "service", false, "run under the Windows service control manager")
	flag.BoolVar(&installMode, "install", false, "install and start the Windows service")
	flag.BoolVar(&uninstallMode, "uninstall", false, "stop and remove the Windows service")
	flag.BoolVar(&statusMode, "status", false, "print Windows service status")
	flag.BoolVar(&selfTestMode, "self-test", false, "test local RDP and relay WebSocket connectivity")
	flag.Parse()

	relayURL := relayURLs.String()
	if relayURL == "" {
		relayURL = defaultRelayURL
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
		if err := runService(relayURL, proxyFlag, rdpFlag); err != nil {
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
		cfg, err := loadConfig(relayURL, proxyFlag, rdpFlag)
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
		if err := installService(relayURL, proxyFlag, rdpFlag); err != nil {
			log.Fatal(err)
		}
		return
	}

	cfg, err := loadConfig(relayURL, proxyFlag, rdpFlag)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func loadConfig(relayURL, proxyOverride, rdpOverride string) (config, error) {
	cfg := config{
		RelayAddrs: splitRelayURLs(relayURL),
		Proxy:      strings.TrimSpace(proxyOverride),
		RDPAddr:    strings.TrimSpace(rdpOverride),
	}
	cfg.applyDefaults()
	return cfg, cfg.validate()
}

func (c *config) applyDefaults() {
	c.normalizeRelayAddresses()
	if c.RDPAddr == "" {
		c.RDPAddr = "127.0.0.1:3389"
	}
	if c.RelayAddr == "" {
		c.RelayAddr = defaultRelayURL
		c.normalizeRelayAddresses()
	}
	if c.Proxy == "" {
		c.Proxy = "env"
	}
	if c.MinBackoff == "" {
		c.MinBackoff = "1s"
	}
	if c.MaxBackoff == "" {
		c.MaxBackoff = "60s"
	}
}

func (c config) validate() error {
	relayAddrs := c.relayAddresses()
	if len(relayAddrs) == 0 {
		return fmt.Errorf("relay_addr is required")
	}
	for _, relayAddr := range relayAddrs {
		if !tunnel.IsWebSocketRelay(relayAddr) {
			return fmt.Errorf("relay URL %q must start with http://, https://, ws://, or wss://", relayAddr)
		}
		if _, err := tunnel.WebSocketEndpoint(relayAddr); err != nil {
			return err
		}
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

func (c *config) normalizeRelayAddresses() {
	values := make([]string, 0, 1+len(c.RelayAddrs))
	values = append(values, splitRelayURLs(c.RelayAddr)...)
	for _, relayAddr := range c.RelayAddrs {
		values = append(values, splitRelayURLs(relayAddr)...)
	}
	c.RelayAddrs = uniqueRelayURLs(values)
	if len(c.RelayAddrs) > 0 {
		c.RelayAddr = c.RelayAddrs[0]
	}
}

func (c config) relayAddresses() []string {
	if len(c.RelayAddrs) > 0 {
		return append([]string(nil), c.RelayAddrs...)
	}
	return uniqueRelayURLs(splitRelayURLs(c.RelayAddr))
}

func (c config) withRelayAddress(relayAddr string) config {
	next := c
	next.RelayAddr = strings.TrimSpace(relayAddr)
	next.RelayAddrs = []string{next.RelayAddr}
	return next
}

func splitRelayURLs(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '\r' || r == '\n' || r == ',' || r == ';'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func uniqueRelayURLs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func joinRelayURLs(values []string) string {
	return strings.Join(uniqueRelayURLs(values), ";")
}

func installService(relayURL, proxyFlag, rdpFlag string) error {
	if strings.TrimSpace(relayURL) == "" {
		relayURL = defaultRelayURL
	}
	if !isElevated() {
		return relaunchElevated(relayURL, proxyFlag, rdpFlag)
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

	args := serviceArgs(relayURL, proxyFlag, rdpFlag)
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
			return installScheduledTask(relayURL, proxyFlag, rdpFlag)
		} else {
			return fmt.Errorf("create service: %w", err)
		}
	}
	defer s.Close()
	_ = eventlog.InstallAsEventCreate(serviceName, eventlog.Info|eventlog.Warning|eventlog.Error)
	if err := s.Start(); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already") {
		return fmt.Errorf("start service: %w", err)
	}
	fmt.Println("DeskFerry Agent service installed and started")
	return nil
}

func serviceArgs(relayURL, proxyFlag, rdpFlag string) []string {
	args := []string{"-service"}
	if relayURL != "" {
		args = append(args, "-relay-url", relayURL)
	}
	if proxyFlag != "" {
		args = append(args, "-proxy", proxyFlag)
	}
	if rdpFlag != "" {
		args = append(args, "-rdp", rdpFlag)
	}
	return args
}

func serviceInstallConfig(exePath string, args []string) mgr.Config {
	return mgr.Config{
		ServiceType:    windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:      mgr.StartAutomatic,
		ErrorControl:   mgr.ErrorNormal,
		BinaryPathName: serviceBinaryPath(exePath, args),
		DisplayName:    "DeskFerry Agent",
		Description:    "Work-side RDP backend for DeskFerry.",
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
	fmt.Println("DeskFerry Agent service removed")
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

func runService(relayURL, proxyFlag, rdpFlag string) error {
	return svc.Run(serviceName, &agentService{relayURL: relayURL, proxyFlag: proxyFlag, rdpFlag: rdpFlag})
}

type agentService struct {
	relayURL  string
	proxyFlag string
	rdpFlag   string
}

func (s *agentService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	cfg, err := loadConfig(s.relayURL, s.proxyFlag, s.rdpFlag)
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
	return runWebSocketPools(ctx, cfg)
}

func runWebSocketPools(ctx context.Context, cfg config) error {
	const slots = 4
	var wg sync.WaitGroup
	relayAddrs := cfg.relayAddresses()
	log.Printf("starting websocket agent pools for %d relay URL(s)", len(relayAddrs))
	for _, relayAddr := range relayAddrs {
		relayCfg := cfg.withRelayAddress(relayAddr)
		for i := 0; i < slots; i++ {
			wg.Add(1)
			go func(slot int, slotCfg config) {
				defer wg.Done()
				runWebSocketSlot(ctx, slotCfg, slot)
			}(i+1, relayCfg)
		}
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

func runWebSocketSlot(ctx context.Context, cfg config, slot int) {
	minBackoff, _ := time.ParseDuration(cfg.MinBackoff)
	maxBackoff, _ := time.ParseDuration(cfg.MaxBackoff)
	backoff := minBackoff
	for ctx.Err() == nil {
		err := runWebSocketOnce(ctx, cfg, slot)
		if ctx.Err() != nil {
			return
		}
		log.Printf("websocket agent slot %d for relay %s disconnected: %v; reconnecting in %s", slot, cfg.RelayAddr, err, backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func runWebSocketOnce(ctx context.Context, cfg config, slot int) error {
	ws, err := tunnel.DialWebSocket(ctx, cfg.RelayAddr, cfg.Proxy, tunnel.RoleAgent, "")
	if err != nil {
		return err
	}
	defer tunnel.CloseWebSocket(ws)

	log.Printf("websocket agent slot %d connected to relay %s via %s", slot, cfg.RelayAddr, tunnel.ProxySpecForLog(cfg.Proxy))
	if err := tunnel.AwaitWebSocketStart(ctx, ws); err != nil {
		return err
	}
	log.Printf("websocket agent slot %d paired on relay %s; forwarding to %s", slot, cfg.RelayAddr, cfg.RDPAddr)
	stream := tunnel.WebSocketNetConn(ctx, ws)
	handleStream(ctx, stream, cfg.RDPAddr)
	return nil
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

	var failures []error
	for _, relayAddr := range cfg.relayAddresses() {
		relayCfg := cfg.withRelayAddress(relayAddr)
		if err := selfTestRelay(ctx, relayCfg); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", relayAddr, err))
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	return nil
}

func selfTestRelay(ctx context.Context, cfg config) error {
	log.Printf("self-test relay target: %s via %s", cfg.RelayAddr, tunnel.ProxySpecForLog(cfg.Proxy))
	ws, err := tunnel.DialWebSocket(ctx, cfg.RelayAddr, cfg.Proxy, tunnel.RoleProbe, "")
	if err != nil {
		return fmt.Errorf("websocket relay connection test failed: %w. %s", err, relayDialHint(err, cfg))
	}
	tunnel.CloseWebSocket(ws)
	return nil
}

func relayDialHint(err error, cfg config) string {
	errText := strings.ToLower(err.Error())
	if strings.Contains(errText, "proxy connect") {
		return fmt.Sprintf(
			"The proxy returned an HTTP error before the WebSocket handshake. Confirm that the proxy allows CONNECT to %s, that it can reach the relay host, and that port %s is permitted. If the work network allows direct outbound connections, rerun the agent without -proxy.",
			cfg.RelayAddr,
			relayPortForHint(cfg.RelayAddr),
		)
	}
	if strings.Contains(errText, "dial proxy") {
		return fmt.Sprintf("Confirm that the configured proxy %s is reachable from this PC.", tunnel.ProxySpecForLog(cfg.Proxy))
	}
	return "Confirm that the Azure relay URL is current, WebSockets are enabled, and the selected relay endpoint is reachable from this PC."
}

func relayPortForHint(addr string) string {
	if tunnel.IsWebSocketRelay(addr) {
		u, err := url.Parse(strings.TrimSpace(addr))
		if err == nil {
			if port := u.Port(); port != "" {
				return port
			}
			switch strings.ToLower(u.Scheme) {
			case "https", "wss":
				return "443"
			case "http", "ws":
				return "80"
			}
		}
	}
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

func installScheduledTask(relayURL, proxyFlag, rdpFlag string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{strconv.Quote(exePath), "-console"}
	if relayURL != "" {
		args = append(args, "-relay-url", strconv.Quote(relayURL))
	}
	if proxyFlag != "" {
		args = append(args, "-proxy", strconv.Quote(proxyFlag))
	}
	if rdpFlag != "" {
		args = append(args, "-rdp", strconv.Quote(rdpFlag))
	}
	out, err := exec.Command("schtasks", "/Create", "/TN", "DeskFerry Agent", "/SC", "ONLOGON", "/TR", strings.Join(args, " "), "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("service install requires admin and Scheduled Task fallback failed: %v: %s", err, out)
	}
	fmt.Println("Installed Scheduled Task fallback for current-user logon")
	return nil
}

func relaunchElevated(relayURL, proxyFlag, rdpFlag string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"-install"}
	if relayURL != "" {
		args = append(args, "-relay-url", relayURL)
	}
	if proxyFlag != "" {
		args = append(args, "-proxy", proxyFlag)
	}
	if rdpFlag != "" {
		args = append(args, "-rdp", rdpFlag)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	exe, _ := windows.UTF16PtrFromString(exePath)
	params, _ := windows.UTF16PtrFromString(joinWindowsArgs(args))
	if err := windows.ShellExecute(0, verb, exe, params, nil, windows.SW_NORMAL); err != nil {
		if isAccessDenied(err) {
			return installScheduledTask(relayURL, proxyFlag, rdpFlag)
		}
		return err
	}
	fmt.Println("Elevation requested; continue in the UAC-launched configurator window")
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
