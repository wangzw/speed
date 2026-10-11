package login

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestTokenDigestKeysAndBinds holds that a flow token yields one digest used both
// as the record's key and as its binding: possession of the token is the binding.
func TestTokenDigestKeysAndBinds(t *testing.T) {
	token, err := newFlowToken()
	if err != nil {
		t.Fatalf("drawing a token: %v", err)
	}
	if len(token) < 40 {
		t.Errorf("a flow token is %d characters, want a 256-bit value", len(token))
	}
	digest := tokenDigest(token)
	if !strings.HasPrefix(flowKey(token), flowKeyPrefix) || flowKey(token) != flowKeyPrefix+digest {
		t.Errorf("flowKey = %q, want the prefix and the digest", flowKey(token))
	}
	if !bindingMatches(token, digest) {
		t.Error("a record bound to the token's own digest does not match it")
	}
	if bindingMatches(token, tokenDigest("another")) {
		t.Error("a record bound to another digest matches the token")
	}
	other, _ := newFlowToken()
	if other == token {
		t.Error("two flows drew the same token")
	}
}

// TestRecordDecodeRefusesAnUnknownVersionAndField holds the record's forward
// safety: a version this build does not know is refused, and so is an unknown
// field.
func TestRecordDecodeRefusesAnUnknownVersionAndField(t *testing.T) {
	good := record{Version: recordVersion, Purpose: authn.PurposeLogin, State: stateFirst}
	raw, err := json.Marshal(good)
	if err != nil {
		t.Fatalf("encoding a record: %v", err)
	}
	if _, err := decodeRecord(raw); err != nil {
		t.Fatalf("a record this build wrote is refused: %v", err)
	}
	future, _ := json.Marshal(map[string]any{"v": recordVersion + 1, "state": stateFirst})
	if _, err := decodeRecord(future); err == nil {
		t.Error("a record of an unknown version is accepted")
	}
	extra, _ := json.Marshal(map[string]any{"v": recordVersion, "state": stateFirst, "surprise": true})
	if _, err := decodeRecord(extra); err == nil {
		t.Error("a record with an unknown field is accepted")
	}
}

// TestCheckReturnToAcceptsARelativePathOrATrustedAbsoluteURL holds the rule the
// engine applies at Begin, and that an absolute URL is accepted only for a
// trusted origin.
func TestCheckReturnToAcceptsARelativePathOrATrustedAbsoluteURL(t *testing.T) {
	trusted := []string{"https://app.example.org"}
	for _, ok := range []string{"/", "/orders", "/orders/42?x=1", "https://app.example.org/after"} {
		if err := checkReturnTo(ok, trusted); err != nil {
			t.Errorf("checkReturnTo(%q) = %v, want nil", ok, err)
		}
	}
	bad := []string{
		"", "//evil.example.org", "/a\\b", "/a@b", "/a\nb", "https://evil.example.org/x",
		"http://app.example.org/x", "javascript:alert(1)", "/" + strings.Repeat("a", maxReturnToBytes),
	}
	for _, v := range bad {
		if err := checkReturnTo(v, trusted); err == nil {
			t.Errorf("checkReturnTo(%q) = nil, want a refusal", v)
		}
	}
}

// TestIDPAttributesAreTrimmedToTheDeclaredBounds holds the three bounds of the
// idp.* snapshot: the count, the key length and the value length, with the value
// cut at a character boundary so it never carries an invalid rune.
func TestIDPAttributesAreTrimmedToTheDeclaredBounds(t *testing.T) {
	attrs := map[string]string{}
	for i := 0; i < authn.SessionIDPAttributesMax+4; i++ {
		attrs[authn.ReservedPrefixIDP+"k"+string(rune('a'+i))] = "v"
	}
	// A multi-byte value longer than the bound must be cut without splitting a
	// character.
	attrs[authn.ReservedPrefixIDP+"long"] = strings.Repeat("世", 100)
	// A key over the length is dropped; a key without the prefix is dropped.
	attrs[authn.ReservedPrefixIDP+strings.Repeat("x", authn.SessionIDPKeyMax)] = "v"
	attrs["plain"] = "v"
	out := idpAttributes(&authn.External{Attributes: attrs})
	if len(out) != authn.SessionIDPAttributesMax {
		t.Errorf("kept %d attributes, want at most %d", len(out), authn.SessionIDPAttributesMax)
	}
	if v, ok := out[authn.ReservedPrefixIDP+"long"]; ok {
		if len(v) > authn.SessionIDPValueMax {
			t.Errorf("a value is %d bytes, want at most %d", len(v), authn.SessionIDPValueMax)
		}
		if !validUTF8(v) {
			t.Errorf("a cut value is not valid UTF-8")
		}
	}
	for k := range out {
		if len(k) > authn.SessionIDPKeyMax {
			t.Errorf("a kept key %q is longer than %d bytes", k, authn.SessionIDPKeyMax)
		}
		if !strings.HasPrefix(k, authn.ReservedPrefixIDP) {
			t.Errorf("a kept key %q does not carry the idp. prefix", k)
		}
	}
}

func validUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

// TestCheckFieldsHoldsTheStartInputToItsBounds holds the field count and length
// bounds.
func TestCheckFieldsHoldsTheStartInputToItsBounds(t *testing.T) {
	if err := checkFields(nil); err != nil {
		t.Errorf("an empty input is refused: %v", err)
	}
	ok := map[string]string{}
	for i := 0; i < maxStartFields; i++ {
		ok[string(rune('a'+i))] = strings.Repeat("x", maxFieldBytes)
	}
	if err := checkFields(ok); err != nil {
		t.Errorf("an input within the bounds is refused: %v", err)
	}
	tooMany := map[string]string{}
	for i := 0; i < maxStartFields+1; i++ {
		tooMany[string(rune('a'+i))] = "x"
	}
	if err := checkFields(tooMany); err == nil {
		t.Error("an input with too many fields is accepted")
	}
	if err := checkFields(map[string]string{"a": strings.Repeat("x", maxFieldBytes+1)}); err == nil {
		t.Error("an input field over the length bound is accepted")
	}
}

// TestParseListReadsTheJSONArrayOfAListSetting holds the reading of a list
// setting, whose value is a JSON array in a string item.
func TestParseListReadsTheJSONArrayOfAListSetting(t *testing.T) {
	if got, err := parseList(`["session","token"]`); err != nil || len(got) != 2 {
		t.Errorf("parseList = %v, %v; want two names", got, err)
	}
	if got, err := parseList(""); err != nil || got != nil {
		t.Errorf("parseList(empty) = %v, %v; want the empty list", got, err)
	}
	if _, err := parseList("not json"); err == nil {
		t.Error("parseList accepts a value that is not a JSON array")
	}
	if !deliveryModesTextInDomain(`["session"]`) {
		t.Error("a single session mode is outside the domain")
	}
	if deliveryModesTextInDomain(`[]`) || deliveryModesTextInDomain(`["sso"]`) {
		t.Error("an empty or unknown delivery mode is inside the domain")
	}
}
