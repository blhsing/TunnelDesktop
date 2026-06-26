//go:build windows

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"

	"tunneldesktop/internal/relaycore"
)

const (
	serviceName        = "TunnelDesktopAgent"
	serviceDisplayName = "TunnelDesktop Agent"
	installedAgentName = "agent.exe"
	installedBundle    = "agent.tnl"
)

type app struct {
	mw         *walk.MainWindow
	installDir *walk.LineEdit
	agentPath  *walk.LineEdit
	bundlePath *walk.LineEdit
	status     *walk.Label
	log        *walk.TextEdit
}

type actionOptions struct {
	InstallDir string
	AgentPath  string
	BundlePath string
}

type serviceInfo struct {
	Installed bool
	State     uint32
	ProcessID uint32
}

func main() {
	if hasElevatedAction(os.Args[1:]) {
		runElevatedAction(os.Args[1:])
		return
	}
	if err := (&app{}).run(); err != nil {
		windowsMessageBox(appTitle(), err.Error(), windows.MB_OK|windows.MB_ICONERROR)
		os.Exit(1)
	}
}

func (a *app) run() error {
	installDir := defaultInstallDir()
	agentPath := defaultAgentPath()
	bundlePath := defaultBundlePath(installDir)

	window := MainWindow{
		AssignTo: &a.mw,
		Title:    appTitle(),
		MinSize:  Size{Width: 760, Height: 520},
		Size:     Size{Width: 860, Height: 620},
		Layout:   VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 8},
		Children: []Widget{
			GroupBox{
				Title:  "Files",
				Layout: Grid{Columns: 3, Spacing: 6},
				Children: []Widget{
					Label{Text: "Install directory"},
					LineEdit{AssignTo: &a.installDir, Text: installDir, ColumnSpan: 1},
					PushButton{Text: "Browse", OnClicked: a.browseInstallDir},

					Label{Text: "Agent executable"},
					LineEdit{AssignTo: &a.agentPath, Text: agentPath, ColumnSpan: 1},
					PushButton{Text: "Browse", OnClicked: a.browseAgentPath},

					Label{Text: "Agent bundle"},
					LineEdit{AssignTo: &a.bundlePath, Text: bundlePath, ColumnSpan: 1},
					PushButton{Text: "Browse", OnClicked: a.browseBundlePath},
				},
			},
			GroupBox{
				Title:  "Service",
				Layout: VBox{Spacing: 6},
				Children: []Widget{
					Label{AssignTo: &a.status, Text: "Status: checking..."},
					Composite{
						Layout: Flow{Spacing: 6},
						Children: []Widget{
							PushButton{Text: "Install / Update", OnClicked: func() { a.runAction("install") }},
							PushButton{Text: "Start", OnClicked: func() { a.runAction("start") }},
							PushButton{Text: "Stop", OnClicked: func() { a.runAction("stop") }},
							PushButton{Text: "Restart", OnClicked: func() { a.runAction("restart") }},
							PushButton{Text: "Uninstall", OnClicked: func() { a.runAction("uninstall") }},
							PushButton{Text: "Self-test", OnClicked: a.runSelfTest},
							PushButton{Text: "Open Folder", OnClicked: a.openInstallFolder},
							PushButton{Text: "Refresh", OnClicked: a.refreshStatus},
						},
					},
				},
			},
			TextEdit{AssignTo: &a.log, ReadOnly: true, VScroll: true, MinSize: Size{Height: 220}},
		},
	}
	if err := window.Create(); err != nil {
		return err
	}
	a.refreshStatus()
	a.appendLog("Ready.")
	a.mw.Run()
	return nil
}

func appTitle() string {
	exe, err := os.Executable()
	if err == nil && strings.Contains(strings.ToLower(filepath.Base(exe)), "installer") {
		return "TunnelDesktop Agent Installer"
	}
	return "TunnelDesktop Agent Configurator"
}

func (a *app) browseInstallDir() {
	dlg := new(walk.FileDialog)
	dlg.Title = "Select install directory"
	dlg.InitialDirPath = a.installDir.Text()
	if ok, err := dlg.ShowBrowseFolder(a.mw); err != nil {
		a.showError(err)
	} else if ok {
		a.installDir.SetText(dlg.FilePath)
	}
}

func (a *app) browseAgentPath() {
	dlg := new(walk.FileDialog)
	dlg.Title = "Select agent.exe"
	dlg.FilePath = a.agentPath.Text()
	dlg.Filter = "Executable files (*.exe)|*.exe|All files (*.*)|*.*"
	if ok, err := dlg.ShowOpen(a.mw); err != nil {
		a.showError(err)
	} else if ok {
		a.agentPath.SetText(dlg.FilePath)
	}
}

