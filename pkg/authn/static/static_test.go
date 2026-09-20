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

// cheapHash makes a hash at a cost a test can afford. The iteration count
// travels inside the hash, so what is exercised here is the same code path a
// production table takes, at a cost the default would not be.
func cheapHash(t *testing.T, password string) string {
	t.Helper()
	encoded, err := encode(2, []byte("0123456789abcdef"), password)
	if err != nil {
		t.Fatalf("deriving a hash for this case: %v", err)
	}
	return encoded
}

// tableWith builds a backend holding the subjects named, each with the given
// password.
func tableWith(t *testing.T, name string, subjects map[string]string) *backend {
	t.Helper()
	cfg := moduleConfig{Name: name, Subjects: make(map[string]string, len(subjects))}
	for subject, password := range subjects {
		cfg.Subjects[subject] = cheapHash(t, password)
	}
	set, err := cfg.resolve()
	if err != nil {
		t.Fatalf("resolving the configuration for this case: %v", err)
	}
	return newBackend(set)
}

// basic renders a Basic credential the way a caller sends one.
func basic(user, password string) authn.Credential {
	return authn.Credential{
		Scheme: "Basic",
		Value:  base64.StdEncoding.EncodeToString([]byte(user + ":" + password)),
	}
}

// TestTheRightPasswordAuthenticatesAndTheWrongOneDoesNot holds what this
// backend is for, and holds the refusal apart from the pass.
func TestTheRightPasswordAuthenticatesAndTheWrongOneDoesNot(t *testing.T) {
	b := tableWith(t, "local", map[string]string{"alice": "correct horse"})

	t.Run("the password in the table", func(t *testing.T) {
		got := b.Verify(context.Background(), "alice", basic("alice", "correct horse"))
		if verdict.OutcomeOf(got) != verdict.Authenticated {
			t.Fatalf("the verdict reads %v, want authenticated: %v",
				verdict.OutcomeOf(got), verdict.CauseOf(got))
		}
		if subject := verdict.SubjectOf(got); subject != "alice" {
			t.Errorf("the verdict carries the subject %q, want %q", subject, "alice")
		}
	})

	t.Run("a password that is not", func(t *testing.T) {
		got := b.Verify(context.Background(), "alice", basic("alice", "correct hors"))
		if verdict.OutcomeOf(got) != verdict.Denied {
			t.Errorf("the verdict reads %v, want denied", verdict.OutcomeOf(got))
		}
	})
}

// TestThisBackendNeverAnswersUnavailable holds the one thing a backend must not
// get wrong, from the other direction.
//
// A refusal and an outage are the distinction every lockout, rate limiter and
// alert rule is keyed off. This backend's table is in memory from startup, so
// there is no moment at which it is present but unable to say — and so no case
// in which it may answer as though there were.
func TestThisBackendNeverAnswersUnavailable(t *testing.T) {
	b := tableWith(t, "local", map[string]string{"alice": "secret"})

	for _, c := range []struct {
		name       string
		subject    string
		credential authn.Credential
	}{
		{
			"a scheme this backend does not read", "alice",
			authn.Credential{Scheme: "Bearer", Value: "a.token"},
		},
		{
			"a credential that is not base64", "alice",
			authn.Credential{Scheme: "Basic", Value: "not base64!"},
		},
		{
			"a credential with no colon in it", "alice",
			authn.Credential{Scheme: "Basic", Value: base64.StdEncoding.EncodeToString([]byte("alice"))},
		},
		{"a subject that is not in the table", "mallory", basic("mallory", "secret")},
		{"the wrong password", "alice", basic("alice", "wrong")},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := b.Verify(context.Background(), c.subject, c.credential)
			if verdict.OutcomeOf(got) != verdict.Denied {
				t.Errorf("the verdict reads %v, want denied: this backend is never unable to "+
					"say, so every no it gives is about the credential",
					verdict.OutcomeOf(got))
			}
		})
	}
}

// TestACredentialCannotVouchForSomebodyElse holds the check that keeps this
// backend from being turned into a confused deputy.
//
// Nothing on the gateway's path produces a mismatch today. The check is what
// stops a future caller — another gateway, a test harness, a later refactor —
// from having this backend confirm one subject using another's credential.
func TestACredentialCannotVouchForSomebodyElse(t *testing.T) {
	// The two accounts share a password on purpose. With different ones the
	// hash comparison would turn this away by itself and the case would
	// hold nothing: what is being held is that the credential has to name
	// the subject it is checked against, not that bob's password fails
	// against alice's hash.
	const shared = "the same password"
	b := tableWith(t, "local", map[string]string{"alice": shared, "bob": shared})

	got := b.Verify(context.Background(), "alice", basic("bob", shared))
	if verdict.OutcomeOf(got) != verdict.Denied {
		t.Errorf("a credential naming bob authenticated alice (%v), so a caller holding one "+
			"account could be confirmed as another wherever the two share a password",
			verdict.OutcomeOf(got))
	}
	if subject := verdict.SubjectOf(got); subject != "" {
		t.Errorf("the refusal carries the subject %q, want none", subject)
	}
}

