package step_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/model"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// TestAStepNobodyAnsweredReadsAsNoAnswer holds the criterion the shape rests
// on: the zero Step is not a pass and not a refusal, it is no answer.
func TestAStepNobodyAnsweredReadsAsNoAnswer(t *testing.T) {
	var s step.Step
	if got := step.KindOf(s); got != step.KindUnanswered {
		t.Errorf("the zero Step reads as %v, want %v", got, step.KindUnanswered)
	}
	if p := step.ProofOf(s); p.Account != "" || p.Identifier != nil || p.External != nil {
		t.Errorf("the zero Step carries a proof: %+v", p)
	}
	if step.NameOf(s) != "" || step.PrivateOf(s) != nil || step.FieldsOf(s) != nil || step.RedirectOf(s) != "" ||
		step.DataOf(s) != nil || step.CauseOf(s) != nil || step.RetryAfterOf(s) != 0 || step.SendsOf(s) != 0 ||
		len(step.PassOf(s).Methods) != 0 || step.RecordOf(s) != (model.FactorRecord{}) {
		t.Error("the zero Step carries a payload")
	}
	if got := step.KindOf(s.WithSends(3)); got != step.KindUnanswered {
		t.Errorf("WithSends made an answer of none: %v", got)
	}
	if got := step.SendsOf(s.WithSends(3)); got != 0 {
		t.Errorf("WithSends set a count on no answer: %d", got)
	}
}

func TestEachConstructorSaysWhatItWasCalledFor(t *testing.T) {
	boom := errors.New("boom")
	for _, c := range []struct {
		name string
		made step.Step
		want step.Kind
	}{
		{"prompt", step.Prompt("pw", nil), step.KindPrompt},
		{"redirect", step.Redirect("go", nil, "https://idp.example/auth"), step.KindRedirect},
		{"data", step.Data("opts", nil, map[string]int{"a": 1}), step.KindData},
		{"proven", step.Proven(model.Proof{Account: "a"}), step.KindProven},
		{"refused", step.Refused(boom), step.KindRefused},
		{"throttled", step.Throttled(time.Second), step.KindThrottled},
		{"unavailable", step.Unavailable(boom), step.KindUnavailable},
		{"passed", step.Passed(model.Pass{}), step.KindPassed},
		{"enrolled", step.Enrolled(model.FactorRecord{}), step.KindEnrolled},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := step.KindOf(c.made); got != c.want {
				t.Errorf("reads as %v, want %v", got, c.want)
			}
			if c.want == step.KindUnanswered {
				t.Error("a constructor produced the no-answer kind")
			}
		})
	}
}

func TestKindNames(t *testing.T) {
	seen := map[string]step.Kind{}
	for k := step.KindUnanswered; k <= step.KindEnrolled; k++ {
		name := k.String()
		if name == "unknown" || name == "" {
			t.Errorf("kind %d has no name", k)
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("kinds %d and %d share the name %q", prev, k, name)
		}
		seen[name] = k
	}
	if got := (step.KindEnrolled + 1).String(); got != "unknown" {
		t.Errorf("an out-of-range kind is named %q", got)
	}
}

func TestPromptPayloadIsReadBackAsGiven(t *testing.T) {
	fields := []model.Field{
		{Name: "identifier", Kind: model.FieldText, Required: true},
		{Name: "password", Kind: model.FieldPassword, Required: true, Secret: true},
	}
	s := step.Prompt("credentials", []byte("state"), fields...)
	if got := step.NameOf(s); got != "credentials" {
		t.Errorf("NameOf = %q", got)
	}
	if got := step.PrivateOf(s); string(got) != "state" {
		t.Errorf("PrivateOf = %q", got)
	}
	if got := step.FieldsOf(s); !slices.Equal(got, fields) {
		t.Errorf("FieldsOf = %+v, want %+v", got, fields)
	}
	if got := step.RedirectOf(s); got != "" {
		t.Errorf("a prompt has the redirect target %q", got)
	}
}

