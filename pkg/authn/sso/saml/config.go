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

// The configuration items this module declares, relative to its namespace. The
// service provider's key and certificates belong to the whole deployment, not
// to one provider; only the private key is secret. The key file is the one item
// the register reads across namespaces, to refuse a provider whose requests
// could not be signed.
const (
	spKeyFileItem      = "sp-key-file"
	spCertFileItem     = "sp-cert-file"
	spNextCertFileItem = "sp-next-cert-file"
)

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// SPKeyFile is the path of the service provider's private key. It is read
	// only when a provider signs its requests or an IdP encrypts assertions.
	SPKeyFile string `config:"sp-key-file"`
	// SPCertFile is the path of the current service provider certificate.
	SPCertFile string `config:"sp-cert-file"`
	// SPNextCertFile is the path of the next service provider certificate,
	// published beside the current one while a rotation is in progress.
	SPNextCertFile string `config:"sp-next-cert-file"`
}

// resolved is the configuration this module actually runs on: the parsed signer
// and the DER bytes of the current and next certificates. A nil signer is a
// deployment that carries no key.
type resolved struct {
	signer  crypto.Signer
	certDER []byte
	nextDER []byte
}

// configDefaults is what this module is worth without a configuration file: no
// key and no certificate, a legal deployment that never signs its requests.
var configDefaults = moduleConfig{}

// resolve loads the declared key and certificates and holds them to the rules
// the module can run on: the key and the current certificate are a pair, and a
// file that is declared is one that parses.
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

// configured reports whether the deployment carries a service provider key.
func (r resolved) configured() bool { return r.signer != nil }

// configSchema declares this module's input items under its own namespace.
func configSchema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
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

// readConfig decodes this module's section and validates it.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, fmt.Errorf("%s%s: reading the service provider's key configuration: %w",
			diagnosticPrefix, ModuleName, err)
	}
	return cfg.resolve()
}

// readerOf takes up the configuration reader, whose absence is the host's
// omission and is carried out as it stands.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the service provider's key comes from configuration, and no reader was "+
			"handed to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

// loadKey reads and parses a PEM private key. It accepts the PKCS#8, PKCS#1 and
// SEC 1 encodings, which are what `openssl` and the Go standard library write.
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

// signerOf reports an RSA, ECDSA or Ed25519 key as a signer.
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

// loadCertificate reads a PEM certificate and returns its DER bytes, which is
// what the metadata publishes.
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
