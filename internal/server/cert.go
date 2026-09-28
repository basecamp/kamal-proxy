package server

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"time"
)

var (
	ErrorUnableToLoadCertificate         = errors.New("unable to load certificate")
	ErrorUnableToLoadClientCACertificate = errors.New("unable to load client CA certificate")

	pemBlockBegin = []byte("-----BEGIN")
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

type certificateFingerprint [sha256.Size]byte

type ClientCA struct {
	certPool     *x509.CertPool
	fingerprints map[certificateFingerprint]bool
}

func NewClientCA(tlsClientCAFilePath string) (*ClientCA, error) {
	pemData, err := os.ReadFile(tlsClientCAFilePath)
	if err != nil {
		slog.Error("Error loading client CA certificate", "path", tlsClientCAFilePath, "error", err)
		return nil, ErrorUnableToLoadClientCACertificate
	}

	certs, err := parseCACertificates(pemData)
	if err != nil {
		slog.Error("Error parsing client CA certificate", "path", tlsClientCAFilePath, "error", err)
		return nil, ErrorUnableToLoadClientCACertificate
	}

	clientCA := &ClientCA{
		certPool:     x509.NewCertPool(),
		fingerprints: map[certificateFingerprint]bool{},
	}
	for _, cert := range certs {
		clientCA.certPool.AddCert(cert)
		clientCA.fingerprints[sha256.Sum256(cert.Raw)] = true
	}
	return clientCA, nil
}

func (ca *ClientCA) CertPool() *x509.CertPool {
	return ca.certPool
}

func (ca *ClientCA) TrustsConnection(state *tls.ConnectionState) bool {
	return slices.ContainsFunc(state.VerifiedChains, ca.trustsChain)
}

func (ca *ClientCA) trustsChain(chain []*x509.Certificate) bool {
	return len(chain) > 0 && ca.isAnchorOf(chain) && allCurrentlyValid(chain)
}

func (ca *ClientCA) isAnchorOf(chain []*x509.Certificate) bool {
	chainAnchor := chain[len(chain)-1]
	return ca.fingerprints[sha256.Sum256(chainAnchor.Raw)]
}

func allCurrentlyValid(certs []*x509.Certificate) bool {
	now := time.Now()

	return !slices.ContainsFunc(certs, func(cert *x509.Certificate) bool {
		return now.Before(cert.NotBefore) || now.After(cert.NotAfter)
	})
}

func parseCACertificates(pemData []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate

	for {
		block, rest, err := decodeNextPEMBlock(pemData)
		if err != nil {
			return nil, err
		}
		if block == nil {
			break
		}

		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("unexpected PEM block type %q", block.Type)
		}

		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}

		certs = append(certs, cert)
		pemData = rest
	}

	if len(certs) == 0 {
		return nil, errors.New("no certificates found")
	}

	return certs, nil
}

func decodeNextPEMBlock(pemData []byte) (*pem.Block, []byte, error) {
	nextBlockStart := bytes.Index(pemData, pemBlockBegin)
	if nextBlockStart < 0 {
		return nil, nil, nil
	}

	block, rest := pem.Decode(pemData[nextBlockStart:])
	decodedData := pemData[nextBlockStart : len(pemData)-len(rest)]
	skippedMalformedBlock := bytes.Count(decodedData, pemBlockBegin) > 1
	if block == nil || skippedMalformedBlock {
		return nil, nil, errors.New("malformed PEM block")
	}

	return block, rest, nil
}
