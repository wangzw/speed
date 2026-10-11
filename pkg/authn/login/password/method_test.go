package password

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// The method is driven here the way its contract says it is tested: a Start, a
// FlowState and a StepInput are built by hand, and the kind of answer is read.

func TestTheMethodIsNamedPassword(t *testing.T) {
	if got := newRig().m.Name(); got != "password" {
		t.Errorf("Name() = %q, want password", got)
	}
}

func TestAMissingFieldIsAskedForAndNothingIsCalled(t *testing.T) {
	for name, fields := range map[string]map[string]string{
		"no fields":           nil,
		"empty map":           {},
		"identifier only":     {fieldIdentifier: "alice"},
		"password only":       {fieldPassword: sentinelPassword},
		"empty identifier":    credentialFields("", sentinelPassword),
		"blank identifier":    credentialFields(" \t ", sentinelPassword),
		"empty password":      credentialFields("alice", ""),
		"unrelated fields":    {"phone": "+8613800000000"},
		"new password at 1st": {fieldNewPassword: sentinelNew},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig()
			for label, got := range map[string]authn.Step{
				"begin":  must(r.m.Begin(t.Context(), loginStart(fields))),
				"answer": must(r.m.Answer(t.Context(), flowAt(stepCredentials, nil), submit(fields))),
			} {
				if kindOf(got) != step.KindPrompt || step.NameOf(got) != stepCredentials {
					t.Fatalf("%s: answered %s, want a prompt for the credentials", label, describe(got))
				}
				want := []authn.Field{
					{Name: "identifier", Kind: authn.FieldText, Required: true},
					{Name: "password", Kind: authn.FieldPassword, Required: true, Secret: true},
				}
				if fs := step.FieldsOf(got); !slices.Equal(fs, want) {
					t.Errorf("%s: fields = %+v, want %+v", label, fs, want)
				}
				if p := step.PrivateOf(got); len(p) != 0 {
					t.Errorf("%s: a prompt for the credentials carries state %q", label, p)
				}
			}
			if calls := r.l.all(); len(calls) != 0 {
				t.Errorf("a request with a field missing reached a dependency: %+v", calls)
			}
		})
	}
}

// must keeps the error of a call that is not about it out of a table.
func must(s authn.Step, err error) authn.Step {
	if err != nil {
		panic(err)
	}
	return s
}

func TestACredentialThatHoldsIsProvenForThePasswordMethod(t *testing.T) {
	for label, run := range map[string]func(r *rig) authn.Step{
		"begin": func(r *rig) authn.Step {
			return must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", sentinelPassword))))
		},
		"answer": func(r *rig) authn.Step {
			return must(r.m.Answer(t.Context(), flowAt(stepCredentials, nil), submit(credentialFields("alice", sentinelPassword))))
		},
	} {
		t.Run(label, func(t *testing.T) {
			got := run(newRig())
			if kindOf(got) != step.KindProven {
				t.Fatalf("answered %s, want a proof", describe(got))
			}
			p := step.ProofOf(got)
			if p.Account != testAccount {
				t.Errorf("Account = %q, want %q", p.Account, testAccount)
			}
			if !slices.Equal(p.Methods, []authn.Method{authn.MethodPassword}) {
				t.Errorf("Methods = %v, want [pwd]", p.Methods)
			}
			// A proof reports the facts the method proved and no more: the account
			// came from the check, not from an identifier or an external identity,
			// nothing here says the factor counts as two, and a credential that did
			// not carry must_change replaced no password.
			if p.Identifier != nil || p.External != nil || p.UserVerified || p.IDPAsserted || p.Provider != "" || len(p.Aliases) != 0 || p.PasswordChanged {
				t.Errorf("the proof claims more than a password check proves: %+v", p)
			}
		})
	}
}

func TestTheCredentialReachesThePasswordCapabilityAsSubmitted(t *testing.T) {
	r := newRig()
	const identifier, secret = "  Alice@Example.COM ", " " + sentinelPassword + " "
	r.pw.verify = func(_ context.Context, id, pw string) (authn.AccountID, authn.VerifyResult, error) {
		return "", authn.VerifyResult{}, authn.ErrInvalidCredentials
	}
	r.m.Begin(t.Context(), loginStart(credentialFields(identifier, secret)))
	var got call
	for _, c := range r.l.all() {
		if c.op == "verify" {
			got = c
		}
	}
	if got.identifier != identifier || got.password != secret {
		t.Errorf("Verify saw (%q, %q), want the strings as submitted: the account module normalizes the identifier and derives from the password as it is", got.identifier, got.password)
	}
}

