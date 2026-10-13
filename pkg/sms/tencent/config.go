package tencent

import (
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/sms"
)

// The keys this module accepts, relative to its namespace. All of them are
// configuration: a carrier's credentials, application identity and templates
// have to be in hand before a message can be built, so there is no setting here.
const (
	secretIDItem  = "secret-id"
	secretKeyItem = "secret-key"
	sdkAppIDItem  = "sdk-app-id"
	regionItem    = "region"
	signNameItem  = "sign-name"
	templatesItem = "templates"
)

// moduleConfig is the set of input items this module accepts. Every scalar item
// is required: a carrier adapter with no credential or no template cannot send,
// and whether this module is taken in at all is the command tree's decision.
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
	// Templates declares each template this side sends under: its ordered
	// parameter names and its carrier number for each language. A message names a
	// template by this key, and the declaration turns it into a number.
	Templates map[string]sms.TemplateSpec `config:"templates"`
}

// resolved is the configuration this module runs on: the credentials and the
// frozen declaration, with the template set already checked for self-consistency.
type resolved struct {
	secretID  string
	secretKey string
	sdkAppID  string
	region    string
	signName  string
	decl      sms.Declaration
}

// resolve validates the decoded configuration and turns it into what the module
// runs on.
//
// The declaration needs every template to substitute at least one parameter and
// to have a non-empty carrier code for at least one language; a set that fails is
// a startup failure naming the template. Whether the templates cover what a
// consumer needs is checked by the consumer against Sender.Declared.
func (c moduleConfig) resolve() (resolved, error) {
	decl, err := sms.NewDeclaration(c.Templates)
	if err != nil {
		return resolved{}, fmt.Errorf("%s.%s: %w", ConfigNamespace, templatesItem, err)
	}
	return resolved{
		secretID:  c.SecretID,
		secretKey: c.SecretKey,
		sdkAppID:  c.SDKAppID,
		region:    c.Region,
		signName:  c.SignName,
		decl:      decl,
	}, nil
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
		templatesItem: {
			Origins:  config.OriginPrimary,
			Required: true,
			Description: "the declared templates, keyed by the template name the caller sends " +
				"under; each holds the ordered parameter names the approved template substitutes and " +
				"the carrier number for each language",
		},
	}
}
