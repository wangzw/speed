package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestEveryRefusalOfAnAttributeSetIsAnInvalidArgument checks every refusal either
// check makes is ErrInvalidArgument.
func TestEveryRefusalOfAnAttributeSetIsAnInvalidArgument(t *testing.T) {
	account := map[string]map[string]string{
		"count":    entries(AccountAttributesMax+1, plainKey, "v"),
		"key":      {"Bad Key": "v"},
		"reserved": {"authn.flag": "v"},
		"value":    {"k": "a\x01"},
		"size": func() map[string]string {
			m := entries(8, func(i int) string { return fmt.Sprintf("k%d", i) }, strings.Repeat("v", 254))
			m["k0"] += "v"
			return m
		}(),
	}
	for name, m := range account {
		if err := CheckAccountAttributes(m); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("CheckAccountAttributes(%s) = %v, want ErrInvalidArgument", name, err)
		}
	}
	idp := map[string]map[string]string{
		"count": entries(SessionIDPAttributesMax+1, func(i int) string { return fmt.Sprintf("idp.a%d", i) }, "v"),
		"key":   {"groups": "v"},
		"value": {"idp.g": strings.Repeat("v", SessionIDPValueMax+1)},
	}
	for name, m := range idp {
		if err := CheckIDPAttributes(m); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("CheckIDPAttributes(%s) = %v, want ErrInvalidArgument", name, err)
		}
	}
}

// TestTheBoundsAreTheDesignsNumbers pins the sub-bounds and checks both writers'
// worst-case sets fit the gateway's totals.
func TestTheBoundsAreTheDesignsNumbers(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"AccountAttributesMax", AccountAttributesMax, 24},
		{"AccountAttributeBytesMax", AccountAttributeBytesMax, 2 * 1024},
		{"AttributeKeyMax", AttributeKeyMax, 64},
		{"AccountAttributeValueMax", AccountAttributeValueMax, 256},
		{"SessionIDPAttributesMax", SessionIDPAttributesMax, 8},
		{"SessionIDPKeyMax", SessionIDPKeyMax, 32},
		{"SessionIDPValueMax", SessionIDPValueMax, 128},
		{"IdentityAttributesMax", IdentityAttributesMax, 32},
		{"IdentityAttributeBytesMax", IdentityAttributeBytesMax, 4 * 1024},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if ReservedPrefixIDP != "idp." || ReservedPrefixAuthn != "authn." {
		t.Errorf("the reserved prefixes read %q and %q, want idp. and authn.", ReservedPrefixIDP, ReservedPrefixAuthn)
	}

	if got := AccountAttributesMax + SessionIDPAttributesMax; got > IdentityAttributesMax {
		t.Errorf("the entries of both writers come to %d, over the gateway's %d", got, IdentityAttributesMax)
	}
	worst := AccountAttributeBytesMax + SessionIDPAttributesMax*(SessionIDPKeyMax+SessionIDPValueMax)
	if worst != 3328 {
		t.Errorf("the worst case of both writers is %d bytes, want 3328", worst)
	}
	if worst > IdentityAttributeBytesMax {
		t.Errorf("the worst case of both writers is %d bytes, over the gateway's %d", worst, IdentityAttributeBytesMax)
	}
	if got := SessionIDPKeyMax - len(ReservedPrefixIDP); got != 28 {
		t.Errorf("a mapped attribute name may be %d bytes, want 28", got)
	}
}

func entries(n int, key func(int) string, value string) map[string]string {
	m := make(map[string]string, n)
	for i := range n {
		m[key(i)] = value
	}
	return m
}

func plainKey(i int) string { return fmt.Sprintf("k%02d", i) }

