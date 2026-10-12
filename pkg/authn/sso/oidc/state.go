package oidc

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
)

// stateVersion is the version of the state kept between the step that answered
// the authorization URL and the step that handles the redirect back.
const stateVersion = 1

// methodState is the method's private state for one flow, kept by the engine as
// it stands: the provider the flow belongs to, the digest of the state it drew,
// the PKCE verifier and the nonce. The state is held only as its digest, so a
// read of the flow record yields no value an attacker could present as the
// state; the verifier and the nonce are secrets the record holds, and the
// record's read permission is treated as a credential.
type methodState struct {
	Version  int    `json:"v"`
	Provider string `json:"provider"`
	State    string `json:"state"`
	Verifier string `json:"verifier"`
	Nonce    string `json:"nonce"`
}

// writeState writes the state the redirect step reads.
func writeState(provider, stateDigest, verifier, nonce string) []byte {
	// A struct of an int and four strings always encodes.
	b, _ := json.Marshal(methodState{
		Version:  stateVersion,
		Provider: provider,
		State:    stateDigest,
		Verifier: verifier,
		Nonce:    nonce,
	})
	return b
}

// readState reads the state writeState wrote. Anything else, any empty field
// included, names no drawn provider, state, verifier or nonce.
func readState(private []byte) (methodState, bool) {
	var s methodState
	dec := json.NewDecoder(bytes.NewReader(private))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil || s.Version != stateVersion ||
		s.Provider == "" || s.State == "" || s.Verifier == "" || s.Nonce == "" || dec.More() {
		return methodState{}, false
	}
	return s, true
}

// digest is the SHA-256 of a value, in hexadecimal. The state is stored only as
// this digest, so a record read yields no reusable state.
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// randomToken is 32 bytes of crypto/rand as unpadded base64url, for the state
// and the nonce. Its length and encoding keep it a single URL-safe parameter.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