func TestOnlyASubmittedAnswerAtANamedStepIsTaken(t *testing.T) {
	cases := map[string]struct {
		flow authn.FlowState
		in   authn.StepInput
	}{
		"a callback":        {flowAt(stepCredentials, nil), authn.StepInput{Action: authn.InputCallback, Fields: credentialFields("alice", sentinelPassword)}},
		"no action":         {flowAt(stepCredentials, nil), authn.StepInput{Fields: credentialFields("alice", sentinelPassword)}},
		"an unnamed step":   {flowAt("", nil), submit(credentialFields("alice", sentinelPassword))},
		"another step name": {flowAt("code", nil), submit(credentialFields("alice", sentinelPassword))},
		"no purpose":        {authn.FlowState{Step: stepCredentials}, submit(credentialFields("alice", sentinelPassword))},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig()
			got := must(r.m.Answer(t.Context(), c.flow, c.in))
			if kindOf(got) != step.KindRefused {
				t.Errorf("answered %s, want a refusal", describe(got))
			}
			if calls := r.l.all(); len(calls) != 0 {
				t.Errorf("a refused answer reached a dependency: %+v", calls)
			}
		})
	}
}

func TestOnlyALoginAndAStepUpAreServed(t *testing.T) {
	for purpose, served := range map[string]bool{
		authn.PurposeLogin:  true,
		authn.PurposeStepUp: true,
		authn.PurposeLink:   false,
		authn.PurposeEnroll: false,
		"":                  false,
		"admin":             false,
	} {
		t.Run(purpose, func(t *testing.T) {
			r := newRig()
			got := must(r.m.Begin(t.Context(), authn.Start{Purpose: purpose, Fields: credentialFields("alice", sentinelPassword)}))
			if served {
				if kindOf(got) != step.KindProven {
					t.Errorf("answered %s, want a proof", describe(got))
				}
				return
			}
			if kindOf(got) != step.KindRefused {
				t.Errorf("answered %s, want a refusal", describe(got))
			}
			if calls := r.l.all(); len(calls) != 0 {
				t.Errorf("a purpose that is not served counted or checked something: %+v", calls)
			}
			// The same for an answer in a flow of that purpose.
			flow := flowAt(stepCredentials, nil)
			flow.Purpose = purpose
			if a := must(r.m.Answer(t.Context(), flow, submit(credentialFields("alice", sentinelPassword)))); kindOf(a) != step.KindRefused {
				t.Errorf("an answer in a %q flow was %s, want a refusal", purpose, describe(a))
			}
			if calls := r.l.all(); len(calls) != 0 {
				t.Errorf("an answer in a flow that is not served reached a dependency: %+v", calls)
			}
		})
	}
}

// What follows is the back-off shape: the key is read before anything is
// derived, a success clears it, a refusal counts, and what is not a guess
// counts for nothing.

func TestTheBackOffIsReadBeforeTheCheckAndSettledAfterIt(t *testing.T) {
	r := newRig()
	must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", sentinelPassword))))
	if got, want := r.l.ops(), []string{"check", "verify", "succeed"}; !slices.Equal(got, want) {
		t.Errorf("a credential that holds made %v, want %v", got, want)
	}

	r = newRig()
	must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", "wrong"))))
	if got, want := r.l.ops(), []string{"check", "verify", "fail"}; !slices.Equal(got, want) {
		t.Errorf("a credential that fails made %v, want %v", got, want)
	}
}

func TestEveryCallToTheBackOffUsesTheIdentifierScopeAndOneKey(t *testing.T) {
	for label, password := range map[string]string{"success": sentinelPassword, "failure": "wrong"} {
		t.Run(label, func(t *testing.T) {
			r := newRig()
			must(r.m.Begin(t.Context(), loginStart(credentialFields("Alice", password))))
			var keys []string
			for _, c := range r.l.all() {
				if c.op == "verify" {
					continue
				}
				if c.scope != authn.ScopeIdentifier {
					t.Errorf("%s was made in scope %q, want %q", c.op, c.scope, authn.ScopeIdentifier)
				}
				keys = append(keys, c.key)
			}
			if len(keys) != 2 || keys[0] != keys[1] {
				t.Errorf("the check and the settlement used keys %q, want one key twice", keys)
			}
		})
	}
}

func TestABackOffInForceIsAnsweredWithoutADerivation(t *testing.T) {
	for _, wait := range []time.Duration{time.Second, 17 * time.Minute, time.Nanosecond, -time.Second} {
		t.Run(wait.String(), func(t *testing.T) {
			r := newRig()
			r.th.wait = wait
			got := must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", sentinelPassword))))
			if kindOf(got) != step.KindThrottled {
				t.Fatalf("answered %s, want a throttle", describe(got))
			}
			if want := max(wait, 0); wait > 0 && step.RetryAfterOf(got) != want {
				t.Errorf("retry after = %s, want %s", step.RetryAfterOf(got), want)
			}
			if got, want := r.l.ops(), []string{"check"}; !slices.Equal(got, want) {
				t.Errorf("a key in back-off made %v, want %v: nothing is derived and nothing is counted", got, want)
			}
		})
	}
}

