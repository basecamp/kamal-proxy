package server

import (
	"sort"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// DomainGroup represents a group of domains that share the same root domain
// and can be included in a single SAN certificate
type DomainGroup struct {
	// RootDomain is the registrable domain (e.g., "example.com")
	RootDomain string

	// Domains contains all full domain names in this group
	Domains []string

	// IncludesApex is true if the apex domain itself is in the group
	IncludesApex bool
}

// DomainGrouper groups domains by their root domain for efficient certificate management
type DomainGrouper struct {
	// MinDomainsForBatching is the minimum number of domains needed to batch into a SAN cert
	// Default is 2 (batch when we have 2+ domains for the same root)
	MinDomainsForBatching int
}

// NewDomainGrouper creates a new DomainGrouper with default settings
func NewDomainGrouper() *DomainGrouper {
	return &DomainGrouper{
		MinDomainsForBatching: 2,
	}
}

// GroupDomains analyzes a list of domains and groups them by root domain
func (g *DomainGrouper) GroupDomains(domains []string) []*DomainGroup {
	if len(domains) == 0 {
		return nil
	}

	// Group domains by their root domain
	groups := make(map[string]*DomainGroup)

	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}

		rootDomain, err := publicsuffix.EffectiveTLDPlusOne(domain)
		if err != nil {
			// If we can't determine the root, treat domain as its own root
			rootDomain = domain
		}

		group, exists := groups[rootDomain]
		if !exists {
			group = &DomainGroup{
				RootDomain: rootDomain,
				Domains:    []string{},
			}
			groups[rootDomain] = group
		}

		// Check if this is the apex domain
		if domain == rootDomain {
			group.IncludesApex = true
		}

		// Add domain if not already present
		if !contains(group.Domains, domain) {
			group.Domains = append(group.Domains, domain)
		}
	}

	// Convert map to sorted slice
	result := make([]*DomainGroup, 0, len(groups))
	for _, group := range groups {
		// Sort domains within each group for consistent ordering
		sort.Strings(group.Domains)
		result = append(result, group)
	}

	// Sort groups by root domain for consistent ordering
	sort.Slice(result, func(i, j int) bool {
		return result[i].RootDomain < result[j].RootDomain
	})

	return result
}

// ShouldBatch returns true if the group has enough domains to warrant batching
func (g *DomainGrouper) ShouldBatch(group *DomainGroup) bool {
	return len(group.Domains) >= g.MinDomainsForBatching
}

// GetDomainsForCert returns the domains that should be included in the certificate
// For SAN certificates, this is simply all domains in the group
func (group *DomainGroup) GetDomainsForCert() []string {
	return group.Domains
}

// CertificateIdentifier returns a unique identifier for this group's certificate
func (group *DomainGroup) CertificateIdentifier() string {
	if len(group.Domains) == 1 {
		return "single:" + group.Domains[0]
	}
	return "san:" + group.RootDomain
}

// MatchesDomain checks if a domain belongs to this group
func (group *DomainGroup) MatchesDomain(domain string) bool {
	domain = strings.ToLower(domain)
	for _, d := range group.Domains {
		if d == domain {
			return true
		}
	}
	return false
}

// GetRootDomain extracts the registrable domain from a full domain name
// e.g., "app.example.com" -> "example.com", "www.example.co.uk" -> "example.co.uk"
func GetRootDomain(domain string) (string, error) {
	return publicsuffix.EffectiveTLDPlusOne(strings.ToLower(domain))
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
