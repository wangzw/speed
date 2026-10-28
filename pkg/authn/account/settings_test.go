package account

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// A setting the store holds is read on every use: a longer minimum length
// refuses a password the declared default would have allowed.
func TestASettingTheStoreHoldsIsRead(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.withSettings().set(minLengthPath, 20)
		id := h.user("longer", "")
		err := h.p.Set(ctx, id, "fifteen chars!!", authn.SetPasswordOptions{})
		if !errors.Is(err, authn.ErrPasswordPolicy) {
			t.Errorf("a password below the stored minimum gave %v, want ErrPasswordPolicy", err)
		}
	})
}

// A value outside its domain is not acted on: the declared default is used and
// the fault is reported at Error.
func TestAnOutOfDomainSettingFallsBackAndIsReported(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.withSettings().set(minLengthPath, 200)
		if got := h.p.minLength(); got != defaults.PasswordMinLength {
			t.Errorf("minLength with an out-of-domain value is %d, want the declared default %d", got, defaults.PasswordMinLength)
		}
		if lines := h.logs.lines("outside its domain"); len(lines) == 0 {
			t.Errorf("no line reports the out-of-domain value: %s", h.logs.String())
		}
	})
}

// A combination of the Argon2id cost settings below what OWASP recommends falls
// back to the default combination and is reported at Error.
func TestABelowFloorCombinationFallsBackAndIsReported(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		set := h.withSettings()
		set.set(argonMemoryPath, 1)
		set.set(argonIterationPath, 1)
		got, err := h.p.hashParams()
		if err != nil {
			t.Fatalf("reading the hash parameters: %v", err)
		}
		if got != password.DefaultParams() {
			t.Errorf("the hash parameters are %+v, want the default combination %+v", got, password.DefaultParams())
		}
		if lines := h.logs.lines("combine below"); len(lines) == 0 {
			t.Errorf("no line reports the below-floor combination: %s", h.logs.String())
		}
	})
}

// A setting whose read fails is the declared default, and the fault is reported
// at Error.
func TestASettingThatCannotBeReadFallsBackAndIsReported(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		set := h.withSettings()
		set.failOn(minLengthPath, errors.New("the store is down"))
		if got := h.p.minLength(); got != defaults.PasswordMinLength {
			t.Errorf("minLength on a failed read is %d, want the declared default %d", got, defaults.PasswordMinLength)
		}
		if lines := h.logs.lines("cannot be read"); len(lines) == 0 {
			t.Errorf("no line reports the failed read: %s", h.logs.String())
		}
	})
}

// A change that moves a setting in the direction that relaxes a check is a Warn
// and a best-effort audit record; one that does not relax anything is silent.
func TestARelaxedSettingIsAWarnAndAnAuditRecord(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		audit := h.withAudit()
		h.withSettings()
		relaxing := event.Event{
			Type:    settings.ChangedEventType,
			Payload: settings.Change{Key: minLengthPath, Old: "12", New: "8"},
		}
		if err := h.p.onSettingsChanged(ctx, relaxing); err != nil {
			t.Fatalf("the relaxation handler returned %v", err)
		}
		entries := audit.written()
		if len(entries) != 1 || entries[0].Action != relaxedAction {
			t.Errorf("the audit saw %+v, want one %s record", entries, relaxedAction)
		}
		if lines := h.logs.lines("checks less than before"); len(lines) == 0 {
			t.Errorf("no Warn reports the relaxation: %s", h.logs.String())
		}

		// Tightening the same setting, and a setting that relaxes nothing, are
		// both silent.
		audit.entries = nil
		h.logs.buf.Reset()
		for _, c := range []settings.Change{
			{Key: minLengthPath, Old: "8", New: "14"},
			{Key: retentionPath, Old: "12h", New: "1h"},
		} {
			if err := h.p.onSettingsChanged(ctx, event.Event{Type: settings.ChangedEventType, Payload: c}); err != nil {
				t.Fatalf("the handler returned %v", err)
			}
		}
		if len(audit.written()) != 0 || len(h.logs.lines("checks less than before")) != 0 {
			t.Errorf("a change that does not relax a check was reported: audit %+v, log %s", audit.written(), h.logs.String())
		}

		// A change the store flagged sensitive is silent even when the values it
		// carries would loosen: the flag says the value was withheld, and the
		// handler must not read a withheld change as a relaxation.
		audit.entries = nil
		h.logs.buf.Reset()
		sensitive := settings.Change{Key: minLengthPath, Old: "12", New: "8", Sensitive: true}
		if err := h.p.onSettingsChanged(ctx, event.Event{Type: settings.ChangedEventType, Payload: sensitive}); err != nil {
			t.Fatalf("the handler returned %v", err)
		}
		if len(audit.written()) != 0 || len(h.logs.lines("checks less than before")) != 0 {
			t.Errorf("a sensitive change was reported: audit %+v, log %s", audit.written(), h.logs.String())
		}
	})
}

// The startup diagnostics name every value that relaxes a check and the absence
// of an outbox or a bus.
func TestTheStartupDiagnosticsNameWhatRelaxes(t *testing.T) {
	h := newHarness(t, db.SQLite)
	h.withSettings()
	h.set.set(signupPath, signupOpen)
	h.set.set(passwordSignupPath, signupOpen)
	h.p.diagnose()

	for _, fragment := range []string{
		"the account store is running",
		"setting=" + signupPath,
		"setting=" + passwordSignupPath,
		"no outbox",
	} {
		if len(h.logs.lines(fragment)) == 0 {
			t.Errorf("the diagnostics carry no line naming %q: %s", fragment, h.logs.String())
		}
	}
	if lines := h.logs.lines(signupOpen); len(lines) != 0 {
		t.Errorf("the diagnostics name the value %q:\n%s", signupOpen, h.logs.String())
	}
}
