package gate

import "testing"

func TestBackendNamesAreHeldAtTheEdgesOfTheirAlphabet(t *testing.T) {
	for _, name := range []string{"a", "z", "0", "9", "az09", "a-z", "a.9", "authz"} {
		if !validBackendName(name) {
			t.Errorf("the backend name %q is refused", name)
		}
	}
	for _, name := range []string{"", "A", "Z", "-a", ".a", "a_b", "a b", "a/b", "é"} {
		if validBackendName(name) {
			t.Errorf("the backend name %q is admitted", name)
		}
	}
}

func TestTokensAreHeldAtTheEdgesOfTheirAlphabetAndAtTheirFirstByte(t *testing.T) {
	for _, s := range []string{"a", "z", "A", "Z", "0", "9", "aZ09", "__Host-sid", "~"} {
		if !isToken(s) {
			t.Errorf("the token %q is refused", s)
		}
	}
	for _, s := range []string{"", " a", "=a", ";a", "(a", "a b", "@", "[", "{", "/"} {
		if isToken(s) {
			t.Errorf("the token %q is admitted", s)
		}
	}
}

func TestAttributeKeysAreHeldAtTheEdgesOfTheirAlphabetAndAtTheirSecondByte(t *testing.T) {
	for _, k := range []string{"a", "z", "0", "9", "a-b", "a.b", "a_b", "k0", "k9", "z9"} {
		if !attributeKey(k) {
			t.Errorf("the attribute key %q is refused", k)
		}
	}
	for _, k := range []string{"", "-a", ".a", "_a", "A", "aB", "a b", "a/b"} {
		if attributeKey(k) {
			t.Errorf("the attribute key %q is admitted", k)
		}
	}
}

func TestUUIDShapeChecksTheFirstByte(t *testing.T) {
	if uuidShape("g1234567-89ab-cdef-0123-456789abcdef") {
		t.Error("a first byte outside hex is admitted")
	}
	if uuidShape("-1234567-89ab-cdef-0123-456789abcdef") {
		t.Error("a dash for a first byte is admitted")
	}
}

func TestUUIDShapeNeedsDashes(t *testing.T) {
	if !uuidShape("01234567-89ab-cdef-0123-456789abcdef") {
		t.Fatal("a canonical UUID is refused")
	}
	if uuidShape("01234567_89ab_cdef_0123_456789abcdef") {
		t.Error("underscores stand in for the dashes")
	}
}
