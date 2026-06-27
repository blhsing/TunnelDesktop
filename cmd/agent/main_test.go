package main

import (
	"reflect"
	"testing"
)

func TestSplitRelayURLs(t *testing.T) {
	got := splitRelayURLs(" https://test-officialwebsite.azurewebsites.net/relay/workdesk;\nhttp://217.142.228.117/relay/workdesk, ws://localhost:8000/relay/dev ")
	want := []string{
		"https://test-officialwebsite.azurewebsites.net/relay/workdesk",
		"http://217.142.228.117/relay/workdesk",
		"ws://localhost:8000/relay/dev",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitRelayURLs() = %#v, want %#v", got, want)
	}
}

func TestRelayURLFlagAccumulatesValues(t *testing.T) {
	var flag relayURLFlag
	if err := flag.Set("https://one.example/relay/a;https://two.example/relay/a"); err != nil {
		t.Fatal(err)
	}
	if err := flag.Set("https://three.example/relay/a"); err != nil {
		t.Fatal(err)
	}
	want := "https://one.example/relay/a;https://two.example/relay/a;https://three.example/relay/a"
	if got := flag.String(); got != want {
		t.Fatalf("relayURLFlag.String() = %q, want %q", got, want)
	}
}

func TestLoadConfigAcceptsMultipleWebSocketRelayURLs(t *testing.T) {
	cfg, err := loadConfig("https://test-officialwebsite.azurewebsites.net/relay/workdesk;http://217.142.228.117/relay/workdesk", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://test-officialwebsite.azurewebsites.net/relay/workdesk",
		"http://217.142.228.117/relay/workdesk",
	}
	if got := cfg.relayAddresses(); !reflect.DeepEqual(got, want) {
		t.Fatalf("relayAddresses() = %#v, want %#v", got, want)
	}
	if cfg.RelayAddr != want[0] {
		t.Fatalf("RelayAddr = %q, want first relay %q", cfg.RelayAddr, want[0])
	}
}

func TestConfigFileRelayAddrsAreNormalized(t *testing.T) {
	cfg := config{
		RelayAddrs: []string{
			"https://test-officialwebsite.azurewebsites.net/relay/workdesk",
			"https://test-officialwebsite.azurewebsites.net/relay/workdesk",
			"http://217.142.228.117/relay/workdesk",
		},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://test-officialwebsite.azurewebsites.net/relay/workdesk",
		"http://217.142.228.117/relay/workdesk",
	}
	if got := cfg.relayAddresses(); !reflect.DeepEqual(got, want) {
		t.Fatalf("relayAddresses() = %#v, want %#v", got, want)
	}
}
