package server

import (
	"crypto/tls"
	"net/http"
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
		return nil, err
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
