package tunnel

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func DialContext(ctx context.Context, targetAddr, proxySpec string) (net.Conn, error) {
	if strings.TrimSpace(proxySpec) == "" || strings.EqualFold(proxySpec, "direct") {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", targetAddr)
	}
	proxyURL, err := resolveProxyURL(targetAddr, proxySpec)
	if err != nil {
		return nil, err
	}
	if proxyURL == nil {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", targetAddr)
	}
	return dialHTTPConnect(ctx, targetAddr, proxyURL)
}

func ProxySpecForLog(proxySpec string) string {
	spec := strings.TrimSpace(proxySpec)
	if spec == "" || strings.EqualFold(spec, "direct") {
		return "direct"
	}
	if strings.EqualFold(spec, "env") || strings.EqualFold(spec, "auto") {
		return spec
	}
	if !strings.Contains(spec, "://") {
		spec = "http://" + spec
	}
	proxyURL, err := url.Parse(spec)
	if err != nil || proxyURL.Host == "" {
		return proxySpec
	}
	return proxyURLForLog(proxyURL)
}

func resolveProxyURL(targetAddr, proxySpec string) (*url.URL, error) {
	spec := strings.TrimSpace(proxySpec)
	if strings.EqualFold(spec, "env") || strings.EqualFold(spec, "auto") {
		reqURL := &url.URL{Scheme: "https", Host: targetAddr}
		req := &http.Request{URL: reqURL}
		return http.ProxyFromEnvironment(req)
	}
	if !strings.Contains(spec, "://") {
		spec = "http://" + spec
	}
	proxyURL, err := url.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("parse proxy URL: %w", err)
	}
	if proxyURL.Scheme != "http" {
		return nil, fmt.Errorf("unsupported proxy scheme %q; only http CONNECT is supported", proxyURL.Scheme)
	}
	if proxyURL.Host == "" {
		return nil, errors.New("proxy host is required")
	}
	return proxyURL, nil
}

func dialHTTPConnect(ctx context.Context, targetAddr string, proxyURL *url.URL) (net.Conn, error) {
	targetAddr = CanonicalHostPort(targetAddr)
	proxyLabel := proxyURLForLog(proxyURL)
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, fmt.Errorf("dial proxy %s: %w", proxyURL.Host, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = conn.Close()
		}
	}()

	restoreDeadline := setConnDeadline(ctx, conn, 30_000_000_000)
	defer restoreDeadline()

	var b strings.Builder
	fmt.Fprintf(&b, "CONNECT %s HTTP/1.1\r\n", targetAddr)
	fmt.Fprintf(&b, "Host: %s\r\n", targetAddr)
	fmt.Fprintf(&b, "User-Agent: TunnelDesktop/0.1\r\n")
	fmt.Fprintf(&b, "Proxy-Connection: Keep-Alive\r\n")
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		encoded := base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username() + ":" + password))
		fmt.Fprintf(&b, "Proxy-Authorization: Basic %s\r\n", encoded)
	}
	fmt.Fprintf(&b, "\r\n")
	if _, err := conn.Write([]byte(b.String())); err != nil {
		return nil, fmt.Errorf("write CONNECT request: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("proxy CONNECT %s via %s failed: %s", targetAddr, proxyLabel, resp.Status)
	}
	ok = true
	return conn, nil
}

func proxyURLForLog(proxyURL *url.URL) string {
	if proxyURL == nil {
		return "direct"
	}
	if proxyURL.Scheme == "" {
		return proxyURL.Host
	}
	return proxyURL.Scheme + "://" + proxyURL.Host
}

func CanonicalHostPort(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return net.JoinHostPort(host, port)
}

func SplitHostPort(addr, defaultPort string) (string, string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		return host, port, nil
	}
	if strings.Contains(err.Error(), "missing port in address") && defaultPort != "" {
		return addr, defaultPort, nil
	}
	return "", "", err
}
