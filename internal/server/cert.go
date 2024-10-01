package server

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
)

var ErrorUnableToLoadCertificate = errors.New("unable to load certificate")

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
	// TODO: check s.cert.Leaf.PermittedDNSDomains
	return m.cert, nil
}

func (m *StaticCertManager) HTTPHandler(handler http.Handler) http.Handler {
	return handler
}
