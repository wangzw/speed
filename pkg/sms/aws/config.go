package aws

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/sms"
)

// The keys this module accepts, relative to its namespace. The region, the
// endpoint, the configuration set, the identities and the template set behind
// them are configuration, read once at startup; so are the credentials, which
// build the client and sign every call. The templates are not here: the set a
// message is resolved against belongs to the template registry, and this carrier
// holds none.
const (
	regionItem           = "region"
	accessKeyIDItem      = "access-key-id"
	accessKeyValueItem   = "access-key-secret"
	endpointItem         = "endpoint"
	configurationSetItem = "configuration-set-name"
	defaultIdentityItem  = "default-identity"
	identitiesItem       = "identities"
)

// configDefaults is what this module is worth without a configuration file. None
// of the items has one: a guessed region, endpoint or identity would send
// messages through an account nobody named, so every one of them is either
// declared by the host or refused at startup.
var configDefaults = moduleConfig{}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Region is the AWS region the client is built against.
	Region string `config:"region"`
	// AccessKeyID is the access key identifier, when the host gives static
	// credentials. Empty leaves the SDK's own default credential chain in place.
	AccessKeyID string `config:"access-key-id"`
	// AccessKeySecret is the secret access key that signs every call. It is
	// registered as a literal redaction rule when the host gives it.
	AccessKeySecret string `config:"access-key-secret"`
	// Endpoint is the access point the client is built against. Empty takes the
	// endpoint the region resolves to.
	Endpoint string `config:"endpoint"`
	// ConfigurationSetName is the vendor configuration set sends are filed
	// under. Empty leaves the vendor's own default.
	ConfigurationSetName string `config:"configuration-set-name"`
	// DefaultIdentity names the identity a message that names none goes out
	// from.
	DefaultIdentity string `config:"default-identity"`
	// Identities are the sending identities this deployment has.
	Identities []identityConfig `config:"identities"`
}

// identityConfig is one sending identity as the configuration declares it: what
// Message.Originator names it by, what sort of identity it is, the vendor's own
// value for it, the countries it covers and any vendor values it carries beside
// that value.
//
// It declares the identity and its coverage and nothing about registration: the
// brand or campaign, the DLT entity and the toll-free verification are the
// vendor's and the contract's, and this process neither tracks nor claims them.
type identityConfig struct {
	// Name is what a message names the identity by.
	Name string `config:"name"`
	// Kind is the shared identity vocabulary's word for it.
	Kind string `config:"kind"`
	// Value is the vendor's own value: a number, a sender ID, a pool or a
	// messaging service identifier.
	Value string `config:"value"`
	// Countries are the countries it covers, as ISO 3166-1 alpha-2 codes. Empty
	// means it covers every country the carrier reaches.
	Countries []string `config:"countries"`
	// Attributes are vendor values that live beside the identity, such as the
	// Indian DLT entity identifier.
	Attributes map[string]string `config:"attributes"`
}

// resolved is the configuration this module actually runs on.
type resolved struct {
	region               string
	accessKeyID          string
	accessKeySecret      string
	endpoint             string
	configurationSetName string
	defaultIdentity      string
	identities           []sms.Identity
	byName               map[string]sms.Identity
}

// identityKinds is the identity vocabulary this carrier accepts. The words are
// the capability's, not the vendor's: AWS takes a number, a sender ID, a pool or
// a messaging service, and this is where the deployment says which one it is.
var identityKinds = map[sms.IdentityKind]bool{
	sms.IdentitySignName:         true,
	sms.IdentitySenderID:         true,
	sms.IdentityLongCode:         true,
	sms.IdentityShortCode:        true,
	sms.IdentityTollFree:         true,
	sms.IdentityPool:             true,
	sms.IdentityMessagingService: true,
}

