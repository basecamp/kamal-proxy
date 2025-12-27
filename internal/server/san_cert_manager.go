package server

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

const (
	// LetsEncryptProduction is the production ACME directory
	LetsEncryptProduction = "https://acme-v02.api.letsencrypt.org/directory"
	// LetsEncryptStaging is the staging ACME directory for testing
	LetsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"

	// MaxSANsPerCertificate is the maximum number of SANs allowed per certificate
	// Let's Encrypt limit is 100
	MaxSANsPerCertificate = 100
)

var (
	ErrNoDomains          = errors.New("no domains to provision")
	ErrCertNotFound       = errors.New("certificate not found")
	ErrManagerNotReady    = errors.New("certificate manager not initialized")
	ErrProvisioningFailed = errors.New("certificate provisioning failed")
)

// SANCertManagerConfig holds configuration for the SAN certificate manager
type SANCertManagerConfig struct {
	// Email is the ACME account email (required)
	Email string

	// Directory is the ACME directory URL (defaults to Let's Encrypt production)
	Directory string

	// CachePath is where certificates are stored
	CachePath string

	// StatePath is where manager state is persisted
	StatePath string

	// BatchDelay is how long to wait for more domains before provisioning
	// This allows batching multiple service deployments together
	BatchDelay time.Duration
}

// SANCertManager manages SAN certificates with domain batching
// It groups domains by their root domain and provisions a single certificate
// for all subdomains, reducing the number of certificates needed
type SANCertManager struct {
	mu     sync.RWMutex
	config SANCertManagerConfig

	// Domain grouper
	grouper *DomainGrouper

	// ACME client
	client *lego.Client
	user   *acmeUser

	// Certificate storage: certID -> certificate
	certificates map[string]*ManagedCert

	// Domain to certificate mapping
	domainToCert map[string]string

	// Pending domains waiting to be batched: domain -> service name
	pendingDomains map[string]string

	// Currently provisioning: rootDomain -> done channel
	provisioning map[string]chan struct{}

	// HTTP-01 challenge tokens: token -> key authorization
	challengeTokens map[string]string

	// State
	ready bool
}

// ManagedCert represents a certificate managed by the manager
type ManagedCert struct {
	Identifier  string          `json:"identifier"`
	Domains     []string        `json:"domains"`
	NotAfter    time.Time       `json:"not_after"`
	Certificate *tls.Certificate `json:"-"` // Not persisted, loaded from files
}

// acmeUser implements registration.User for lego
type acmeUser struct {
	Email        string                 `json:"email"`
	Registration *registration.Resource `json:"registration"`
	Key          *ecdsa.PrivateKey      `json:"-"`
	KeyPEM       []byte                 `json:"key_pem"`
}

func (u *acmeUser) GetEmail() string                        { return u.Email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.Registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.Key }

// NewSANCertManager creates a new SAN certificate manager
func NewSANCertManager(config SANCertManagerConfig) (*SANCertManager, error) {
	if config.Email == "" {
		return nil, errors.New("email is required for ACME registration")
	}

	if config.Directory == "" {
		config.Directory = LetsEncryptProduction
	}

	if config.BatchDelay == 0 {
		config.BatchDelay = 5 * time.Second
	}

	manager := &SANCertManager{
		config:          config,
		grouper:         NewDomainGrouper(),
		certificates:    make(map[string]*ManagedCert),
		domainToCert:    make(map[string]string),
		pendingDomains:  make(map[string]string),
		provisioning:    make(map[string]chan struct{}),
		challengeTokens: make(map[string]string),
	}

	// Ensure cache directory exists
	if config.CachePath != "" {
		if err := os.MkdirAll(config.CachePath, 0700); err != nil {
			return nil, fmt.Errorf("failed to create cache directory: %w", err)
		}
	}

	return manager, nil
}

