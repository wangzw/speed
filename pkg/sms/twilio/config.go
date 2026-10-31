package twilio

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/sms"
)

// The keys this module accepts, relative to its namespace. All of them are
// configuration, read once at startup: the credentials build the client, the two
// base URLs fix where it posts, and the identities and the default identity fix
// how a message is addressed for the life of the process. The templates are not
// here: the set a message is resolved against belongs to the template registry,
// and this carrier holds none.
const (
	accountSIDItem      = "account-sid"
	authTokenItem       = "auth-token"
	apiBaseURLItem      = "api-base-url"
	contentBaseURLItem  = "content-base-url"
	defaultIdentityItem = "default-identity"
	identitiesItem      = "identities"
)

// The carrier's public access points, used when the host names none. The
// Messages API is served by the first and the Content API by the second; each is
// a value and not a deployment, so a host in another region or behind a gateway
// sets the one its account is provisioned against.
const (
	defaultAPIBaseURL     = "https://api.twilio.com"
	defaultContentBaseURL = "https://content.twilio.com"
)

// configDefaults is what this module is worth without a configuration file. Only
// the two base URLs have a value: there is no default credential and no default
// identity, because a message sent from a guessed account is a message nobody
// authorised, and a message sent from a guessed identity is one nobody approved.
var configDefaults = moduleConfig{
	APIBaseURL:     defaultAPIBaseURL,
	ContentBaseURL: defaultContentBaseURL,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// AccountSID is the account the messages are sent as and the content
	// resources are created under.
	AccountSID string `config:"account-sid"`
	// AuthToken is the account's secret. It authenticates every call and is
	// registered as a literal redaction rule.
	AuthToken string `config:"auth-token"`
	// APIBaseURL is the Messages API access point; a case points it at a loopback
	// server.
	APIBaseURL string `config:"api-base-url"`
	// ContentBaseURL is the Content API access point.
	ContentBaseURL string `config:"content-base-url"`
	// DefaultIdentity names the identity a message goes out from when it names
	// none.
	DefaultIdentity string `config:"default-identity"`
	// Identities are the sending identities this deployment has.
	Identities []identitySpec `config:"identities"`
}