func (a *app) browseBundlePath() {
	dlg := new(walk.FileDialog)
	dlg.Title = "Select agent.tnl"
	dlg.FilePath = a.bundlePath.Text()
	dlg.Filter = "TunnelDesktop bundles (*.tnl)|*.tnl|All files (*.*)|*.*"
	if ok, err := dlg.ShowOpen(a.mw); err != nil {
		a.showError(err)
	} else if ok {
		a.bundlePath.SetText(dlg.FilePath)
	}
}

func (a *app) runAction(action string) {
	opts := a.options()
	if action == "install" {
		if err := validateInstallInputs(opts); err != nil {
			a.showError(err)
			return
		}
	}
	go func() {
		if !isElevated() {
			if err := relaunchElevatedAction(action, opts); err != nil {
				a.appendLog("Elevation failed: %v", err)
				return
			}
			a.appendLog("Elevation requested for %s.", action)
			return
		}
		message, err := performAction(action, opts)
		if err != nil {
			a.appendLog("%s failed: %v", action, err)
		} else {
			a.appendLog("%s", message)
		}
		a.refreshStatus()
	}()
}

func (a *app) runSelfTest() {
	opts := a.options()
	exePath, bundlePath := installedPaths(opts.InstallDir)
	if _, err := os.Stat(exePath); err != nil {
		exePath = opts.AgentPath
	}
	if _, err := os.Stat(bundlePath); err != nil {
		bundlePath = opts.BundlePath
	}
	if exePath == "" || bundlePath == "" {
		a.showError(errors.New("select an agent executable and agent bundle first"))
		return
	}
	go func() {
		a.appendLog("Running self-test with %s", bundlePath)
		cmd := exec.Command(exePath, "-self-test", "-bundle", bundlePath)
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		err := cmd.Run()
		text := strings.TrimSpace(output.String())
		if text != "" {
			a.appendLog("%s", text)
		}
		if err != nil {
			a.appendLog("Self-test failed: %v", err)
			return
		}
		a.appendLog("Self-test OK.")
	}()
}

func (a *app) openInstallFolder() {
	dir := strings.TrimSpace(a.installDir.Text())
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0755)
	if err := shellOpen(dir); err != nil {
		a.showError(err)
	}
}

func (a *app) refreshStatus() {
	info, err := queryServiceInfo()
	text := "Status: "
	if err != nil {
		text += err.Error()
	} else if !info.Installed {
		text += "not installed"
	} else {
		text += serviceStateText(info.State)
		if info.ProcessID != 0 {
			text += fmt.Sprintf(" (pid %d)", info.ProcessID)
		}
	}
	if a.status != nil {
		a.mw.Synchronize(func() {
			a.status.SetText(text)
		})
	}
}

func (a *app) options() actionOptions {
	return actionOptions{
		InstallDir: strings.TrimSpace(a.installDir.Text()),
		AgentPath:  strings.TrimSpace(a.agentPath.Text()),
		BundlePath: strings.TrimSpace(a.bundlePath.Text()),
	}
}

func (a *app) appendLog(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if a.log == nil {
		return
	}
	a.mw.Synchronize(func() {
		a.log.AppendText(time.Now().Format("15:04:05") + "  " + line + "\r\n")
	})
}