// TestAnOwnerQualifiedUserNamesTheSameSubject holds the writing form a caller
// uses under several backends. The gateway has already resolved the owner by
// the time the credential arrives here, so the qualification is stripped rather
// than compared.
func TestAnOwnerQualifiedUserNamesTheSameSubject(t *testing.T) {
	b := tableWith(t, "local", map[string]string{"alice": "secret"})

	got := b.Verify(context.Background(), "alice", basic("local/alice", "secret"))
	if verdict.OutcomeOf(got) != verdict.Authenticated {
		t.Errorf("the qualified writing form reads %v, want authenticated: it names the same "+
			"subject as the bare one", verdict.OutcomeOf(got))
	}
}

// TestACredentialQualifiedWithAnotherBackendIsRefused holds the second half of
// the check that keeps this backend from being a confused deputy.
//
// A credential has to name what it is being checked against in both halves. The
// subject comparison covers one; without this, local/alice and anyone-at-all/alice
// would be the same credential here, and a backend that accepts a name it never
// answered to is one that can be handed somebody else's credential and made to
// vouch for it. The gateway settles ownership before the call and turns a wrong
// name away first, so this is depth rather than the only line.
func TestACredentialQualifiedWithAnotherBackendIsRefused(t *testing.T) {
	b := tableWith(t, "local", map[string]string{"alice": "secret"})

	for _, written := range []string{"other/alice", "OTHER/alice", "/alice", "local/other/alice"} {
		t.Run(written, func(t *testing.T) {
			got := b.Verify(context.Background(), "alice", basic(written, "secret"))
			if verdict.OutcomeOf(got) != verdict.Denied {
				t.Errorf("a credential written %q reads %v against this backend, want denied: "+
					"it names a backend this one is not", written, verdict.OutcomeOf(got))
			}
		})
	}
}

// TestTheClaimIsTheTableWrittenOut holds what this backend tells the gateway
// about itself. A claim wider than the table would have the gateway route
// subjects here that this backend has nothing to say about, and an open claim
// would have it answer for subjects a file cannot know about.
func TestTheClaimIsTheTableWrittenOut(t *testing.T) {
	b := tableWith(t, "local", map[string]string{"alice": "a", "bob": "b"})

	claim := b.Claim()
	if got := strings.Join(claim.Subjects, ","); got != "alice,bob" {
		t.Errorf("the claim enumerates %q, want the table's subjects in a stable order", got)
	}
	if claim.Open {
		t.Error("the claim is open, but a table read from a file has nothing to say about a " +
			"subject that is not in it")
	}
	if len(claim.Schemes) != 0 {
		t.Errorf("the claim takes the schemes %v whole, want none: claiming a scheme would "+
			"route every credential under it here, including subjects this backend lacks",
			claim.Schemes)
	}
}

// TestAConfigurationThisBackendCannotRunOnIsRefusedAtStartup holds the
// validations that turn a request-time surprise into a startup failure.
func TestAConfigurationThisBackendCannotRunOnIsRefusedAtStartup(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  moduleConfig
		want error
	}{
		{
			name: "a name that is blank",
			cfg:  moduleConfig{Name: "  ", Subjects: map[string]string{"alice": "x"}},
			want: authn.ErrInvalidClaim,
		},
		{
			name: "a name carrying the separator",
			cfg:  moduleConfig{Name: "a/b", Subjects: map[string]string{"alice": "x"}},
			want: authn.ErrInvalidClaim,
		},
		{
			name: "a subject that is blank",
			cfg:  moduleConfig{Name: "local", Subjects: map[string]string{"  ": "x"}},
			want: authn.ErrInvalidClaim,
		},
		{
			name: "a subject carrying the separator",
			cfg:  moduleConfig{Name: "local", Subjects: map[string]string{"a/b": "x"}},
			want: authn.ErrInvalidClaim,
		},
		{
			name: "a password hash nothing can read",
			cfg:  moduleConfig{Name: "local", Subjects: map[string]string{"alice": "plaintext"}},
			want: ErrMalformedHash,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.cfg.resolve()
			if !errors.Is(err, c.want) {
				t.Fatalf("resolving gave %v, want one wrapping %v", err, c.want)
			}
		})
	}
}

// TestAnUnusableHashNeverReachesAnErrorMessage holds the one thing an error
// about a credential table must not do.
func TestAnUnusableHashNeverReachesAnErrorMessage(t *testing.T) {
	const looksLikeASecret = "pbkdf2-sha256$2$AAAA$NOT-BASE64!!"

	cfg := moduleConfig{Name: "local", Subjects: map[string]string{"alice": looksLikeASecret}}
	_, err := cfg.resolve()
	if err == nil {
		t.Fatal("an unreadable hash resolved without complaint")
	}
	if strings.Contains(err.Error(), looksLikeASecret) || strings.Contains(err.Error(), "AAAA") {
		t.Errorf("the failure reads %q and echoes the stored hash, which is the secret this "+
			"table exists to hold", err)
	}
}

// TestABackendWithNobodyToVouchForStandsDownWithAReasonNamingItself holds the
// stance a backend has to be able to take, and what its reason has to carry.
//
// Under an assembly with several backends, that line is the only thing telling
// an operator which of them stood down and what it lacked.
func TestABackendWithNobodyToVouchForStandsDownWithAReasonNamingItself(t *testing.T) {
	reason := standDownReason()

	for _, want := range []string{ModuleName, ConfigNamespace + "." + subjectsItem} {
		if !strings.Contains(reason, want) {
			t.Errorf("the stand-down reason reads %q and does not carry %q", reason, want)
		}
	}
}