func TestACorrectPasswordInsideABackOffIsStillNotChecked(t *testing.T) {
	r := newRig()
	r.th.wait = time.Minute
	got := must(r.m.Answer(t.Context(), flowAt(stepCredentials, nil), submit(credentialFields("alice", sentinelPassword))))
	if kindOf(got) == step.KindProven {
		t.Fatal("a credential inside a back-off was proven")
	}
	if r.l.count("verify") != 0 {
		t.Error("Passwords.Verify was called for a key in back-off")
	}
}

func TestABackOffThatCannotBeReadOrWrittenIsNeverAPass(t *testing.T) {
	boom := errors.New("kv down")
	cases := map[string]struct {
		set  func(r *rig)
		pass string
		want []string
	}{
		"check fails": {func(r *rig) { r.th.checkErr = boom }, sentinelPassword, []string{"check"}},
		// A refusal that cannot be counted is not a refusal the client may have: the
		// guess would go uncounted.
		"fail fails": {func(r *rig) { r.th.failErr = boom }, "wrong", []string{"check", "verify", "fail"}},
		// A proof with the back-off left standing is not handed over either.
		"succeed fails": {func(r *rig) { r.th.succeedErr = boom }, sentinelPassword, []string{"check", "verify", "succeed"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig()
			c.set(r)
			got := must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", c.pass))))
			if kindOf(got) != step.KindUnavailable {
				t.Fatalf("answered %s, want an outage", describe(got))
			}
			if !errors.Is(step.CauseOf(got), boom) {
				t.Errorf("the cause %v lost the dependency's error", step.CauseOf(got))
			}
			if ops := r.l.ops(); !slices.Equal(ops, c.want) {
				t.Errorf("made %v, want %v", ops, c.want)
			}
		})
	}
}

func TestACheckThatFailsToBeginIsNotAGuess(t *testing.T) {
	cases := map[string]struct {
		err      error
		wantKind step.Kind
	}{
		"busy":              {fmt.Errorf("derivation: %w", authn.ErrBusy), step.KindUnavailable},
		"unavailable":       {fmt.Errorf("db: %w", authn.ErrUnavailable), step.KindUnavailable},
		"anything else":     {errors.New("boom"), step.KindUnavailable},
		"a malformed ident": {authn.ErrInvalidIdentifier, step.KindRefused},
		"cancelled":         {context.Canceled, step.KindUnavailable},
		"deadline":          {context.DeadlineExceeded, step.KindUnavailable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig()
			r.pw.verify = func(context.Context, string, string) (authn.AccountID, authn.VerifyResult, error) {
				return "", authn.VerifyResult{}, c.err
			}
			got := must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", "x"))))
			if kindOf(got) != c.wantKind {
				t.Fatalf("answered %s, want kind %s", describe(got), c.wantKind)
			}
			if !errors.Is(step.CauseOf(got), c.err) {
				t.Errorf("the cause %v does not carry %v: the engine reads ErrBusy out of it", step.CauseOf(got), c.err)
			}
			if got := r.l.ops(); !slices.Equal(got, []string{"check", "verify"}) {
				t.Errorf("made %v, want a check and a derivation and no count either way", got)
			}
		})
	}
}

func TestAnAccountThePasswordCapabilityMayNotAnswerForIsNotProven(t *testing.T) {
	for _, bad := range []authn.AccountID{"", "static.ops", "has space", authn.AccountID(strings.Repeat("a", 65))} {
		t.Run(string(bad), func(t *testing.T) {
			r := newRig()
			r.pw.verify = func(context.Context, string, string) (authn.AccountID, authn.VerifyResult, error) {
				return bad, authn.VerifyResult{}, nil
			}
			got := must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", "x"))))
			if kindOf(got) == step.KindProven || kindOf(got) == step.KindPrompt {
				t.Fatalf("answered %s for account %q", describe(got), bad)
			}
			if r.l.count("succeed") != 0 {
				t.Error("the back-off was cleared for an answer that was not taken")
			}
		})
	}
}

// What follows is the enumeration guard: whatever made a credential fail, the
// answer is one.

