package tunnel

import "testing"

func TestWebSocketEndpointUsesRelayPath(t *testing.T) {
	endpoint, err := WebSocketEndpoint("https://test-officialwebsite.azurewebsites.net/relay/")
	if err != nil {
		t.Fatalf("WebSocketEndpoint: %v", err)
	}
	want := "wss://test-officialwebsite.azurewebsites.net/relay/ws"
	if endpoint != want {
		t.Fatalf("endpoint = %q, want %q", endpoint, want)
	}
}

func TestWebSocketEndpointPreservesNamedRoomPath(t *testing.T) {
	endpoint, err := WebSocketEndpoint("https://test-officialwebsite.azurewebsites.net/relay/workdesk")
	if err != nil {
		t.Fatalf("WebSocketEndpoint: %v", err)
	}
	want := "wss://test-officialwebsite.azurewebsites.net/relay/workdesk/ws"
	if endpoint != want {
		t.Fatalf("endpoint = %q, want %q", endpoint, want)
	}
}

func TestRelayRoomTokenUsesPathRoom(t *testing.T) {
	token := RelayRoomToken("https://test-officialwebsite.azurewebsites.net/relay/workdesk", "")
	if token != "workdesk" {
		t.Fatalf("token = %q, want workdesk", token)
	}
}

func TestWebSocketEndpointDefaultsToRelayPath(t *testing.T) {
	endpoint, err := WebSocketEndpoint("https://test-officialwebsite.azurewebsites.net/")
	if err != nil {
		t.Fatalf("WebSocketEndpoint: %v", err)
	}
	want := "wss://test-officialwebsite.azurewebsites.net/relay/ws"
	if endpoint != want {
		t.Fatalf("endpoint = %q, want %q", endpoint, want)
	}
}
