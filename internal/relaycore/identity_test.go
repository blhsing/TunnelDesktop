package relaycore

import (
	"encoding/json"
	"testing"
)

func TestGenerateIdentityProducesUsableBundles(t *testing.T) {
	result, err := GenerateIdentity(SetupOptions{
		RelayAddr:    "localhost:8443",
		RelayHosts:   []string{"localhost", "127.0.0.1"},
		AgentProxy:   "direct",
		ClientListen: "127.0.0.1:3390",
	})
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	var relayCfg Config
	if err := json.Unmarshal([]byte(result.RelayConfigJSON), &relayCfg); err != nil {
		t.Fatalf("relay config JSON: %v", err)
	}
	if err := relayCfg.Validate(); err != nil {
		t.Fatalf("relay config validate: %v", err)
	}
	if relayCfg.ListenAddr != ":8443" {
		t.Fatalf("relay listen addr = %q, want :8443", relayCfg.ListenAddr)
	}
	agent, err := DecodeBundle(result.AgentBundle)
	if err != nil {
		t.Fatalf("DecodeBundle(agent): %v", err)
	}
	if agent.Role != "agent" || agent.Proxy != "direct" {
		t.Fatalf("unexpected agent bundle: %#v", agent)
	}
	client, err := DecodeBundle(result.ClientBundle)
	if err != nil {
		t.Fatalf("DecodeBundle(client): %v", err)
	}
	if client.Role != "client" || client.ListenAddr != "127.0.0.1:3390" {
		t.Fatalf("unexpected client bundle: %#v", client)
	}
}

func TestGenerateIdentityDefaultsBlankProxyToDirect(t *testing.T) {
	result, err := GenerateIdentity(SetupOptions{
		RelayAddr:    "[2001:db8::42]:443",
		ClientListen: "127.0.0.1:3390",
	})
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	agent, err := DecodeBundle(result.AgentBundle)
	if err != nil {
		t.Fatalf("DecodeBundle(agent): %v", err)
	}
	if agent.Proxy != "direct" {
		t.Fatalf("agent proxy = %q, want direct", agent.Proxy)
	}
	if agent.RelayAddr != "[2001:db8::42]:443" {
		t.Fatalf("agent relay addr = %q", agent.RelayAddr)
	}
	if agent.ServerName != "2001:db8::42" {
		t.Fatalf("agent server name = %q", agent.ServerName)
	}
}

func TestGenerateIdentityRequiresRelayPort(t *testing.T) {
	_, err := GenerateIdentity(SetupOptions{
		RelayAddr:  "phone.example.com",
		RelayHosts: []string{"phone.example.com"},
	})
	if err == nil {
		t.Fatal("GenerateIdentity succeeded without relay port")
	}
}
