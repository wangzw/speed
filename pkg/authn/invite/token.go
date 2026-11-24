package invite

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
)

// tokenPrefix starts every acceptance token. A log redactor recognises the token
// by it, and it is host-visible surface: renaming it is a breaking change.
const tokenPrefix = "spd_inv_"

// SourceInvite is the value written into Membership.Source for every membership
// row this module creates, distinguishing an invitation join from a direct add.
const SourceInvite = "invite"

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("%w: minting an identifier: %w", ErrInvite, err)
	}
	return id.String(), nil
}

func newToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("%w: drawing a token: %w", ErrInvite, err)
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// tokenDigest is what a token is found by: the SHA-256 of the presented token, as
// hex. The plaintext never reaches the table.
func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