func TestEveryWayACredentialFailsIsOneAnswer(t *testing.T) {
	causes := map[string]error{
		"unknown identifier": authn.ErrInvalidCredentials,
		"wrong password":     fmt.Errorf("mismatch: %w", authn.ErrInvalidCredentials),
		"disabled account":   fmt.Errorf("account %s is disabled: %w", testAccount, authn.ErrInvalidCredentials),
		"no password":        fmt.Errorf("no credential: %w", authn.ErrInvalidCredentials),
		"deleted account":    authn.ErrAccountNotFound,
	}
	var first string
	var firstOps []string
	for _, name := range []string{"unknown identifier", "wrong password", "disabled account", "no password", "deleted account"} {
		cause := causes[name]
		r := newRig()
		r.pw.verify = func(context.Context, string, string) (authn.AccountID, authn.VerifyResult, error) {
			return "", authn.VerifyResult{}, cause
		}
		got := must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", "guess"))))
		if kindOf(got) != step.KindRefused {
			t.Fatalf("%s: answered %s, want a refusal", name, describe(got))
		}
		if !errors.Is(step.CauseOf(got), authn.ErrInvalidCredentials) {
			t.Errorf("%s: the cause %v is not in the class of invalid credentials", name, step.CauseOf(got))
		}
		// The whole of what the answer carries, the text of the cause included, is
		// the same: the log does not tell the cases apart either.
		if first == "" {
			first, firstOps = describe(got), r.l.ops()
		} else if describe(got) != first {
			t.Errorf("%s: answered\n  %s\nwant\n  %s", name, describe(got), first)
		}
		if ops := r.l.ops(); !slices.Equal(ops, firstOps) || !slices.Equal(ops, []string{"check", "verify", "fail"}) {
			t.Errorf("%s: made %v, want the same calls for every cause, ending in a counted failure", name, ops)
		}
	}
}

func TestARefusalIsTheSameAtTheSecondStep(t *testing.T) {
	r := newRig()
	first := must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", "wrong"))))
	second := must(r.m.Answer(t.Context(), flowAt(stepCredentials, nil), submit(credentialFields("alice", "wrong"))))
	if describe(first) != describe(second) {
		t.Errorf("a refusal at Begin is %s and at Answer %s", describe(first), describe(second))
	}
}

// What follows is the must_change path.

// directory is a Passwords with the state a must_change path needs: one account,
// its password, whether it carries must_change, and a policy.
type directory struct {
	mu         sync.Mutex
	password   string
	mustChange bool
}

func (d *directory) verify(_ context.Context, _, password string) (authn.AccountID, authn.VerifyResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if password != d.password {
		return "", authn.VerifyResult{}, authn.ErrInvalidCredentials
	}
	return testAccount, authn.VerifyResult{MustChange: d.mustChange}, nil
}

func (d *directory) set(_ context.Context, id authn.AccountID, password string, opt authn.SetPasswordOptions) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id != testAccount {
		return authn.ErrAccountNotFound
	}
	if len(password) < 12 || password == d.password {
		return fmt.Errorf("the policy refuses it: %w", authn.ErrPasswordPolicy)
	}
	d.password, d.mustChange = password, opt.MustChange
	return nil
}

func mustChangeRig(current string) (*rig, *directory) {
	r := newRig()
	d := &directory{password: current, mustChange: true}
	r.pw.verify, r.pw.set = d.verify, d.set
	return r, d
}

func TestACredentialThatMustChangeIsNotProvenByItsPassword(t *testing.T) {
	r, _ := mustChangeRig(sentinelPassword)
	got := must(r.m.Begin(t.Context(), loginStart(credentialFields(sentinelIdent, sentinelPassword))))
	if kindOf(got) != step.KindPrompt || step.NameOf(got) != stepNewPassword {
		t.Fatalf("answered %s, want a prompt for the new password", describe(got))
	}
	if want := []authn.Field{{Name: "new_password", Kind: authn.FieldPassword, Required: true, Secret: true}}; !slices.Equal(step.FieldsOf(got), want) {
		t.Errorf("fields = %+v, want %+v", step.FieldsOf(got), want)
	}
	private := string(step.PrivateOf(got))
	if private == "" {
		t.Error("the prompt carries no state, so the next step could not tell the password was verified")
	}
	for _, secret := range []string{sentinelPassword, sentinelIdent} {
		if strings.Contains(private, secret) {
			t.Errorf("the state %q holds %q", private, secret)
		}
	}
	if p := step.ProofOf(got); p.Account != "" {
		t.Errorf("the prompt already names account %q", p.Account)
	}
	if r.l.count("set") != 0 {
		t.Error("a password was written before the client sent one")
	}
	// The password was verified, so the identifier's failures are cleared; nothing
	// was counted against it.
	if got, want := r.l.ops(), []string{"check", "verify", "succeed"}; !slices.Equal(got, want) {
		t.Errorf("made %v, want %v", got, want)
	}
}

