package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	encryptionPurpose = "speed/db/field-encryption"
	blindIndexPurpose = "speed/db/blind-index"
)

// minimumRootKeyBytes is the shortest current root key an assembly accepts: 112 bits.
const minimumRootKeyBytes = 112 / 8

const subkeyLength = 32

type keySet struct {
	aead       cipher.AEAD
	blindIndex []byte
}

// keyRing is the material one assembled database works under: only the current set seals, and the retired sets open only.
type keyRing struct {
	current keySet
	retired []keySet
}

func newKeyRing(namespace string, cfg Config) (*keyRing, error) {
	if cfg.EncryptionKey == "" {
		return nil, nil
	}
	if strings.TrimSpace(cfg.EncryptionKey) == "" {
		return nil, fmt.Errorf("db: %s.%s holds nothing but whitespace, which anyone can guess and "+
			"which seals nothing: set a real root key, or leave the item out",
			namespace, encryptionKeyKey)
	}
	if len(cfg.EncryptionKey) < minimumRootKeyBytes {
		return nil, fmt.Errorf("db: %s.%s is %d bytes long, and a root key shorter than %d bytes "+
			"(%d bits) can be found by trying every key of its length: set a longer one, and list "+
			"this one under %s so the rows it sealed stay readable",
			namespace, encryptionKeyKey, len(cfg.EncryptionKey), minimumRootKeyBytes,
			minimumRootKeyBytes*8, encryptionRetiredKeysKey)
	}
	current, err := deriveKeySet(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("db: %s.%s cannot be used as a root key: %w",
			namespace, encryptionKeyKey, err)
	}
	ring := &keyRing{current: current}
	for _, rootKey := range cfg.EncryptionRetiredKeys {
		if strings.TrimSpace(rootKey) == "" {
			return nil, fmt.Errorf("db: a key listed under %s.%s is empty or nothing but whitespace, "+
				"which is not a root key anything was sealed under: remove it from the list",
				namespace, encryptionRetiredKeysKey)
		}
		retired, err := deriveKeySet(rootKey)
		if err != nil {
			return nil, fmt.Errorf("db: a key listed under %s.%s cannot be used as a root key: %w",
				namespace, encryptionRetiredKeysKey, err)
		}
		ring.retired = append(ring.retired, retired)
	}
	return ring, nil
}

func digestFor(namespace string, cfg Config) (func(string) string, error) {
	keys, err := newKeyRing(namespace, cfg)
	if err != nil {
		return nil, err
	}
	if keys == nil {
		return nil, nil
	}
	return keys.blindIndex, nil
}

// deriveKeySet turns one root key into its two subkeys. Every replica must
// arrive at the same subkeys, so nothing here is random.
func deriveKeySet(rootKey string) (keySet, error) {
	encryption, err := deriveSubkey(rootKey, encryptionPurpose)
	if err != nil {
		return keySet{}, err
	}
	blindIndex, err := deriveSubkey(rootKey, blindIndexPurpose)
	if err != nil {
		return keySet{}, err
	}
	// Neither call can refuse a subkey of subkeyLength, but their errors travel
	// back so a change to that width fails here rather than on a statement.
	block, err := aes.NewCipher(encryption)
	if err != nil {
		return keySet{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return keySet{}, err
	}
	return keySet{aead: aead, blindIndex: blindIndex}, nil
}

func deriveSubkey(rootKey, purpose string) ([]byte, error) {
	return hkdf.Key(sha256.New, []byte(rootKey), nil, purpose, subkeyLength)
}

// encrypt returns what the column holds: the fresh nonce followed by the sealed
// value, base64 encoded so that it is text on every engine.
func (k *keyRing) encrypt(plaintext []byte) string {
	nonce := make([]byte, k.current.aead.NonceSize())
	// crypto/rand.Read fills the slice entirely, or crashes the process.
	rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(k.current.aead.Seal(nonce, nonce, plaintext, nil))
}

// decrypt opens a stored value, trying the current subkey and then each retired
// one: nothing in the column says which key sealed it.
func (k *keyRing) decrypt(stored string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		return nil, fmt.Errorf("the column does not hold the text this module writes into it: %w", err)
	}
	for _, set := range k.sets() {
		if plaintext, err := set.open(raw); err == nil {
			return plaintext, nil
		}
	}
	return nil, errors.New("no configured root key opens this value: it was sealed under a key " +
		"that is neither the current one nor one of the retired ones")
}

func (k *keyRing) sets() []keySet {
	sets := make([]keySet, 0, 1+len(k.retired))
	sets = append(sets, k.current)
	return append(sets, k.retired...)
}

// A value too short to hold a nonce is refused rather than sliced: the column
// is unauthenticated and can hold anything.
func (s keySet) open(raw []byte) ([]byte, error) {
	if len(raw) < s.aead.NonceSize() {
		return nil, errors.New("the value is shorter than a nonce, so it cannot be a sealed one")
	}
	return s.aead.Open(nil, raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():], nil)
}

// blindIndex returns the digest of the plaintext as given, under the current
// subkey only: a retired key cannot help, and a rotation leaves stored digests
// stale until the caller rewrites those rows.
func (k *keyRing) blindIndex(plaintext string) string {
	digest := hmac.New(sha256.New, k.current.blindIndex)
	digest.Write([]byte(plaintext))
	return hex.EncodeToString(digest.Sum(nil))
}