func TestRedirectPayloadIsReadBackAsGiven(t *testing.T) {
	s := step.Redirect("authorize", []byte{1, 2, 3}, "https://idp.example/authorize?state=x")
	if got := step.RedirectOf(s); got != "https://idp.example/authorize?state=x" {
		t.Errorf("RedirectOf = %q", got)
	}
	if got := step.NameOf(s); got != "authorize" {
		t.Errorf("NameOf = %q", got)
	}
	if got := step.PrivateOf(s); !slices.Equal(got, []byte{1, 2, 3}) {
		t.Errorf("PrivateOf = %v", got)
	}
	if got := step.FieldsOf(s); got != nil {
		t.Errorf("a redirect asks for fields: %v", got)
	}
}

func TestDataPayloadIsReadBackAsGiven(t *testing.T) {
	v := map[string]string{"challenge": "abc"}
	s := step.Data("assert", nil, v)
	got, ok := step.DataOf(s).(map[string]string)
	if !ok || got["challenge"] != "abc" {
		t.Errorf("DataOf = %#v", step.DataOf(s))
	}
	if step.NameOf(s) != "assert" {
		t.Errorf("NameOf = %q", step.NameOf(s))
	}
}

func TestProvenCarriesTheProof(t *testing.T) {
	proof := model.Proof{
		Identifier:   &model.Identifier{Kind: model.IdentifierPhone, Value: "+8613800000000", VerifiedVia: "sms-code"},
		Methods:      []model.Method{model.MethodSMS},
		UserVerified: false,
		Provider:     "p",
	}
	s := step.Proven(proof)
	got := step.ProofOf(s)
	if got.Identifier == nil || got.Identifier.Value != "+8613800000000" || got.Provider != "p" ||
		!slices.Equal(got.Methods, []model.Method{model.MethodSMS}) || got.Account != "" || got.External != nil {
		t.Errorf("ProofOf = %+v", got)
	}
	// A proof read off another kind names nothing, so account resolution
	// fails on it.
	if p := step.ProofOf(step.Refused(errors.New("x"))); p.Account != "" || p.Identifier != nil || p.External != nil {
		t.Errorf("a refusal carries a proof: %+v", p)
	}
}

func TestPayloadsAreCopiedOnTheWayInAndOut(t *testing.T) {
	private := []byte("abc")
	fields := []model.Field{{Name: "a"}}
	proof := model.Proof{
		External: &model.External{Provider: "google", Subject: "s", Attributes: map[string]string{"idp.k": "v"}},
		Methods:  []model.Method{model.MethodOAuth},
	}
	pass := model.Pass{Methods: []model.Method{model.MethodTOTP}}

	prompt := step.Prompt("p", private, fields...)
	proven := step.Proven(proof)
	passed := step.Passed(pass)

	// The method keeps editing what it handed over.
	private[0] = 'X'
	fields[0].Name = "changed"
	proof.External.Subject = "changed"
	proof.External.Attributes["idp.k"] = "changed"
	proof.Methods[0] = model.MethodSAML
	pass.Methods[0] = model.MethodSMS

	if string(step.PrivateOf(prompt)) != "abc" {
		t.Error("the private state follows the method's slice")
	}
	if step.FieldsOf(prompt)[0].Name != "a" {
		t.Error("the fields follow the method's slice")
	}
	got := step.ProofOf(proven)
	if got.External.Subject != "s" || got.External.Attributes["idp.k"] != "v" || got.Methods[0] != model.MethodOAuth {
		t.Errorf("the proof follows the method's value: %+v", got)
	}
	if step.PassOf(passed).Methods[0] != model.MethodTOTP {
		t.Error("the pass follows the method's slice")
	}

	// And what a reader got is its own.
	step.PrivateOf(prompt)[0] = 'Y'
	step.FieldsOf(prompt)[0].Name = "changed"
	r := step.ProofOf(proven)
	r.External.Subject = "changed"
	r.Methods[0] = model.MethodSAML
	step.PassOf(passed).Methods[0] = model.MethodSMS
	if string(step.PrivateOf(prompt)) != "abc" || step.FieldsOf(prompt)[0].Name != "a" ||
		step.ProofOf(proven).External.Subject != "s" || step.ProofOf(proven).Methods[0] != model.MethodOAuth ||
		step.PassOf(passed).Methods[0] != model.MethodTOTP {
		t.Error("a reader's change reached the step")
	}
}

