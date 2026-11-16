package server

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"

	"golang.org/x/crypto/acme/autocert"
)

var (
	ErrorUnableToLoadCertificate = errors.New("unable to load certificate")

	errorCertManagerRetired = errors.New("certificate manager has been retired")
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

// retireCertManager cuts an automatic TLS certificate manager off from the CA
// once it has been replaced or its service removed. autocert starts a renewal
// timer for each certificate a manager loads or issues, with no way to stop
// it, so a discarded manager would otherwise keep renewing certificates in the
// background for the life of the process. Its timers now fail without
// contacting the CA.
//
// A handshake that is issuing a certificate through the manager at that moment
// fails too. New handshakes use the service's current manager, which issues
// the certificate again if needed.
//
// Only new requests are refused: one already in flight when the manager is
// retired can still complete and store its certificate in the shared cache.
// That is at most one renewal finishing, with a valid certificate for the same
// host, rather than renewals continuing in the background.
func retireCertManager(certManager CertManager) {
	manager, ok := certManager.(*autocert.Manager)
	if !ok || manager.Client == nil || manager.Client.HTTPClient == nil {
		return
	}

	if transport, ok := manager.Client.HTTPClient.Transport.(*renewalTransport); ok {
		transport.close()
	}
}

// renewalTransport passes requests to the CA until it is closed, and fails
// them afterwards. It uses http.DefaultTransport, as acme.Client does by
// default.
type renewalTransport struct {
	closed atomic.Bool
}

func (t *renewalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.closed.Load() {
		return nil, errorCertManagerRetired
	}
	return http.DefaultTransport.RoundTrip(req)
}

func (t *renewalTransport) close() {
	t.closed.Store(true)
}