// resolve turns decoded configuration into what the module runs on and checks
// that it can run on it.
//
// Everything it refuses is ErrInvalidConfig and is refused at startup rather than
// at the first send: a region nobody named, one credential without the other, no
// identity at all, an identity without a name, a kind or a value, two identities
// under one name, a kind outside the shared vocabulary, and a default identity
// the section does not declare. The loader has already enforced that the section
// names a region, a default identity and an identity list; this is the
// consistency of what it named.
func (c moduleConfig) resolve() (resolved, error) {
	if strings.TrimSpace(c.Region) == "" {
		return resolved{}, fmt.Errorf("%w: %s names no region, and every call has to be sent to one",
			sms.ErrInvalidConfig, ModuleName)
	}
	if (c.AccessKeyID == "") != (c.AccessKeySecret == "") {
		return resolved{}, fmt.Errorf("%w: %s gives one of access-key-id and access-key-secret; give both "+
			"or neither, so the SDK's own credential chain can be used", sms.ErrInvalidConfig, ModuleName)
	}

	identities := make([]sms.Identity, 0, len(c.Identities))
	byName := make(map[string]sms.Identity, len(c.Identities))
	for i, declared := range c.Identities {
		name := strings.TrimSpace(declared.Name)
		if name == "" {
			return resolved{}, fmt.Errorf("%w: %s declares identity %d with no name",
				sms.ErrInvalidConfig, ModuleName, i)
		}
		if _, seen := byName[name]; seen {
			return resolved{}, fmt.Errorf("%w: %s declares the identity %q twice",
				sms.ErrInvalidConfig, ModuleName, name)
		}
		kind := sms.IdentityKind(strings.TrimSpace(declared.Kind))
		if kind == "" {
			return resolved{}, fmt.Errorf("%w: the identity %q names no kind",
				sms.ErrInvalidConfig, name)
		}
		if !identityKinds[kind] {
			return resolved{}, fmt.Errorf("%w: the identity %q names the kind %q, which is outside the "+
				"shared identity vocabulary", sms.ErrInvalidConfig, name, kind)
		}
		value := strings.TrimSpace(declared.Value)
		if value == "" {
			return resolved{}, fmt.Errorf("%w: the identity %q has no vendor value",
				sms.ErrInvalidConfig, name)
		}
		countries := make([]string, 0, len(declared.Countries))
		for _, country := range declared.Countries {
			country = strings.TrimSpace(country)
			if country == "" {
				return resolved{}, fmt.Errorf("%w: the identity %q declares an empty country",
					sms.ErrInvalidConfig, name)
			}
			countries = append(countries, country)
		}
		attributes := make(map[string]string, len(declared.Attributes))
		for key, attribute := range declared.Attributes {
			attributes[key] = attribute
		}
		identity := sms.Identity{
			Name:       name,
			Kind:       kind,
			Value:      value,
			Countries:  countries,
			Attributes: attributes,
		}
		identities = append(identities, identity)
		byName[name] = identity
	}
	if len(identities) == 0 {
		return resolved{}, fmt.Errorf("%w: %s declares no sending identity, and a message with nowhere "+
			"to go out from is not one this carrier can send", sms.ErrInvalidConfig, ModuleName)
	}

	defaultIdentity := strings.TrimSpace(c.DefaultIdentity)
	if defaultIdentity == "" {
		return resolved{}, fmt.Errorf("%w: %s names no default identity, so a message that names none "+
			"has nowhere to go out from", sms.ErrInvalidConfig, ModuleName)
	}
	if _, ok := byName[defaultIdentity]; !ok {
		return resolved{}, fmt.Errorf("%w: %s names the default identity %q, which it does not declare",
			sms.ErrInvalidConfig, ModuleName, defaultIdentity)
	}

	return resolved{
		region:               strings.TrimSpace(c.Region),
		accessKeyID:          c.AccessKeyID,
		accessKeySecret:      c.AccessKeySecret,
		endpoint:             c.Endpoint,
		configurationSetName: c.ConfigurationSetName,
		defaultIdentity:      defaultIdentity,
		identities:           identities,
		byName:               byName,
	}, nil
}

// items declares this module's input items. The module descriptor mounts them
// under this module's namespace.
//
// The identity list is a list of structs, which has no flat form the environment
// or the command line could give: it takes the primary configuration source
// alone, so the identities are changed in the config file or the remote config
// centre. The two credentials are optional because a deployment on AWS may run on
// the credentials the SDK's own chain finds, and both are declared rather than
// inferred so a host reading the manifest sees them.
func items() map[string]config.Item {
	return map[string]config.Item{
		regionItem: {
			Description: "the AWS region the sender signs and sends against",
		},
		accessKeyIDItem: {
			Description: "the AWS access key identifier, when the host gives static credentials",
		},
		accessKeyValueItem: {
			Sensitive:   true,
			Description: "the AWS secret access key that signs every call, when the host gives static credentials",
		},
		endpointItem: {
			Description: "the End User Messaging SMS access point to send through",
		},
		configurationSetItem: {
			Description: "the configuration set the vendor files sends under",
		},
		defaultIdentityItem: {
			Description: "the identity a message that names none is sent from",
		},
		identitiesItem: {
			Origins:     config.OriginPrimary,
			Description: "the sending identities this deployment has, each with a name, a kind, a value, its countries and attributes",
		},
	}
}
