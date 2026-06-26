package relaycore

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"tunneldesktop/internal/tunnel"
)

func TestRelayBridgesClientToAgent(t *testing.T) {
	certs := writeTestCerts(t)
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echoListener.Close()
	go echoLoop(echoListener)

	relay, err := New(Config{
		ListenAddr:        "127.0.0.1:0",
		CAFile:            certs.ca,
		CertFile:          certs.relayCert,
		KeyFile:           certs.relayKey,
		Token:             "secret",
		AgentWaitTimeout:  "5s",
		MaxHomeStreams:    4,
		MaxTLSConnections: 8,
	}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.Start(); err != nil {
		t.Fatal(err)
	}
	defer relay.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentErr := make(chan error, 1)
	go func() {
		agentErr <- runTestAgent(ctx, relay.tlsListener.Addr().String(), certs, echoListener.Addr().String())
	}()

	client, err := dialTestClient(ctx, relay.tlsListener.Addr().String(), certs)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	payload := []byte("relay integration payload")
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo = %q, want %q", got, payload)
	}

	cancel()
	select {
	case err := <-agentErr:
		if err != nil && ctx.Err() == nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop")
	}
}

func runTestAgent(ctx context.Context, relayAddr string, certs testCerts, targetAddr string) error {
	tlsConfig, err := tunnel.ClientTLSConfig(certs.ca, certs.agentCert, certs.agentKey, "localhost")
	if err != nil {
		return err
	}
	rawConn, err := tunnel.DialContext(ctx, relayAddr, "direct")
	if err != nil {
		return err
	}
	tlsConn := tls.Client(rawConn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return err
	}
	if err := tunnel.SendAuth(ctx, tlsConn, "secret", tunnel.RoleAgent); err != nil {
		_ = tlsConn.Close()
		return err
	}
	session, err := yamux.Server(tlsConn, tunnel.YamuxConfig())
	if err != nil {
		_ = tlsConn.Close()
		return err
	}
	defer session.Close()
	go func() {
		<-ctx.Done()
		_ = session.Close()
	}()
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			return err
		}
		go func() {
			target, err := net.Dial("tcp", targetAddr)
			if err != nil {
				_ = stream.Close()
				return
			}
			tunnel.Pipe(stream, target)
		}()
	}
}

func dialTestClient(ctx context.Context, relayAddr string, certs testCerts) (net.Conn, error) {
	tlsConfig, err := tunnel.ClientTLSConfig(certs.ca, certs.clientCert, certs.clientKey, "localhost")
	if err != nil {
		return nil, err
	}
	rawConn, err := tunnel.DialContext(ctx, relayAddr, "direct")
	if err != nil {
		return nil, err
	}
	tlsConn := tls.Client(rawConn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	if err := tunnel.SendAuth(ctx, tlsConn, "secret", tunnel.RoleClient); err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func echoLoop(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}()
	}
}

type testCerts struct {
	ca         string
	relayCert  string
	relayKey   string
	agentCert  string
	agentKey   string
	clientCert string
	clientKey  string
}

func writeTestCerts(t *testing.T) testCerts {
	t.Helper()
	dir := t.TempDir()
	caKey, caDER, caCert := makeTestCA(t)
	relayKey, relayDER := makeTestLeaf(t, "relay", caCert, caKey, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, []string{"localhost", "127.0.0.1"})
	agentKey, agentDER := makeTestLeaf(t, "agent", caCert, caKey, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil)
	clientKey, clientDER := makeTestLeaf(t, "client", caCert, caKey, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil)
	certs := testCerts{
		ca:         filepath.Join(dir, "ca.crt"),
		relayCert:  filepath.Join(dir, "relay.crt"),
		relayKey:   filepath.Join(dir, "relay.key"),
		agentCert:  filepath.Join(dir, "agent.crt"),
		agentKey:   filepath.Join(dir, "agent.key"),
		clientCert: filepath.Join(dir, "client.crt"),
		clientKey:  filepath.Join(dir, "client.key"),
	}
	writePEM(t, certs.ca, "CERTIFICATE", caDER, 0o644)
	writePEM(t, certs.relayCert, "CERTIFICATE", relayDER, 0o644)
	writeKey(t, certs.relayKey, relayKey)
	writePEM(t, certs.agentCert, "CERTIFICATE", agentDER, 0o644)
	writeKey(t, certs.agentKey, agentKey)
	writePEM(t, certs.clientCert, "CERTIFICATE", clientDER, 0o644)
	writeKey(t, certs.clientKey, clientKey)
	return certs
}

func makeTestCA(t *testing.T) (*ecdsa.PrivateKey, []byte, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          testSerial(t),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return key, der, cert
}

func makeTestLeaf(t *testing.T, cn string, ca *x509.Certificate, caKey *ecdsa.PrivateKey, eku []x509.ExtKeyUsage, hosts []string) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: testSerial(t),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  eku,
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, host)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return key, der
}

func testSerial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func writePEM(t *testing.T, path, blockType string, der []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), mode); err != nil {
		t.Fatal(err)
	}
}

func writeKey(t *testing.T, path string, key *ecdsa.PrivateKey) {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	writePEM(t, path, "EC PRIVATE KEY", der, 0o600)
}
