package gate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/model"
)

// The two writers of an identity's attribute set are held to bounds of their own,
// declared once in the model package, so that what they write together stays
// inside what the exit check stamps. These cases read the model's constants and
// the exit check's against each other, and put the largest set each writer may
// produce, merged, to the exit check and to authntest, which holds a second copy
// of the totals.

// worstAccountAttributes is the largest account attribute set the model admits:
// every entry allowed, the keys and values together exactly at the byte bound,
// and no value above its own.
func worstAccountAttributes(t *testing.T) map[string]string {
	t.Helper()
	m := make(map[string]string, model.AccountAttributesMax)
	size := 0
	for i := range model.AccountAttributesMax {
		k := fmt.Sprintf("k%02d", i)
		v := strings.Repeat("v", (model.AccountAttributeBytesMax-model.AccountAttributesMax*len(k))/model.AccountAttributesMax)
		m[k] = v
		size += len(k) + len(v)
	}
	for size < model.AccountAttributeBytesMax {
		m["k00"] += "v"
		size++
	}
	if err := model.CheckAccountAttributes(m); err != nil {
		t.Fatalf("the worst account set is not admissible to the model: %v", err)
	}
	return m
}

// worstIDPAttributes is the largest idp.* set the model admits: every entry
// allowed, every key at the key bound and every value at the value bound.
func worstIDPAttributes(t *testing.T) map[string]string {
	t.Helper()
	m := make(map[string]string, model.SessionIDPAttributesMax)
	for i := range model.SessionIDPAttributesMax {
		k := fmt.Sprintf("%s%0*d", model.ReservedPrefixIDP, model.SessionIDPKeyMax-len(model.ReservedPrefixIDP), i)
		m[k] = strings.Repeat("v", model.SessionIDPValueMax)
	}
	if err := model.CheckIDPAttributes(m); err != nil {
		t.Fatalf("the worst idp set is not admissible to the model: %v", err)
	}
	return m
}

// TestTheModelsTotalsAreTheExitChecksOwn holds the three numbers the model
// repeats from this package against the constants the exit check enforces, so
// that a change to one is a change to the other.
func TestTheModelsTotalsAreTheExitChecksOwn(t *testing.T) {
	if model.IdentityAttributesMax != maxAttributes {
		t.Errorf("model.IdentityAttributesMax = %d, the exit check's maxAttributes = %d", model.IdentityAttributesMax, maxAttributes)
	}
	if model.IdentityAttributeBytesMax != maxAttributeBytes {
		t.Errorf("model.IdentityAttributeBytesMax = %d, the exit check's maxAttributeBytes = %d", model.IdentityAttributeBytesMax, maxAttributeBytes)
	}
	if model.AttributeKeyMax != maxAttributeKey {
		t.Errorf("model.AttributeKeyMax = %d, the exit check's maxAttributeKey = %d", model.AttributeKeyMax, maxAttributeKey)
	}
	if model.AccountAttributeValueMax != maxAttributeValue {
		t.Errorf("model.AccountAttributeValueMax = %d, the exit check's maxAttributeValue = %d", model.AccountAttributeValueMax, maxAttributeValue)
	}
}

// TestTheSubBoundsAddUpToLessThanTheStampedTotals is the arithmetic of the
// design: 24 and 8 entries come to the 32 the exit check allows, and the worst
// size, 2 KiB of account attributes and eight entries of a 32-byte key and a
// 128-byte value, comes to 3328 bytes under its 4 KiB.
func TestTheSubBoundsAddUpToLessThanTheStampedTotals(t *testing.T) {
	entries := model.AccountAttributesMax + model.SessionIDPAttributesMax
	if entries != 32 || entries > maxAttributes {
		t.Errorf("the entries of both writers come to %d, want 32 and no more than the exit check's %d", entries, maxAttributes)
	}
	worst := model.AccountAttributeBytesMax + model.SessionIDPAttributesMax*(model.SessionIDPKeyMax+model.SessionIDPValueMax)
	if worst != 3328 || worst > maxAttributeBytes {
		t.Errorf("the worst size of both writers is %d bytes, want 3328 and no more than the exit check's %d", worst, maxAttributeBytes)
	}
	if model.SessionIDPKeyMax > maxAttributeKey || model.SessionIDPValueMax > maxAttributeValue {
		t.Errorf("an idp.* key or value may be longer than the exit check's bounds: %d and %d", model.SessionIDPKeyMax, model.SessionIDPValueMax)
	}
}

