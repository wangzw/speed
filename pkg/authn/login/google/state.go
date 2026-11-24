package google

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const stateVersion = 1

// methodState is the method's private state for one flow, kept by the engine as
// it stands: the digest of the state it drew, the PKCE verifier and the nonce.
// It holds the state itself only as its digest, so a read of the flow record
// yields no value an attacker could present as the state; the PKCE verifier and
// the nonce are secrets the record holds, and the record's read permission is
// treated as a credential.
type methodState struct {
	Version  int    `json:"v"`
	State    string `json:"state"`
	Verifier string `json:"verifier"`
	Nonce    string `json:"nonce"`
}

func writeState(stateDigest, verifier, nonce string) []byte {
	b, _ := json.Marshal(methodState{Version: stateVersion, State: stateDigest, Verifier: verifier, Nonce: nonce})
	return b
}

func readState(private []byte) (methodState, bool) {
	var s methodState
	dec := json.NewDecoder(bytes.NewReader(private))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil || s.Version != stateVersion ||
		s.State == "" || s.Verifier == "" || s.Nonce == "" || dec.More() {
		return methodState{}, false
	}
	return s, true
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
