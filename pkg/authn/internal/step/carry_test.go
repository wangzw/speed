package step_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/model"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

var at = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func TestProofAndPassAndRecordSurviveTheStepWhole(t *testing.T) {
	proof := model.Proof{
		Account:    "acct",
		Identifier: &model.Identifier{ID: "i", Kind: model.IdentifierPhone, Value: "+86", VerifiedAt: at, VerifiedVia: "sms-code"},
		External: &model.External{
			Provider: "google", Subject: "s", Email: "e@x", EmailVerified: true, DisplayName: "d",
			Tenant: "t", Attributes: map[string]string{"idp.k": "v"},
		},
		Methods: []model.Method{model.MethodSMS}, UserVerified: true, IDPAsserted: true, Provider: "prov",
		Aliases: []model.ExternalLink{{Provider: "p", Subject: "s", Email: "e", EmailVerified: true}},
	}
	if got := step.ProofOf(step.Proven(proof)); !reflect.DeepEqual(got, proof) {
		t.Errorf("ProofOf = %+v, want %+v", got, proof)
	}
	// The other polarity of every flag.
	proof.UserVerified, proof.IDPAsserted = false, false
	proof.External.EmailVerified, proof.Aliases[0].EmailVerified = false, false
	if got := step.ProofOf(step.Proven(proof)); !reflect.DeepEqual(got, proof) {
		t.Errorf("ProofOf = %+v, want %+v", got, proof)
	}
	for _, pass := range []model.Pass{
		{Methods: []model.Method{model.MethodWebAuthn}, UserVerified: true},
		{Methods: []model.Method{model.MethodTOTP}, UserVerified: false},
	} {
		if got := step.PassOf(step.Passed(pass)); !reflect.DeepEqual(got, pass) {
			t.Errorf("PassOf = %+v, want %+v", got, pass)
		}
	}
	rec := model.FactorRecord{
		ID: "f", Account: "a", Kind: model.FactorWebAuthn, Label: "key", Status: model.FactorActive,
		CreatedAt: at, ActivatedAt: at.Add(time.Hour), LastUsedAt: at.Add(2 * time.Hour),
	}
	if got := step.RecordOf(step.Enrolled(rec)); got != rec {
		t.Errorf("RecordOf = %+v, want %+v", got, rec)
	}
}

func TestEveryAnswerWithStateKeepsItAndCopiesItIn(t *testing.T) {
	for name, mk := range map[string]func([]byte) step.Step{
		"prompt":   func(b []byte) step.Step { return step.Prompt("n", b, model.Field{Name: "f"}) },
		"redirect": func(b []byte) step.Step { return step.Redirect("n", b, "https://idp.example/") },
		"data":     func(b []byte) step.Step { return step.Data("n", b, map[string]string{"a": "b"}) },
	} {
		state := []byte("ceremony")
		s := mk(state)
		state[0] = 'X'
		if got := string(step.PrivateOf(s)); got != "ceremony" {
			t.Errorf("%s: PrivateOf = %q after the method changed its slice, want ceremony", name, got)
		}
		if step.PrivateOf(mk(nil)) != nil {
			t.Errorf("%s: an empty state reads back non-nil", name)
		}
	}
}

// The names are what a log line or a metric label carries.
func TestKindNamesAreFrozen(t *testing.T) {
	for k, want := range map[step.Kind]string{
		step.KindUnanswered: "unanswered", step.KindPrompt: "prompt", step.KindRedirect: "redirect", step.KindData: "data",
		step.KindProven: "proven", step.KindRefused: "refused", step.KindThrottled: "throttled",
		step.KindUnavailable: "unavailable", step.KindPassed: "passed", step.KindEnrolled: "enrolled",
	} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

// Every reader that returns a payload returns the zero value of it for any
// other kind than the one that carries it.
func TestReadersOfOtherKindsReadZero(t *testing.T) {
	boom := model.Pass{Methods: []model.Method{model.MethodTOTP}}
	cases := map[string]struct {
		s    step.Step
		owns map[string]bool
	}{
		"prompt":    {step.Prompt("n", []byte("p"), model.Field{Name: "f"}), map[string]bool{"name": true, "private": true, "fields": true}},
		"redirect":  {step.Redirect("n", []byte("p"), "https://x/"), map[string]bool{"name": true, "private": true, "to": true}},
		"data":      {step.Data("n", []byte("p"), 1), map[string]bool{"name": true, "private": true, "data": true}},
		"proven":    {step.Proven(model.Proof{Account: "a"}), map[string]bool{"proof": true}},
		"passed":    {step.Passed(boom), map[string]bool{"pass": true}},
		"throttled": {step.Throttled(time.Second), map[string]bool{"retry": true}},
		"refused":   {step.Refused(model.ErrInvalidAccountID), map[string]bool{"cause": true}},
		"enrolled":  {step.Enrolled(model.FactorRecord{ID: "f"}), map[string]bool{"record": true}},
	}
	for name, c := range cases {
		zero := map[string]bool{
			"name":    step.NameOf(c.s) == "",
			"private": step.PrivateOf(c.s) == nil,
			"fields":  step.FieldsOf(c.s) == nil,
			"to":      step.RedirectOf(c.s) == "",
			"data":    step.DataOf(c.s) == nil,
			"proof":   step.ProofOf(c.s).Account == "" && step.ProofOf(c.s).Identifier == nil && step.ProofOf(c.s).External == nil,
			"pass":    len(step.PassOf(c.s).Methods) == 0,
			"retry":   step.RetryAfterOf(c.s) == 0,
			"cause":   step.CauseOf(c.s) == nil,
			"record":  step.RecordOf(c.s) == (model.FactorRecord{}),
		}
		for reader, isZero := range zero {
			if !c.owns[reader] && !isZero {
				t.Errorf("%s: reader %s returns a payload for a step that does not carry it", name, reader)
			}
		}
	}
}

// A cause reaches the log and changes nothing: the kind is the constructor's,
// whatever error rides along. A backend that returns the gateway's own timeout
// as the cause of a refusal must not turn a wrong password into an outage.
func TestACauseNeverChangesTheKind(t *testing.T) {
	for _, cause := range []error{errors.New("x"), context.DeadlineExceeded, context.Canceled, nil} {
		if got := step.KindOf(step.Refused(cause)); got != step.KindRefused {
			t.Errorf("Refused(%v) reads as %v", cause, got)
		}
		if got := step.KindOf(step.Unavailable(cause)); got != step.KindUnavailable {
			t.Errorf("Unavailable(%v) reads as %v", cause, got)
		}
		if got := step.CauseOf(step.Refused(cause)); !errors.Is(got, cause) {
			t.Errorf("Refused(%v): cause comes back as %v", cause, got)
		}
		if got := step.CauseOf(step.Unavailable(cause)); !errors.Is(got, cause) {
			t.Errorf("Unavailable(%v): cause comes back as %v", cause, got)
		}
	}
}

// The engine requires https; the constructor does not judge, so that the engine
// is the one place that classifies a bad target.
func TestRedirectDoesNotJudgeItsTarget(t *testing.T) {
	for _, to := range []string{"http://idp.example/", "", "javascript:alert(1)", "//idp.example"} {
		s := step.Redirect("n", nil, to)
		if step.KindOf(s) != step.KindRedirect || step.RedirectOf(s) != to {
			t.Errorf("Redirect(%q) reads as %v with target %q", to, step.KindOf(s), step.RedirectOf(s))
		}
	}
}