// TestAccountAttributesAreHeldToTheirBounds puts each bound to a value on either
// side of it.
func TestAccountAttributesAreHeldToTheirBounds(t *testing.T) {
	for _, c := range []struct {
		name string
		m    map[string]string
		ok   bool
	}{
		{"nil", nil, true},
		{"empty", map[string]string{}, true},
		{"one", map[string]string{"region": "eu"}, true},
		{"24 entries", entries(24, plainKey, "v"), true},
		{"25 entries", entries(25, plainKey, "v"), false},
		{"a key of 64 bytes", map[string]string{strings.Repeat("a", 64): "v"}, true},
		{"a key of 65 bytes", map[string]string{strings.Repeat("a", 65): "v"}, false},
		{"an empty key", map[string]string{"": "v"}, false},
		{"a key starting with a dash", map[string]string{"-a": "v"}, false},
		{"a key starting with a dot", map[string]string{".a": "v"}, false},
		{"a key with a capital", map[string]string{"Region": "eu"}, false},
		{"a key with a space", map[string]string{"re gion": "eu"}, false},
		{"a key with an at sign", map[string]string{"a@b": "v"}, false},
		{"a key with every permitted punctuation", map[string]string{"a.b_c-d9": "v"}, true},
		{"a value of 256 bytes", map[string]string{"k": strings.Repeat("v", 256)}, true},
		{"a value of 257 bytes", map[string]string{"k": strings.Repeat("v", 257)}, false},
		{"an empty value", map[string]string{"k": ""}, true},
		{"a value with a control character", map[string]string{"k": "a\x01b"}, false},
		{"a value with a newline", map[string]string{"k": "a\nb"}, false},
		{"a value with a NUL", map[string]string{"k": "a\x00"}, false},
		{"a value that is not UTF-8", map[string]string{"k": "a\xffb"}, false},
		{"a multi-byte value", map[string]string{"k": "用户"}, true},
		{"an idp. key", map[string]string{"idp.groups": "v"}, false},
		{"an authn. key", map[string]string{"authn.flag": "v"}, false},
		{"idp without the dot is not the prefix", map[string]string{"idpgroups": "v"}, true},
		{"authn without the dot is not the prefix", map[string]string{"authnflag": "v"}, true},
		{"2048 bytes of keys and values", entries(8, func(i int) string { return fmt.Sprintf("k%d", i) }, strings.Repeat("v", 254)), true},
		{"2049 bytes of keys and values", func() map[string]string {
			m := entries(8, func(i int) string { return fmt.Sprintf("k%d", i) }, strings.Repeat("v", 254))
			m["k0"] += "v"
			return m
		}(), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := CheckAccountAttributes(c.m)
			if (err == nil) != c.ok {
				t.Errorf("CheckAccountAttributes = %v, want ok = %v", err, c.ok)
			}
		})
	}
}

// TestTheAccountCheckNeverEchoesWhatItRefused checks a refusal names the class,
// not the key or value.
func TestTheAccountCheckNeverEchoesWhatItRefused(t *testing.T) {
	for _, m := range []map[string]string{
		{"SECRET-KEY": "v"},
		{"k": "SECRET-VALUE\x01"},
		{"idp.SECRET-KEY": "v"},
		{"authn.secret": "v"},
	} {
		if err := CheckAccountAttributes(m); err == nil {
			t.Fatalf("%v was accepted", m)
		} else if strings.Contains(strings.ToLower(err.Error()), "secret") {
			t.Errorf("the error %q echoes what it refused", err)
		}
	}
	if err := CheckIDPAttributes(map[string]string{"idp.SECRET": "v"}); err == nil || strings.Contains(strings.ToLower(err.Error()), "secret") {
		t.Errorf("the idp error is %v, want a refusal that does not echo the key", err)
	}
}

// TestIDPAttributesAreHeldToTheirBounds puts each bound of the session payload's
// idp.* set to a value on either side of it.
func TestIDPAttributesAreHeldToTheirBounds(t *testing.T) {
	idpKey := func(i int) string { return fmt.Sprintf("idp.a%d", i) }
	for _, c := range []struct {
		name string
		m    map[string]string
		ok   bool
	}{
		{"nil", nil, true},
		{"one", map[string]string{"idp.groups": "a"}, true},
		{"8 entries", entries(8, idpKey, "v"), true},
		{"9 entries", entries(9, idpKey, "v"), false},
		{"a key of 32 bytes", map[string]string{"idp." + strings.Repeat("a", 28): "v"}, true},
		{"a key of 33 bytes", map[string]string{"idp." + strings.Repeat("a", 29): "v"}, false},
		{"the prefix alone", map[string]string{"idp.": "v"}, false},
		{"no prefix", map[string]string{"groups": "v"}, false},
		{"the other reserved prefix", map[string]string{"authn.groups": "v"}, false},
		{"a prefix without its dot", map[string]string{"idpgroups": "v"}, false},
		{"a capital in the name", map[string]string{"idp.Groups": "v"}, false},
		{"a value of 128 bytes", map[string]string{"idp.g": strings.Repeat("v", 128)}, true},
		{"a value of 129 bytes", map[string]string{"idp.g": strings.Repeat("v", 129)}, false},
		{"a value with a control character", map[string]string{"idp.g": "a\tb"}, false},
		{"a value that is not UTF-8", map[string]string{"idp.g": "\xc3"}, false},
		{"a multi-byte value of 126 bytes", map[string]string{"idp.g": strings.Repeat("用", 42)}, true},
		{"a multi-byte value of 129 bytes", map[string]string{"idp.g": strings.Repeat("用", 43)}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := CheckIDPAttributes(c.m)
			if (err == nil) != c.ok {
				t.Errorf("CheckIDPAttributes = %v, want ok = %v", err, c.ok)
			}
		})
	}
}

