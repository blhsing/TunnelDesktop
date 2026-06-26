//go:build windows

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
	"sync"
	"syscall"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"golang.org/x/sys/windows"

	"tunneldesktop/internal/tunnel"
)

const (
	defaultRelayURL         = "https://test-officialwebsite.azurewebsites.net/relay/workdesk"
	defaultListenAddr       = "127.0.0.1:3390"
	legacyDefaultListenAddr = "127.0.0.1:3389"
	appIconResourceID       = 2
	statusTileWidth         = 150
	rdpStatusTileWidth      = 230
)

type config struct {
	RelayMode  string `json:"relay_mode"`
	ListenAddr string `json:"listen_addr"`
	RelayAddr  string `json:"relay_addr"`
	Proxy      string `json:"proxy"`
	CAFile     string `json:"ca_file,omitempty"`
	CertFile   string `json:"cert_file,omitempty"`
	KeyFile    string `json:"key_file,omitempty"`
	CAPEM      string `json:"ca_pem,omitempty"`
	CertPEM    string `json:"cert_pem,omitempty"`
	KeyPEM     string `json:"key_pem,omitempty"`
	ServerName string `json:"server_name,omitempty"`
	Token      string `json:"token,omitempty"`
}

type clientApp struct {
	mw *walk.MainWindow
	ni *walk.NotifyIcon

	relayURL   *walk.LineEdit
	listenAddr *walk.LineEdit
	proxy      *walk.LineEdit

	tunnelStatus *walk.Label
	workStatus   *walk.Label
	homeStatus   *walk.Label
	rdpStatus    *walk.Label
	details      *walk.TextEdit
	logView      *walk.TextEdit

	connectButton *walk.PushButton
	openRDPButton *walk.PushButton

	trayOpen    *walk.Action
	trayConnect *walk.Action
	trayStop    *walk.Action
	trayRDP     *walk.Action

	mu           sync.Mutex
	cfg          config
	cancel       context.CancelFunc
	listener     net.Listener
	activeLocal  int
	statusCancel context.CancelFunc
	exiting      bool
}

type relaySnapshot struct {
	Service string              `json:"service"`
	Time    time.Time           `json:"time"`
	Rooms   []relayRoomSnapshot `json:"rooms"`
}

type relayRoomSnapshot struct {
	ID                       string    `json:"id"`
	WaitingAgents            int       `json:"waiting_agents"`
	ActivePairs              int       `json:"active_pairs"`
	TotalPairs               int64     `json:"total_pairs"`
	LastAgentRemote          string    `json:"last_agent_remote"`
	LastAgentConnectedAt     time.Time `json:"last_agent_connected_at"`
	HomeAgentConnected       bool      `json:"home_agent_connected"`
	HomeAgentRemote          string    `json:"home_agent_remote"`
	HomeAgentConnectedAt     time.Time `json:"home_agent_connected_at"`
	LastClientRemote         string    `json:"last_client_remote"`
	LastClientConnectedAt    time.Time `json:"last_client_connected_at"`
	LastClientDisconnectedAt time.Time `json:"last_client_disconnected_at"`
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
	CheckedAt  time.Time
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	var configFile string
	var relayURL string
	var listenAddr string
	var proxyFlag string
	var consoleMode bool
	var smokeTest bool
	flag.StringVar(&configFile, "config", "", "legacy JSON config file")
	flag.StringVar(&relayURL, "relay-url", "", "Azure relay room URL")
	flag.StringVar(&listenAddr, "listen", "", "local RDP listen address")
	flag.StringVar(&proxyFlag, "proxy", "", "proxy: env, direct, or http://host:port")
	flag.BoolVar(&consoleMode, "console", false, "run in the foreground instead of the control panel")
	flag.BoolVar(&smokeTest, "ui-smoke-test", false, "start and close the GUI")
	flag.Parse()

	cfg, err := loadConfig(configFile, relayURL, listenAddr, proxyFlag)
	if err != nil {
		windowsMessageBox(appTitle(), err.Error(), windows.MB_OK|windows.MB_ICONERROR)
		os.Exit(1)
	}
	if consoleMode {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := run(ctx, cfg, false); err != nil && ctx.Err() == nil {
			log.Fatal(err)
		}
		return
	}

	app := &clientApp{cfg: cfg}
	if err := app.run(smokeTest); err != nil {
		windowsMessageBox(appTitle(), err.Error(), windows.MB_OK|windows.MB_ICONERROR)
		os.Exit(1)
	}
}