func TestANewPasswordTheEngineHandsBackEndsInAProofOnlyWhenItIsSet(t *testing.T) {
	r, d := mustChangeRig(sentinelPassword)
	prompt := must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", sentinelPassword))))
	flow := flowAt(stepNewPassword, step.PrivateOf(prompt))

	// A policy violation keeps the flow: the same step is asked again with the
	// same state, and the error says why, as a hint the client can answer.
	for name, weak := range map[string]string{"too short": "short", "the current password": sentinelPassword} {
		got, err := r.m.Answer(t.Context(), flow, submit(map[string]string{fieldNewPassword: weak}))
		if !errors.Is(err, authn.ErrPasswordPolicy) {
			t.Errorf("%s: error = %v, want ErrPasswordPolicy", name, err)
		}
		if kindOf(got) != step.KindPrompt || step.NameOf(got) != stepNewPassword {
			t.Fatalf("%s: answered %s, want the new password asked for again", name, describe(got))
		}
		if string(step.PrivateOf(got)) != string(flow.Private) {
			t.Errorf("%s: the state changed to %q, want it kept as it stands", name, step.PrivateOf(got))
		}
		if d.password != sentinelPassword || !d.mustChange {
			t.Errorf("%s: the stored credential changed", name)
		}
	}

	got, err := r.m.Answer(t.Context(), flow, submit(map[string]string{fieldNewPassword: sentinelNew}))
	if err != nil {
		t.Fatalf("a password that satisfies the policy failed: %v", err)
	}
	if kindOf(got) != step.KindProven {
		t.Fatalf("answered %s, want a proof", describe(got))
	}
	if p := step.ProofOf(got); p.Account != testAccount || !slices.Equal(p.Methods, []authn.Method{authn.MethodPassword}) {
		t.Errorf("proof = %+v, want account %q by password", p, testAccount)
	}
	if p := step.ProofOf(got); !p.PasswordChanged {
		t.Errorf("the proof does not report the password it just replaced: %+v", p)
	}
	if d.password != sentinelNew || d.mustChange {
		t.Error("the new password is not in force, or the credential still says it must change")
	}
	var set []call
	for _, c := range r.l.all() {
		if c.op == "set" {
			set = append(set, c)
		}
	}
	if len(set) != 3 || set[2].key != testAccount.String() {
		t.Errorf("Set calls = %+v, want three, the last for %q", set, testAccount)
	}
	// The new password is not a guess at the identifier's credential.
	if r.l.count("fail") != 0 {
		t.Error("a policy refusal of the new password was counted as a failed guess")
	}
}

func TestNothingIsProvenAtTheNewPasswordStepWithoutTheVerifiedState(t *testing.T) {
	marker := func(v int, account string) []byte {
		return fmt.Appendf(nil, `{"v":%d,"account":%q}`, v, account)
	}
	cases := map[string][]byte{
		"no state":              nil,
		"empty state":           {},
		"not json":              []byte("verified"),
		"an empty object":       []byte(`{}`),
		"the wrong version":     marker(2, string(testAccount)),
		"version zero":          marker(0, string(testAccount)),
		"no account":            marker(1, ""),
		"a static account":      marker(1, "static.ops"),
		"a malformed account":   marker(1, "not an account"),
		"a field of another":    []byte(`{"v":1,"account":"acct_0001","password":"x"}`),
		"trailing data":         append(marker(1, string(testAccount)), []byte(` {"v":1}`)...),
		"state of another kind": []byte(`["acct_0001"]`),
	}
	for name, private := range cases {
		t.Run(name, func(t *testing.T) {
			r, _ := mustChangeRig(sentinelPassword)
			got, err := r.m.Answer(t.Context(), flowAt(stepNewPassword, private), submit(map[string]string{fieldNewPassword: sentinelNew}))
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if kindOf(got) != step.KindRefused {
				t.Errorf("answered %s, want a refusal", describe(got))
			}
			if r.l.count("set") != 0 {
				t.Error("a password was written on state the method did not write")
			}
		})
	}
}

func TestTheNewPasswordIsSetForTheAccountTheStateNames(t *testing.T) {
	r := newRig()
	var gotAccount authn.AccountID
	var gotOpt authn.SetPasswordOptions
	r.pw.set = func(_ context.Context, id authn.AccountID, _ string, opt authn.SetPasswordOptions) error {
		gotAccount, gotOpt = id, opt
		return nil
	}
	got := must(r.m.Answer(t.Context(), flowAt(stepNewPassword, markVerified("acct_other")), submit(map[string]string{fieldNewPassword: sentinelNew})))
	if p := step.ProofOf(got); kindOf(got) != step.KindProven || p.Account != "acct_other" {
		t.Fatalf("answered %s, want a proof for acct_other", describe(got))
	}
	if gotAccount != "acct_other" {
		t.Errorf("Set was called for %q", gotAccount)
	}
	// The password is changed to a settled one: it does not carry must_change on,
	// and the current password is not one this method holds.
	if gotOpt != (authn.SetPasswordOptions{}) {
		t.Errorf("Set options = %+v, want none", gotOpt)
	}
}

