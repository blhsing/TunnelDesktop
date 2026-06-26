package relaycore

import core "tunneldesktop/internal/relaycore"

type LogCallback interface {
	Log(line string)
}

func Configure(configJSON string) error {
	return core.Configure(configJSON)
}

func Start() error {
	return core.Start()
}

func Stop() error {
	return core.Stop()
}

func Status() string {
	return core.Status()
}

func ConnectionStatus() string {
	return core.ConnectionStatus()
}

func SetLogCallback(callback LogCallback) {
	core.SetLogCallback(callback)
}

func RecentLogs() string {
	return core.RecentLogs()
}

func GenerateSetup(configJSON string) (string, error) {
	return core.GenerateSetup(configJSON)
}
