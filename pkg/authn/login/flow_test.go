package login

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestTokenDigestKeysAndBinds holds that a flow token's one digest serves as both the record key and its binding.
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

// TestRecordDecodeRefusesAnUnknownVersionFieldAndRetiredMerge holds that an unknown version, an unknown field and a retired merged record are all refused.
func TestRecordDecodeRefusesAnUnknownVersionFieldAndRetiredMerge(t *testing.T) {
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
	merged, _ := json.Marshal(map[string]any{"v": recordVersion, "state": stateFirst, "merged": true})
	if _, err := decodeRecord(merged); err == nil {
		t.Error("a record from the retired automatic merge is accepted")
	}
}

// TestAFlowRecordsTheProviderItsCallbackPathCarries holds that a social method records its name and an enterprise provider the start input's value.
func TestAFlowRecordsTheProviderItsCallbackPathCarries(t *testing.T) {
	social := newRigMethod(t, &redirectMethod{})
	socialCookie := startRedirect(t, social, "")
	if got := flowProviderOf(t, social, socialCookie); got != "password" {
		t.Errorf("a social flow recorded provider %q, want the method name", got)
	}
	sneaky := startRedirect(t, social, `,"input":{"provider":"ent"}`)
	if got := flowProviderOf(t, social, sneaky); got != "password" {
		t.Errorf("a social flow with input.provider recorded provider %q, want the method name", got)
	}

	ent := newRigMethod(t, &redirectMethod{name: "oidc"})
	entCookie := startRedirect(t, ent, `,"input":{"provider":"ent"}`)
	if got := flowProviderOf(t, ent, entCookie); got != "ent" {
		t.Errorf("an enterprise flow recorded provider %q, want the input's provider", got)
	}
}

func flowProviderOf(t *testing.T, r *rig, cookie string) string {
	t.Helper()
	_, token, ok := strings.Cut(cookie, "=")
	if !ok {
		t.Fatalf("the cookie %q carries no token", cookie)
	}
	raw, present, err := r.kv.Get(context.Background(), flowKey(token))
	if err != nil || !present {
		t.Fatalf("reading the flow record: present=%v err=%v", present, err)
	}
	rec, err := decodeRecord(raw)
	if err != nil {
		t.Fatalf("decoding the flow record: %v", err)
	}
	return rec.Provider
}

// TestCheckReturnToAcceptsARelativePathOrATrustedAbsoluteURL holds that an absolute return target is accepted only for a trusted origin.
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

// TestIDPAttributesAreTrimmedToTheDeclaredBounds holds the count, key and value bounds, the value cut at a character boundary.
func TestIDPAttributesAreTrimmedToTheDeclaredBounds(t *testing.T) {
	attrs := map[string]string{}
	for i := 0; i < authn.SessionIDPAttributesMax+4; i++ {
		attrs[authn.ReservedPrefixIDP+"k"+string(rune('a'+i))] = "v"
	}
	// A multi-byte value longer than the bound must be cut without splitting a
	// character.
	attrs[authn.ReservedPrefixIDP+"long"] = strings.Repeat("世", 100)
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

// TestCheckFieldsHoldsTheStartInputToItsBounds holds the field count and length bounds of a start request's input.
func TestCheckFieldsHoldsTheStartInputToItsBounds(t *testing.T) {
	if err := checkFields(nil, maxStartFieldBytes); err != nil {
		t.Errorf("an empty input is refused: %v", err)
	}
	ok := map[string]string{}
	for i := 0; i < maxInputFields; i++ {
		ok[string(rune('a'+i))] = strings.Repeat("x", maxStartFieldBytes)
	}
	if err := checkFields(ok, maxStartFieldBytes); err != nil {
		t.Errorf("an input within the bounds is refused: %v", err)
	}
	tooMany := map[string]string{}
	for i := 0; i < maxInputFields+1; i++ {
		tooMany[string(rune('a'+i))] = "x"
	}
	if err := checkFields(tooMany, maxStartFieldBytes); err == nil {
		t.Error("an input with too many fields is accepted")
	}
	if err := checkFields(map[string]string{"a": strings.Repeat("x", maxStartFieldBytes+1)}, maxStartFieldBytes); err == nil {
		t.Error("an input field over the length bound is accepted")
	}
}

// TestTheAnswerFieldBoundIsLargerThanTheStartBound holds that the answer bound exceeds 512 bytes so a WebAuthn credential fits.
func TestTheAnswerFieldBoundIsLargerThanTheStartBound(t *testing.T) {
	credential := strings.Repeat("x", 1100)
	if len(credential) <= maxStartFieldBytes {
		t.Fatalf("the case does not exercise the gap: the credential is %d bytes", len(credential))
	}
	if err := checkFields(map[string]string{"credential": credential}, maxStartFieldBytes); err == nil {
		t.Error("a credential-sized field is accepted under the start bound")
	}
	if err := checkFields(map[string]string{"credential": credential}, maxAnswerFieldBytes); err != nil {
		t.Errorf("a credential-sized field is refused under the answer bound: %v", err)
	}
}

// TestParseListReadsTheJSONArrayOfAListSetting holds the reading of a list setting stored as a JSON array string.
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