func appTitle() string {
	return "TunnelDesktop Home"
}

func (a *clientApp) run(smokeTest bool) error {
	window := MainWindow{
		AssignTo: &a.mw,
		Title:    appTitle(),
		MinSize:  Size{Width: 780, Height: 540},
		Size:     Size{Width: 900, Height: 650},
		Icon:     appIcon(),
		Layout:   VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}, Spacing: 9},
		Visible:  !smokeTest,
		Children: []Widget{
			Composite{
				Layout: Grid{Columns: 2, Spacing: 6},
				Children: []Widget{
					Label{
						Text:      "TunnelDesktop Home",
						Font:      Font{PointSize: 16, Bold: true},
						TextColor: walk.RGB(31, 41, 55),
					},
					Label{
						Text:          "Outbound WebSocket RDP",
						TextAlignment: AlignFar,
						TextColor:     walk.RGB(93, 104, 116),
					},
				},
			},
			GroupBox{
				Title:  "Status",
				Layout: Grid{Columns: 4, Spacing: 8},
				Children: []Widget{
					statusTile("Tunnel", &a.tunnelStatus, "Stopped", statusTileWidth),
					statusTile("Work Agent", &a.workStatus, "Checking", statusTileWidth),
					statusTile("Home App", &a.homeStatus, "Connecting", statusTileWidth),
					statusTile("RDP", &a.rdpStatus, defaultListenAddr, rdpStatusTileWidth),
				},
			},
			GroupBox{
				Title:  "Connection",
				Layout: Grid{Columns: 4, Spacing: 7},
				Children: []Widget{
					Label{Text: "Relay room URL"},
					LineEdit{AssignTo: &a.relayURL, Text: a.cfg.RelayAddr, CueBanner: defaultRelayURL, ColumnSpan: 3},
					Label{Text: "Local RDP address"},
					LineEdit{AssignTo: &a.listenAddr, Text: a.cfg.ListenAddr, CueBanner: defaultListenAddr},
					Label{Text: "Proxy"},
					LineEdit{AssignTo: &a.proxy, Text: a.cfg.Proxy, CueBanner: "env, direct, or http://host:port"},
				},
			},
			Composite{
				Layout: Flow{Spacing: 7},
				Children: []Widget{
					PushButton{AssignTo: &a.connectButton, Text: "Connect", MinSize: Size{Width: 120, Height: 34}, OnClicked: func() { a.connectFromUI() }},
					PushButton{AssignTo: &a.openRDPButton, Text: "Open Remote Desktop", MinSize: Size{Width: 150, Height: 34}, OnClicked: a.openRemoteDesktop},
					PushButton{Text: "Save", MinSize: Size{Width: 88, Height: 34}, OnClicked: func() { a.saveFromUI(true) }},
					PushButton{Text: "Copy RDP Address", MinSize: Size{Width: 130, Height: 34}, OnClicked: a.copyRDPAddress},
					PushButton{Text: "Relay Dashboard", MinSize: Size{Width: 130, Height: 34}, OnClicked: a.openDashboard},
					PushButton{Text: "Refresh", MinSize: Size{Width: 90, Height: 34}, OnClicked: a.refreshRelayStatusAsync},
				},
			},
			GroupBox{
				Title:  "Room Details",
				Layout: VBox{Spacing: 5},
				Children: []Widget{
					TextEdit{AssignTo: &a.details, ReadOnly: true, VScroll: true, MinSize: Size{Height: 96}, Text: "Checking relay room..."},
				},
			},
			GroupBox{
				Title:  "Activity",
				Layout: VBox{Spacing: 5},
				Children: []Widget{
					TextEdit{AssignTo: &a.logView, ReadOnly: true, VScroll: true, MinSize: Size{Height: 120}},
				},
			},
		},
	}
	if err := window.Create(); err != nil {
		return err
	}
	if err := a.setupNotifyIcon(); err != nil {
		return err
	}
	a.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if a.exiting || smokeTest {
			return
		}
		*canceled = true
		a.mw.Hide()
		_ = a.ni.ShowInfo(appTitle(), "Still running in the notification area.")
	})

	a.appendLog("Ready.")
	a.refreshLocalState()
	a.restartHomePresence()
	a.refreshRelayStatusAsync()
	a.startStatusPoller()

	if smokeTest {
		time.AfterFunc(350*time.Millisecond, func() {
			a.onUI(func() {
				a.exiting = true
				_ = a.mw.Close()
			})
		})
	}
	a.mw.Run()
	a.shutdown()
	return nil
}

