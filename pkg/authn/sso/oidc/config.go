package oidc

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
)

// The configuration keys this module reads: its own enable switch, the gateway's
// public address and base path, from which the redirect target is built, and the
// SSO module's egress items, which the restricted egress client is built with.
// They are read at construction, before any provider is reached.
const (
	enabledItem       = "enabled"
	gateNamespace     = "authn.gate"
	gateMountPath     = "mount"
	gatePublicURLItem = "mount.public-url"

	ssoNamespace   = "authn.sso"
	oidcCAFileItem = "oidc-ca-file"
)

// moduleConfig is this module's own section: the switch that states whether the
// run offers enterprise OIDC sign-in. The providers themselves are rows in the
// enterprise register, so no credential of theirs lives here.
type moduleConfig struct {
	// Enabled states whether this run offers the method. The method has nothing
	// of its own to infer that from: its providers are created at run time, so
	// an empty register is not evidence of a deployment that never intended to
	// offer enterprise sign-in.
	Enabled bool `config:"enabled"`
}

// configDefaults is what this module is worth without a configuration file:
// off. A deployment turns enterprise OIDC sign-in on by naming it.
var configDefaults = moduleConfig{}

// schema declares this module's one input item.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			enabledItem: {
				Description: "whether this run offers enterprise OIDC sign-in; off, the method is " +
					"not delivered and the sign-in surface offers no enterprise entry",
			},
		},
	}
}

// readEnabled decodes this module's enable switch.
func readEnabled(reader config.Reader) (bool, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return false, err
	}
	return cfg.Enabled, nil
}

// ssoConfig is the part of the SSO module's section this module reads.
type ssoConfig struct {
	AllowPrivateEgress bool   `config:"allow-private-egress"`
	OIDCCAFile         string `config:"oidc-ca-file"`
}

// gateMount is the part of the gateway's configuration this module reads.
type gateMount struct {
	PublicURL string `config:"public-url"`
	Bases     struct {
		Default string `config:"default"`
	} `config:"bases"`
}

// resolved is the configuration the method runs on.
type resolved struct {
	redirect           redirectBase
	allowPrivateEgress bool
	// rootCAs carries the system roots plus the extra roots the deployment
	// named. Nil means the system roots alone.
	rootCAs *x509.CertPool
}

// redirectBase builds the redirect target of a provider: the gateway's public
// address, the base path the routes are mounted under and the provider's own
// callback path.
type redirectBase struct {
	publicURL string
	basePath  string
}

// callback is the redirect URI of one provider, which the user agent comes back
// to. Each provider's path is different, so a redirect aimed at one provider
// cannot be presented to another.
func (r redirectBase) callback(id string) string {
	return strings.TrimRight(r.publicURL, "/") + "/" + strings.Trim(r.basePath, "/") + "/callback/" + id
}

// readConfig decodes the gateway's mount items and the SSO module's egress
// items, validates the public address the redirect target is built from, and
// loads the extra roots the egress client trusts when the deployment named one.
func readConfig(reader config.Reader) (resolved, error) {
	var gm gateMount
	if err := reader.Decode(gateNamespace+"."+gateMountPath, &gm); err != nil {
		return resolved{}, err
	}
	var sso ssoConfig
	if err := reader.Decode(ssoNamespace, &sso); err != nil {
		return resolved{}, err
	}
	publicURL := strings.TrimSpace(gm.PublicURL)
	if publicURL == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s is empty, and the redirect target is built from it",
			authn.ErrInvalidConfig, gateNamespace, gatePublicURLItem)
	}
	if _, err := mount.Origin(publicURL); err != nil {
		return resolved{}, fmt.Errorf("%w: %s.%s is not an origin: %w",
			authn.ErrInvalidConfig, gateNamespace, gatePublicURLItem, err)
	}
	base := strings.TrimSpace(gm.Bases.Default)
	if base == "" {
		base = mount.DefaultBasePath
	}
	roots, err := trustRoots(sso.OIDCCAFile)
	if err != nil {
		return resolved{}, err
	}
	return resolved{
		redirect:           redirectBase{publicURL: publicURL, basePath: base},
		allowPrivateEgress: sso.AllowPrivateEgress,
		rootCAs:            roots,
	}, nil
}

// trustRoots returns the system roots with the certificates of the named PEM
// file added. An empty path returns nil, which leaves the egress client on the
// system roots alone. A file the deployment named has to be readable and to hold
// at least one certificate: falling back to the system roots would silently make
// the deployment's trust decision a no-op.
func trustRoots(path string) (*x509.CertPool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("%w: %s cannot be read: %w", authn.ErrInvalidConfig, oidcCAFileItem, err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("%w: the system roots cannot be read, so %s cannot extend them: %w",
			authn.ErrInvalidConfig, oidcCAFileItem, err)
	}
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("%w: %s holds no PEM certificate", authn.ErrInvalidConfig, oidcCAFileItem)
	}
	return pool, nil
}

// providerConfig is the shape of an OIDC provider's stored configuration. It is
// validated when the provider is written; the method decodes it to run on it and
// refuses anything it cannot read as an outage.
type providerConfig struct {
	Issuer       string            `json:"issuer"`
	ClientID     string            `json:"client_id"`
	Scopes       []string          `json:"scopes"`
	ALGs         []string          `json:"algs"`
	Claims       map[string]string `json:"claims"`
	MFAACRValues []string          `json:"mfa_acr_values"`
}

// decodeProviderConfig reads a provider's configuration. An unknown key, a
// missing issuer or client identifier is a configuration the method cannot run
// on.
func decodeProviderConfig(raw []byte) (providerConfig, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var cfg providerConfig
	if err := dec.Decode(&cfg); err != nil {
		return providerConfig{}, fmt.Errorf("the provider's configuration cannot be read: %w", err)
	}
	if strings.TrimSpace(cfg.Issuer) == "" {
		return providerConfig{}, fmt.Errorf("the provider's configuration names no issuer")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return providerConfig{}, fmt.Errorf("the provider's configuration names no client identifier")
	}
	return cfg, nil
}

// The scopes and signature algorithms the method uses when the provider's
// configuration names none. The OpenID scope is required for the identity token
// and the other two carry the address and its flag and the display name; the two
// algorithms are the asymmetric ones an identity token may be signed with.
var (
	defaultScopes = []string{"openid", "email", "profile"}
	defaultALGs   = []string{"RS256", "ES256"}
)

// scopesOf is the authorization scopes of a provider.
func scopesOf(cfg providerConfig) []string {
	if len(cfg.Scopes) > 0 {
		return cfg.Scopes
	}
	return defaultScopes
}

// algsOf is the signature algorithms an identity token of a provider may be
// signed with; a token signed with any other algorithm is refused by the
// verifier.
func algsOf(cfg providerConfig) []string {
	if len(cfg.ALGs) > 0 {
		return cfg.ALGs
	}
	return defaultALGs
}
