package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSANCertManager(t *testing.T) {
	tmpDir := t.TempDir()

	config := SANCertManagerConfig{
		Email:     "test@example.com",
		Directory: LetsEncryptStaging,
		CachePath: filepath.Join(tmpDir, "certs"),
		StatePath: filepath.Join(tmpDir, "state.json"),
	}

	manager, err := NewSANCertManager(config)
	require.NoError(t, err)
	assert.NotNil(t, manager)
	assert.NotNil(t, manager.grouper)
}

func TestNewSANCertManager_RequiresEmail(t *testing.T) {
	config := SANCertManagerConfig{}

	_, err := NewSANCertManager(config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "email is required")
}

func TestNewSANCertManager_DefaultDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	config := SANCertManagerConfig{
		Email:     "test@example.com",
		CachePath: filepath.Join(tmpDir, "certs"),
	}

	manager, err := NewSANCertManager(config)
	require.NoError(t, err)
	assert.Equal(t, LetsEncryptProduction, manager.config.Directory)
}

func TestSANCertManager_RegisterDomain_NotReady(t *testing.T) {
	tmpDir := t.TempDir()

	config := SANCertManagerConfig{
		Email:     "test@example.com",
		CachePath: filepath.Join(tmpDir, "certs"),
	}

	manager, err := NewSANCertManager(config)
	require.NoError(t, err)

	// Don't initialize - should fail
	err = manager.RegisterDomain("app.example.com", "service1")
	require.ErrorIs(t, err, ErrManagerNotReady)
}

func TestSANCertManager_RegisterDomain_AddsToPending(t *testing.T) {
	tmpDir := t.TempDir()

	config := SANCertManagerConfig{
		Email:     "test@example.com",
		CachePath: filepath.Join(tmpDir, "certs"),
	}

	manager, err := NewSANCertManager(config)
	require.NoError(t, err)

	// Manually mark as ready for testing
	manager.ready = true

	err = manager.RegisterDomain("app.example.com", "service1")
	require.NoError(t, err)

	assert.Contains(t, manager.pendingDomains, "app.example.com")
	assert.Equal(t, "service1", manager.pendingDomains["app.example.com"])
}

func TestSANCertManager_RegisterMultipleDomains(t *testing.T) {
	tmpDir := t.TempDir()

	config := SANCertManagerConfig{
		Email:     "test@example.com",
		CachePath: filepath.Join(tmpDir, "certs"),
	}

	manager, err := NewSANCertManager(config)
	require.NoError(t, err)
	manager.ready = true

	// Register multiple domains
	require.NoError(t, manager.RegisterDomain("app.example.com", "service1"))
	require.NoError(t, manager.RegisterDomain("api.example.com", "service2"))
	require.NoError(t, manager.RegisterDomain("www.example.com", "service3"))

	assert.Len(t, manager.pendingDomains, 3)
}

func TestSANCertManager_RegisterDifferentRootDomains(t *testing.T) {
	tmpDir := t.TempDir()

	config := SANCertManagerConfig{
		Email:     "test@example.com",
		CachePath: filepath.Join(tmpDir, "certs"),
	}

	manager, err := NewSANCertManager(config)
	require.NoError(t, err)
	manager.ready = true

	// Register domains from completely different root domains
	// All should be batched together (up to 100)
	require.NoError(t, manager.RegisterDomain("app.example.com", "service1"))
	require.NoError(t, manager.RegisterDomain("api.other.org", "service2"))
	require.NoError(t, manager.RegisterDomain("www.mysite.net", "service3"))
	require.NoError(t, manager.RegisterDomain("admin.different.io", "service4"))

	// All 4 should be pending for a single SAN certificate
	assert.Len(t, manager.pendingDomains, 4)
	assert.Contains(t, manager.pendingDomains, "app.example.com")
	assert.Contains(t, manager.pendingDomains, "api.other.org")
	assert.Contains(t, manager.pendingDomains, "www.mysite.net")
	assert.Contains(t, manager.pendingDomains, "admin.different.io")
}

func TestMaxSANsPerCertificate(t *testing.T) {
	// Verify the constant is set correctly
	assert.Equal(t, 100, MaxSANsPerCertificate)
}

func TestSortStrings(t *testing.T) {
	tests := []struct {
		input    []string
		expected []string
	}{
		{[]string{"c", "a", "b"}, []string{"a", "b", "c"}},
		{[]string{"z.com", "a.com", "m.com"}, []string{"a.com", "m.com", "z.com"}},
		{[]string{"single"}, []string{"single"}},
		{[]string{}, []string{}},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			input := make([]string, len(tt.input))
			copy(input, tt.input)
			sortStrings(input)
			assert.Equal(t, tt.expected, input)
		})
	}
}

func TestSANCertManager_RegisterDomain_ExistingCert(t *testing.T) {
	tmpDir := t.TempDir()

	config := SANCertManagerConfig{
		Email:     "test@example.com",
		CachePath: filepath.Join(tmpDir, "certs"),
	}

	manager, err := NewSANCertManager(config)
	require.NoError(t, err)
	manager.ready = true

	// Add an existing certificate
	manager.certificates["san:example.com"] = &ManagedCert{
		Identifier: "san:example.com",
		Domains:    []string{"app.example.com", "api.example.com"},
	}
	manager.domainToCert["app.example.com"] = "san:example.com"
	manager.domainToCert["api.example.com"] = "san:example.com"

	// Register a domain that's already covered
	err = manager.RegisterDomain("app.example.com", "service1")
	require.NoError(t, err)

	// Should not be in pending since it's already covered
	assert.NotContains(t, manager.pendingDomains, "app.example.com")
}

func TestSANCertManager_HTTPChallengeHandler(t *testing.T) {
	tmpDir := t.TempDir()

	config := SANCertManagerConfig{
		Email:     "test@example.com",
		CachePath: filepath.Join(tmpDir, "certs"),
	}

	manager, err := NewSANCertManager(config)
	require.NoError(t, err)

	// Add a challenge token
	manager.challengeTokens["test-token"] = "test-key-auth"

	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("fallback"))
	})

	handler := manager.HTTPChallengeHandler(fallback)

	// Test challenge request
	req := httptest.NewRequest("GET", "/.well-known/acme-challenge/test-token", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "test-key-auth", rec.Body.String())

	// Test unknown token falls through
	req = httptest.NewRequest("GET", "/.well-known/acme-challenge/unknown-token", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "fallback", rec.Body.String())

	// Test non-challenge request falls through
	req = httptest.NewRequest("GET", "/some/other/path", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "fallback", rec.Body.String())
}

func TestSANCertManager_GetStats(t *testing.T) {
	tmpDir := t.TempDir()

	config := SANCertManagerConfig{
		Email:     "test@example.com",
		CachePath: filepath.Join(tmpDir, "certs"),
	}

	manager, err := NewSANCertManager(config)
	require.NoError(t, err)

	stats := manager.GetStats()

	assert.Equal(t, false, stats["ready"])
	assert.Equal(t, 0, stats["total_certificates"])
	assert.Equal(t, 0, stats["domains_mapped"])
	assert.Equal(t, 0, stats["pending_domains"])
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"san:example.com", "san_example.com"},
		{"simple", "simple"},
		{"with-dash", "with-dash"},
		{"with_underscore", "with_underscore"},
		{"with.dot", "with.dot"},
		{"with/slash", "with_slash"},
		{"with:colon", "with_colon"},
		{"MixedCase123", "MixedCase123"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := sanitizeFilename(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