// TestAWorstCaseSetOfEachWriterTogetherFitsTheTotals checks the merged largest
// sets fit the gateway's entry and byte totals.
func TestAWorstCaseSetOfEachWriterTogetherFitsTheTotals(t *testing.T) {
	account := map[string]string{}
	size := 0
	for i := range AccountAttributesMax {
		k := fmt.Sprintf("k%02d", i)
		v := strings.Repeat("v", (AccountAttributeBytesMax-AccountAttributesMax*len(k))/AccountAttributesMax)
		account[k] = v
		size += len(k) + len(v)
	}
	for size < AccountAttributeBytesMax {
		account["k00"] += "v"
		size++
	}
	if err := CheckAccountAttributes(account); err != nil {
		t.Fatalf("the worst account set is refused: %v", err)
	}
	idp := map[string]string{}
	for i := range SessionIDPAttributesMax {
		key := fmt.Sprintf("idp.%0*d", SessionIDPKeyMax-len(ReservedPrefixIDP), i)
		idp[key] = strings.Repeat("v", SessionIDPValueMax)
	}
	if err := CheckIDPAttributes(idp); err != nil {
		t.Fatalf("the worst idp set is refused: %v", err)
	}

	merged := map[string]string{}
	total := 0
	for _, m := range []map[string]string{account, idp} {
		for k, v := range m {
			merged[k] = v
			total += len(k) + len(v)
		}
	}
	if len(merged) != IdentityAttributesMax {
		t.Errorf("the merged set has %d entries, want the gateway's %d exactly", len(merged), IdentityAttributesMax)
	}
	if total != 3328 || total > IdentityAttributeBytesMax {
		t.Errorf("the merged set is %d bytes, want 3328, under %d", total, IdentityAttributeBytesMax)
	}
}

// TestTruncateUTF8CutsOnACharacterBoundary checks every limit yields the longest
// valid UTF-8 prefix within it.
func TestTruncateUTF8CutsOnACharacterBoundary(t *testing.T) {
	s := "aé用😀b用😀éz"
	if !utf8.ValidString(s) {
		t.Fatal("the fixture is not UTF-8")
	}
	for limit := -2; limit <= len(s)+2; limit++ {
		got := TruncateUTF8(s, limit)
		if !strings.HasPrefix(s, got) {
			t.Errorf("limit %d: %q is not a prefix", limit, got)
		}
		if !utf8.ValidString(got) {
			t.Errorf("limit %d: %q is not valid UTF-8", limit, got)
		}
		if bound := max(limit, 0); len(got) > bound {
			t.Errorf("limit %d: the result is %d bytes", limit, len(got))
		}
		if rest := s[len(got):]; rest != "" {
			_, size := utf8.DecodeRuneInString(rest)
			if len(got)+size <= max(limit, 0) {
				t.Errorf("limit %d: %q dropped a character that fits", limit, got)
			}
		}
	}
	for _, c := range []struct {
		in    string
		limit int
		want  string
	}{
		{"", 5, ""},
		{"abc", 0, ""},
		{"abc", -1, ""},
		{"abc", 3, "abc"},
		{"abc", 99, "abc"},
		{"abc", 2, "ab"},
		{"用", 2, ""},
		{"用", 3, "用"},
		{"a用", 3, "a"},
		{"😀", 3, ""},
		{"😀", 4, "😀"},
		{strings.Repeat("用", 50), 128, strings.Repeat("用", 42)},
	} {
		if got := TruncateUTF8(c.in, c.limit); got != c.want {
			t.Errorf("TruncateUTF8(%q, %d) = %q, want %q", c.in, c.limit, got, c.want)
		}
	}
	// A string that is not UTF-8 is still cut at the bound and never panics.
	if got := TruncateUTF8("\x80\x80\x80\x80", 2); len(got) > 2 {
		t.Errorf("TruncateUTF8 of stray bytes returned %d bytes", len(got))
	}
}

// TestACutValueIsAdmissible checks a value cut to SessionIDPValueMax passes the
// idp.* check that the uncut value fails.
func TestACutValueIsAdmissible(t *testing.T) {
	for _, v := range []string{strings.Repeat("v", 500), strings.Repeat("用", 100), strings.Repeat("😀", 100), strings.Repeat("é", 200)} {
		raw := map[string]string{"idp.g": v}
		if err := CheckIDPAttributes(raw); err == nil {
			t.Fatalf("a %d byte value is admissible uncut", len(v))
		}
		cut := map[string]string{"idp.g": TruncateUTF8(v, SessionIDPValueMax)}
		if err := CheckIDPAttributes(cut); err != nil {
			t.Errorf("the cut of a %d byte value is refused: %v", len(v), err)
		}
	}
}
