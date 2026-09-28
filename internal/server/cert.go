package server

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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

func loadCACertPool(tlsClientCACertificateFilePath string) (*x509.CertPool, error) {
	pemData, err := os.ReadFile(tlsClientCACertificateFilePath)
	if err != nil {
		slog.Error("Error loading client CA certificate", "path", tlsClientCACertificateFilePath, "error", err)
		return nil, ErrorUnableToLoadClientCACertificate
	}

	certs, err := parseCACertificates(pemData)
	if err != nil {
		slog.Error("Error parsing client CA certificate", "path", tlsClientCACertificateFilePath, "error", err)
		return nil, ErrorUnableToLoadClientCACertificate
	}

	pool := x509.NewCertPool()
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool, nil
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
