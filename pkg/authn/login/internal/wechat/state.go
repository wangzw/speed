package wechat

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
)

// stateBytes is how many random bytes the state carries. WeChat's browser
// authorization accepts a state of letters and digits up to 128 bytes, and a
// 32-byte value written in hexadecimal is 64 such characters.
const stateBytes = 32

const stateVersion = 1

// State is the state a WeChat flow carries between begin and callback: the value sent to the
// provider and the digest the record keeps. Only the digest is private state, and the
// comparison hides where two values differ.
type State struct {
	value  string
	digest string
}

// NewState draws a fresh state.
func NewState() (State, error) {
	var raw [stateBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return State{}, err
	}
	return stateOf(hex.EncodeToString(raw[:])), nil
}

func stateOf(value string) State {
	sum := sha256.Sum256([]byte(value))
	return State{value: value, digest: hex.EncodeToString(sum[:])}
}

// Value is the state sent to the provider in the authorization address.
func (s State) Value() string { return s.value }

// Private is the flow state the engine keeps: the digest of the value, so a
// reader of the store learns nothing it could send back.
func (s State) Private() []byte {
	b, _ := json.Marshal(stateRecord{Version: stateVersion, Digest: s.digest})
	return b
}

// Matches reports whether a value the provider returned is the one this state
// was drawn for. An empty digest matches nothing; the comparison is constant
// time.
func (s State) Matches(value string) bool {
	if s.digest == "" {
		return false
	}
	sum := sha256.Sum256([]byte(value))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(s.digest)) == 1
}

type stateRecord struct {
	Version int    `json:"v"`
	Digest  string `json:"d"`
}

// LoadState reads the private state Private wrote. Anything else, empty state
// included, names no state and so matches nothing.
func LoadState(private []byte) (State, bool) {
	var r stateRecord
	dec := json.NewDecoder(bytes.NewReader(private))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil || r.Version != stateVersion || r.Digest == "" || dec.More() {
		return State{}, false
	}
	return State{digest: r.Digest}, true
}