func TestRefusedAndUnavailableCarryTheirCause(t *testing.T) {
	boom := errors.New("boom")
	if got := step.CauseOf(step.Refused(boom)); !errors.Is(got, boom) {
		t.Errorf("Refused cause = %v", got)
	}
	if got := step.CauseOf(step.Unavailable(boom)); !errors.Is(got, boom) {
		t.Errorf("Unavailable cause = %v", got)
	}
	// The cause does not change the kind.
	if got := step.KindOf(step.Refused(nil)); got != step.KindRefused {
		t.Errorf("Refused(nil) reads as %v", got)
	}
	if got := step.KindOf(step.Unavailable(nil)); got != step.KindUnavailable {
		t.Errorf("Unavailable(nil) reads as %v", got)
	}
}

func TestThrottledCarriesRetryAfter(t *testing.T) {
	for _, c := range []struct {
		in, want time.Duration
	}{
		{30 * time.Second, 30 * time.Second},
		{time.Nanosecond, time.Nanosecond},
		{0, 0},
		{-time.Second, 0},
	} {
		s := step.Throttled(c.in)
		if got := step.RetryAfterOf(s); got != c.want {
			t.Errorf("Throttled(%v): RetryAfterOf = %v, want %v", c.in, got, c.want)
		}
		if step.KindOf(s) != step.KindThrottled {
			t.Errorf("Throttled(%v) reads as %v", c.in, step.KindOf(s))
		}
	}
}

func TestPassedAndEnrolledCarryTheirPayload(t *testing.T) {
	p := step.PassOf(step.Passed(model.Pass{Methods: []model.Method{model.MethodWebAuthn}, UserVerified: true}))
	if !p.UserVerified || !slices.Equal(p.Methods, []model.Method{model.MethodWebAuthn}) {
		t.Errorf("PassOf = %+v", p)
	}
	rec := model.FactorRecord{ID: "f1", Account: "a", Kind: model.FactorTOTP, Label: "phone", Status: model.FactorPending}
	if got := step.RecordOf(step.Enrolled(rec)); got != rec {
		t.Errorf("RecordOf = %+v, want %+v", got, rec)
	}
	if got := step.RecordOf(step.Passed(model.Pass{})); got != (model.FactorRecord{}) {
		t.Errorf("a pass carries a record: %+v", got)
	}
}

func TestWithSends(t *testing.T) {
	base := step.Prompt("code", []byte("st"), model.Field{Name: "code"})
	for _, c := range []struct {
		name string
		in   int
		want int
	}{
		{"a count", 2, 2},
		{"a count above any per-flow limit is carried whole", 1000, 1000},
		{"zero", 0, 0},
		{"negative reads as zero", -5, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := base.WithSends(c.in)
			if got := step.SendsOf(s); got != c.want {
				t.Errorf("SendsOf = %d, want %d", got, c.want)
			}
			if step.KindOf(s) != step.KindPrompt || step.NameOf(s) != "code" || string(step.PrivateOf(s)) != "st" {
				t.Error("WithSends changed the answer it was put on")
			}
		})
	}
	if step.SendsOf(base) != 0 {
		t.Error("WithSends changed the step it was called on")
	}
	if got := step.SendsOf(base.WithSends(2).WithSends(1)); got != 1 {
		t.Errorf("a second report adds to the first: %d, want it to replace", got)
	}
	for _, k := range []step.Step{
		step.Throttled(0), step.Refused(nil), step.Unavailable(nil), step.Passed(model.Pass{}),
		step.Proven(model.Proof{Account: "a"}), step.Enrolled(model.FactorRecord{}),
	} {
		if got := step.SendsOf(k.WithSends(4)); got != 4 {
			t.Errorf("%v: WithSends(4) reads %d", step.KindOf(k), got)
		}
	}
}
