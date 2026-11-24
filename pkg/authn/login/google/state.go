package google

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const stateVersion = 1

// methodState is the method's private state: the state's digest, the PKCE verifier and the
// nonce. The state is kept only as its digest; the verifier and nonce are secrets the record holds.
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
