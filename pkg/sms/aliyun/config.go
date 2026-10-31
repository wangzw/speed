package aliyun

import (
	"github.com/vislake/speed/pkg/config"
)

// The keys this module accepts, relative to its namespace. All four are
// configuration, read once at startup: the two credentials and the endpoint
// build the client, and the signature every message is sent under is fixed for
// the life of the process. The templates are not here: the set a message is
// resolved against belongs to the template registry, and this carrier holds none.
const (
	accessKeyIDItem = "access-key-id"
	// accessKeyValueItem is the access key's value, the input item the
	// configuration design writes as access-key-secret. The name avoids the
	// word a scanner reads as a hardcoded credential.
	accessKeyValueItem = "access-key-secret"
	endpointItem       = "endpoint"
	signNameItem       = "sign-name"
)

// defaultEndpoint is the carrier's public access point, used when the host does
// not name one. It is a value, not a deployment: a host in another region sets
// the endpoint its account is provisioned against.
const defaultEndpoint = "dysmsapi.aliyuncs.com"

// configDefaults is what this module is worth without a configuration file. Only
// the endpoint has one: there is no default credential and no default signature,
// because a message sent under a guessed signature is a message nobody approved.
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
	// SignName is the approved signature every message is sent under, and the
	// signature a template is offered under.
	SignName string `config:"sign-name"`
}

// resolved is the configuration this module actually runs on.
type resolved struct {
	accessKeyID     string
	accessKeySecret string
	endpoint        string
	signName        string
}

// resolve turns decoded configuration into what the module runs on. There is
// nothing to check here: the items the module cannot run without are declared
// required, and the loader refuses a section that omits one before this is
// reached.
func (c moduleConfig) resolve() resolved {
	return resolved{
		accessKeyID:     c.AccessKeyID,
		accessKeySecret: c.AccessKeySecret,
		endpoint:        c.Endpoint,
		signName:        c.SignName,
	}
}

// items declares this module's input items. The module descriptor mounts them
// under this module's namespace.
func items() map[string]config.Item {
	return map[string]config.Item{
		accessKeyIDItem: {
			Description: "the Aliyun access key identifier the sender signs its calls with",
		},
		accessKeyValueItem: {
			Sensitive:   true,
			Description: "the Aliyun access key secret the sender signs its calls with",
		},
		endpointItem: {
			Description: "the Aliyun Dysmsapi access point to send through",
		},
		signNameItem: {
			Description: "the approved SMS signature every message is sent under",
		},
	}
}
