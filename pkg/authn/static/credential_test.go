package static

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
)

func credential(raw string) authn.Credential {
	return authn.Credential{Scheme: "basic", Value: base64.StdEncoding.EncodeToString([]byte(raw))}
}

// TestAMalformedCredentialIsRefusedBeforeAnythingIsLookedUpOrCounted holds that a bad credential derives nothing and counts against nobody.
func TestAMalformedCredentialIsRefusedBeforeAnythingIsLookedUpOrCounted(t *testing.T) {
	empty, err := HashPBKDF2("", 2)
	if err != nil {
		t.Fatal(err)
	}
	b := backendOf(t, moduleConfig{
		Subjects:    map[string]string{"alice": empty, "bob": cheapEntry(t, "bobs")},
		CacheSize:   16,
		Concurrency: 2,
	})
	derived := counting(b)
	fixed := b.throttle.Len()

	if got := b.Verify(context.Background(), credential("alice:")); outcome(got) != verdict.Authenticated {
		t.Fatalf("the empty password of a subject whose password is empty reads %v, want authenticated", outcome(got))
	}
	if derived.Load() != 1 {
		t.Fatalf("the control derived %d times, want once", derived.Load())
	}

	for name, c := range map[string]authn.Credential{
		"no colon, and a subject whose password is empty": credential("alice"),
		"no colon":                credential("bob"),
		"no user and no password": credential(":"),
		"no user":                 credential(":bobs"),
		"not base64":              {Scheme: "basic", Value: "not base64!"},
		"another scheme":          {Scheme: "bearer", Value: credential("bob:bobs").Value},
		"a cookie":                {Cookie: "sid", Value: credential("bob:bobs").Value},
		"the scheme in capitals":  {Scheme: "Basic", Value: credential("bob:bobs").Value},
	} {
		for range 12 {
			got := b.Verify(context.Background(), c)
			if outcome(got) != verdict.Denied {
				t.Fatalf("%s: reads %v, want denied", name, outcome(got))
			}
			if errors.Is(cause(got), authn.ErrThrottled) {
				t.Fatalf("%s: a malformed credential is held back as though it were a name that failed", name)
			}
		}
	}
	if derived.Load() != 1 {
		t.Errorf("malformed credentials brought the derivations to %d, want the 1 of the control", derived.Load())
	}
	if got := b.throttle.Len(); got != fixed {
		t.Errorf("malformed credentials left %d keys in the back-off, want the %d fixed ones: they were counted as failed attempts", got, fixed)
	}
}

// the credential is standard base64, which has '+' and '/' in it.
func TestABasicCredentialIsReadInTheStandardAlphabet(t *testing.T) {
	const plaintext = "p>>??pass~~>?"
	b := tableWith(t, map[string]string{"alice": plaintext})
	for _, raw := range []string{"alice:" + plaintext, "alice:" + plaintext + "a", "alice:" + plaintext + "ab"} {
		enc := base64.StdEncoding.EncodeToString([]byte(raw))
		if !strings.ContainsAny(enc, "+/") {
			t.Fatalf("the precondition fails: %q has no character the URL alphabet spells differently", enc)
		}
		want := verdict.Denied
		if raw == "alice:"+plaintext {
			want = verdict.Authenticated
		}
		if got := b.Verify(context.Background(), authn.Credential{Scheme: "basic", Value: enc}); outcome(got) != want {
			t.Errorf("%q reads %v (%v), want %v", raw, outcome(got), cause(got), want)
		}
	}
}
