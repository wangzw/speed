package oidc

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
)

const stateVersion = 1

type methodState struct {
	Version  int    `json:"v"`
	Provider string `json:"provider"`
	State    string `json:"state"`
	Verifier string `json:"verifier"`
	Nonce    string `json:"nonce"`
}

func writeState(provider, stateDigest, verifier, nonce string) []byte {
	b, _ := json.Marshal(methodState{
		Version:  stateVersion,
		Provider: provider,
		State:    stateDigest,
		Verifier: verifier,
		Nonce:    nonce,
	})
	return b
}

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

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
