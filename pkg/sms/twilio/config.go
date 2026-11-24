package twilio

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/sms"
)

const (
	accountSIDItem      = "account-sid"
	authTokenItem       = "auth-token"
	apiBaseURLItem      = "api-base-url"
	contentBaseURLItem  = "content-base-url"
	defaultIdentityItem = "default-identity"
	identitiesItem      = "identities"
)

const (
	defaultAPIBaseURL     = "https://api.twilio.com"
	defaultContentBaseURL = "https://content.twilio.com"
)

var configDefaults = moduleConfig{
	APIBaseURL:     defaultAPIBaseURL,
	ContentBaseURL: defaultContentBaseURL,
}

type moduleConfig struct {
	AccountSID string `config:"account-sid"`
	// AuthToken authenticates every call and is registered as a literal redaction
	// rule.
	AuthToken       string         `config:"auth-token"`
	APIBaseURL      string         `config:"api-base-url"`
	ContentBaseURL  string         `config:"content-base-url"`
	DefaultIdentity string         `config:"default-identity"`
	Identities      []identitySpec `config:"identities"`
}

type identitySpec struct {
	Name  string           `config:"name"`
	Kind  sms.IdentityKind `config:"kind"`
	Value string           `config:"value"`
	// Empty means every country the carrier reaches.
	Countries  []string          `config:"countries"`
	Attributes map[string]string `config:"attributes"`
}

type resolved struct {
	accountSID      string
	authToken       string
	apiBase         *url.URL
	contentBase     *url.URL
	defaultIdentity string
	identities      []sms.Identity
	identityByName  map[string]sms.Identity
}

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

var identityKinds = map[sms.IdentityKind]bool{
	sms.IdentitySignName:         true,
	sms.IdentitySenderID:         true,
	sms.IdentityLongCode:         true,
	sms.IdentityShortCode:        true,
	sms.IdentityTollFree:         true,
	sms.IdentityPool:             true,
	sms.IdentityMessagingService: true,
}

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