func TestAMissingNewPasswordIsAskedForAgainAndWritesNothing(t *testing.T) {
	r, _ := mustChangeRig(sentinelPassword)
	private := markVerified(testAccount)
	got := must(r.m.Answer(t.Context(), flowAt(stepNewPassword, private), submit(map[string]string{})))
	if kindOf(got) != step.KindPrompt || step.NameOf(got) != stepNewPassword || string(step.PrivateOf(got)) != string(private) {
		t.Errorf("answered %s, want the new password asked for again with the state kept", describe(got))
	}
	if len(r.l.all()) != 0 {
		t.Errorf("made calls: %v", r.l.ops())
	}
}

func TestTheNewPasswordStepRefusesWhatIsNotASubmit(t *testing.T) {
	r, _ := mustChangeRig(sentinelPassword)
	in := authn.StepInput{Action: authn.InputCallback, Fields: map[string]string{fieldNewPassword: sentinelNew}}
	if got := must(r.m.Answer(t.Context(), flowAt(stepNewPassword, markVerified(testAccount)), in)); kindOf(got) != step.KindRefused {
		t.Errorf("answered %s, want a refusal", describe(got))
	}
	if r.l.count("set") != 0 {
		t.Error("a callback wrote a password")
	}
}

func TestAFailureToSetTheNewPasswordIsNotAProof(t *testing.T) {
	cases := map[string]struct {
		err  error
		want step.Kind
	}{
		"busy":            {fmt.Errorf("x: %w", authn.ErrBusy), step.KindUnavailable},
		"db":              {fmt.Errorf("x: %w", authn.ErrUnavailable), step.KindUnavailable},
		"account is gone": {authn.ErrAccountNotFound, step.KindRefused},
		"credentials":     {authn.ErrInvalidCredentials, step.KindRefused},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig()
			r.pw.set = func(context.Context, authn.AccountID, string, authn.SetPasswordOptions) error { return c.err }
			got, err := r.m.Answer(t.Context(), flowAt(stepNewPassword, markVerified(testAccount)), submit(map[string]string{fieldNewPassword: sentinelNew}))
			if err != nil {
				t.Errorf("error = %v: only a policy refusal is the client's to retry", err)
			}
			if kindOf(got) != c.want {
				t.Fatalf("answered %s, want kind %s", describe(got), c.want)
			}
			if c.want == step.KindUnavailable && !errors.Is(step.CauseOf(got), c.err) {
				t.Errorf("the cause %v lost %v", step.CauseOf(got), c.err)
			}
		})
	}
}

func TestAPasswordThatMustChangeIsClearedOnceTheOldOneIsKnown(t *testing.T) {
	r, d := mustChangeRig(sentinelPassword)
	prompt := must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", sentinelPassword))))
	must(r.m.Answer(t.Context(), flowAt(stepNewPassword, step.PrivateOf(prompt)), submit(map[string]string{fieldNewPassword: sentinelNew})))
	// The next login with the new password is a plain proof.
	got := must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", sentinelNew))))
	if kindOf(got) != step.KindProven {
		t.Errorf("a login after the change answered %s, want a proof (must_change was %v)", describe(got), d.mustChange)
	}
}

// What follows is the secrecy rule: a password is in no answer, no error and no
// log line.

