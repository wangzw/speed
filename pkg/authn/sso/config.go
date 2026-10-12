package sso

import (
	"strings"
	"time"

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

// The configuration item this module accepts, relative to its namespace.
const allowPrivateEgressItem = "allow-private-egress"

// moduleConfig is the set of input items this module accepts. The restricted
// egress client that reaches an IdP is built by the protocol modules, but the
// relaxation that admits a loopback address is declared and reported here,
// because this module owns the authn.sso namespace.
type moduleConfig struct {
	// AllowPrivateEgress admits connections by the restricted egress client to
	// loopback addresses, for development against an IdP on the same machine.
	AllowPrivateEgress bool `config:"allow-private-egress"`
}

// configDefaults is what this module is worth without a configuration file. The
// relaxation is off: a deployment reaches only public addresses.
var configDefaults = moduleConfig{AllowPrivateEgress: false}

// resolved is the configuration this module actually runs on.
type resolved struct {
	AllowPrivateEgress bool
}

// resolve holds the configuration to the ranges the module can run on. Neither
// value has a range beyond the boolean, so a decode that succeeds resolves.
func (c moduleConfig) resolve() (resolved, error) { return resolved(c), nil }

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
