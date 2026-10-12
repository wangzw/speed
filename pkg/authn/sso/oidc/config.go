package oidc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
)

// The configuration keys this module reads: the gateway's public address and
// base path, from which the redirect target is built, and the SSO module's
// developer relaxation, which the restricted egress client is built with. They
// are read at construction, before any provider is reached.
const (
	gateNamespace     = "authn.gate"
	gateMountPath     = "mount"
	gatePublicURLItem = "mount.public-url"

	ssoNamespace = "authn.sso"
)

// moduleConfig is the part of the SSO module's section this module reads.
type moduleConfig struct {
	AllowPrivateEgress bool `config:"allow-private-egress"`
}

// gateMount is the part of the gateway's configuration this module reads.
type gateMount struct {
	PublicURL string `config:"public-url"`
	BasePath  string `config:"base-path"`
}

// resolved is the configuration the method runs on.
type resolved struct {
	redirect           redirectBase
	allowPrivateEgress bool
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

// readConfig decodes the gateway's mount items and the SSO module's relaxation,
// and validates the public address the redirect target is built from.
func readConfig(reader config.Reader) (resolved, error) {
	var gm gateMount
	if err := reader.Decode(gateNamespace+"."+gateMountPath, &gm); err != nil {
		return resolved{}, err
	}
	var sso moduleConfig
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
	base := strings.TrimSpace(gm.BasePath)
	if base == "" {
		base = mount.DefaultBasePath
	}
	return resolved{
		redirect:           redirectBase{publicURL: publicURL, basePath: base},
		allowPrivateEgress: sso.AllowPrivateEgress,
	}, nil
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