// identitySpec is one identity as the configuration writes it.
type identitySpec struct {
	// Name is what a message's Originator names it by; it is unique in the list.
	Name string `config:"name"`
	// Kind is what sort of identity it is, in sms.IdentityKind's vocabulary.
	Kind sms.IdentityKind `config:"kind"`
	// Value is the vendor's own value: a messaging service SID, a phone number, a
	// short code.
	Value string `config:"value"`
	// Countries are the countries it can serve, passed through to the carrier
	// declaration unchanged. Empty means every country the carrier reaches.
	Countries []string `config:"countries"`
	// Attributes carries vendor-specific values the identity needs; the "from"
	// attribute pins a sender inside a messaging service.
	Attributes map[string]string `config:"attributes"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	accountSID      string
	authToken       string
	apiBase         *url.URL
	contentBase     *url.URL
	defaultIdentity string
	// identities is the declaration in a stable order, and identityByName is the
	// lookup Send performs.
	identities     []sms.Identity
	identityByName map[string]sms.Identity
}

// resolve turns decoded configuration into what the module runs on, refusing a
// section it cannot run on.
//
// The required items are enforced by the loader before this is reached; what is
// checked here is what the loader cannot see: the two base URLs have to be usable
// HTTP endpoints, the identity list has to hold at least one identity with a
// unique, non-empty name and a value, and the default identity has to name one of
// them. A section that fails is a startup failure, before any message is built.
func (c moduleConfig) resolve() (resolved, error) {
	apiBase, err := parseBaseURL(apiBaseURLItem, c.APIBaseURL)
	if err != nil {
		return resolved{}, err
	}
	contentBase, err := parseBaseURL(contentBaseURLItem, c.ContentBaseURL)
	if err != nil {
		return resolved{}, err
	}
	if len(c.Identities) == 0 {
		return resolved{}, fmt.Errorf("%w: %s.%s declares no sending identity, and this carrier has "+
			"nothing to send a message from", sms.ErrInvalidConfig, ConfigNamespace, identitiesItem)
	}
	identities := make([]sms.Identity, 0, len(c.Identities))
	identityByName := make(map[string]sms.Identity, len(c.Identities))
	for i, spec := range c.Identities {
		if strings.TrimSpace(spec.Name) == "" {
			return resolved{}, fmt.Errorf("%w: %s.%s entry %d names no identity", sms.ErrInvalidConfig,
				ConfigNamespace, identitiesItem, i)
		}
		if _, seen := identityByName[spec.Name]; seen {
			return resolved{}, fmt.Errorf("%w: %s.%s declares the identity %q twice", sms.ErrInvalidConfig,
				ConfigNamespace, identitiesItem, spec.Name)
		}
		if !identityKinds[spec.Kind] {
			return resolved{}, fmt.Errorf("%w: the identity %q names the kind %q, which this package does "+
				"not know", sms.ErrInvalidConfig, spec.Name, spec.Kind)
		}
		if strings.TrimSpace(spec.Value) == "" {
			return resolved{}, fmt.Errorf("%w: the identity %q carries no value", sms.ErrInvalidConfig, spec.Name)
		}
		identity := sms.Identity{
			Name:       spec.Name,
			Kind:       spec.Kind,
			Value:      spec.Value,
			Countries:  append([]string(nil), spec.Countries...),
			Attributes: copyAttributes(spec.Attributes),
		}
		identities = append(identities, identity)
		identityByName[identity.Name] = identity
	}
	sort.SliceStable(identities, func(i, j int) bool { return identities[i].Name < identities[j].Name })
	if _, ok := identityByName[c.DefaultIdentity]; !ok {
		return resolved{}, fmt.Errorf("%w: %s.%s names %q, which %s.%s does not declare", sms.ErrInvalidConfig,
			ConfigNamespace, defaultIdentityItem, c.DefaultIdentity, ConfigNamespace, identitiesItem)
	}
	return resolved{
		accountSID:      c.AccountSID,
		authToken:       c.AuthToken,
		apiBase:         apiBase,
		contentBase:     contentBase,
		defaultIdentity: c.DefaultIdentity,
		identities:      identities,
		identityByName:  identityByName,
	}, nil
}

// parseBaseURL reads one configured access point, requiring an absolute HTTP(S)
// URL. A value that is not one would fail on the first call, after a message was
// already built; here it stops the startup.
func parseBaseURL(item, value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %s.%s is not a URL: %w", sms.ErrInvalidConfig, ConfigNamespace, item, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("%w: %s.%s is %q, and an access point is an absolute http or https URL",
			sms.ErrInvalidConfig, ConfigNamespace, item, value)
	}
	return parsed, nil
}

// identityKinds is the closed set of identity kinds this carrier accepts. They
// are the vocabulary every carrier shares; the carrier's own translation to the
// vendor's spelling happens in Send.
var identityKinds = map[sms.IdentityKind]bool{
	sms.IdentitySignName:         true,
	sms.IdentitySenderID:         true,
	sms.IdentityLongCode:         true,
	sms.IdentityShortCode:        true,
	sms.IdentityTollFree:         true,
	sms.IdentityPool:             true,
	sms.IdentityMessagingService: true,
}

// copyAttributes copies a string map, so a caller holding the configuration
// cannot change what the sender reads.
func copyAttributes(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	copied := make(map[string]string, len(attributes))
	for key, value := range attributes {
		copied[key] = value
	}
	return copied
}

// items declares this module's input items. The module descriptor mounts them
// under this module's namespace.
func items() map[string]config.Item {
	return map[string]config.Item{
		accountSIDItem: {
			Description: "the Twilio account SID the messages are sent as and the content resources are created under",
		},
		authTokenItem: {
			Sensitive:   true,
			Description: "the Twilio auth token that authenticates every call",
		},
		apiBaseURLItem: {
			Description: "the Messages API access point to send through",
		},
		contentBaseURLItem: {
			Description: "the Content API access point to create and remove content resources through",
		},
		defaultIdentityItem: {
			Description: "the name of the sending identity a message goes out from when it names none",
		},
		identitiesItem: {
			Description: "the sending identities this deployment has, each with a unique name, a kind, the vendor " +
				"value, the countries it serves and any vendor-specific attributes; a From value may live in an " +
				"identity's attributes under \"from\"",
		},
	}
}
