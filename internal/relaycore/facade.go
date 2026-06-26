package relaycore

import (
	"encoding/json"
	"fmt"
	"sync"
)

type LogCallback interface {
	Log(line string)
}

var defaultRelay = struct {
	sync.Mutex
	cfg      Config
	relay    *Relay
	callback LogCallback
	logs     []string
}{}

func Configure(configJSON string) error {
	defaultRelay.Lock()
	defer defaultRelay.Unlock()
	if defaultRelay.relay != nil && defaultRelay.relay.Status() == "running" {
		return fmt.Errorf("relay is running")
	}
	var cfg Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return err
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	defaultRelay.cfg = cfg
	return nil
}

func Start() error {
	defaultRelay.Lock()
	if defaultRelay.relay != nil && defaultRelay.relay.Status() == "running" {
		defaultRelay.Unlock()
		return nil
	}
	cfg := defaultRelay.cfg
	defaultRelay.Unlock()

	relay, err := New(cfg, logLine)
	if err != nil {
		return err
	}
	if err := relay.Start(); err != nil {
		return err
	}

	defaultRelay.Lock()
	defaultRelay.relay = relay
	defaultRelay.Unlock()
	return nil
}

func Stop() error {
	defaultRelay.Lock()
	relay := defaultRelay.relay
	defaultRelay.relay = nil
	defaultRelay.Unlock()
	if relay == nil {
		return nil
	}
	return relay.Stop()
}

func Status() string {
	defaultRelay.Lock()
	relay := defaultRelay.relay
	defaultRelay.Unlock()
	if relay == nil {
		return "stopped"
	}
	return relay.Status()
}

func SetLogCallback(callback LogCallback) {
	defaultRelay.Lock()
	defaultRelay.callback = callback
	defaultRelay.Unlock()
}

func RecentLogs() string {
	defaultRelay.Lock()
	defer defaultRelay.Unlock()
	data, _ := json.Marshal(defaultRelay.logs)
	return string(data)
}

func GenerateSetup(configJSON string) (string, error) {
	return GenerateSetupJSON(configJSON)
}

func logLine(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	defaultRelay.Lock()
	defaultRelay.logs = append(defaultRelay.logs, line)
	if len(defaultRelay.logs) > 300 {
		defaultRelay.logs = defaultRelay.logs[len(defaultRelay.logs)-300:]
	}
	callback := defaultRelay.callback
	defaultRelay.Unlock()
	if callback != nil {
		callback.Log(line)
	}
}
