package tencent

import (
	"github.com/vislake/speed/pkg/config"
)

// The keys this module accepts, relative to its namespace. All of them are
// configuration: a carrier's credentials, application identity and signature
// have to be in hand before a message can be built, so there is no setting here.
const (
	secretIDItem  = "secret-id"
	secretKeyItem = "secret-key"
	sdkAppIDItem  = "sdk-app-id"
	regionItem    = "region"
	signNameItem  = "sign-name"
)

// moduleConfig is the set of input items this module accepts. Every item is
// required: a carrier adapter with no credential cannot send, and whether this
// module is taken in at all is the command tree's decision.
type moduleConfig struct {
	// SecretID is the credential's identifier.
	SecretID string `config:"secret-id"`
	// SecretKey is the credential's secret. It is the value a leaked log line
	// hands over, so it is marked sensitive and registered as a literal
	// redaction rule.
	SecretKey string `config:"secret-key"`
	// SDKAppID is the carrier application the messages are sent as.
	SDKAppID string `config:"sdk-app-id"`
	// Region is the carrier region the requests are signed for.
	Region string `config:"region"`
	// SignName is the approved signature a message is sent under.
	SignName string `config:"sign-name"`
}

// resolved is the configuration this module runs on: the credentials, the
// application identity and the signature.
type resolved struct {
	secretID  string
	secretKey string
	sdkAppID  string
	region    string
	signName  string
}

// resolve turns the decoded configuration into what the module runs on.
func (c moduleConfig) resolve() resolved {
	return resolved{
		secretID:  c.SecretID,
		secretKey: c.SecretKey,
		sdkAppID:  c.SDKAppID,
		region:    c.Region,
		signName:  c.SignName,
	}
}

// items declares this module's input items. The module descriptor mounts them
// under this module's namespace.
func items() map[string]config.Item {
	return map[string]config.Item{
		secretIDItem: {
			Required:    true,
			Description: "the identifier of the credential the requests are signed with",
		},
		secretKeyItem: {
			Required:    true,
			Sensitive:   true,
			Description: "the secret of the credential the requests are signed with",
		},
		sdkAppIDItem: {
			Required:    true,
			Description: "the carrier application the messages are sent as",
		},
		regionItem: {
			Required:    true,
			Description: "the carrier region the requests are signed for, such as ap-guangzhou",
		},
		signNameItem: {
			Required:    true,
			Description: "the approved signature the messages are sent under",
		},
	}
}
