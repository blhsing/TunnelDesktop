package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"tunneldesktop/internal/relaycore"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	cfg, err := parseConfig()
	if err != nil {
		log.Fatal(err)
	}
	relay, err := relaycore.New(cfg, log.Printf)
	if err != nil {
		log.Fatal(err)
	}
	if err := relay.Start(); err != nil {
		log.Fatal(err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	if err := relay.Stop(); err != nil {
		log.Fatal(err)
	}
}

func parseConfig() (relaycore.Config, error) {
	var configFile string
	var listen string
	var rawListen string
	var allow string
	var disableAllowlist bool
	var ca string
	var cert string
	var key string
	var token string
	var maxStreams int
	var maxTLS int
	var agentWait string
	flag.StringVar(&configFile, "config", "", "JSON config file")
	flag.StringVar(&listen, "listen", "", "TLS listener address")
	flag.StringVar(&rawListen, "raw-listen", "", "LAN-only raw RDP listener address")
	flag.StringVar(&allow, "raw-allow", "", "comma-separated raw RDP source IP/CIDR allowlist")
	flag.BoolVar(&disableAllowlist, "disable-raw-allowlist", false, "disable raw RDP source allowlist")
	flag.StringVar(&ca, "ca", "", "CA certificate PEM")
	flag.StringVar(&cert, "cert", "", "relay server certificate PEM")
	flag.StringVar(&key, "key", "", "relay server private key PEM")
	flag.StringVar(&token, "token", "", "shared bearer token")
	flag.IntVar(&maxStreams, "max-streams", 0, "maximum concurrent home streams")
	flag.IntVar(&maxTLS, "max-tls", 0, "maximum concurrent TLS connections")
	flag.StringVar(&agentWait, "agent-wait", "", "home connection wait time for an agent")
	flag.Parse()

	cfg := relaycore.Config{}
	var err error
	if configFile != "" {
		cfg, err = relaycore.LoadConfigFile(configFile)
		if err != nil {
			return cfg, err
		}
	}
	overlay(&cfg.ListenAddr, listen)
	overlay(&cfg.RawRDPAddr, rawListen)
	overlay(&cfg.CAFile, ca)
	overlay(&cfg.CertFile, cert)
	overlay(&cfg.KeyFile, key)
	overlay(&cfg.Token, token)
	overlay(&cfg.AgentWaitTimeout, agentWait)
	if allow != "" {
		cfg.RawAllowlist = splitCSV(allow)
	}
	if disableAllowlist {
		cfg.DisableRawAllowlist = true
	}
	if maxStreams > 0 {
		cfg.MaxHomeStreams = maxStreams
	}
	if maxTLS > 0 {
		cfg.MaxTLSConnections = maxTLS
	}
	cfg.ApplyDefaults()
	return cfg, cfg.Validate()
}

func overlay(dst *string, src string) {
	if src != "" {
		*dst = src
	}
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
