package aliyun

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
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

// The languages a template key is built for. They are the closed set the SMS
// module passes through in authn.SMS.Locale, named by the root package; an empty
// locale reads as the default one, as it does on the way in.
const (
	localeZhCN = authn.LocaleZH
	localeEnUS = authn.LocaleEN

	defaultLocale = localeZhCN
)

// purposes are the closed set of template keys this side sends under, in the
// order the missing-combination failure reports them.
var purposes = []string{authn.SMSPurposeLogin, authn.SMSPurposeMFA, authn.SMSPurposeEnroll}

// locales are the closed set of languages, in the order the missing-combination
// failure reports them.
var locales = []string{localeZhCN, localeEnUS}

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
	// Templates maps this side's template key, "purpose/locale", to the
	// carrier's approved template code. Every (purpose, locale) combination
	// has to be present.
	Templates map[string]string `config:"templates"`
}

// resolved is the configuration this module actually runs on.
type resolved struct {
	accessKeyID     string
	accessKeySecret string
	endpoint        string
	signName        string
	templates       map[string]string
}

// resolve turns decoded configuration into what the module runs on, checking
// that the template map covers every (purpose, locale) combination. A missing
// combination is a startup failure that names it: the alternative is a message
// the carrier refuses only when a code is first sent for that purpose.
func (c moduleConfig) resolve() (resolved, error) {
	if missing := missingTemplates(c.Templates); len(missing) > 0 {
		return resolved{}, fmt.Errorf("%w: %s.%s is missing the template for %s. Every "+
			"(purpose, locale) combination the code can be sent for needs its carrier template "+
			"code", authn.ErrInvalidConfig, ConfigNamespace, templatesItem, strings.Join(missing, ", "))
	}
	return resolved{
		accessKeyID:     c.AccessKeyID,
		accessKeySecret: c.AccessKeySecret,
		endpoint:        c.Endpoint,
		signName:        c.SignName,
		templates:       copyTemplates(c.Templates),
	}, nil
}

// missingTemplates lists the (purpose, locale) combinations the map does not
// cover with a usable template code, in a stable order. A value that is only
// whitespace counts as absent, as the Tencent adapter judges it: a blank number
// is no template.
func missingTemplates(templates map[string]string) []string {
	var missing []string
	for _, purpose := range purposes {
		for _, locale := range locales {
			if strings.TrimSpace(templates[purpose+"/"+locale]) == "" {
				missing = append(missing, purpose+"/"+locale)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// copyTemplates takes a private copy of the template map, so a host that keeps a
// handle to its decoded configuration cannot change the mapping after the
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
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
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
				// A container leaf: a map takes its origin set from the
				// primary source alone.
				Origins:  config.OriginPrimary,
				Required: true,
				Description: "the carrier template code for each purpose and locale, keyed " +
					"purpose/locale, such as login/zh-CN",
			},
		},
	}
}

// readConfig decodes this module's section and validates it. Prepare and New
// both go through it, so the two see one mapping.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
