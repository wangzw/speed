package db

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

// The root keys these cases derive from. They are values rather than secrets: what is
// observed is that the material is a function of them, and of nothing else.
const (
	firstRootKey  = "the-root-key-of-the-cases"
	secondRootKey = "another-root-key"
)

// ringOver derives the material one section configures, and fails the case if
// it cannot be derived at all.
func ringOver(t *testing.T, cfg Config) *keyRing {
	t.Helper()
	ring, err := newKeyRing("db.crypto", cfg)
	if err != nil {
		t.Fatalf("deriving the material of %+v: %v", cfg, err)
	}
	return ring
}

// TestBlindIndexSubkeyDiffersFromEncryptionSubkey pins that one root key yields two unrelated
// subkeys.
func TestBlindIndexSubkeyDiffersFromEncryptionSubkey(t *testing.T) {
	encryption, err := deriveSubkey(firstRootKey, encryptionPurpose)
	if err != nil {
		t.Fatalf("deriving the encryption subkey: %v", err)
	}
	blindIndex, err := deriveSubkey(firstRootKey, blindIndexPurpose)
	if err != nil {
		t.Fatalf("deriving the blind index subkey: %v", err)
	}
	if bytes.Equal(encryption, blindIndex) {
		t.Error("the two purposes derive one subkey, so a digest and a ciphertext of the same plaintext " +
			"are computed under the same key")
	}
	if len(encryption) != subkeyLength || len(blindIndex) != subkeyLength {
		t.Errorf("the subkeys are %d and %d bytes, want %d each: a narrower AES key is a different "+
			"cipher and a narrower HMAC key is a weaker one",
			len(encryption), len(blindIndex), subkeyLength)
	}
	// The digest is not the one the encryption subkey would give, which is what
	// a swapped pair of purposes looks like from the outside.
	swapped := digestUnder(encryption, "user@example.com")
	if got := ringOver(t, Config{EncryptionKey: firstRootKey}).blindIndex("user@example.com"); got == swapped {
		t.Error("the digest is computed under the encryption subkey, so one key answers both purposes " +
			"however the derivation names them")
	}
}

// TestTheSameRootKeyAlwaysDerivesTheSameSubkeys pins what makes a configured root key usable across
// a deployment.
func TestTheSameRootKeyAlwaysDerivesTheSameSubkeys(t *testing.T) {
	first := ringOver(t, Config{EncryptionKey: firstRootKey})
	same := ringOver(t, Config{EncryptionKey: firstRootKey})
	other := ringOver(t, Config{EncryptionKey: secondRootKey})

	sealed := first.encrypt([]byte("user@example.com"))
	plaintext, err := same.decrypt(sealed)
	if err != nil {
		t.Fatalf("a second derivation of the same root key does not open what the first sealed: %v", err)
	}
	if string(plaintext) != "user@example.com" {
		t.Errorf("the second derivation read %q, want the plaintext that was sealed", plaintext)
	}
	if _, err := other.decrypt(sealed); err == nil {
		t.Error("another root key opens what this one sealed, so the material does not depend on the " +
			"root key at all")
	}
	// A second seal of the same plaintext is a different column, which is what
	// makes the encrypted column unusable for an equality query.
	again := first.encrypt([]byte("user@example.com"))
	if again == sealed {
		t.Error("one plaintext sealed to one value twice, so a ciphertext identifies its plaintext")
	}
}

// TestDecryptionRefusesWhatNoKeyOpens pins the three shapes a column can take without a key to open
// it.
func TestDecryptionRefusesWhatNoKeyOpens(t *testing.T) {
	ring := ringOver(t, Config{EncryptionKey: firstRootKey})
	foreign := ringOver(t, Config{EncryptionKey: secondRootKey})

	for _, c := range []struct {
		name   string
		stored string
	}{
		{"a value sealed under a key nobody configured", foreign.encrypt([]byte("user@example.com"))},
		{"a value too short to hold a nonce", base64.StdEncoding.EncodeToString([]byte("half-a-nonce"))},
		{"a value that is not the text this module writes", "not base64, and not a ciphertext either"},
	} {
		t.Run(c.name, func(t *testing.T) {
			plaintext, err := ring.decrypt(c.stored)
			if err == nil {
				t.Fatalf("decrypting %s returned %q, want a refusal", c.name, plaintext)
			}
			if plaintext != nil {
				t.Errorf("decryption returned %q beside its error, want no plaintext", plaintext)
			}
		})
	}
}

func digestUnder(subkey []byte, plaintext string) string {
	digest := hmac.New(sha256.New, subkey)
	digest.Write([]byte(plaintext))
	return hex.EncodeToString(digest.Sum(nil))
}
