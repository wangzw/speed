package wechat

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func TestNewStateIsSixtyFourHexCharacters(t *testing.T) {
	st, err := NewState()
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if len(st.Value()) != 64 {
		t.Fatalf("state length = %d, want 64", len(st.Value()))
	}
	if _, derr := hex.DecodeString(st.Value()); derr != nil {
		t.Fatalf("state %q is not hexadecimal: %v", st.Value(), derr)
	}
	other, err := NewState()
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if st.Value() == other.Value() {
		t.Fatalf("two draws gave the same state %q", st.Value())
	}
}

// TestPrivateCarriesOnlyTheDigest holds that the state the flow record keeps is
// the digest, not the value: a reader of the key-value store cannot send back
// what it sees.
func TestPrivateCarriesOnlyTheDigest(t *testing.T) {
	st, err := NewState()
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	private := st.Private()
	if bytes.Contains(private, []byte(st.Value())) {
		t.Fatalf("the private state %q carries the value", private)
	}

	back, ok := LoadState(private)
	if !ok {
		t.Fatalf("LoadState could not read the state Private wrote: %q", private)
	}
	if !back.Matches(st.Value()) {
		t.Errorf("the loaded state does not match the value it was drawn for")
	}
	if back.Matches("0000000000000000000000000000000000000000000000000000000000000000") {
		t.Errorf("the loaded state matched a value it was not drawn for")
	}
}

func TestAMismatchedOrAbsentStateMatchesNothing(t *testing.T) {
	st := stateOf("the-right-state")
	if !st.Matches("the-right-state") {
		t.Fatal("the state did not match its own value")
	}
	if st.Matches("the-wrong-state") {
		t.Errorf("the state matched a different value")
	}
	if st.Matches("") {
		t.Errorf("the state matched the empty value")
	}
	if (State{}).Matches("the-right-state") {
		t.Errorf("a zero state matched a value")
	}
}

func TestLoadStateRefusesAnythingItDidNotWrite(t *testing.T) {
	cases := []struct {
		name    string
		private []byte
	}{
		{"empty", nil},
		{"not JSON", []byte("not json")},
		{"another version", []byte(`{"v":99,"d":"abcd"}`)},
		{"no digest", []byte(`{"v":1}`)},
		{"an unknown field", []byte(`{"v":1,"d":"abcd","x":1}`)},
		{"a trailing value", []byte(`{"v":1,"d":"abcd"} {}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := LoadState(tc.private); ok {
				t.Errorf("LoadState read %q, want a refusal", tc.private)
			}
		})
	}
}

// TestThePrivateStateSpellsItsVersion checks that the record carries a version
// field, which the loader refuses to misread as a future format.
func TestThePrivateStateSpellsItsVersion(t *testing.T) {
	st := stateOf(strings.Repeat("a", 64))
	if !bytes.Contains(st.Private(), []byte(`"v":1`)) {
		t.Errorf("the private state %q carries no version", st.Private())
	}
}