// TestTheLargestSetOfBothWritersPassesTheExitCheckAndAuthntest merges the worst
// account set with the worst idp.* set and asks the exit check and authntest
// whether each admits it, which is the sentence the sub-bounds exist for. The two
// have to agree with each other and with the arithmetic, and the same set one
// entry or one byte larger than the totals has to be refused by both.
func TestTheLargestSetOfBothWritersPassesTheExitCheckAndAuthntest(t *testing.T) {
	d := exitTable(t)
	capable := backendNamed(t, d, "capable")

	merged := map[string]string{}
	for _, m := range []map[string]string{worstAccountAttributes(t), worstIDPAttributes(t)} {
		for k, v := range m {
			merged[k] = v
		}
	}
	if len(merged) != maxAttributes {
		t.Fatalf("the merged set has %d entries, want exactly the exit check's %d", len(merged), maxAttributes)
	}
	total := 0
	for k, v := range merged {
		total += len(k) + len(v)
	}
	if total != 3328 {
		t.Fatalf("the merged set is %d bytes, want 3328", total)
	}

	with := func(m map[string]string) authn.Principal {
		p := wellFormed()
		p.Attributes = m
		return p
	}
	check := func(name string, m map[string]string, want bool) {
		t.Helper()
		p := with(m)
		_, reason := checkPrincipal(p, anchor, capable, d)
		if got := reason == ""; got != want {
			t.Errorf("%s: the exit check accepts it = %v (reason %q), want %v", name, got, reason, want)
		}
		if got := stampedByAuthntest(p, capable.name); got != want {
			t.Errorf("%s: authntest accepts it = %v, want %v", name, got, want)
		}
	}

	check("the largest set of both writers", merged, true)

	// One entry more than the totals allow.
	over := map[string]string{}
	for k, v := range merged {
		over[k] = v
	}
	over["one-more"] = "v"
	check("one entry past the total", over, false)

	// The byte total is reached with fewer entries and larger values: the largest
	// set of both writers is 768 bytes under it, and the totals are the model's
	// IdentityAttributeBytesMax, which has to be where both copies draw the line.
	check("exactly the byte total", bytesOf(model.IdentityAttributeBytesMax), true)
	check("one byte past the byte total", bytesOf(model.IdentityAttributeBytesMax+1), false)
}

// TestEachWritersLargestKeyAndValueAreKeysAndValuesTheExitCheckKnows holds that
// what each writer may put in an entry is spelt as the exit check spells a key
// and bounds a value: the longest account key and the longest idp.* key are keys
// to it, one byte more than the exit check's own bound is not, and a value at
// each writer's bound is admitted.
func TestEachWritersLargestKeyAndValueAreKeysAndValuesTheExitCheckKnows(t *testing.T) {
	d := exitTable(t)
	capable := backendNamed(t, d, "capable")
	idpKey := model.ReservedPrefixIDP + strings.Repeat("a", model.SessionIDPKeyMax-len(model.ReservedPrefixIDP))
	for _, c := range []struct {
		name string
		m    map[string]string
		want bool
	}{
		{"the longest account key", map[string]string{strings.Repeat("a", model.AttributeKeyMax): "v"}, true},
		{"the longest account key and one byte more", map[string]string{strings.Repeat("a", model.AttributeKeyMax+1): "v"}, false},
		{"the longest account value", map[string]string{"k": strings.Repeat("v", model.AccountAttributeValueMax)}, true},
		{"the longest account value and one byte more", map[string]string{"k": strings.Repeat("v", model.AccountAttributeValueMax+1)}, false},
		{"the longest idp key", map[string]string{idpKey: "v"}, true},
		{"the longest idp value", map[string]string{"idp.k": strings.Repeat("v", model.SessionIDPValueMax)}, true},
		{"an idp value of a cut multi-byte character", map[string]string{"idp.k": model.TruncateUTF8(strings.Repeat("用", 100), model.SessionIDPValueMax)}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := wellFormed()
			p.Attributes = c.m
			_, reason := checkPrincipal(p, anchor, capable, d)
			if got := reason == ""; got != c.want {
				t.Errorf("the exit check accepts it = %v (reason %q), want %v", got, reason, c.want)
			}
			if got := stampedByAuthntest(p, capable.name); got != c.want {
				t.Errorf("authntest accepts it = %v, want %v", got, c.want)
			}
		})
	}
	// What the model admits as an account key or an idp key is a key the exit
	// check admits: the bound is the exit check's own at the account side, and the
	// idp side is tighter.
	for _, k := range []string{strings.Repeat("a", model.AttributeKeyMax), idpKey, "a.b_c-d9"} {
		if !attributeKey(k) {
			t.Errorf("the exit check does not take %q as a key, which the model admits", k)
		}
	}
}