// Initialize sets up the ACME client and loads persisted state
func (m *SANCertManager) Initialize(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Load or create ACME user
	user, err := m.loadOrCreateUser()
	if err != nil {
		return fmt.Errorf("failed to setup ACME user: %w", err)
	}
	m.user = user

	// Create lego config
	legoConfig := lego.NewConfig(user)
	legoConfig.CADirURL = m.config.Directory
	legoConfig.Certificate.KeyType = certcrypto.EC256

	// Create ACME client
	client, err := lego.NewClient(legoConfig)
	if err != nil {
		return fmt.Errorf("failed to create ACME client: %w", err)
	}

	// Setup HTTP-01 challenge provider using our custom provider
	httpProvider := &memoryHTTP01Provider{manager: m}
	if err := client.Challenge.SetHTTP01Provider(httpProvider); err != nil {
		return fmt.Errorf("failed to set HTTP-01 provider: %w", err)
	}

	m.client = client

	// Register with ACME if not already registered
	if user.Registration == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{
			TermsOfServiceAgreed: true,
		})
		if err != nil {
			return fmt.Errorf("failed to register with ACME: %w", err)
		}
		user.Registration = reg

		// Save user with registration
		if err := m.saveUser(); err != nil {
			slog.Warn("Failed to save ACME user", "error", err)
		}
	}

	// Load persisted state
	if err := m.loadState(); err != nil {
		slog.Warn("Failed to load certificate state", "error", err)
	}

	m.ready = true
	slog.Info("SAN certificate manager initialized",
		"email", m.config.Email,
		"directory", m.config.Directory,
	)

	return nil
}

// memoryHTTP01Provider is a custom HTTP-01 challenge provider that stores tokens in memory
type memoryHTTP01Provider struct {
	manager *SANCertManager
}

func (p *memoryHTTP01Provider) Present(domain, token, keyAuth string) error {
	p.manager.mu.Lock()
	p.manager.challengeTokens[token] = keyAuth
	p.manager.mu.Unlock()
	slog.Debug("HTTP-01 challenge presented", "domain", domain, "token", token)
	return nil
}

func (p *memoryHTTP01Provider) CleanUp(domain, token, keyAuth string) error {
	p.manager.mu.Lock()
	delete(p.manager.challengeTokens, token)
	p.manager.mu.Unlock()
	slog.Debug("HTTP-01 challenge cleaned up", "domain", domain, "token", token)
	return nil
}

// RegisterDomain registers a domain for certificate management
func (m *SANCertManager) RegisterDomain(domain string, service string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.ready {
		return ErrManagerNotReady
	}

	// Check if domain already has a certificate
	if certID, ok := m.domainToCert[domain]; ok {
		cert := m.certificates[certID]
		if cert != nil && time.Until(cert.NotAfter) > 24*time.Hour {
			slog.Debug("Domain already has valid certificate",
				"domain", domain,
				"certificate", certID,
			)
			return nil
		}
	}

	// Check if domain is covered by an existing SAN certificate
	for _, cert := range m.certificates {
		for _, d := range cert.Domains {
			if d == domain {
				m.domainToCert[domain] = cert.Identifier
				slog.Debug("Domain covered by existing SAN certificate",
					"domain", domain,
					"certificate", cert.Identifier,
				)
				return nil
			}
		}
	}

	// Add to pending domains for batched provisioning
	m.pendingDomains[domain] = service
	slog.Debug("Domain added to pending batch",
		"domain", domain,
		"service", service,
		"pending_count", len(m.pendingDomains),
	)

	return nil
}

// UnregisterDomain removes a domain from management
func (m *SANCertManager) UnregisterDomain(domain string, service string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.pendingDomains, domain)
	return nil
}

// GetCertificate returns a certificate for the TLS handshake
func (m *SANCertManager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	domain := hello.ServerName
	if domain == "" {
		return nil, errors.New("no server name provided")
	}

	m.mu.RLock()
	ready := m.ready
	certID, hasCert := m.domainToCert[domain]
	var cert *ManagedCert
	if hasCert {
		cert = m.certificates[certID]
	}
	m.mu.RUnlock()

	if !ready {
		return nil, ErrManagerNotReady
	}

	// Return existing valid certificate
	if cert != nil && cert.Certificate != nil {
		if time.Until(cert.NotAfter) > 24*time.Hour {
			return cert.Certificate, nil
		}
		slog.Info("Certificate expiring soon, will reprovision",
			"domain", domain,
			"expiresAt", cert.NotAfter,
		)
	}

	// Need to provision certificate
	return m.provisionCertificate(hello.Context(), domain)
}

