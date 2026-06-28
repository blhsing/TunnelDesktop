package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestRoomIDMatchesOtherRelays(t *testing.T) {
	tests := map[string]string{
		"":                      "default",
		" WorkDesk ":            "workdesk",
		"/Team Room!!/":         "team-room",
		"...":                   "default",
		strings.Repeat("A", 80): strings.Repeat("a", 64),
	}
	for input, want := range tests {
		if got := roomID(input); got != want {
			t.Fatalf("roomID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHealthAndEmptyStatus(t *testing.T) {
	server := httptest.NewServer(newServer())
	defer server.Close()

	resp, err := http.Get(server.URL + "/relay/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
	var health map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health["service"] != serviceName {
		t.Fatalf("service = %v", health["service"])
	}

	resp, err = http.Get(server.URL + "/relay/status?room=unit-empty")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var status StatusSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Service != serviceName {
		t.Fatalf("service = %q", status.Service)
	}
	if len(status.Rooms) != 0 {
		t.Fatalf("rooms = %d, want 0", len(status.Rooms))
	}
}

func TestHomeAgentStatusPresence(t *testing.T) {
	server := httptest.NewServer(newServer())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	home := dialRole(t, ctx, server.URL, "/relay/unit-home/ws", "home-agent")
	status := getStatus(t, server.URL, "unit-home")
	if len(status.Rooms) != 1 || !status.Rooms[0].HomeAgentConnected {
		t.Fatalf("home presence not reflected: %+v", status.Rooms)
	}
	_ = home.Close(websocket.StatusNormalClosure, "")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status = getStatus(t, server.URL, "unit-home")
		if len(status.Rooms) == 1 && !status.Rooms[0].HomeAgentConnected {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("home presence did not disconnect: %+v", status.Rooms)
}

func TestAgentClientPairAndBridgeBytes(t *testing.T) {
	server := httptest.NewServer(newServer())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agent := dialRole(t, ctx, server.URL, "/relay/unit-bridge/ws", "agent")
	home := dialRole(t, ctx, server.URL, "/relay/unit-bridge/ws", "client")
	defer agent.Close(websocket.StatusNormalClosure, "")
	defer home.Close(websocket.StatusNormalClosure, "")

	expectText(t, ctx, agent, startMessage)
	expectText(t, ctx, home, startMessage)

	if err := home.Write(ctx, websocket.MessageBinary, []byte("from-home")); err != nil {
		t.Fatal(err)
	}
	expectBinary(t, ctx, agent, "from-home")

	if err := agent.Write(ctx, websocket.MessageBinary, []byte("from-agent")); err != nil {
		t.Fatal(err)
	}
	expectBinary(t, ctx, home, "from-agent")

	status := getStatus(t, server.URL, "unit-bridge")
	if len(status.Rooms) != 1 || status.Rooms[0].ActivePairs != 1 || status.Rooms[0].TotalPairs != 1 {
		t.Fatalf("unexpected bridge status: %+v", status.Rooms)
	}
}

func TestDashboardWebSocketReceivesSnapshot(t *testing.T) {
	server := httptest.NewServer(newServer())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dashboard := dialRole(t, ctx, server.URL, "/relay/unit-dashboard/ws?role=dashboard", "")
	defer dashboard.Close(websocket.StatusNormalClosure, "")

	typ, payload, err := dashboard.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("dashboard message type = %v", typ)
	}
	var status StatusSnapshot
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatal(err)
	}
	if status.Service != serviceName {
		t.Fatalf("service = %q", status.Service)
	}
}

func dialRole(t *testing.T, ctx context.Context, baseURL, path, role string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(baseURL, "http") + path
	headers := http.Header{}
	if role != "" {
		headers.Set("X-DeskFerry-Role", role)
	}
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader:      headers,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func getStatus(t *testing.T, baseURL, room string) StatusSnapshot {
	t.Helper()
	resp, err := http.Get(baseURL + "/relay/status?room=" + room)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var status StatusSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func expectText(t *testing.T, ctx context.Context, c *websocket.Conn, want string) {
	t.Helper()
	typ, payload, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText || string(payload) != want {
		t.Fatalf("message = (%v, %q), want text %q", typ, payload, want)
	}
}

func expectBinary(t *testing.T, ctx context.Context, c *websocket.Conn, want string) {
	t.Helper()
	typ, payload, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageBinary || string(payload) != want {
		t.Fatalf("message = (%v, %q), want binary %q", typ, payload, want)
	}
}
