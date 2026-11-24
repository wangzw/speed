package sso

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
)

const samlNamespace = "authn.sso.saml"

type samlKeySettings struct {
	SPKeyFile string `config:"sp-key-file"`
}

func (p *product) spKeyConfigured() bool {
	if p.reader == nil {
		return false
	}
	var s samlKeySettings
	if err := p.reader.Decode(samlNamespace, &s); err != nil {
		return false
	}
	return strings.TrimSpace(s.SPKeyFile) != ""
}

const (
	allowPrivateEgressItem = "allow-private-egress"
	dnsResolverItem        = "dns-resolver"
	oidcCAFileItem         = "oidc-ca-file"
)

type moduleConfig struct {
	AllowPrivateEgress bool   `config:"allow-private-egress"`
	DNSResolver        string `config:"dns-resolver"`
	OIDCCAFile         string `config:"oidc-ca-file"`
}

var configDefaults = moduleConfig{AllowPrivateEgress: false}

type resolved struct {
	AllowPrivateEgress bool
	DNSResolver        string
}

func (c moduleConfig) resolve() (resolved, error) {
	address := strings.TrimSpace(c.DNSResolver)
	if address != "" {
		if err := validateResolverAddress(address); err != nil {
			return resolved{}, err
		}
	}
	return resolved{AllowPrivateEgress: c.AllowPrivateEgress, DNSResolver: address}, nil
}

func validateResolverAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return fmt.Errorf("%w: %s has to be a resolver address of the form host:port",
			authn.ErrInvalidConfig, dnsResolverItem)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%w: %s names the port %q, which is not a port number",
			authn.ErrInvalidConfig, dnsResolverItem, port)
	}
	return nil
}

func configSchema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			allowPrivateEgressItem: {
				Origins: config.OriginPrimary,
				Description: "whether the restricted egress client admits connections to loopback " +
					"addresses, for development against an IdP on the same machine; this relaxes an " +
					"outbound protection and is read from the primary configuration source alone",
			},
			dnsResolverItem: {
				Origins: config.OriginPrimary,
				Description: "the address (host:port) of the DNS resolver the TXT record of a domain " +
					"ownership proof is looked up through; empty uses the system resolver, and a " +
					"deployment that names one trusts its answers",
			},
			oidcCAFileItem: {
				Origins: config.OriginPrimary,
				Description: "the path of a PEM file of extra root certificates the enterprise OIDC " +
					"egress trusts beside the system roots; empty trusts the system roots alone, and a " +
					"deployment that names one takes certificate verification into its own hands",
			},
		},
	}
}

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}

const (
	assertionMaxAgeKey  = "assertion-max-age"
	providerCacheTTLKey = "provider-cache-ttl"
	egressTimeoutKey    = "egress-timeout"
)

const (
	minAssertionMaxAge  = time.Minute
	maxAssertionMaxAge  = 30 * time.Minute
	minProviderCacheTTL = 5 * time.Second
	maxProviderCacheTTL = time.Hour
	minEgressTimeout    = time.Second
	maxEgressTimeout    = 60 * time.Second
)

type settingsDefaults struct {
	AssertionMaxAge  time.Duration `config:"assertion-max-age"`
	ProviderCacheTTL time.Duration `config:"provider-cache-ttl"`
	EgressTimeout    time.Duration `config:"egress-timeout"`
}

var settingDefaults = settingsDefaults{
	AssertionMaxAge:  5 * time.Minute,
	ProviderCacheTTL: 60 * time.Second,
	EgressTimeout:    10 * time.Second,
}

func assertionMaxAgeInDomain(d time.Duration) bool {
	return d >= minAssertionMaxAge && d <= maxAssertionMaxAge
}

func providerCacheTTLInDomain(d time.Duration) bool {
	return d >= minProviderCacheTTL && d <= maxProviderCacheTTL
}

func egressTimeoutInDomain(d time.Duration) bool {
	return d >= minEgressTimeout && d <= maxEgressTimeout
}

func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &settingDefaults}},
		Items: map[string]settings.Item{
			assertionMaxAgeKey: {
				Description: "how long before now a SAML assertion's IssueInstant may be and still " +
					"be accepted; a larger window widens the replay window",
				Domain: settings.DomainOf("durations from 1m0s to 30m0s", assertionMaxAgeInDomain),
			},
			providerCacheTTLKey: {
				Description: "how long an OIDC provider or a SAML service provider stays in the " +
					"per-process cache of a protocol module",
				Domain: settings.DomainOf("durations from 5s to 1h0m0s", providerCacheTTLInDomain),
			},
			egressTimeoutKey: {
				Description: "the deadline of an outbound call to an IdP; the client carries no total " +
					"timeout and each call takes its deadline from its context",
				Domain: settings.DomainOf("durations from 1s to 1m0s", egressTimeoutInDomain),
			},
		},
	}
}