func statusTile(title string, assignTo **walk.Label, initial string, width int) Widget {
	return Composite{
		MinSize:       Size{Width: width, Height: 66},
		StretchFactor: 1,
		Layout:        VBox{Margins: Margins{Left: 8, Top: 8, Right: 8, Bottom: 8}, Spacing: 4},
		Children: []Widget{
			Label{Text: title, TextColor: walk.RGB(93, 104, 116), Font: Font{Bold: true}, MinSize: Size{Width: width - 16}},
			Label{
				AssignTo:      assignTo,
				Text:          initial,
				Font:          Font{PointSize: 13, Bold: true},
				EllipsisMode:  EllipsisEnd,
				MinSize:       Size{Width: width - 16, Height: 26},
				TextColor:     walk.RGB(31, 41, 55),
				TextAlignment: AlignNear,
			},
		},
	}
}

func (a *clientApp) setupNotifyIcon() error {
	ni, err := walk.NewNotifyIcon(a.mw)
	if err != nil {
		return err
	}
	a.ni = ni
	if err := ni.SetIcon(appIcon()); err != nil {
		return err
	}
	if err := ni.SetToolTip("TunnelDesktop Home"); err != nil {
		return err
	}
	ni.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			a.showWindow()
		}
	})

	a.trayOpen = trayAction("Open Control Panel", a.showWindow)
	a.trayConnect = trayAction("Connect", a.connectFromUI)
	a.trayStop = trayAction("Stop Tunnel", a.stopTunnel)
	a.trayRDP = trayAction("Open Remote Desktop", a.openRemoteDesktop)
	quit := trayAction("Quit", func() {
		a.exiting = true
		_ = a.mw.Close()
	})

	actions := ni.ContextMenu().Actions()
	for _, action := range []*walk.Action{
		a.trayOpen,
		walk.NewSeparatorAction(),
		a.trayConnect,
		a.trayStop,
		a.trayRDP,
		walk.NewSeparatorAction(),
		quit,
	} {
		if err := actions.Add(action); err != nil {
			return err
		}
	}
	return ni.SetVisible(true)
}

func trayAction(text string, action func()) *walk.Action {
	item := walk.NewAction()
	_ = item.SetText(text)
	item.Triggered().Attach(action)
	return item
}

func appIcon() *walk.Icon {
	icon, err := walk.NewIconFromResourceId(appIconResourceID)
	if err == nil {
		return icon
	}
	return walk.IconApplication()
}

func (a *clientApp) showWindow() {
	if a.mw == nil {
		return
	}
	a.mw.Show()
	_ = a.mw.SetFocus()
}