// provisionCertificate provisions a certificate for a domain
// It batches together ALL pending domains (up to MaxSANsPerCertificate) into a single cert
// This minimizes the number of certificates and avoids rate limits
func (m *SANCertManager) provisionCertificate(ctx context.Context, domain string) (*tls.Certificate, error) {
	// Use a single provisioning lock - we batch everything together
	const provisioningKey = "_batch_"

	m.mu.Lock()
	if done, ok := m.provisioning[provisioningKey]; ok {
		m.mu.Unlock()
		// Wait for existing provisioning to complete
		select {
		case <-done:
			return m.getCertForDomain(domain)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Collect ALL pending domains (up to MaxSANsPerCertificate)
	domainsToProvision := []string{domain}
	for pendingDomain := range m.pendingDomains {
		if pendingDomain != domain {
			domainsToProvision = append(domainsToProvision, pendingDomain)
		}
		if len(domainsToProvision) >= MaxSANsPerCertificate {
			break
		}
	}

	// Start provisioning
	done := make(chan struct{})
	m.provisioning[provisioningKey] = done

	// Remove domains from pending
	for _, d := range domainsToProvision {
		delete(m.pendingDomains, d)
	}
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.provisioning, provisioningKey)
		close(done)
		m.mu.Unlock()
	}()

	// Sort domains for consistent certificate identifiers
	sortedDomains := make([]string, len(domainsToProvision))
	copy(sortedDomains, domainsToProvision)
	sortStrings(sortedDomains)

	slog.Info("Provisioning SAN certificate",
		"domains", sortedDomains,
		"batch_size", len(sortedDomains),
	)

	// Request certificate for all domains
	request := certificate.ObtainRequest{
		Domains: sortedDomains,
		Bundle:  true,
	}

	resource, err := m.client.Certificate.Obtain(request)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain certificate: %w", err)
	}

	// Parse the certificate
	tlsCert, err := tls.X509KeyPair(resource.Certificate, resource.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Determine expiry from leaf certificate
	var notAfter time.Time
	if tlsCert.Leaf != nil {
		notAfter = tlsCert.Leaf.NotAfter
	} else {
		notAfter = time.Now().Add(90 * 24 * time.Hour) // Approximate
	}

	// Generate certificate ID from first domain (sorted)
	certID := fmt.Sprintf("san:%d:%s", len(sortedDomains), sortedDomains[0])
	managed := &ManagedCert{
		Identifier:  certID,
		Domains:     sortedDomains,
		NotAfter:    notAfter,
		Certificate: &tlsCert,
	}

	m.mu.Lock()
	m.certificates[certID] = managed
	for _, d := range sortedDomains {
		m.domainToCert[d] = certID
	}
	m.mu.Unlock()

	// Save certificate to disk
	if err := m.saveCertificate(certID, resource); err != nil {
		slog.Warn("Failed to save certificate", "error", err)
	}

	// Persist state
	if err := m.saveState(); err != nil {
		slog.Warn("Failed to save state", "error", err)
	}

	slog.Info("Certificate provisioned successfully",
		"identifier", certID,
		"domains", sortedDomains,
		"batch_size", len(sortedDomains),
		"expires", notAfter,
	)

	return &tlsCert, nil
}

// sortStrings sorts a slice of strings in place
func sortStrings(s []string) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// getCertForDomain retrieves a certificate for a domain
func (m *SANCertManager) getCertForDomain(domain string) (*tls.Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	certID, ok := m.domainToCert[domain]
	if !ok {
		return nil, ErrCertNotFound
	}

	cert := m.certificates[certID]
	if cert == nil || cert.Certificate == nil {
		return nil, ErrCertNotFound
	}

	return cert.Certificate, nil
}

// HTTPChallengeHandler returns the HTTP handler for ACME challenges
func (m *SANCertManager) HTTPChallengeHandler(fallback http.Handler) http.Handler {
	const challengePrefix = "/.well-known/acme-challenge/"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if this is an ACME challenge request
		if len(r.URL.Path) > len(challengePrefix) && r.URL.Path[:len(challengePrefix)] == challengePrefix {
			token := r.URL.Path[len(challengePrefix):]

			m.mu.RLock()
			keyAuth, ok := m.challengeTokens[token]
			m.mu.RUnlock()

			if ok {
				slog.Debug("Serving HTTP-01 challenge", "token", token)
				w.Header().Set("Content-Type", "text/plain")
				w.Write([]byte(keyAuth))
				return
			}

			slog.Debug("HTTP-01 challenge token not found", "token", token)
		}

		fallback.ServeHTTP(w, r)
	})
}

