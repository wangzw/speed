package password

import (
	"errors"
	"fmt"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// TestTheStepNamesArePublished pins the method's own names for its steps, which
// the engine keeps in the flow record and hands back as FlowState.Step.
func TestTheStepNamesArePublished(t *testing.T) {
	if got := step.NameOf(promptCredentials()); got != "credentials" {
		t.Errorf("the credentials prompt names step %q, want credentials", got)
	}
	if got := step.NameOf(promptNewPassword(markVerified(testAccount))); got != "new_password" {
		t.Errorf("the new-password prompt names step %q, want new_password", got)
	}
}

// TestTheVerifiedStateWireFormatIsPinned pins the exact bytes written and reads a hand-written blob of the same shape.
func TestTheVerifiedStateWireFormatIsPinned(t *testing.T) {
	want := fmt.Sprintf(`{"v":1,"account":%q}`, string(testAccount))
	if got := string(markVerified(testAccount)); got != want {
		t.Errorf("markVerified wrote %s, want %s", got, want)
	}
	account, ok := readMark([]byte(want))
	if !ok || account != testAccount {
		t.Errorf("readMark refused the published shape %s: account %q, ok %v", want, account, ok)
	}
}

// TestTheRefusalCausesAreDistinct holds that a callback, an unnamed step and a bad private state each carry their own cause.
func TestTheRefusalCausesAreDistinct(t *testing.T) {
	r := newRig()

	callback := must(r.m.Answer(t.Context(), flowAt(stepCredentials, nil),
		authn.StepInput{Action: authn.InputCallback, Fields: credentialFields("alice", sentinelPassword)}))
	if !errors.Is(step.CauseOf(callback), errNotSubmitted) {
		t.Errorf("a callback's cause is %v, want errNotSubmitted", step.CauseOf(callback))
	}

	unknown := must(r.m.Answer(t.Context(), flowAt("", nil), submit(credentialFields("alice", sentinelPassword))))
	if !errors.Is(step.CauseOf(unknown), errUnknownStep) {
		t.Errorf("an unnamed step's cause is %v, want errUnknownStep", step.CauseOf(unknown))
	}

	bad := must(r.m.Answer(t.Context(), flowAt(stepNewPassword, []byte("junk")),
		submit(map[string]string{fieldNewPassword: sentinelNew})))
	if !errors.Is(step.CauseOf(bad), errBadState) {
		t.Errorf("a bad private state's cause is %v, want errBadState", step.CauseOf(bad))
	}
	if errors.Is(step.CauseOf(bad), authn.ErrInvalidCredentials) {
		t.Error("the bad-state refusal reads as a credential refusal")
	}

	refused := must(r.m.Answer(t.Context(), flowAt(stepCredentials, nil), submit(credentialFields("alice", "wrong"))))
	cause := step.CauseOf(refused)
	if !errors.Is(cause, authn.ErrInvalidCredentials) {
		t.Errorf("a credential refusal's cause %v is not in the class of invalid credentials", cause)
	}
	if cause.Error() == authn.ErrInvalidCredentials.Error() {
		t.Error("the credential refusal cause is the bare sentinel, not a distinct wrapped error")
	}
}
