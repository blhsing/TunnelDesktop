package tunnel

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"nhooyr.io/websocket"
)

const (
	RoleProbe     = "probe"
	RoleHomeAgent = "home-agent"

	webSocketStartMessage = "start"
)

func IsWebSocketRelay(relayAddr string) bool {
	scheme := relayScheme(relayAddr)
	return scheme == "http" || scheme == "https" || scheme == "ws" || scheme == "wss"
}

func WebSocketEndpoint(relayAddr string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(relayAddr))
	if err != nil {
		return "", fmt.Errorf("parse relay URL: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("relay URL must include a host")
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "wss", "ws":
	default:
		return "", fmt.Errorf("unsupported relay URL scheme %q", u.Scheme)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/relay/ws"
	} else if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/ws") && !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/dashboard") {
		u.Path = strings.TrimRight(u.Path, "/") + "/ws"
	}
	return u.String(), nil
}

func HostFromRelayAddress(relayAddr string) string {
	if IsWebSocketRelay(relayAddr) {
		u, err := url.Parse(strings.TrimSpace(relayAddr))
		if err == nil {
			return u.Hostname()
		}
	}
	host, _, err := net.SplitHostPort(relayAddr)
	if err != nil {
		return relayAddr
	}
	return host
}

func RelayRoomToken(relayAddr, configuredToken string) string {
	if token := strings.TrimSpace(configuredToken); token != "" {
		return token
	}
	u, err := url.Parse(strings.TrimSpace(relayAddr))
	if err == nil {
		if room := RoomFromRelayPath(u.Path); room != "" {
			return room
		}
		if token := strings.TrimSpace(u.Query().Get("room")); token != "" {
			return token
		}
		if token := strings.TrimSpace(u.Query().Get("token")); token != "" {
			return token
		}
	}
	return "default"
}

func RoomFromRelayPath(path string) string {
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] != "relay" {
		return ""
	}
	if parts[1] == "ws" || parts[1] == "status" || parts[1] == "health" || parts[1] == "dashboard" {
		return ""
	}
	return parts[1]
}

func DialWebSocketStream(ctx context.Context, relayAddr, proxySpec, role, token string) (net.Conn, error) {
	c, err := DialWebSocket(ctx, relayAddr, proxySpec, role, token)
	if err != nil {
		return nil, err
	}
	if role == RoleClient {
		if err := AwaitWebSocketStart(ctx, c); err != nil {
			CloseWebSocket(c)
			return nil, err
		}
	}
	return WebSocketNetConn(ctx, c), nil
}

func WebSocketNetConn(ctx context.Context, c *websocket.Conn) net.Conn {
	return websocket.NetConn(ctx, c, websocket.MessageBinary)
}

func DialWebSocket(ctx context.Context, relayAddr, proxySpec, role, token string) (*websocket.Conn, error) {
	if err := validateWebSocketRole(role); err != nil {
		return nil, err
	}
	token = RelayRoomToken(relayAddr, token)
	endpoint, err := WebSocketEndpoint(relayAddr)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	header.Set("X-DeskFerry-Role", role)
	header.Set("X-TunnelDesktop-Role", role)
	header.Set("User-Agent", "DeskFerry/0.2")
	c, resp, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPClient:      webSocketHTTPClient(relayAddr, proxySpec),
		HTTPHeader:      header,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("websocket dial failed: HTTP %s", resp.Status)
		}
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}
	return c, nil
}

func AwaitWebSocketStart(ctx context.Context, c *websocket.Conn) error {
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for {
		typ, data, err := c.Read(readCtx)
		if err != nil {
			return fmt.Errorf("wait for relay pairing: %w", err)
		}
		if typ == websocket.MessageText && strings.TrimSpace(string(data)) == webSocketStartMessage {
			return nil
		}
	}
}

func CloseWebSocket(c *websocket.Conn) {
	if c == nil {
		return
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
}

func webSocketHTTPClient(relayAddr, proxySpec string) *http.Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: HostFromRelayAddress(relayAddr),
		},
		Proxy: proxyFunc(relayAddr, proxySpec),
	}
	return &http.Client{Transport: transport}
}

func proxyFunc(relayAddr, proxySpec string) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		spec := strings.TrimSpace(proxySpec)
		if spec == "" || strings.EqualFold(spec, "direct") {
			return nil, nil
		}
		if strings.EqualFold(spec, "env") || strings.EqualFold(spec, "auto") {
			return http.ProxyFromEnvironment(req)
		}
		target := req.URL.Host
		if target == "" {
			target = HostFromRelayAddress(relayAddr)
		}
		return resolveProxyURL(target, spec)
	}
}

func validateWebSocketRole(role string) error {
	switch role {
	case RoleAgent, RoleClient, RoleProbe, RoleHomeAgent:
		return nil
	default:
		return fmt.Errorf("invalid websocket role %q", role)
	}
}

func relayScheme(relayAddr string) string {
	u, err := url.Parse(strings.TrimSpace(relayAddr))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Scheme)
}