func TestNoSecretReachesAnAnswerAnErrorOrALogLine(t *testing.T) {
	logs := captureLogs(t)
	boom := errors.New("backend failure")

	type run struct {
		name string
		do   func() (authn.Step, error)
	}
	var runs []run
	add := func(name string, setup func(r *rig), do func(r *rig) (authn.Step, error)) {
		r := newRig()
		if setup != nil {
			setup(r)
		}
		runs = append(runs, run{name, func() (authn.Step, error) { return do(r) }})
	}
	begin := func(pw string) func(r *rig) (authn.Step, error) {
		return func(r *rig) (authn.Step, error) {
			return r.m.Begin(t.Context(), loginStart(credentialFields(sentinelIdent, pw)))
		}
	}
	answerNew := func(r *rig) (authn.Step, error) {
		return r.m.Answer(t.Context(), flowAt(stepNewPassword, markVerified(testAccount)), submit(map[string]string{fieldNewPassword: sentinelNew}))
	}
	add("a proof", nil, begin(sentinelPassword))
	add("a refusal", nil, begin(sentinelPassword+"x"))
	add("a throttle", func(r *rig) { r.th.wait = time.Minute }, begin(sentinelPassword))
	add("check outage", func(r *rig) { r.th.checkErr = boom }, begin(sentinelPassword))
	add("fail outage", func(r *rig) { r.th.failErr = boom }, begin(sentinelPassword+"x"))
	add("succeed outage", func(r *rig) { r.th.succeedErr = boom }, begin(sentinelPassword))
	add("verify outage", func(r *rig) {
		r.pw.verify = func(context.Context, string, string) (authn.AccountID, authn.VerifyResult, error) {
			return "", authn.VerifyResult{}, boom
		}
	}, begin(sentinelPassword))
	add("busy", func(r *rig) {
		r.pw.verify = func(context.Context, string, string) (authn.AccountID, authn.VerifyResult, error) {
			return "", authn.VerifyResult{}, authn.ErrBusy
		}
	}, begin(sentinelPassword))
	add("malformed identifier", func(r *rig) {
		r.pw.verify = func(context.Context, string, string) (authn.AccountID, authn.VerifyResult, error) {
			return "", authn.VerifyResult{}, authn.ErrInvalidIdentifier
		}
	}, begin(sentinelPassword))
	add("must change", func(r *rig) {
		r.pw.verify = func(context.Context, string, string) (authn.AccountID, authn.VerifyResult, error) {
			return testAccount, authn.VerifyResult{MustChange: true}, nil
		}
	}, begin(sentinelPassword))
	add("a new password", nil, answerNew)
	add("a weak new password", func(r *rig) {
		r.pw.set = func(context.Context, authn.AccountID, string, authn.SetPasswordOptions) error {
			return authn.ErrPasswordPolicy
		}
	}, answerNew)
	add("a new password outage", func(r *rig) {
		r.pw.set = func(context.Context, authn.AccountID, string, authn.SetPasswordOptions) error { return boom }
	}, answerNew)
	add("the new password with bad state", nil, func(r *rig) (authn.Step, error) {
		return r.m.Answer(t.Context(), flowAt(stepNewPassword, []byte("junk")), submit(map[string]string{fieldNewPassword: sentinelNew}))
	})
	add("a callback", nil, func(r *rig) (authn.Step, error) {
		return r.m.Answer(t.Context(), flowAt(stepCredentials, nil), authn.StepInput{Action: authn.InputCallback, Fields: credentialFields(sentinelIdent, sentinelPassword)})
	})
	add("a purpose not served", nil, func(r *rig) (authn.Step, error) {
		return r.m.Begin(t.Context(), authn.Start{Purpose: authn.PurposeLink, Fields: credentialFields(sentinelIdent, sentinelPassword)})
	})
	add("a prompt", nil, func(r *rig) (authn.Step, error) {
		return r.m.Begin(t.Context(), loginStart(map[string]string{fieldPassword: sentinelPassword}))
	})

	for _, run := range runs {
		s, err := run.do()
		seen := describe(s)
		if err != nil {
			seen += " error=" + err.Error()
		}
		for _, secret := range []string{sentinelPassword, sentinelNew, sentinelIdent} {
			if strings.Contains(seen, secret) {
				t.Errorf("%s: %q is in %s", run.name, secret, seen)
			}
		}
		if kindOf(s) == step.KindUnanswered {
			t.Errorf("%s: the method answered nothing", run.name)
		}
	}
	if out := logs.String(); out != "" {
		t.Errorf("the method wrote to the log: %s", out)
	}
}

func TestTheBackOffKeyHoldsNothingOfTheIdentifier(t *testing.T) {
	r := newRig()
	must(r.m.Begin(t.Context(), loginStart(credentialFields(sentinelIdent, sentinelPassword))))
	for _, c := range r.l.all() {
		if c.op == "verify" {
			continue
		}
		if strings.Contains(c.key, sentinelIdent) || strings.Contains(c.key, strings.ToLower(sentinelIdent)) || strings.Contains(c.key, sentinelPassword) {
			t.Errorf("the %s key %q carries a submitted value", c.op, c.key)
		}
	}
}

// What follows is the context rule: a call that has ended derives nothing.

func TestACancelledContextDerivesNothingAndCountsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	expired, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	for name, c := range map[string]context.Context{"cancelled": ctx, "past its deadline": expired} {
		t.Run(name, func(t *testing.T) {
			r, _ := mustChangeRig(sentinelPassword)
			for label, got := range map[string]authn.Step{
				"begin":    must(r.m.Begin(c, loginStart(credentialFields("alice", sentinelPassword)))),
				"answer":   must(r.m.Answer(c, flowAt(stepCredentials, nil), submit(credentialFields("alice", sentinelPassword)))),
				"new step": must(r.m.Answer(c, flowAt(stepNewPassword, markVerified(testAccount)), submit(map[string]string{fieldNewPassword: sentinelNew}))),
			} {
				if kindOf(got) != step.KindUnavailable {
					t.Errorf("%s: answered %s, want an outage: a call that ended is neither a refusal nor a proof", label, describe(got))
				}
				if cause := step.CauseOf(got); !errors.Is(cause, c.Err()) {
					t.Errorf("%s: the cause %v does not say why", label, cause)
				}
			}
			if calls := r.l.all(); len(calls) != 0 {
				t.Errorf("a context that had ended reached a dependency: %+v", calls)
			}
		})
	}
}

