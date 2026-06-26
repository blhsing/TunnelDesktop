package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tunneldesktop/internal/relaycore"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var outDir string
	var relayHosts string
	var relayAddr string
	var rawRDPAddr string
	var rawAllow string
	var agentProxy string
	var rdpAddr string
	var clientListen string
	var validDays int
	flag.StringVar(&outDir, "out", "dist/certs", "output bundle directory")
	flag.StringVar(&relayHosts, "relay-host", "localhost,127.0.0.1,::1", "comma-separated relay DNS names/IPs for the server cert")
	flag.StringVar(&relayAddr, "relay-addr", "localhost:443", "relay address for generated agent/client configs")
	flag.StringVar(&rawRDPAddr, "raw-rdp-addr", "", "raw RDP listener address for relay config")
	flag.StringVar(&rawAllow, "raw-allow", "", "comma-separated raw RDP source IP/CIDR allowlist")
	flag.StringVar(&agentProxy, "agent-proxy", "env", "agent proxy config: env/auto, direct, or http://host:port")
	flag.StringVar(&rdpAddr, "rdp-addr", "127.0.0.1:3389", "work PC RDP target for agent config")
	flag.StringVar(&clientListen, "client-listen", "127.0.0.1:3389", "local listener for client config")
	flag.IntVar(&validDays, "valid-days", 825, "certificate validity in days")
	flag.Parse()

	result, err := relaycore.GenerateIdentity(relaycore.SetupOptions{
		RelayAddr:    relayAddr,
		RelayHosts:   splitCSV(relayHosts),
		RawRDPAddr:   rawRDPAddr,
		RawAllowlist: splitCSV(rawAllow),
		AgentProxy:   agentProxy,
		RDPAddr:      rdpAddr,
		ClientListen: clientListen,
		ValidDays:    validDays,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(outDir, "relay"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(outDir, "agent"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(outDir, "client"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "relay", "config.json"), []byte(result.RelayConfigJSON+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "agent", "agent.tnl"), []byte(result.AgentBundle+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "client", "client.tnl"), []byte(result.ClientBundle+"\n"), 0o600); err != nil {
		return err
	}
	summary, err := json.MarshalIndent(map[string]string{
		"server_name": result.ServerName,
		"relay_addr":  relayAddr,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "summary.json"), append(summary, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote relay config and .tnl bundles to %s\n", outDir)
	return nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
