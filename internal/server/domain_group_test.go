package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDomainGrouper_GroupDomains_Empty(t *testing.T) {
	grouper := NewDomainGrouper()
	groups := grouper.GroupDomains(nil)
	assert.Nil(t, groups)

	groups = grouper.GroupDomains([]string{})
	assert.Nil(t, groups)
}

func TestDomainGrouper_GroupDomains_SingleDomain(t *testing.T) {
	grouper := NewDomainGrouper()
	groups := grouper.GroupDomains([]string{"app.example.com"})

	require.Len(t, groups, 1)
	assert.Equal(t, "example.com", groups[0].RootDomain)
	assert.Equal(t, []string{"app.example.com"}, groups[0].Domains)
	assert.False(t, groups[0].IncludesApex)
}

func TestDomainGrouper_GroupDomains_ApexDomain(t *testing.T) {
	grouper := NewDomainGrouper()
	groups := grouper.GroupDomains([]string{"example.com"})

	require.Len(t, groups, 1)
	assert.Equal(t, "example.com", groups[0].RootDomain)
	assert.Equal(t, []string{"example.com"}, groups[0].Domains)
	assert.True(t, groups[0].IncludesApex)
}

func TestDomainGrouper_GroupDomains_MultipleSubdomains(t *testing.T) {
	grouper := NewDomainGrouper()
	groups := grouper.GroupDomains([]string{
		"app.example.com",
		"api.example.com",
		"www.example.com",
	})

	require.Len(t, groups, 1)
	assert.Equal(t, "example.com", groups[0].RootDomain)
	assert.Len(t, groups[0].Domains, 3)
	assert.Contains(t, groups[0].Domains, "app.example.com")
	assert.Contains(t, groups[0].Domains, "api.example.com")
	assert.Contains(t, groups[0].Domains, "www.example.com")
	assert.False(t, groups[0].IncludesApex)
}

func TestDomainGrouper_GroupDomains_ApexWithSubdomains(t *testing.T) {
	grouper := NewDomainGrouper()
	groups := grouper.GroupDomains([]string{
		"example.com",
		"www.example.com",
		"api.example.com",
	})

	require.Len(t, groups, 1)
	assert.Equal(t, "example.com", groups[0].RootDomain)
	assert.Len(t, groups[0].Domains, 3)
	assert.True(t, groups[0].IncludesApex)
}

func TestDomainGrouper_GroupDomains_MultipleDomains(t *testing.T) {
	grouper := NewDomainGrouper()
	groups := grouper.GroupDomains([]string{
		"app.example.com",
		"api.example.com",
		"app.other.com",
		"www.other.com",
	})

	require.Len(t, groups, 2)

	// Groups are sorted by root domain
	assert.Equal(t, "example.com", groups[0].RootDomain)
	assert.Len(t, groups[0].Domains, 2)

	assert.Equal(t, "other.com", groups[1].RootDomain)
	assert.Len(t, groups[1].Domains, 2)
}

func TestDomainGrouper_GroupDomains_MultiLevelSubdomain(t *testing.T) {
	grouper := NewDomainGrouper()
	groups := grouper.GroupDomains([]string{
		"deep.nested.example.com",
		"app.example.com",
	})

	require.Len(t, groups, 1)
	assert.Equal(t, "example.com", groups[0].RootDomain)
	assert.Len(t, groups[0].Domains, 2)
}

func TestDomainGrouper_GroupDomains_PublicSuffix(t *testing.T) {
	grouper := NewDomainGrouper()
	groups := grouper.GroupDomains([]string{
		"app.example.co.uk",
		"www.example.co.uk",
	})

	require.Len(t, groups, 1)
	assert.Equal(t, "example.co.uk", groups[0].RootDomain)
	assert.Len(t, groups[0].Domains, 2)
}

func TestDomainGrouper_GroupDomains_Deduplication(t *testing.T) {
	grouper := NewDomainGrouper()
	groups := grouper.GroupDomains([]string{
		"app.example.com",
		"APP.EXAMPLE.COM", // Duplicate with different case
		"app.example.com", // Exact duplicate
	})

	require.Len(t, groups, 1)
	assert.Len(t, groups[0].Domains, 1)
	assert.Equal(t, "app.example.com", groups[0].Domains[0])
}

func TestDomainGrouper_ShouldBatch(t *testing.T) {
	grouper := NewDomainGrouper()

	// Single domain - no batch
	group1 := &DomainGroup{Domains: []string{"app.example.com"}}
	assert.False(t, grouper.ShouldBatch(group1))

	// Two domains - should batch
	group2 := &DomainGroup{Domains: []string{"app.example.com", "api.example.com"}}
	assert.True(t, grouper.ShouldBatch(group2))
}

func TestDomainGroup_GetDomainsForCert(t *testing.T) {
	group := &DomainGroup{
		RootDomain: "example.com",
		Domains:    []string{"app.example.com", "api.example.com"},
	}

	domains := group.GetDomainsForCert()
	assert.Equal(t, []string{"app.example.com", "api.example.com"}, domains)
}

func TestDomainGroup_CertificateIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		domains  []string
		expected string
	}{
		{
			name:     "single domain",
			domains:  []string{"app.example.com"},
			expected: "single:app.example.com",
		},
		{
			name:     "multiple domains",
			domains:  []string{"app.example.com", "api.example.com"},
			expected: "san:example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group := &DomainGroup{
				RootDomain: "example.com",
				Domains:    tt.domains,
			}
			assert.Equal(t, tt.expected, group.CertificateIdentifier())
		})
	}
}

func TestDomainGroup_MatchesDomain(t *testing.T) {
	group := &DomainGroup{
		RootDomain: "example.com",
		Domains:    []string{"app.example.com", "api.example.com"},
	}

	assert.True(t, group.MatchesDomain("app.example.com"))
	assert.True(t, group.MatchesDomain("API.EXAMPLE.COM"))
	assert.False(t, group.MatchesDomain("www.example.com"))
	assert.False(t, group.MatchesDomain("app.other.com"))
}

func TestGetRootDomain(t *testing.T) {
	tests := []struct {
		domain   string
		expected string
		hasErr   bool
	}{
		{"app.example.com", "example.com", false},
		{"api.example.com", "example.com", false},
		{"deep.nested.example.com", "example.com", false},
		{"example.com", "example.com", false},
		{"app.example.co.uk", "example.co.uk", false},
		{"www.example.co.uk", "example.co.uk", false},
		{"APP.EXAMPLE.COM", "example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.domain, func(t *testing.T) {
			result, err := GetRootDomain(tt.domain)
			if tt.hasErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