func TestAContextThatEndsWhileTheBackOffIsReadDerivesNothing(t *testing.T) {
	r := newRig()
	ctx, cancel := context.WithCancel(t.Context())
	r.th.onCheck = cancel
	got := must(r.m.Begin(ctx, loginStart(credentialFields("alice", sentinelPassword))))
	if kindOf(got) != step.KindUnavailable {
		t.Errorf("answered %s, want an outage", describe(got))
	}
	if got, want := r.l.ops(), []string{"check"}; !slices.Equal(got, want) {
		t.Errorf("made %v, want %v: the derivation is the step that must not start", got, want)
	}
}

func TestADerivationThatEndedWithTheContextIsNotARefusal(t *testing.T) {
	r := newRig()
	ctx, cancel := context.WithCancel(t.Context())
	r.pw.verify = func(context.Context, string, string) (authn.AccountID, authn.VerifyResult, error) {
		cancel()
		return "", authn.VerifyResult{}, ctx.Err()
	}
	got := must(r.m.Begin(ctx, loginStart(credentialFields("alice", "x"))))
	if kindOf(got) != step.KindUnavailable {
		t.Errorf("answered %s, want an outage", describe(got))
	}
	if r.l.count("fail") != 0 {
		t.Error("a derivation that never finished was counted as a wrong password")
	}
}

// What follows is the key: one identifier in several spellings is one key.

func TestSpellingsOfOneIdentifierShareABackOffKey(t *testing.T) {
	spellings := []string{"alice", "Alice", "ALICE", " alice", "alice ", "\talice\n", "  ALice  ", "Alice "}
	keyOf := func(identifier string) string {
		r := newRig()
		must(r.m.Begin(t.Context(), loginStart(credentialFields(identifier, "wrong"))))
		return r.l.all()[0].key
	}
	want := keyOf("alice")
	for _, s := range spellings {
		if got := keyOf(s); got != want {
			t.Errorf("identifier %q has key %s, want %s", s, got, want)
		}
	}
	for _, other := range []string{"alice2", "alic", "al ice", "bob", "alice@example.test"} {
		if keyOf(other) == want {
			t.Errorf("identifier %q shares the key of alice", other)
		}
	}
	// The key is the SHA-256 of the folded identifier in lower-case hexadecimal,
	// which is the form the account module's table holds a key in.
	sum := sha256.Sum256([]byte("alice"))
	if want != hex.EncodeToString(sum[:]) {
		t.Errorf("the key of alice is %s, want the SHA-256 of the folded identifier, %x", want, sum)
	}
}

func TestTheSameKeyIsUsedForCheckFailAndSucceedWhateverTheSpelling(t *testing.T) {
	r := newRig()
	must(r.m.Begin(t.Context(), loginStart(credentialFields("  Alice ", "wrong"))))
	must(r.m.Begin(t.Context(), loginStart(credentialFields("alice", "wrong"))))
	keys := map[string]bool{}
	for _, c := range r.l.all() {
		if c.op != "verify" {
			keys[c.key] = true
		}
	}
	if len(keys) != 1 {
		t.Errorf("two spellings of one identifier counted under %d keys, want 1", len(keys))
	}
}

// What follows is the method under parallel use, for the race detector: it holds
// nothing between calls.

func TestTheMethodHoldsNoStateBetweenCalls(t *testing.T) {
	r := newRig()
	var wg sync.WaitGroup
	results := make([]step.Kind, 64)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			password := sentinelPassword
			if i%2 == 1 {
				password = "wrong"
			}
			s, _ := r.m.Begin(t.Context(), loginStart(credentialFields(fmt.Sprintf("user%d", i), password)))
			results[i] = kindOf(s)
		}()
	}
	wg.Wait()
	for i, k := range results {
		want := step.KindProven
		if i%2 == 1 {
			want = step.KindRefused
		}
		if k != want {
			t.Errorf("call %d answered %s, want %s", i, k, want)
		}
	}
	if r.l.count("succeed") != 32 || r.l.count("fail") != 32 {
		t.Errorf("succeed=%d fail=%d, want 32 each", r.l.count("succeed"), r.l.count("fail"))
	}
}
