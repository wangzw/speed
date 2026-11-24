package gate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/model"
)

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

// TestTheModelsTotalsAreTheExitChecksOwn holds the model's repeated totals equal
// to the constants the exit check enforces.
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

// TestTheSubBoundsAddUpToLessThanTheStampedTotals holds the two writers'
// sub-bounds to totals the exit check admits.
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

// TestTheLargestSetOfBothWritersPassesTheExitCheckAndAuthntest admits the merged
// worst set to the exit check and authntest.
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

	over := map[string]string{}
	for k, v := range merged {
		over[k] = v
	}
	over["one-more"] = "v"
	check("one entry past the total", over, false)

	check("exactly the byte total", bytesOf(model.IdentityAttributeBytesMax), true)
	check("one byte past the byte total", bytesOf(model.IdentityAttributeBytesMax+1), false)
}

// TestEachWritersLargestKeyAndValueAreKeysAndValuesTheExitCheckKnows admits each
// writer's largest key and value, and refuses one byte more.
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
	for _, k := range []string{strings.Repeat("a", model.AttributeKeyMax), idpKey, "a.b_c-d9"} {
		if !attributeKey(k) {
			t.Errorf("the exit check does not take %q as a key, which the model admits", k)
		}
	}
}
