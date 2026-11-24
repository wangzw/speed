package saml

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

const (
	enabledItem        = "enabled"
	spKeyFileItem      = "sp-key-file"
	spCertFileItem     = "sp-cert-file"
	spNextCertFileItem = "sp-next-cert-file"
)

type moduleConfig struct {
	Enabled        bool   `config:"enabled"`
	SPKeyFile      string `config:"sp-key-file"`
	SPCertFile     string `config:"sp-cert-file"`
	SPNextCertFile string `config:"sp-next-cert-file"`
}

type resolved struct {
	signer  crypto.Signer
	certDER []byte
	nextDER []byte
}

var configDefaults = moduleConfig{}

func (c moduleConfig) resolve() (resolved, error) {
	keyGiven := c.SPKeyFile != ""
	certGiven := c.SPCertFile != ""
	if keyGiven != certGiven {
		return resolved{}, fmt.Errorf("%w: %s and %s are a pair, and one without the other leaves the "+
			"service provider unable to sign or to publish a certificate", authn.ErrInvalidConfig,
			spKeyFileItem, spCertFileItem)
	}
	if !keyGiven {
		if c.SPNextCertFile != "" {
			return resolved{}, fmt.Errorf("%w: %s is set with no current certificate", authn.ErrInvalidConfig,
				spNextCertFileItem)
		}
		return resolved{}, nil
	}
	signer, err := loadKey(c.SPKeyFile)
	if err != nil {
		return resolved{}, err
	}
	cert, err := loadCertificate(c.SPCertFile)
	if err != nil {
		return resolved{}, err
	}
	out := resolved{signer: signer, certDER: cert}
	if c.SPNextCertFile != "" {
		next, err := loadCertificate(c.SPNextCertFile)
		if err != nil {
			return resolved{}, err
		}
		out.nextDER = next
	}
	return out, nil
}

func (r resolved) configured() bool { return r.signer != nil }

func configSchema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			enabledItem: {
				Description: "whether this run offers enterprise SAML sign-in; off, the method and " +
					"the metadata refresher are not delivered, the metadata route is not mounted, and " +
					"the sign-in surface offers no SAML entry",
			},
			spKeyFileItem: {
				Sensitive: true,
				Description: "the path of the service provider's PEM private key, shared by the whole " +
					"deployment; it is needed only by a provider that signs its AuthnRequests or an IdP " +
					"that encrypts assertions",
			},
			spCertFileItem: {
				Description: "the path of the service provider's current PEM certificate, published to a " +
					"provider's metadata",
			},
			spNextCertFileItem: {
				Description: "the path of the next service provider certificate, published beside the " +
					"current one while a rotation is in progress",
			},
		},
	}
}

func readEnabled(reader config.Reader) (bool, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return false, fmt.Errorf("%s%s: reading the enable switch: %w", diagnosticPrefix, ModuleName, err)
	}
	return cfg.Enabled, nil
}

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, fmt.Errorf("%s%s: reading the service provider's key configuration: %w",
			diagnosticPrefix, ModuleName, err)
	}
	return cfg.resolve()
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the service provider's key comes from configuration, and no reader was "+
			"handed to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

func loadKey(path string) (crypto.Signer, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("%w: reading %s: %w", authn.ErrInvalidConfig, spKeyFileItem, err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%w: %s is not a PEM file", authn.ErrInvalidConfig, spKeyFileItem)
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return signerOf(k)
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, fmt.Errorf("%w: %s is not a PKCS#8, PKCS#1 or SEC 1 private key", authn.ErrInvalidConfig, spKeyFileItem)
}

func signerOf(k any) (crypto.Signer, error) {
	switch key := k.(type) {
	case *rsa.PrivateKey:
		return key, nil
	case *ecdsa.PrivateKey:
		return key, nil
	case ed25519.PrivateKey:
		return key, nil
	default:
		return nil, fmt.Errorf("%w: %s is not an RSA, ECDSA or Ed25519 key", authn.ErrInvalidConfig, spKeyFileItem)
	}
}

func loadCertificate(path string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("%w: reading a service provider certificate: %w", authn.ErrInvalidConfig, err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%w: a service provider certificate file is not a PEM certificate", authn.ErrInvalidConfig)
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return nil, fmt.Errorf("%w: a service provider certificate does not parse: %w", authn.ErrInvalidConfig, err)
	}
	return block.Bytes, nil
}