func (a *clientApp) connectFromUI() {
	if a.isTunnelRunning() {
		a.stopTunnel()
		return
	}
	if err := a.saveFromUI(false); err != nil {
		a.showError(err)
		return
	}
	if err := a.startTunnel(true); err != nil {
		a.showError(err)
		a.appendLog("Connect failed: %v", err)
		return
	}
}

func (a *clientApp) saveFromUI(showMessage bool) error {
	cfg, err := a.configFromUI()
	if err != nil {
		if showMessage {
			a.showError(err)
		}
		return err
	}
	wasRunning := a.isTunnelRunning()
	if wasRunning {
		a.stopTunnel()
	}
	a.setConfig(cfg)
	if err := saveSettingsConfig(cfg); err != nil {
		if showMessage {
			a.showError(err)
		}
		return err
	}
	a.restartHomePresence()
	a.refreshRelayStatusAsync()
	if wasRunning {
		if err := a.startTunnel(false); err != nil {
			if showMessage {
				a.showError(err)
			}
			return err
		}
	}
	if showMessage {
		a.appendLog("Saved settings.")
	}
	return nil
}

func (a *clientApp) configFromUI() (config, error) {
	cfg := config{
		RelayMode:  "websocket",
		RelayAddr:  strings.TrimSpace(a.relayURL.Text()),
		ListenAddr: strings.TrimSpace(a.listenAddr.Text()),
		Proxy:      strings.TrimSpace(a.proxy.Text()),
	}
	cfg.applyDefaults()
	if normalized, err := normalizeRelayURL(cfg.RelayAddr); err != nil {
		return config{}, err
	} else {
		cfg.RelayAddr = normalized
	}
	if _, _, err := net.SplitHostPort(cfg.ListenAddr); err != nil {
		return config{}, fmt.Errorf("local RDP address must be host:port: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func (a *clientApp) setConfig(cfg config) {
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	a.onUI(func() {
		_ = a.relayURL.SetText(cfg.RelayAddr)
		_ = a.listenAddr.SetText(cfg.ListenAddr)
		_ = a.proxy.SetText(cfg.Proxy)
	})
}

func (a *clientApp) currentConfig() config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

func (a *clientApp) startTunnel(openRDP bool) error {
	cfg := a.currentConfig()
	ctx, cancel := context.WithCancel(context.Background())
	listener, cfg, usedFallback, err := listenLocalRDP(cfg)
	if err != nil {
		cancel()
		return localListenError(cfg.ListenAddr, err)
	}
	if usedFallback {
		a.setConfig(cfg)
		if err := saveSettingsConfig(cfg); err != nil {
			a.appendLog("Could not save fallback local RDP address: %v", err)
		}
		a.appendLog("Port 3389 was unavailable; using %s for the home-side RDP listener.", cfg.ListenAddr)
	}

	a.mu.Lock()
	if a.listener != nil {
		a.mu.Unlock()
		cancel()
		_ = listener.Close()
		if openRDP {
			launchMSTSC(cfg.ListenAddr)
		}
		return nil
	}
	a.cancel = cancel
	a.listener = listener
	a.activeLocal = 0
	a.mu.Unlock()

	a.appendLog("Local RDP listener started on %s.", listener.Addr())
	a.refreshLocalState()
	go func() {
		err := serveListener(ctx, cfg, listener, a.localConnStarted, a.localConnDone, a.appendLog)
		if err != nil && ctx.Err() == nil {
			a.appendLog("Listener stopped: %v", err)
		}
		a.mu.Lock()
		if a.listener == listener {
			a.listener = nil
			a.cancel = nil
			a.activeLocal = 0
		}
		a.mu.Unlock()
		a.refreshLocalState()
	}()

	if openRDP {
		launchMSTSC(cfg.ListenAddr)
	}
	return nil
}

func (a *clientApp) stopTunnel() {
	a.mu.Lock()
	cancel := a.cancel
	listener := a.listener
	a.cancel = nil
	a.listener = nil
	a.activeLocal = 0
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if listener != nil {
		_ = listener.Close()
		a.appendLog("Local RDP listener stopped.")
	}
	a.refreshLocalState()
}

func (a *clientApp) isTunnelRunning() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.listener != nil
}

func (a *clientApp) localConnStarted(remote string) {
	a.mu.Lock()
	a.activeLocal++
	a.mu.Unlock()
	a.appendLog("RDP connection from %s.", remote)
	a.refreshLocalState()
}

func (a *clientApp) localConnDone(remote string) {
	a.mu.Lock()
	if a.activeLocal > 0 {
		a.activeLocal--
	}
	a.mu.Unlock()
	a.appendLog("RDP connection closed from %s.", remote)
	a.refreshLocalState()
}

func (a *clientApp) refreshLocalState() {
	a.mu.Lock()
	running := a.listener != nil
	active := a.activeLocal
	cfg := a.cfg
	a.mu.Unlock()

	a.onUI(func() {
		if running {
			_ = a.tunnelStatus.SetText("Running")
			_ = a.connectButton.SetText("Stop Tunnel")
			_ = a.rdpStatus.SetText(fmt.Sprintf("%s (%d active)", cfg.ListenAddr, active))
			_ = a.trayConnect.SetEnabled(false)
			_ = a.trayStop.SetEnabled(true)
			_ = a.trayRDP.SetEnabled(true)
			_ = a.ni.SetToolTip("TunnelDesktop Home: running")
		} else {
			_ = a.tunnelStatus.SetText("Stopped")
			_ = a.connectButton.SetText("Connect")
			_ = a.rdpStatus.SetText(cfg.ListenAddr)
			_ = a.trayConnect.SetEnabled(true)
			_ = a.trayStop.SetEnabled(false)
			_ = a.trayRDP.SetEnabled(false)
			_ = a.ni.SetToolTip("TunnelDesktop Home: stopped")
		}
	})
}

func (a *clientApp) openRemoteDesktop() {
	cfg := a.currentConfig()
	if !a.isTunnelRunning() {
		if err := a.saveFromUI(false); err != nil {
			a.showError(err)
			return
		}
		if err := a.startTunnel(false); err != nil {
			a.showError(err)
			return
		}
		cfg = a.currentConfig()
	}
	launchMSTSC(cfg.ListenAddr)
}

func (a *clientApp) copyRDPAddress() {
	cfg := a.currentConfig()
	if err := walk.Clipboard().SetText(mstscTarget(cfg.ListenAddr)); err != nil {
		a.showError(err)
		return
	}
	a.appendLog("Copied RDP address: %s", mstscTarget(cfg.ListenAddr))
}

func (a *clientApp) openDashboard() {
	cfg := a.currentConfig()
	if err := shellOpen(dashboardURL(cfg.RelayAddr)); err != nil {
		a.showError(err)
	}
}

func (a *clientApp) restartHomePresence() {
	a.mu.Lock()
	if a.statusCancel != nil {
		a.statusCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.statusCancel = cancel
	cfg := a.cfg
	a.mu.Unlock()

	go a.homePresenceLoop(ctx, cfg)
}

func (a *clientApp) homePresenceLoop(ctx context.Context, cfg config) {
	for {
		a.setHomePresence("Connecting")
		conn, err := tunnel.DialWebSocket(ctx, cfg.RelayAddr, cfg.Proxy, tunnel.RoleHomeAgent, cfg.Token)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.setHomePresence("Offline")
			a.appendLog("Home status connection failed: %v", err)
		} else {
			a.setHomePresence("Online")
			a.appendLog("Home status connected to %s.", cfg.RelayAddr)
			_, _, err = conn.Read(ctx)
			tunnel.CloseWebSocket(conn)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				a.setHomePresence("Reconnecting")
				a.appendLog("Home status disconnected: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (a *clientApp) setHomePresence(text string) {
	a.onUI(func() {
		_ = a.homeStatus.SetText(text)
	})
}

func (a *clientApp) refreshRelayStatusAsync() {
	cfg := a.currentConfig()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()
		summary, err := queryRelaySummary(ctx, cfg)
		a.onUI(func() {
			if err != nil {
				_ = a.workStatus.SetText("Check relay")
				_ = a.details.SetText("Relay status: " + err.Error())
				return
			}
			if summary.WorkOnline {
				_ = a.workStatus.SetText("Connected")
			} else {
				_ = a.workStatus.SetText("Waiting")
			}
			if summary.HomeOnline {
				_ = a.homeStatus.SetText("Online")
			}
			_ = a.details.SetText(formatRelayDetails(summary, cfg))
		})
	}()
}

func (a *clientApp) startStatusPoller() {
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if a.mw == nil || a.exiting {
				return
			}
			a.refreshRelayStatusAsync()
		}
	}()
}

func (a *clientApp) appendLog(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	log.Print(line)
	if a.logView == nil || a.mw == nil {
		return
	}
	a.onUI(func() {
		a.logView.AppendText(time.Now().Format("15:04:05") + "  " + line + "\r\n")
	})
}

func (a *clientApp) showError(err error) {
	if err == nil {
		return
	}
	walk.MsgBox(a.mw, appTitle(), err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
}

func (a *clientApp) onUI(f func()) {
	if a.mw == nil {
		return
	}
	a.mw.Synchronize(f)
}

func (a *clientApp) shutdown() {
	a.exiting = true
	a.stopTunnel()
	a.mu.Lock()
	cancel := a.statusCancel
	a.statusCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if a.ni != nil {
		_ = a.ni.SetVisible(false)
		_ = a.ni.Dispose()
	}
}

func loadConfig(configFile, relayURL, listenAddr, proxyFlag string) (config, error) {
	var cfg config
	if strings.TrimSpace(configFile) != "" {
		data, err := os.ReadFile(configFile)
		if err != nil {
			return config{}, fmt.Errorf("read config: %w", err)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return config{}, fmt.Errorf("decode config: %w", err)
		}
		cfg.resolvePaths(filepath.Dir(configFile))
	} else {
		cfg = defaultConfig()
		if saved, ok, err := loadSavedConfig(); err != nil {
			return config{}, err
		} else if ok {
			cfg.merge(saved)
		}
	}
	if strings.TrimSpace(relayURL) != "" {
		cfg.RelayMode = "websocket"
		cfg.RelayAddr = strings.TrimSpace(relayURL)
	}
	if strings.TrimSpace(listenAddr) != "" {
		cfg.ListenAddr = strings.TrimSpace(listenAddr)
	}
	if strings.TrimSpace(proxyFlag) != "" {
		cfg.Proxy = strings.TrimSpace(proxyFlag)
	}
	cfg.applyDefaults()
	if strings.TrimSpace(configFile) == "" && strings.TrimSpace(listenAddr) == "" && isLegacyDefaultListenAddr(cfg.ListenAddr) {
		cfg.ListenAddr = defaultListenAddr
	}
	if tunnel.IsWebSocketRelay(cfg.RelayAddr) {
		normalized, err := normalizeRelayURL(cfg.RelayAddr)
		if err != nil {
			return config{}, err
		}
		cfg.RelayAddr = normalized
	}
	return cfg, cfg.validate()
}

func defaultConfig() config {
	return config{
		RelayMode:  "websocket",
		ListenAddr: defaultListenAddr,
		RelayAddr:  defaultRelayURL,
		Proxy:      "env",
	}
}

func loadSavedConfig() (config, bool, error) {
	path, err := settingsPath()
	if err != nil {
		return config{}, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config{}, false, nil
	}
	if err != nil {
		return config{}, false, fmt.Errorf("read saved settings: %w", err)
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, false, fmt.Errorf("decode saved settings: %w", err)
	}
	return cfg, true, nil
}

func saveSettingsConfig(cfg config) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	data, err := json.MarshalIndent(config{
		RelayMode:  "websocket",
		ListenAddr: cfg.ListenAddr,
		RelayAddr:  cfg.RelayAddr,
		Proxy:      cfg.Proxy,
		Token:      cfg.Token,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

func settingsPath() (string, error) {
	base := os.Getenv("APPDATA")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		base = dir
	}
	return filepath.Join(base, "TunnelDesktop", "home-client.json"), nil
}

func (c *config) merge(other config) {
	if strings.TrimSpace(other.RelayMode) != "" {
		c.RelayMode = other.RelayMode
	}
	if strings.TrimSpace(other.ListenAddr) != "" {
		c.ListenAddr = other.ListenAddr
	}
	if strings.TrimSpace(other.RelayAddr) != "" {
		c.RelayAddr = other.RelayAddr
	}
	if strings.TrimSpace(other.Proxy) != "" {
		c.Proxy = other.Proxy
	}
	if strings.TrimSpace(other.Token) != "" {
		c.Token = other.Token
	}
}

func (c *config) applyDefaults() {
	if c.RelayMode == "" {
		if tunnel.IsWebSocketRelay(c.RelayAddr) {
			c.RelayMode = "websocket"
		} else {
			c.RelayMode = "tls"
		}
	}
	if c.ListenAddr == "" {
		c.ListenAddr = defaultListenAddr
	}
	if c.RelayMode == "websocket" && c.RelayAddr == "" {
		c.RelayAddr = defaultRelayURL
	}
	if c.RelayMode == "websocket" && c.Proxy == "" {
		c.Proxy = "env"
	}
	if c.ServerName == "" && c.RelayAddr != "" {
		c.ServerName = tunnel.HostFromRelayAddress(c.RelayAddr)
	}
}

func (c config) validate() error {
	if c.RelayAddr == "" {
		return fmt.Errorf("relay URL is required")
	}
	if c.RelayMode != "tls" && c.RelayMode != "websocket" {
		return fmt.Errorf("unsupported relay mode %q", c.RelayMode)
	}
	if c.RelayMode == "websocket" {
		if _, err := url.ParseRequestURI(c.RelayAddr); err != nil {
			return fmt.Errorf("relay URL is invalid: %w", err)
		}
		return nil
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

func run(ctx context.Context, cfg config, openMSTSC bool) error {
	listener, cfg, usedFallback, err := listenLocalRDP(cfg)
	if err != nil {
		return localListenError(cfg.ListenAddr, err)
	}
	defer listener.Close()
	if usedFallback {
		log.Printf("port 3389 was unavailable; using %s for the home-side RDP listener", cfg.ListenAddr)
	}
	log.Printf("client listening on %s; mstsc should target this address", listener.Addr())
	if openMSTSC {
		launchMSTSC(cfg.ListenAddr)
	}
	return serveListener(ctx, cfg, listener, func(string) {}, func(string) {}, func(format string, args ...any) {
		log.Printf(format, args...)
	})
}

func serveListener(ctx context.Context, cfg config, listener net.Listener, started func(string), done func(string), logf func(string, ...any)) error {
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
		remote := conn.RemoteAddr().String()
		started(remote)
		go handleLocalConn(ctx, cfg, conn, remote, done, logf)
	}
}

func handleLocalConn(ctx context.Context, cfg config, localConn net.Conn, remote string, done func(string), logf func(string, ...any)) {
	defer done(remote)
	relayConn, err := dialRelay(ctx, cfg)
	if err != nil {
		logf("Relay dial failed for %s: %v", remote, err)
		_ = localConn.Close()
		return
	}
	logf("Bridging local RDP connection from %s.", remote)
	tunnel.Pipe(localConn, relayConn)
	logf("Closed local RDP connection from %s.", remote)
}

func dialRelay(ctx context.Context, cfg config) (net.Conn, error) {
	if cfg.RelayMode == "websocket" || tunnel.IsWebSocketRelay(cfg.RelayAddr) {
		return tunnel.DialWebSocketStream(ctx, cfg.RelayAddr, cfg.Proxy, tunnel.RoleClient, cfg.Token)
	}
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
		"Local RDP address: " + mstscTarget(cfg.ListenAddr),
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
	if !summary.CheckedAt.IsZero() {
		lines = append(lines, "Checked: "+summary.CheckedAt.Local().Format("2006-01-02 15:04:05"))
	}
	return strings.Join(lines, "\r\n")
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
		return "", fmt.Errorf("relay URL must include a host")
	}
	switch parsed.Scheme {
	case "https", "http":
	case "wss":
		parsed.Scheme = "https"
	case "ws":
		parsed.Scheme = "http"
	default:
		return "", fmt.Errorf("relay URL must start with https://")
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
		return "", "", fmt.Errorf("relay URL must include a host")
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

func dashboardURL(relayAddr string) string {
	normalized, err := normalizeRelayURL(relayAddr)
	if err != nil {
		return defaultRelayURL
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return normalized
	}
	if parsed.Scheme == "ws" {
		parsed.Scheme = "http"
	} else if parsed.Scheme == "wss" {
		parsed.Scheme = "https"
	}
	return parsed.String()
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

func listenLocalRDP(cfg config) (net.Listener, config, bool, error) {
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err == nil {
		return listener, cfg, false, nil
	}
	if !isLegacyDefaultListenAddr(cfg.ListenAddr) {
		return nil, cfg, false, err
	}

	fallback := cfg
	fallback.ListenAddr = defaultListenAddr
	listener, fallbackErr := net.Listen("tcp", fallback.ListenAddr)
	if fallbackErr == nil {
		return listener, fallback, true, nil
	}
	return nil, cfg, false, fmt.Errorf("%w; fallback %s also failed: %v", err, fallback.ListenAddr, fallbackErr)
}

func localListenError(listenAddr string, err error) error {
	message := fmt.Sprintf("listen %s: %v", listenAddr, err)
	if isLegacyDefaultListenAddr(listenAddr) {
		message += ". Port 3389 is Windows' normal Remote Desktop port and may already be in use or reserved on this PC. Set Local RDP address to 127.0.0.1:3390, then connect again."
	}
	return errors.New(message)
}

func isLegacyDefaultListenAddr(listenAddr string) bool {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listenAddr))
	if err != nil {
		return strings.EqualFold(strings.TrimSpace(listenAddr), legacyDefaultListenAddr)
	}
	return port == "3389" && (host == "" || host == "127.0.0.1" || strings.EqualFold(host, "localhost"))
}

func launchMSTSC(listenAddr string) {
	_ = exec.Command("mstsc.exe", "/v:"+mstscTarget(listenAddr)).Start()
}

func mstscTarget(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		host = "127.0.0.1"
		port = "3390"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + port
	}
	return net.JoinHostPort(host, port)
}

func shellOpen(path string) error {
	return shellExecute("open", path, "", "")
}

func shellExecute(verb, file, params, dir string) error {
	verbPtr, _ := windows.UTF16PtrFromString(verb)
	filePtr, _ := windows.UTF16PtrFromString(file)
	paramsPtr, _ := windows.UTF16PtrFromString(params)
	dirPtr, _ := windows.UTF16PtrFromString(dir)
	return windows.ShellExecute(0, verbPtr, filePtr, paramsPtr, dirPtr, windows.SW_SHOWNORMAL)
}

func resolvePath(base, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Clean(filepath.Join(base, value))
}

func windowsMessageBox(title, text string, style uint32) {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	textPtr, _ := windows.UTF16PtrFromString(text)
	_, _ = windows.MessageBox(0, textPtr, titlePtr, style)
}
