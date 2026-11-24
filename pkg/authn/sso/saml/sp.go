package saml

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"sync"
	"time"

	saml2 "github.com/russellhaering/gosaml2"
	dsig "github.com/russellhaering/goxmldsig"

	"github.com/vislake/speed/pkg/authn"
)

type providerConfig struct {
	IDPEntityID      string            `json:"idp_entity_id"`
	IDPSSOURL        string            `json:"idp_sso_url"`
	IDPCerts         []string          `json:"idp_certs"`
	MetadataURL      string            `json:"metadata_url"`
	NameIDFormat     string            `json:"name_id_format"`
	SubjectAttribute string            `json:"subject_attribute"`
	SignRequests     bool              `json:"sign_requests"`
	Claims           map[string]string `json:"claims"`
	MFAACRValues     []string          `json:"mfa_acr_values"`
}

func parseProviderConfig(raw json.RawMessage) (providerConfig, error) {
	var cfg providerConfig
	if len(raw) == 0 {
		return providerConfig{}, fmt.Errorf("%w: the provider carries no config", authn.ErrInvalidConfig)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return providerConfig{}, fmt.Errorf("%w: the provider's config does not decode: %w", authn.ErrInvalidConfig, err)
	}
	return cfg, nil
}

type built struct {
	sp      *saml2.SAMLServiceProvider
	config  providerConfig
	version int
	at      time.Time
}

type spCache struct {
	mu sync.Mutex
	m  map[string]built
}

func newSPCache() *spCache { return &spCache{m: make(map[string]built)} }

func (c *spCache) get(row authn.SSOProvider, ttl time.Duration, now time.Time, build func() (built, error)) (built, error) {
	c.mu.Lock()
	entry, ok := c.m[row.ID]
	c.mu.Unlock()
	if ok && entry.version == row.Version && now.Sub(entry.at) < ttl {
		return entry, nil
	}
	fresh, err := build()
	if err != nil {
		return built{}, err
	}
	fresh.at = now
	c.mu.Lock()
	c.m[row.ID] = fresh
	c.mu.Unlock()
	return fresh, nil
}

func (c *spCache) drop(id string) {
	c.mu.Lock()
	delete(c.m, id)
	c.mu.Unlock()
}

func (c *spCache) flush() {
	c.mu.Lock()
	c.m = make(map[string]built)
	c.mu.Unlock()
}

func (p *product) buildServiceProvider(row authn.SSOProvider) (built, error) {
	cfg, err := parseProviderConfig(row.Config)
	if err != nil {
		return built{}, err
	}
	if strings.TrimSpace(cfg.IDPEntityID) == "" {
		return built{}, fmt.Errorf("%w: idp_entity_id is empty", authn.ErrInvalidConfig)
	}
	if strings.TrimSpace(cfg.IDPSSOURL) == "" {
		return built{}, fmt.Errorf("%w: idp_sso_url is empty", authn.ErrInvalidConfig)
	}
	if len(cfg.IDPCerts) == 0 {
		return built{}, fmt.Errorf("%w: idp_certs is empty", authn.ErrInvalidConfig)
	}
	if cfg.SignRequests && !p.sp.configured() {
		return built{}, fmt.Errorf("%w: sign_requests is on and no service provider key is configured",
			authn.ErrInvalidConfig)
	}
	store, err := certificateStore(cfg.IDPCerts)
	if err != nil {
		return built{}, err
	}
	acs := p.acsURL(row.ID)
	entity := p.entityID(row.ID)
	sp := &saml2.SAMLServiceProvider{
		IdentityProviderIssuer:      cfg.IDPEntityID,
		IdentityProviderSSOURL:      cfg.IDPSSOURL,
		IdentityProviderSSOBinding:  saml2.BindingHttpRedirect,
		AssertionConsumerServiceURL: acs,
		ServiceProviderIssuer:       entity,
		AudienceURI:                 entity,
		IDPCertificateStore:         store,
		SignAuthnRequests:           cfg.SignRequests,
		SkipSignatureValidation:     false,
		MaximumDecompressedBodySize: maxDecompressedBody,
		MaximumXMLTokens:            maxXMLTokens,
		NameIdFormat:                nameIDURN(cfg.NameIDFormat),
	}
	if p.sp.configured() {
		if err := sp.SetSPKeyStore(&saml2.KeyStore{Signer: p.sp.signer, Cert: p.sp.certDER}); err != nil {
			return built{}, fmt.Errorf("%w: building the service provider's key store: %w", authn.ErrInvalidConfig, err)
		}
	}
	return built{sp: sp, config: cfg, version: row.Version}, nil
}

const (
	maxDecompressedBody = 1 << 20
	maxXMLTokens        = 50000
)

func certificateStore(pems []string) (*dsig.MemoryX509CertificateStore, error) {
	roots := make([]*x509.Certificate, 0, len(pems))
	for _, text := range pems {
		block, _ := pem.Decode([]byte(text))
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("%w: an idp_certs entry is not a PEM certificate", authn.ErrInvalidConfig)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: an idp_certs entry does not parse: %w", authn.ErrInvalidConfig, err)
		}
		roots = append(roots, cert)
	}
	return &dsig.MemoryX509CertificateStore{Roots: roots}, nil
}

func nameIDURN(format string) string {
	switch format {
	case "unspecified":
		return saml2.NameIdFormatUnspecified
	case "emailAddress":
		return saml2.NameIdFormatEmailAddress
	default:
		return saml2.NameIdFormatPersistent
	}
}
