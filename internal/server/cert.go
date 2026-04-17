package server

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net/http"
	"os"
)

var (
	ErrorUnableToLoadCertificate         = errors.New("unable to load certificate")
	ErrorUnableToLoadClientCACertificate = errors.New("unable to load client CA certificate")
)

type CertManager interface {
	GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
	HTTPHandler(handler http.Handler) http.Handler
}

// StaticCertManager is a certificate manager that loads certificates from disk.
type StaticCertManager struct {
	cert *tls.Certificate
}

func NewStaticCertManager(tlsCertificateFilePath, tlsPrivateKeyFilePath string) (*StaticCertManager, error) {
	cert, err := tls.LoadX509KeyPair(tlsCertificateFilePath, tlsPrivateKeyFilePath)
	if err != nil {
		slog.Error("Error loading TLS certificate", "error", err)
		return nil, ErrorUnableToLoadCertificate
	}

	return &StaticCertManager{
		cert: &cert,
	}, nil
}

func (m *StaticCertManager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return m.cert, nil
}

func (m *StaticCertManager) HTTPHandler(handler http.Handler) http.Handler {
	return handler
}

func loadCACertPool(tlsClientCACertificateFilePath string) (*x509.CertPool, error) {
	pemData, err := os.ReadFile(tlsClientCACertificateFilePath)
	if err != nil {
		slog.Error("Error loading client CA certificate", "path", tlsClientCACertificateFilePath, "error", err)
		return nil, ErrorUnableToLoadClientCACertificate
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		slog.Error("Error parsing client CA certificate", "path", tlsClientCACertificateFilePath)
		return nil, ErrorUnableToLoadClientCACertificate
	}
	return pool, nil
}
