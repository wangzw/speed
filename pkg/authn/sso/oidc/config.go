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

const (
	enabledItem       = "enabled"
	gateNamespace     = "authn.gate"
	gateMountPath     = "mount"
	gatePublicURLItem = "mount.public-url"

	ssoNamespace   = "authn.sso"
	oidcCAFileItem = "oidc-ca-file"
)

type moduleConfig struct {
	Enabled bool `config:"enabled"`
}

var configDefaults = moduleConfig{}

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

func readEnabled(reader config.Reader) (bool, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return false, err
	}
	return cfg.Enabled, nil
}

type ssoConfig struct {
	AllowPrivateEgress bool   `config:"allow-private-egress"`
	OIDCCAFile         string `config:"oidc-ca-file"`
}

type gateMount struct {
	PublicURL string `config:"public-url"`
	Bases     struct {
		Default string `config:"default"`
	} `config:"bases"`
}

type resolved struct {
	redirect           redirectBase
	allowPrivateEgress bool
	rootCAs            *x509.CertPool
}

type redirectBase struct {
	publicURL string
	basePath  string
}

func (r redirectBase) callback(id string) string {
	return strings.TrimRight(r.publicURL, "/") + "/" + strings.Trim(r.basePath, "/") + "/callback/" + id
}

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

type providerConfig struct {
	Issuer       string            `json:"issuer"`
	ClientID     string            `json:"client_id"`
	Scopes       []string          `json:"scopes"`
	ALGs         []string          `json:"algs"`
	Claims       map[string]string `json:"claims"`
	MFAACRValues []string          `json:"mfa_acr_values"`
}

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

var (
	defaultScopes = []string{"openid", "email", "profile"}
	defaultALGs   = []string{"RS256", "ES256"}
)

func scopesOf(cfg providerConfig) []string {
	if len(cfg.Scopes) > 0 {
		return cfg.Scopes
	}
	return defaultScopes
}

func algsOf(cfg providerConfig) []string {
	if len(cfg.ALGs) > 0 {
		return cfg.ALGs
	}
	return defaultALGs
}
