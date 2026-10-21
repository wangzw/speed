package token

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

// The keys this module accepts, relative to its namespace. Every one of them is
// configuration, not a setting: they are read before a database is reachable,
// and the signing key files are startup requirements.
const (
	issuerItem         = "issuer"
	audienceItem       = "audience"
	clientIDItem       = "client-id"
	algorithmItem      = "algorithm"
	signingKeyFileItem = "signing-key-file"
	verifyKeyFilesItem = "verify-key-files"
)

// gateNamespace and gatePublicURLItem are the gateway's, read here because the
// issuer defaults to the address the deployment is reached at.
const (
	gateNamespace     = "authn.gate"
	gatePublicURLItem = "mount.public-url"
)

// clientIDDefault is the client identifier a token issued for a session carries
// when the configuration names none. It is the value RFC 9068 requires a token
// to carry and a deployment rarely cares about, so it has a fixed default.
const clientIDDefault = "speed"

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Issuer is the "iss" claim, the value every verifier compares exactly.
	// Empty takes the public address the deployment is reached at.
	Issuer string `config:"issuer"`
	// Audience is the "aud" claim, a set a verifier needs one member of.
	// Empty takes the issuer alone.
	Audience []string `config:"audience"`
	// ClientID is the "client_id" claim of a token issued for a session.
	ClientID string `config:"client-id"`
	// Algorithm is the signing algorithm: ES256 or RS256.
	Algorithm string `config:"algorithm"`
	// SigningKeyFile is the PEM file of the signing private key. Empty leaves
	// the module verify-only: it checks tokens but issues none.
	SigningKeyFile string `config:"signing-key-file"`
	// VerifyKeyFiles are PEM files of keys used only to verify and to publish.
	VerifyKeyFiles []string `config:"verify-key-files"`
}

// configDefaults is what this module is worth without a configuration file. The
// issuer and the audience default off the gateway's public address, so they are
// resolved rather than held here.
var configDefaults = moduleConfig{
	ClientID:  clientIDDefault,
	Algorithm: algES256,
}

// gateMount is the part of the gateway's configuration this module reads.
type gateMount struct {
	PublicURL string `config:"public-url"`
}

// resolved is the configuration this module actually runs on.
type resolved struct {
	issuer      string
	audience    []string
	clientID    string
	algorithm   string
	signingFile string
	verifyFiles []string
}

// resolve turns decoded configuration into what the module runs on, or says
// which item to correct. The issuer and the audience are filled from the public
// address when they are not given, so an assembly that names neither still
// issues tokens with an "iss" and an "aud" that a verifier can compare.
func (c moduleConfig) resolve(gm gateMount) (resolved, error) {
	r := resolved{
		clientID:    strings.TrimSpace(c.ClientID),
		algorithm:   strings.TrimSpace(c.Algorithm),
		signingFile: strings.TrimSpace(c.SigningKeyFile),
	}

	r.issuer = strings.TrimSpace(c.Issuer)
	if r.issuer == "" {
		r.issuer = strings.TrimSpace(gm.PublicURL)
	}

	for _, a := range c.Audience {
		if a = strings.TrimSpace(a); a != "" {
			r.audience = append(r.audience, a)
		}
	}
	if len(r.audience) == 0 {
		if r.issuer == "" {
			return resolved{}, fmt.Errorf("%w: %s.%s is empty, and no %s.%s is configured to default it to",
				authn.ErrInvalidConfig, ConfigNamespace, audienceItem, gateNamespace, gatePublicURLItem)
		}
		r.audience = []string{r.issuer}
	}
	if r.issuer == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and an access token carries it as its issuer",
			authn.ErrInvalidConfig, ConfigNamespace, issuerItem)
	}

	switch r.algorithm {
	case algES256, algRS256:
	default:
		return resolved{}, fmt.Errorf("%w: %s.%s is not %s or %s", authn.ErrInvalidConfig,
			ConfigNamespace, algorithmItem, algES256, algRS256)
	}

	if r.clientID == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and a token carries it as its client identifier",
			authn.ErrInvalidConfig, ConfigNamespace, clientIDItem)
	}

	for _, f := range c.VerifyKeyFiles {
		if f = strings.TrimSpace(f); f != "" {
			r.verifyFiles = append(r.verifyFiles, f)
		}
	}
	return r, nil
}

// schema declares this module's input items. The signing key file is a secret
// and takes the ordinary origins.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			issuerItem: {
				Description: "the iss claim of an access token, the value every verifier compares exactly; " +
					"empty takes the address the deployment is reached at",
			},
			audienceItem: {
				Description: "the aud claim of an access token, a set a verifier needs one member of; " +
					"empty takes the issuer alone",
			},
			clientIDItem: {
				Description: "the client_id claim of a token issued for a session; a service account's token " +
					"carries the account identifier instead",
			},
			algorithmItem: {
				Description: "the signing algorithm of an access token: ES256 or RS256 from a key of that kind",
			},
			signingKeyFileItem: {
				Sensitive: true,
				Description: "the PEM file of the signing private key; empty leaves the module able to verify " +
					"but not to issue",
			},
			verifyKeyFilesItem: {
				Description: "PEM files of keys used only to verify and to publish, for the halves of a key rotation",
			},
		},
	}
}

// readConfig decodes this module's section and the gateway's public address and
// validates both. Prepare and New both go through it, so the two see one
// configuration.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	var gm gateMount
	if err := reader.Decode(gateNamespace+".mount", &gm); err != nil {
		return resolved{}, err
	}
	return cfg.resolve(gm)
}
