package token

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vislake/speed/pkg/authn"
)

// The signing algorithms the module supports, as their JOSE names. They are the
// closed set the "algorithm" item may take and the algorithms a configured key
// may carry; the symmetric algorithms and "none" are not among them.
const (
	algES256 = "ES256"
	algRS256 = "RS256"
)

// useSig is the key-use the published keys carry: signature.
const useSig = "sig"

// The sizes of a generated key. An ES256 key is a P-256 pair; an RS256 key is
// an RSA pair of the least size the startup check accepts.
const (
	// rsaBits is the modulus size of a generated RSA key. The startup check
	// refuses a configured key below minRSABits.
	rsaBits = 2048
	// minRSABits is the least RSA modulus a configured key may have.
	minRSABits = 2048
)

// keyEntry is one key of the set: its identifier, its algorithm, its public half
// for verification and publication, and, for the signing key alone, the private
// half as a signer. file names where it was read from, for the errors and the
// diagnostics that point a reader at it.
type keyEntry struct {
	kid    string
	alg    string
	pub    crypto.PublicKey
	signer crypto.Signer // nil for a key that only verifies
	file   string
}

// keySet is the keyset loaded at startup and never changed afterwards. entries
// holds every key, signing the one that signs (nil when none is configured), and
// algorithms the allow-list the verifying backend parses a token with.
type keySet struct {
	entries    []keyEntry
	signing    *keyEntry
	algorithms []string
}

// loadKeySet reads the configured files into a keyset, validating the closed set
// of startup conditions. A failure is ErrInvalidKey naming the file; the
// diagnostics collected here are the cautions written once at startup: a private
// key file that other users can read. The signing key file, when given, is the
// one key that signs; the verification files are read for their public halves.
func loadKeySet(cfg resolved) (*keySet, []diagnostic, error) {
	var diags []diagnostic
	var entries []keyEntry
	seen := make(map[string]string)
	var signingKID string

	add := func(e keyEntry, signing bool) error {
		if prev, ok := seen[e.kid]; ok {
			return fmt.Errorf("%w: %s holds a key whose thumbprint %s is already the thumbprint of the key in %s",
				authn.ErrInvalidKey, e.file, e.kid, prev)
		}
		seen[e.kid] = e.file
		entries = append(entries, e)
		if signing {
			signingKID = e.kid
		}
		return nil
	}

	if cfg.signingFile != "" {
		e, diag, err := loadKeyFile(cfg.signingFile, cfg.algorithm, true)
		if err != nil {
			return nil, nil, err
		}
		if diag != nil {
			diags = append(diags, *diag)
		}
		if err := add(e, true); err != nil {
			return nil, nil, err
		}
	}
	for _, file := range cfg.verifyFiles {
		e, diag, err := loadKeyFile(file, cfg.algorithm, false)
		if err != nil {
			return nil, nil, err
		}
		if diag != nil {
			diags = append(diags, *diag)
		}
		if err := add(e, false); err != nil {
			return nil, nil, err
		}
	}
	if len(entries) == 0 {
		return nil, nil, fmt.Errorf("%w: no %s.%s and no %s.%s is configured, so there is no key to verify with",
			authn.ErrInvalidKey, ConfigNamespace, signingKeyFileItem, ConfigNamespace, verifyKeyFilesItem)
	}

	set := &keySet{entries: entries, algorithms: []string{cfg.algorithm}}
	for i := range entries {
		if entries[i].kid == signingKID {
			set.signing = &entries[i]
			break
		}
	}
	return set, diags, nil
}