// GetStats returns statistics about the manager
func (m *SANCertManager) GetStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	expiringCount := 0
	for _, cert := range m.certificates {
		if time.Until(cert.NotAfter) < 30*24*time.Hour {
			expiringCount++
		}
	}

	return map[string]interface{}{
		"ready":               m.ready,
		"total_certificates":  len(m.certificates),
		"domains_mapped":      len(m.domainToCert),
		"pending_domains":     len(m.pendingDomains),
		"expiring_soon":       expiringCount,
		"provisioning_active": len(m.provisioning),
	}
}

// Persistence methods

func (m *SANCertManager) loadOrCreateUser() (*acmeUser, error) {
	userPath := filepath.Join(m.config.CachePath, "acme_user.json")

	data, err := os.ReadFile(userPath)
	if err == nil {
		var user acmeUser
		if err := json.Unmarshal(data, &user); err == nil {
			// Decode the private key
			key, err := certcrypto.ParsePEMPrivateKey(user.KeyPEM)
			if err == nil {
				if ecKey, ok := key.(*ecdsa.PrivateKey); ok {
					user.Key = ecKey
					return &user, nil
				}
			}
		}
	}

	// Create new user
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	user := &acmeUser{
		Email: m.config.Email,
		Key:   privateKey,
	}

	return user, nil
}

func (m *SANCertManager) saveUser() error {
	if m.config.CachePath == "" {
		return nil
	}

	keyPEM := certcrypto.PEMEncode(m.user.Key)
	m.user.KeyPEM = keyPEM

	data, err := json.MarshalIndent(m.user, "", "  ")
	if err != nil {
		return err
	}

	userPath := filepath.Join(m.config.CachePath, "acme_user.json")
	return os.WriteFile(userPath, data, 0600)
}

func (m *SANCertManager) saveCertificate(certID string, resource *certificate.Resource) error {
	if m.config.CachePath == "" {
		return nil
	}

	certDir := filepath.Join(m.config.CachePath, "certs", sanitizeFilename(certID))
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return err
	}

	// Save certificate
	if err := os.WriteFile(filepath.Join(certDir, "cert.pem"), resource.Certificate, 0600); err != nil {
		return err
	}

	// Save private key
	if err := os.WriteFile(filepath.Join(certDir, "key.pem"), resource.PrivateKey, 0600); err != nil {
		return err
	}

	return nil
}

type managerState struct {
	Certificates map[string]*ManagedCert `json:"certificates"`
	DomainMap    map[string]string       `json:"domain_map"`
	SavedAt      time.Time               `json:"saved_at"`
}

func (m *SANCertManager) loadState() error {
	if m.config.StatePath == "" {
		return nil
	}

	data, err := os.ReadFile(m.config.StatePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var state managerState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	// Load certificates from disk
	for id, cert := range state.Certificates {
		if m.config.CachePath != "" {
			certDir := filepath.Join(m.config.CachePath, "certs", sanitizeFilename(id))
			certPath := filepath.Join(certDir, "cert.pem")
			keyPath := filepath.Join(certDir, "key.pem")

			tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err == nil {
				cert.Certificate = &tlsCert
			}
		}
		m.certificates[id] = cert
	}

	m.domainToCert = state.DomainMap

	slog.Info("Loaded certificate manager state",
		"certificates", len(m.certificates),
		"domains", len(m.domainToCert),
	)

	return nil
}

func (m *SANCertManager) saveState() error {
	if m.config.StatePath == "" {
		return nil
	}

	m.mu.RLock()
	state := managerState{
		Certificates: m.certificates,
		DomainMap:    m.domainToCert,
		SavedAt:      time.Now(),
	}
	m.mu.RUnlock()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := m.config.StatePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}

	return os.Rename(tmpPath, m.config.StatePath)
}

func sanitizeFilename(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			result = append(result, c)
		} else {
			result = append(result, '_')
		}
	}
	return string(result)
}
