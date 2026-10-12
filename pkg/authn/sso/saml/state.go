package saml

import (
	"bytes"
	"encoding/json"
)

// stateVersion is the version of the private state kept between the step that
// answered the AuthnRequest URL and the step that consumes the assertion.
const stateVersion = 1

// methodState is the method's private state for one flow, kept by the engine as
// it stands: the identifier of the AuthnRequest the method drew, the RelayState
// it sent beside it and the identifier of the provider the flow is for. The
// provider is kept here so that a callback cannot be walked into a proof for a
// provider the flow did not start on, even were the path check bypassed.
type methodState struct {
	Version  int    `json:"v"`
	Request  string `json:"request"`
	Relay    string `json:"relay"`
	Provider string `json:"provider"`
}

// writeState writes the state the assertion step reads.
func writeState(request, relay, provider string) []byte {
	// A struct of an int and three strings always encodes.
	b, _ := json.Marshal(methodState{Version: stateVersion, Request: request, Relay: relay, Provider: provider})
	return b
}

// readState reads the state writeState wrote. Anything else, empty state
// included, names no drawn request.
func readState(private []byte) (methodState, bool) {
	var s methodState
	dec := json.NewDecoder(bytes.NewReader(private))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil || s.Version != stateVersion ||
		s.Request == "" || s.Relay == "" || s.Provider == "" || dec.More() {
		return methodState{}, false
	}
	return s, true
}
