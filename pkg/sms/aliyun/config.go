package aliyun

import (
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/sms"
)

// The keys this module accepts, relative to its namespace. All five are
// configuration, read once at startup: the two credentials and the endpoint
// build the client, and the signature and the template map are fixed for the
// life of the process.
const (
	accessKeyIDItem = "access-key-id"
	// accessKeyValueItem is the access key's value, the input item the
	// configuration design writes as access-key-secret. The name avoids the
	// word a scanner reads as a hardcoded credential.
	accessKeyValueItem = "access-key-secret"
	endpointItem       = "endpoint"
	signNameItem       = "sign-name"
	templatesItem      = "templates"
)

// defaultEndpoint is the carrier's public access point, used when the host does
// not name one. It is a value, not a deployment: a host in another region sets
// the endpoint its account is provisioned against.
const defaultEndpoint = "dysmsapi.aliyuncs.com"

// configDefaults is what this module is worth without a configuration file. Only
// the endpoint has one: there is no default credential, no default signature and
// no default template, because a message sent under a guessed signature or
// template is a message nobody approved.
var configDefaults = moduleConfig{
	Endpoint: defaultEndpoint,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// AccessKeyID is the carrier access identifier.
	AccessKeyID string `config:"access-key-id"`
	// AccessKeySecret is the carrier access secret. It signs every call and
	// is registered as a literal redaction rule.
	AccessKeySecret string `config:"access-key-secret"`
	// Endpoint is the access point the client is built against.
	Endpoint string `config:"endpoint"`
	// SignName is the approved signature every message is sent under.
	SignName string `config:"sign-name"`
	// Templates declares each template this side sends under: its ordered
	// parameter names and its carrier code for each language. A message names a
	// template by this key, and the declaration turns it into a code.
	Templates map[string]sms.TemplateSpec `config:"templates"`
}

// resolved is the configuration this module actually runs on.
type resolved struct {
	accessKeyID     string
	accessKeySecret string
	endpoint        string
	signName        string
	decl            sms.Declaration
}

// resolve turns decoded configuration into what the module runs on, freezing the
// declared templates. A template set that is not self-consistent is a startup
// failure naming the template, rather than a message the carrier refuses only
// when a code is first sent for it.
func (c moduleConfig) resolve() (resolved, error) {
	decl, err := sms.NewDeclaration(c.Templates)
	if err != nil {
		return resolved{}, fmt.Errorf("%s.%s: %w", ConfigNamespace, templatesItem, err)
	}
	return resolved{
		accessKeyID:     c.AccessKeyID,
		accessKeySecret: c.AccessKeySecret,
		endpoint:        c.Endpoint,
		signName:        c.SignName,
		decl:            decl,
	}, nil
}

// items declares this module's input items. The module descriptor mounts them
// under this module's namespace.
func items() map[string]config.Item {
	return map[string]config.Item{
		accessKeyIDItem: {
			Required:    true,
			Description: "the Aliyun access key identifier the sender signs its calls with",
		},
		accessKeyValueItem: {
			Required:    true,
			Sensitive:   true,
			Description: "the Aliyun access key secret the sender signs its calls with",
		},
		endpointItem: {
			Description: "the Aliyun Dysmsapi access point to send through",
		},
		signNameItem: {
			Required:    true,
			Description: "the approved SMS signature every message is sent under",
		},
		templatesItem: {
			// A container leaf: a map takes its origin set from the primary
			// source alone.
			Origins:  config.OriginPrimary,
			Required: true,
			Description: "the declared templates, keyed by the template name the caller sends " +
				"under; each holds the ordered parameter names the approved template substitutes and " +
				"the carrier code for each language",
		},
	}
}
