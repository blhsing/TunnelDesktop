package relaycore

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

const bundlePrefix = "tnl1."

type SetupOptions struct {
	RelayAddr    string   `json:"relay_addr"`
	RelayHosts   []string `json:"relay_hosts"`
	RawRDPAddr   string   `json:"raw_rdp_addr"`
	RawAllowlist []string `json:"raw_allowlist"`
	AgentProxy   string   `json:"agent_proxy"`
	RDPAddr      string   `json:"rdp_addr"`
	ClientListen string   `json:"client_listen"`
	ValidDays    int      `json:"valid_days"`
}

type SetupResult struct {
	RelayConfigJSON string `json:"relay_config_json"`
	AgentBundle     string `json:"agent_bundle"`
	ClientBundle    string `json:"client_bundle"`
	Token           string `json:"token"`
	ServerName      string `json:"server_name"`
}

type Bundle struct {
	Version     int       `json:"version"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
	RelayAddr   string    `json:"relay_addr"`
	Proxy       string    `json:"proxy,omitempty"`
	ServerName  string    `json:"server_name"`
	Token       string    `json:"token"`
	ListenAddr  string    `json:"listen_addr,omitempty"`
	RDPAddr     string    `json:"rdp_addr,omitempty"`
	MinBackoff  string    `json:"min_backoff,omitempty"`
	MaxBackoff  string    `json:"max_backoff,omitempty"`
	CAPEM       string    `json:"ca_pem"`
	CertPEM     string    `json:"cert_pem"`
	KeyPEM      string    `json:"key_pem"`
	Description string    `json:"description,omitempty"`
}

func GenerateSetupJSON(optionsJSON string) (string, error) {
	var opts SetupOptions
	if strings.TrimSpace(optionsJSON) != "" {
		if err := json.Unmarshal([]byte(optionsJSON), &opts); err != nil {
			return "", err
		}
	}
	result, err := GenerateIdentity(opts)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func GenerateIdentity(opts SetupOptions) (SetupResult, error) {
	opts.applyDefaults()
	if err := opts.validate(); err != nil {
		return SetupResult{}, err
	}
	serverName := opts.RelayHosts[0]
	token, err := randomToken()
	if err != nil {
		return SetupResult{}, err
	}

	notBefore := time.Now().Add(-5 * time.Minute)
	notAfter := notBefore.AddDate(0, 0, opts.ValidDays)
	caKey, caDER, caCert, err := makeCA(notBefore, notAfter)
	if err != nil {
		return SetupResult{}, err
	}
	relayKey, relayDER, err := makeLeaf("TunnelDesktop Relay", caCert, caKey, notBefore, notAfter, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, opts.RelayHosts)
	if err != nil {
		return SetupResult{}, err
	}
	agentKey, agentDER, err := makeLeaf("TunnelDesktop Agent", caCert, caKey, notBefore, notAfter, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil)
	if err != nil {
		return SetupResult{}, err
	}
	clientKey, clientDER, err := makeLeaf("TunnelDesktop Client", caCert, caKey, notBefore, notAfter, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil)
	if err != nil {
		return SetupResult{}, err
	}

	now := time.Now().UTC()
	caPEM := string(certPEM(caDER))
	relayCfg := Config{
		ListenAddr:          ":443",
		RawRDPAddr:          opts.RawRDPAddr,
		RawAllowlist:        opts.RawAllowlist,
		DisableRawAllowlist: false,
		CAPEM:               caPEM,
		CertPEM:             string(certPEM(relayDER)),
		KeyPEM:              string(ecPrivateKeyPEM(relayKey)),
		Token:               token,
		MaxHomeStreams:      32,
		MaxTLSConnections:   128,
		AgentWaitTimeout:    "30s",
		GeneratedRelayAddr:  opts.RelayAddr,
		GeneratedServerName: serverName,
		GeneratedAt:         now.Format(time.RFC3339),
	}
	relayCfg.ApplyDefaults()
	relayConfigJSON, err := json.MarshalIndent(relayCfg, "", "  ")
	if err != nil {
		return SetupResult{}, err
	}

	agentBundle, err := EncodeBundle(Bundle{
		Version:     1,
		Role:        "agent",
		CreatedAt:   now,
		RelayAddr:   opts.RelayAddr,
		Proxy:       opts.AgentProxy,
		ServerName:  serverName,
		Token:       token,
		RDPAddr:     opts.RDPAddr,
		MinBackoff:  "1s",
		MaxBackoff:  "60s",
		CAPEM:       caPEM,
		CertPEM:     string(certPEM(agentDER)),
		KeyPEM:      string(ecPrivateKeyPEM(agentKey)),
		Description: "Work PC agent bundle",
	})
	if err != nil {
		return SetupResult{}, err
	}
	clientBundle, err := EncodeBundle(Bundle{
		Version:     1,
		Role:        "client",
		CreatedAt:   now,
		RelayAddr:   opts.RelayAddr,
		ServerName:  serverName,
		Token:       token,
		ListenAddr:  opts.ClientListen,
		CAPEM:       caPEM,
		CertPEM:     string(certPEM(clientDER)),
		KeyPEM:      string(ecPrivateKeyPEM(clientKey)),
		Description: "Home PC client bundle",
	})
	if err != nil {
		return SetupResult{}, err
	}
	return SetupResult{
		RelayConfigJSON: string(relayConfigJSON),
		AgentBundle:     agentBundle,
		ClientBundle:    clientBundle,
		Token:           token,
		ServerName:      serverName,
	}, nil
}

func EncodeBundle(bundle Bundle) (string, error) {
	if bundle.Version == 0 {
		bundle.Version = 1
	}
	if bundle.CreatedAt.IsZero() {
		bundle.CreatedAt = time.Now().UTC()
	}
	if err := bundle.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}
	return bundlePrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

func DecodeBundle(encoded string) (Bundle, error) {
	encoded = strings.TrimSpace(encoded)
	if !strings.HasPrefix(encoded, bundlePrefix) {
		return Bundle{}, fmt.Errorf("not a TunnelDesktop .tnl bundle")
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, bundlePrefix))
	if err != nil {
		return Bundle{}, fmt.Errorf("decode bundle: %w", err)
	}
	var bundle Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return Bundle{}, fmt.Errorf("parse bundle: %w", err)
	}
	return bundle, bundle.Validate()
}

func (b Bundle) Validate() error {
	if b.Version != 1 {
		return fmt.Errorf("unsupported bundle version %d", b.Version)
	}
	if b.Role != "agent" && b.Role != "client" {
		return fmt.Errorf("unsupported bundle role %q", b.Role)
	}
	if b.RelayAddr == "" {
		return fmt.Errorf("relay_addr is required")
	}
	if b.ServerName == "" {
		return fmt.Errorf("server_name is required")
	}
	if b.Token == "" {
		return fmt.Errorf("token is required")
	}
	if b.CAPEM == "" || b.CertPEM == "" || b.KeyPEM == "" {
		return fmt.Errorf("bundle certificate material is incomplete")
	}
	return nil
}

func (o *SetupOptions) applyDefaults() {
	if o.RelayAddr == "" {
		o.RelayAddr = "tunnel.example.com:443"
	}
	if len(o.RelayHosts) == 0 {
		if host := hostFromAddr(o.RelayAddr); host != "" {
			o.RelayHosts = []string{host}
		}
	}
	if host := hostFromAddr(o.RelayAddr); host != "" && !contains(o.RelayHosts, host) {
		o.RelayHosts = append(o.RelayHosts, host)
	}
	if o.AgentProxy == "" {
		o.AgentProxy = "http://PROXY:PORT"
	}
	if o.RDPAddr == "" {
		o.RDPAddr = "127.0.0.1:3389"
	}
	if o.ClientListen == "" {
		o.ClientListen = "127.0.0.1:3389"
	}
	if o.ValidDays <= 0 {
		o.ValidDays = 825
	}
}

func (o SetupOptions) validate() error {
	if o.RelayAddr == "" {
		return fmt.Errorf("relay_addr is required")
	}
	if len(o.RelayHosts) == 0 {
		return fmt.Errorf("at least one relay host is required")
	}
	return nil
}

func makeCA(notBefore, notAfter time.Time) (*ecdsa.PrivateKey, []byte, *x509.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "TunnelDesktop Local CA"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, err
	}
	return key, der, cert, nil
}

func makeLeaf(cn string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, notBefore, notAfter time.Time, eku []x509.ExtKeyUsage, hosts []string) (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
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
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	return key, der, nil
}

func serial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		panic(err)
	}
	return n
}

func certPEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func ecPrivateKeyPEM(key *ecdsa.PrivateKey) []byte {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hostFromAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return host
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