// loadKeyFile reads one key file and validates it against the configured
// algorithm. mustSign is true for the signing key file, which has to hold a
// private key. The second return is the caution about the file's permissions,
// present only for a file that holds a private key other users can read.
func loadKeyFile(path, algorithm string, mustSign bool) (keyEntry, *diagnostic, error) {
	// The path is a configuration value and is cleaned before it is opened, so
	// the traversal gosec's G304 warns about cannot be written into it.
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return keyEntry{}, nil, fmt.Errorf("%w: reading %s: %w", authn.ErrInvalidKey, path, err)
	}
	priv, pub, err := parseKeyPEM(data)
	if err != nil {
		return keyEntry{}, nil, fmt.Errorf("%w: %s: %w", authn.ErrInvalidKey, path, err)
	}
	if mustSign && priv == nil {
		return keyEntry{}, nil, fmt.Errorf("%w: %s holds a public key and cannot sign", authn.ErrInvalidKey, path)
	}
	alg, err := keyAlgorithm(pub)
	if err != nil {
		return keyEntry{}, nil, fmt.Errorf("%w: %s: %w", authn.ErrInvalidKey, path, err)
	}
	if alg != algorithm {
		return keyEntry{}, nil, fmt.Errorf("%w: %s holds a %s key, and %s.%s is %s",
			authn.ErrInvalidKey, path, alg, ConfigNamespace, algorithmItem, algorithm)
	}
	kid, err := keyID(pub)
	if err != nil {
		return keyEntry{}, nil, fmt.Errorf("%w: %s: %w", authn.ErrInvalidKey, path, err)
	}
	var signer crypto.Signer
	if priv != nil {
		s, ok := priv.(crypto.Signer)
		if !ok {
			return keyEntry{}, nil, fmt.Errorf("%w: %s holds a private key that cannot sign", authn.ErrInvalidKey, path)
		}
		signer = s
	}
	var diag *diagnostic
	if signer != nil && worldReadable(path) {
		diag = &diagnostic{file: path, reason: "the file holds a private key and is readable by other users"}
	}
	return keyEntry{kid: kid, alg: alg, pub: pub, signer: signer, file: path}, diag, nil
}

// keyAlgorithm is the algorithm a public key carries: ES256 for a P-256 ECDSA
// key, RS256 for an RSA key of at least minRSABits. Any other key is refused.
func keyAlgorithm(pub crypto.PublicKey) (string, error) {
	switch key := pub.(type) {
	case *ecdsa.PublicKey:
		if key.Curve != elliptic.P256() {
			return "", fmt.Errorf("the ECDSA key is on curve %s, and only P-256 (ES256) is supported",
				key.Curve.Params().Name)
		}
		return algES256, nil
	case *rsa.PublicKey:
		if bits := key.N.BitLen(); bits < minRSABits {
			return "", fmt.Errorf("the RSA key is %d bits, and %d is the least", bits, minRSABits)
		}
		return algRS256, nil
	default:
		return "", fmt.Errorf("the key is a %T, which is neither an ECDSA P-256 key nor an RSA key", pub)
	}
}

// parseKeyPEM reads the first PEM block of data as a private key, a public key or
// a certificate. A private key is returned both as it is and as its public half.
func parseKeyPEM(data []byte) (crypto.PrivateKey, crypto.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, nil, fmt.Errorf("no PEM block in the file")
	}
	der := block.Bytes
	if priv, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		signer, ok := priv.(crypto.Signer)
		if !ok {
			return nil, nil, fmt.Errorf("the PKCS#8 key is a %T, which cannot sign", priv)
		}
		return priv, signer.Public(), nil
	}
	if priv, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return priv, &priv.PublicKey, nil
	}
	if priv, err := x509.ParseECPrivateKey(der); err == nil {
		return priv, &priv.PublicKey, nil
	}
	if pub, err := x509.ParsePKIXPublicKey(der); err == nil {
		return nil, pub, nil
	}
	if pub, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return nil, pub, nil
	}
	if cert, err := x509.ParseCertificate(der); err == nil {
		return nil, cert.PublicKey, nil
	}
	return nil, nil, fmt.Errorf("the PEM block is not a private key, a public key or a certificate")
}

// worldReadable reports whether a file is readable by any user other than its
// owner, which is only worth a caution for a file that holds a private key.
func worldReadable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o044 != 0
}

// GenerateKey makes a signing key of the given algorithm and returns it as a
// PKCS#8 PEM, the form the signing-key-file and the verification-key-files read.
// It is for a host's command that mints a key and for tests; the module itself
// never generates or rotates a key.
func GenerateKey(alg string) ([]byte, error) {
	var key crypto.Signer
	switch alg {
	case algES256:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("%s: generating an %s key: %w", ModuleName, alg, err)
		}
		key = k
	case algRS256:
		k, err := rsa.GenerateKey(rand.Reader, rsaBits)
		if err != nil {
			return nil, fmt.Errorf("%s: generating an %s key: %w", ModuleName, alg, err)
		}
		key = k
	default:
		return nil, fmt.Errorf("%w: GenerateKey takes %s or %s, not %q", authn.ErrInvalidKey, algES256, algRS256, alg)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("%s: marshalling the generated %s key: %w", ModuleName, alg, err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}
