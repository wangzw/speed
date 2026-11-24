package saml

import (
	"bytes"
	"encoding/json"
)

const stateVersion = 1

type methodState struct {
	Version  int    `json:"v"`
	Request  string `json:"request"`
	Relay    string `json:"relay"`
	Provider string `json:"provider"`
}

func writeState(request, relay, provider string) []byte {
	b, _ := json.Marshal(methodState{Version: stateVersion, Request: request, Relay: relay, Provider: provider})
	return b
}

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
