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

// samlNamespace is where the SAML module declares the service provider's key,
// which a provider that signs its requests needs. The key is read here, in the
// module that owns the provider rows, so that a Put can refuse a provider whose
// requests could not be signed; this is the cross-namespace read the
// configuration design allows for exactly this case.
const samlNamespace = "authn.sso.saml"

// samlKeySettings is the part of the SAML module's section read here, the
// service provider's private key under authn.sso.saml.sp-key-file.
type samlKeySettings struct {
	SPKeyFile string `config:"sp-key-file"`
}

// spKeyConfigured reports whether the deployment carries the SAML service
// provider's private key. A namespace nobody declared, and a read that fails,
// both read as absent: the strict direction, since the answer decides whether a
// request may be signed.
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

// The configuration items this module accepts, relative to its namespace.
const (
	allowPrivateEgressItem = "allow-private-egress"
	dnsResolverItem        = "dns-resolver"
	oidcCAFileItem         = "oidc-ca-file"
)

// moduleConfig is the set of input items this module accepts. The restricted
// egress client that reaches an IdP is built by the protocol modules, but the
// items that shape it are declared and reported here, because this module owns
// the authn.sso namespace. The domain ownership proof resolves through the
// resolver named here; the enterprise OIDC method trusts the extra roots named
// here beside the system ones.
type moduleConfig struct {
	// AllowPrivateEgress admits connections by the restricted egress client to
	// loopback addresses, for development against an IdP on the same machine.
	AllowPrivateEgress bool `config:"allow-private-egress"`
	// DNSResolver is the address (host:port) of the resolver the TXT lookup of
	// a domain ownership proof goes through. Empty keeps the system resolver.
	DNSResolver string `config:"dns-resolver"`
	// OIDCCAFile is the path of a PEM file of extra root certificates the
	// enterprise OIDC egress trusts beside the system roots. Empty trusts the
	// system roots alone.
	OIDCCAFile string `config:"oidc-ca-file"`
}

// configDefaults is what this module is worth without a configuration file. The
// relaxation is off and both sources keep the system's answer: a deployment
// reaches only public addresses, resolves through the system resolver and trusts
// the system roots.
var configDefaults = moduleConfig{AllowPrivateEgress: false}

// resolved is the configuration this module actually runs on.
type resolved struct {
	AllowPrivateEgress bool
	DNSResolver        string
}

// resolve holds the configuration to the ranges the module can run on. The
// resolver, when set, has to be an address the resolver can dial.
func (c moduleConfig) resolve() (resolved, error) {
	address := strings.TrimSpace(c.DNSResolver)
	if address != "" {
		if err := validateResolverAddress(address); err != nil {
			return resolved{}, err
		}
	}
	return resolved{AllowPrivateEgress: c.AllowPrivateEgress, DNSResolver: address}, nil
}

// validateResolverAddress holds the resolver to host:port, so a misspelt value
// fails the start instead of quietly falling back to the system resolver.
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

// configSchema declares this module's input items.
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

// readConfig decodes this module's section and validates it.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}

// The setting keys this module declares, relative to its namespace, and the full
// paths a consumer reads them by. The protocol modules read them at request time;
// declaring them here is what makes them adjustable.
const (
	assertionMaxAgeKey  = "assertion-max-age"
	providerCacheTTLKey = "provider-cache-ttl"
	egressTimeoutKey    = "egress-timeout"
)

// The bounds of the setting domains.
const (
	minAssertionMaxAge  = time.Minute
	maxAssertionMaxAge  = 30 * time.Minute
	minProviderCacheTTL = 5 * time.Second
	maxProviderCacheTTL = time.Hour
	minEgressTimeout    = time.Second
	maxEgressTimeout    = 60 * time.Second
)

// settingsDefaults carries the settings this module declares and their defaults,
// which the settings module reads off it and which it never receives a value for.
type settingsDefaults struct {
	AssertionMaxAge  time.Duration `config:"assertion-max-age"`
	ProviderCacheTTL time.Duration `config:"provider-cache-ttl"`
	EgressTimeout    time.Duration `config:"egress-timeout"`
}

// settingDefaults is the declared default of each setting.
var settingDefaults = settingsDefaults{
	AssertionMaxAge:  5 * time.Minute,
	ProviderCacheTTL: 60 * time.Second,
	EgressTimeout:    10 * time.Second,
}

// The domains of the settings. Each is the one statement of its range.
func assertionMaxAgeInDomain(d time.Duration) bool {
	return d >= minAssertionMaxAge && d <= maxAssertionMaxAge
}

func providerCacheTTLInDomain(d time.Duration) bool {
	return d >= minProviderCacheTTL && d <= maxProviderCacheTTL
}

func egressTimeoutInDomain(d time.Duration) bool {
	return d >= minEgressTimeout && d <= maxEgressTimeout
}

// settingsSchema declares this module's runtime parameters, each with the domain
// the code acts on. The consumers are the protocol modules; a value outside its
// domain reads as the declared default and is reported.
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
