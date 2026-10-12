package tencent

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
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

// The languages a message may be written in. The set is closed: the carrier
// approves a template per language, so the language is one dimension of the
// template lookup and a value outside the set is a caller defect, not a
// language to send in.
const (
	localeZH = "zh-CN"
	localeEN = "en-US"
)

// purposes are the template keys this module maps to a carrier template number.
// The set is the closed one the SMS contract fixes.
var purposes = []string{authn.SMSPurposeLogin, authn.SMSPurposeMFA, authn.SMSPurposeEnroll}

// locales are the languages a message may be written in, in the order the
// declared combinations are checked.
var locales = []string{localeZH, localeEN}

// moduleConfig is the set of input items this module accepts. Every item is
// required: a carrier adapter with no credential or no template cannot send, and
// whether this module is taken in at all is the command tree's decision.
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
	// Templates maps each (purpose, locale) pair to a carrier template number.
	// The key is written "purpose/locale", as in "login/zh-CN".
	Templates map[string]string `config:"templates"`
}

// resolved is the configuration this module runs on: the credentials and the
// template table, with the table already checked to cover every (purpose,
// locale) pair.
type resolved struct {
	secretID  string
	secretKey string
	sdkAppID  string
	region    string
	signName  string
	templates map[string]string
}

// templateKey is the key a (purpose, locale) pair is looked up under, in the
// table and in the carrier's template list.
func templateKey(purpose, locale string) string { return purpose + "/" + locale }

// localeOf is the language a message is built in: the one named, or zh-CN when
// none is. The caller's empty value reads as zh-CN, matching the contract of the
// SendCode this is built from.
func localeOf(locale string) string {
	if locale == "" {
		return localeZH
	}
	return locale
}

// resolve validates the decoded configuration and turns it into what the module
// runs on.
//
// The template table has to map every (purpose, locale) pair the contract fixes:
// a pair with no template number is a startup failure naming the pair, because
// that purpose could never be sent for in that language. A key outside the pairs
// is not refused — the mapping is read under the pairs the code sends for, and an
// unused key changes nothing about what is sent.
func (c moduleConfig) resolve() (resolved, error) {
	for _, purpose := range purposes {
		for _, locale := range locales {
			key := templateKey(purpose, locale)
			if strings.TrimSpace(c.Templates[key]) == "" {
				return resolved{}, fmt.Errorf("%w: %s.%s names no template for %q. Write a "+
					"carrier template number for every (purpose, locale) pair, keyed "+
					"\"purpose/locale\" with the purpose one of %s and the language one of %s",
					authn.ErrInvalidConfig, ConfigNamespace, templatesItem, key,
					strings.Join(purposes, ", "), strings.Join(locales, ", "))
			}
		}
	}
	return resolved{
		secretID:  c.SecretID,
		secretKey: c.SecretKey,
		sdkAppID:  c.SDKAppID,
		region:    c.Region,
		signName:  c.SignName,
		templates: copyTemplates(c.Templates),
	}, nil
}

// copyTemplates takes a private copy of the template table, so a host that keeps
// a handle to its decoded configuration cannot change the mapping after the
// startup checks have run.
func copyTemplates(templates map[string]string) map[string]string {
	out := make(map[string]string, len(templates))
	for key, code := range templates {
		out[key] = code
	}
	return out
}

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &moduleConfig{}}},
		Items: map[string]config.Item{
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
				Description: "a carrier template number for every (purpose, locale) pair, keyed " +
					"\"purpose/locale\", the purpose one of login, mfa, enroll and the language " +
					"one of " + strings.Join(locales, ", "),
			},
		},
	}
}

// readConfig decodes this module's section and validates it. Prepare and New both
// go through it, so the two see one table.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