func (a *app) showError(err error) {
	if err == nil {
		return
	}
	walk.MsgBox(a.mw, appTitle(), err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
}

func hasElevatedAction(args []string) bool {
	for _, arg := range args {
		if arg == "-elevated-action" || strings.HasPrefix(arg, "-elevated-action=") {
			return true
		}
	}
	return false
}

func runElevatedAction(args []string) {
	fs := flag.NewFlagSet("elevated-action", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	action := fs.String("elevated-action", "", "service action")
	installDir := fs.String("install-dir", "", "install directory")
	agentPath := fs.String("agent", "", "agent executable")
	bundlePath := fs.String("bundle", "", "agent bundle")
	if err := fs.Parse(args); err != nil {
		windowsMessageBox(appTitle(), err.Error(), windows.MB_OK|windows.MB_ICONERROR)
		os.Exit(2)
	}
	message, err := performAction(*action, actionOptions{
		InstallDir: *installDir,
		AgentPath:  *agentPath,
		BundlePath: *bundlePath,
	})
	if err != nil {
		windowsMessageBox(appTitle(), err.Error(), windows.MB_OK|windows.MB_ICONERROR)
		os.Exit(1)
	}
	windowsMessageBox(appTitle(), message, windows.MB_OK|windows.MB_ICONINFORMATION)
}

func performAction(action string, opts actionOptions) (string, error) {
	switch action {
	case "install":
		return installOrUpdate(opts)
	case "start":
		return startService()
	case "stop":
		return stopService()
	case "restart":
		if _, err := stopService(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return "", err
		}
		return startService()
	case "uninstall":
		return uninstallService()
	default:
		return "", fmt.Errorf("unknown action %q", action)
	}
}

func installOrUpdate(opts actionOptions) (string, error) {
	if err := validateInstallInputs(opts); err != nil {
		return "", err
	}
	installDir, err := filepath.Abs(opts.InstallDir)
	if err != nil {
		return "", err
	}
	agentDest, bundleDest := installedPaths(installDir)
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return "", fmt.Errorf("create install directory: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return "", fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	existed := false
	if s, err := m.OpenService(serviceName); err == nil {
		existed = true
		if err := stopMgrService(s); err != nil {
			s.Close()
			return "", fmt.Errorf("stop existing service: %w", err)
		}
		s.Close()
	} else if !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return "", fmt.Errorf("open existing service: %w", err)
	}

	if err := copyFile(opts.AgentPath, agentDest); err != nil {
		return "", fmt.Errorf("copy agent executable: %w", err)
	}
	if err := copyFile(opts.BundlePath, bundleDest); err != nil {
		return "", fmt.Errorf("copy agent bundle: %w", err)
	}

	args := []string{"-service", "-bundle", bundleDest}
	config := serviceConfig(agentDest, args)
	s, err := m.CreateService(serviceName, agentDest, config, args...)
	if err != nil {
		if !errors.Is(err, windows.ERROR_SERVICE_EXISTS) {
			return "", fmt.Errorf("create service: %w", err)
		}
		s, err = m.OpenService(serviceName)
		if err != nil {
			return "", fmt.Errorf("open service: %w", err)
		}
		if err := s.UpdateConfig(config); err != nil {
			s.Close()
			return "", fmt.Errorf("update service: %w", err)
		}
		existed = true
	}
	defer s.Close()

	_ = eventlog.InstallAsEventCreate(serviceName, eventlog.Info|eventlog.Warning|eventlog.Error)
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 86400)
	_ = s.SetRecoveryActionsOnNonCrashFailures(true)

	if err := startMgrService(s); err != nil {
		return "", err
	}
	if existed {
		return fmt.Sprintf("Updated and started %s in %s.", serviceDisplayName, installDir), nil
	}
	return fmt.Sprintf("Installed and started %s in %s.", serviceDisplayName, installDir), nil
}

func startService() (string, error) {
	m, err := mgr.Connect()
	if err != nil {
		return "", err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return "", err
	}
	defer s.Close()
	if err := startMgrService(s); err != nil {
		return "", err
	}
	return fmt.Sprintf("Started %s.", serviceDisplayName), nil
}

func stopService() (string, error) {
	m, err := mgr.Connect()
	if err != nil {
		return "", err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return "", err
	}
	defer s.Close()
	if err := stopMgrService(s); err != nil {
		return "", err
	}
	return fmt.Sprintf("Stopped %s.", serviceDisplayName), nil
}

func uninstallService() (string, error) {
	m, err := mgr.Connect()
	if err != nil {
		return "", err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return fmt.Sprintf("%s is not installed.", serviceDisplayName), nil
		}
		return "", err
	}
	defer s.Close()
	_ = stopMgrService(s)
	if err := s.Delete(); err != nil {
		return "", err
	}
	_ = eventlog.Remove(serviceName)
	return fmt.Sprintf("Uninstalled %s. Installed files were left in place.", serviceDisplayName), nil
}

func validateInstallInputs(opts actionOptions) error {
	if opts.InstallDir == "" {
		return errors.New("install directory is required")
	}
	if opts.AgentPath == "" {
		return errors.New("agent executable is required")
	}
	if opts.BundlePath == "" {
		return errors.New("agent bundle is required")
	}
	if _, err := os.Stat(opts.AgentPath); err != nil {
		return fmt.Errorf("agent executable is not readable: %w", err)
	}
	data, err := os.ReadFile(opts.BundlePath)
	if err != nil {
		return fmt.Errorf("agent bundle is not readable: %w", err)
	}
	bundle, err := relaycore.DecodeBundle(string(data))
	if err != nil {
		return err
	}
	if bundle.Role != "agent" {
		return fmt.Errorf("bundle role is %q, want agent", bundle.Role)
	}
	return nil
}

