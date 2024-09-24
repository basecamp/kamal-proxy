package server

import (
	"crypto/tls"
	"log/slog"
	"sync"
)

type CertManager interface {
	GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
}

// StaticCertManager is a certificate manager that loads certificates from disk.
type StaticCertManager struct {
	tlsCertificateFilePath string
	tlsPrivateKeyFilePath  string
	cert                   *tls.Certificate
	lock                   sync.RWMutex
}

func NewStaticCertManager(tlsCertificateFilePath, tlsPrivateKeyFilePath string) *StaticCertManager {
	return &StaticCertManager{
		tlsCertificateFilePath: tlsCertificateFilePath,
		tlsPrivateKeyFilePath:  tlsPrivateKeyFilePath,
	}
}

func (m *StaticCertManager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.lock.RLock()
	if m.cert != nil {
		defer m.lock.RUnlock()
		return m.cert, nil
	}
	m.lock.RUnlock()

	m.lock.Lock()
	defer m.lock.Unlock()
	if m.cert != nil { // Double-check locking
		return m.cert, nil
	}

	slog.Info(
		"Loading custom TLS certificate",
		"tls-certificate-path", m.tlsCertificateFilePath,
		"tls-private-key-path", m.tlsPrivateKeyFilePath,
	)

	cert, err := tls.LoadX509KeyPair(m.tlsCertificateFilePath, m.tlsPrivateKeyFilePath)
	if err != nil {
		return nil, err
	}
	m.cert = &cert

	return m.cert, nil
}
