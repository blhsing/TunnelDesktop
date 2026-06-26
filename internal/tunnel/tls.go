package tunnel

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

func LoadCertPool(caFile string) (*x509.CertPool, error) {
	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	return CertPoolFromPEM(pemBytes)
}

func CertPoolFromPEM(pemBytes []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("CA PEM did not contain a certificate")
	}
	return pool, nil
}

func ServerTLSConfig(caFile, certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load relay certificate: %w", err)
	}
	caPool, err := LoadCertPool(caFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}, nil
}

func ServerTLSConfigFromPEM(caPEM, certPEM, keyPEM string) (*tls.Config, error) {
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, fmt.Errorf("load relay certificate: %w", err)
	}
	caPool, err := CertPoolFromPEM([]byte(caPEM))
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}, nil
}

func ClientTLSConfig(caFile, certFile, keyFile, serverName string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}
	caPool, err := LoadCertPool(caFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{cert},
		RootCAs:            caPool,
		ServerName:         serverName,
		ClientSessionCache: tls.NewLRUClientSessionCache(4),
	}, nil
}

func ClientTLSConfigFromPEM(caPEM, certPEM, keyPEM, serverName string) (*tls.Config, error) {
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}
	caPool, err := CertPoolFromPEM([]byte(caPEM))
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{cert},
		RootCAs:            caPool,
		ServerName:         serverName,
		ClientSessionCache: tls.NewLRUClientSessionCache(4),
	}, nil
}
