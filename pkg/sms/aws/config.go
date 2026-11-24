package aws

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/sms"
)

const (
	regionItem           = "region"
	accessKeyIDItem      = "access-key-id"
	accessKeyValueItem   = "access-key-secret"
	endpointItem         = "endpoint"
	configurationSetItem = "configuration-set-name"
	defaultIdentityItem  = "default-identity"
	identitiesItem       = "identities"
)

var configDefaults = moduleConfig{}

type moduleConfig struct {
	Region      string `config:"region"`
	AccessKeyID string `config:"access-key-id"`
	// AccessKeySecret signs every call and is registered as a literal redaction rule
	// when the host gives it.
	AccessKeySecret      string           `config:"access-key-secret"`
	Endpoint             string           `config:"endpoint"`
	ConfigurationSetName string           `config:"configuration-set-name"`
	DefaultIdentity      string           `config:"default-identity"`
	Identities           []identityConfig `config:"identities"`
}

type identityConfig struct {
	Name  string `config:"name"`
	Kind  string `config:"kind"`
	Value string `config:"value"`
	// Empty means it covers every country the carrier reaches.
	Countries  []string          `config:"countries"`
	Attributes map[string]string `config:"attributes"`
}

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

var identityKinds = map[sms.IdentityKind]bool{
	sms.IdentitySignName:         true,
	sms.IdentitySenderID:         true,
	sms.IdentityLongCode:         true,
	sms.IdentityShortCode:        true,
	sms.IdentityTollFree:         true,
	sms.IdentityPool:             true,
	sms.IdentityMessagingService: true,
}

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
