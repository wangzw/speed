package invite

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
)

// tokenPrefix starts every acceptance token, which is the prefix and 256 random
// bits in base64url. The prefix is what a log redactor recognises the token by;
// it is host-visible surface and renaming it is a breaking change.
const tokenPrefix = "spd_inv_"

// SourceInvite is the value written into Membership.Source for every membership
// row this module writes. It is what tells a subscriber apart an invitation
// join from an administrator's direct add.
const SourceInvite = "invite"

// newID is a fresh UUIDv7 text, which any replica can mint without asking and
// which sorts close to the order of creation.
func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("%w: minting an identifier: %w", ErrInvite, err)
	}
	return id.String(), nil
}

// newToken draws a fresh acceptance token: 256 random bits under the prefix, in
// base64url.
func newToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("%w: drawing a token: %w", ErrInvite, err)
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// tokenDigest is what a token is found by: the SHA-256 of the token as it is
// presented, as hex. The plaintext never reaches the table.
func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