func serviceConfig(exePath string, args []string) mgr.Config {
	return mgr.Config{
		ServiceType:    windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:      mgr.StartAutomatic,
		ErrorControl:   mgr.ErrorNormal,
		BinaryPathName: serviceBinaryPath(exePath, args),
		DisplayName:    serviceDisplayName,
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

func startMgrService(s *mgr.Service) error {
	status, err := s.Query()
	if err == nil && status.State == svc.Running {
		return nil
	}
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("start service: %w", err)
	}
	return waitForState(s, svc.Running, 20*time.Second)
}

func stopMgrService(s *mgr.Service) error {
	status, err := s.Query()
	if err == nil && status.State == svc.Stopped {
		return nil
	}
	if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return fmt.Errorf("stop service: %w", err)
	}
	return waitForState(s, svc.Stopped, 20*time.Second)
}

func waitForState(s *mgr.Service, want svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		status, err := s.Query()
		if err != nil {
			return err
		}
		if status.State == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for service state %s, current state is %s", serviceStateText(uint32(want)), serviceStateText(uint32(status.State)))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func queryServiceInfo() (serviceInfo, error) {
	var info serviceInfo
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return info, fmt.Errorf("service manager: %w", err)
	}
	defer windows.CloseServiceHandle(scm)

	name, err := windows.UTF16PtrFromString(serviceName)
	if err != nil {
		return info, err
	}
	handle, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return info, nil
		}
		return info, fmt.Errorf("service: %w", err)
	}
	defer windows.CloseServiceHandle(handle)

	var status windows.SERVICE_STATUS_PROCESS
	var needed uint32
	err = windows.QueryServiceStatusEx(
		handle,
		windows.SC_STATUS_PROCESS_INFO,
		(*byte)(unsafe.Pointer(&status)),
		uint32(unsafe.Sizeof(status)),
		&needed,
	)
	if err != nil {
		return info, err
	}
	info.Installed = true
	info.State = status.CurrentState
	info.ProcessID = status.ProcessId
	return info, nil
}

func relaunchElevatedAction(action string, opts actionOptions) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"-elevated-action", action}
	if opts.InstallDir != "" {
		args = append(args, "-install-dir", opts.InstallDir)
	}
	if opts.AgentPath != "" {
		args = append(args, "-agent", opts.AgentPath)
	}
	if opts.BundlePath != "" {
		args = append(args, "-bundle", opts.BundlePath)
	}
	return shellExecute("runas", exePath, joinWindowsArgs(args), "")
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

func isElevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

func defaultInstallDir() string {
	if _, err := os.Stat(`D:\`); err == nil {
		return filepath.Join(`D:\`, "TunnelDesktop", "Agent")
	}
	if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
		return filepath.Join(programFiles, "TunnelDesktop", "Agent")
	}
	if systemDrive := os.Getenv("SystemDrive"); systemDrive != "" {
		return filepath.Join(systemDrive+`\`, "TunnelDesktop", "Agent")
	}
	return filepath.Join(`C:\`, "TunnelDesktop", "Agent")
}

func defaultAgentPath() string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exePath)
	for _, name := range []string{"agent.exe", "agent-windows-amd64.exe"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return filepath.Join(dir, "agent-windows-amd64.exe")
}

func defaultBundlePath(installDir string) string {
	exePath, err := os.Executable()
	if err == nil {
		path := filepath.Join(filepath.Dir(exePath), installedBundle)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if _, err := os.Stat(installedBundle); err == nil {
		abs, _ := filepath.Abs(installedBundle)
		return abs
	}
	return filepath.Join(installDir, installedBundle)
}

func installedPaths(installDir string) (string, string) {
	return filepath.Join(installDir, installedAgentName), filepath.Join(installDir, installedBundle)
}

func copyFile(src, dst string) error {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if strings.EqualFold(srcAbs, dstAbs) {
		return nil
	}
	in, err := os.Open(srcAbs)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dstAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func serviceStateText(state uint32) string {
	switch state {
	case windows.SERVICE_STOPPED:
		return "stopped"
	case windows.SERVICE_START_PENDING:
		return "start pending"
	case windows.SERVICE_STOP_PENDING:
		return "stop pending"
	case windows.SERVICE_RUNNING:
		return "running"
	case windows.SERVICE_CONTINUE_PENDING:
		return "continue pending"
	case windows.SERVICE_PAUSE_PENDING:
		return "pause pending"
	case windows.SERVICE_PAUSED:
		return "paused"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}

func joinWindowsArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, syscall.EscapeArg(arg))
	}
	return strings.Join(quoted, " ")
}

func windowsMessageBox(title, text string, style uint32) {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	textPtr, _ := windows.UTF16PtrFromString(text)
	_, _ = windows.MessageBox(0, textPtr, titlePtr, style)
}
