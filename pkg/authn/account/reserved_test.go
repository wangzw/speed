package account

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/settings"
)

// builtinCases are names from the built-in list, one from each part of the
// system the list covers.
var builtinCases = []string{"api", "well-known", "root"}

// TestABuiltInReservedUsernameIsRefusedAtEveryWriteSurface holds the three
// entry points that write a username: an account created by the management face
// or a first login, an anonymous self-registration and an identifier added to an
// existing account. A reserved name is refused at each of them and leaves no row.
func TestABuiltInReservedUsernameIsRefusedAtEveryWriteSurface(t *testing.T) {
	for _, name := range builtinCases {
		t.Run(name, func(t *testing.T) {
			forEachDialect(t, func(t *testing.T, h *harness) {
				ctx := context.Background()

				if _, err := h.p.Create(ctx, authn.CreateAccount{Username: name, Via: "admin"}); !errors.Is(err, authn.ErrUsernameReserved) {
					t.Errorf("Create with the reserved name %q gave %v, want ErrUsernameReserved", name, err)
				}
				if got := h.count(tableAccounts, ""); got != 0 {
					t.Errorf("a refused Create wrote %d account row(s), want none", got)
				}

				h.openSelfRegistration()
				if _, err := h.register(name, name+"@example.com", "password123"); !errors.Is(err, authn.ErrUsernameReserved) {
					t.Errorf("Register with the reserved name %q gave %v, want ErrUsernameReserved", name, err)
				}
				if got := h.count(tableAccounts, ""); got != 0 {
					t.Errorf("a refused Register wrote %d account row(s), want none", got)
				}

				id := h.user("holder", "")
				if err := h.p.AddIdentifier(ctx, id, authn.IdentifierUsername, name, admin); !errors.Is(err, authn.ErrUsernameReserved) {
					t.Errorf("AddIdentifier with the reserved name %q gave %v, want ErrUsernameReserved", name, err)
				}
				if _, err := h.p.Lookup(ctx, authn.IdentifierUsername, name); !errors.Is(err, authn.ErrAccountNotFound) {
					t.Errorf("a refused AddIdentifier left %q readable as an identifier: %v", name, err)
				}
			})
		})
	}
}

// TestADeploymentAppendedReservedUsernameIsRefusedAtEveryWriteSurface holds the
// setting's contribution to the effective set: an appended name is refused like
// a built-in one, and an element written in another case is matched by its
// normalized form.
func TestADeploymentAppendedReservedUsernameIsRefusedAtEveryWriteSurface(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.withSettings().set(reservedPath, `["Acme","partner-support"]`)

		if _, err := h.p.Create(ctx, authn.CreateAccount{Username: "acme", Via: "admin"}); !errors.Is(err, authn.ErrUsernameReserved) {
			t.Errorf("Create with an appended name gave %v, want ErrUsernameReserved", err)
		}
		if _, err := h.p.Create(ctx, authn.CreateAccount{Username: "alice", Via: "admin"}); err != nil {
			t.Fatalf("Create with a name outside the set gave %v, want none", err)
		}

		h.openSelfRegistration()
		if _, err := h.register("partner-support", "partner@example.com", "password123"); !errors.Is(err, authn.ErrUsernameReserved) {
			t.Errorf("Register with an appended name gave %v, want ErrUsernameReserved", err)
		}

		id := h.user("holder", "")
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierUsername, "partner-support", admin); !errors.Is(err, authn.ErrUsernameReserved) {
			t.Errorf("AddIdentifier with an appended name gave %v, want ErrUsernameReserved", err)
		}
	})
}

// TestAReservedUsernameIsNotAnAccountExists holds the two refusals apart: a
// reserved name is a value that may not be used, a taken name is one another
// account holds, and each is only itself.
func TestAReservedUsernameIsNotAnAccountExists(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()

		_, reservedErr := h.p.Create(ctx, authn.CreateAccount{Username: "admin", Via: "admin"})
		if !errors.Is(reservedErr, authn.ErrUsernameReserved) {
			t.Fatalf("a reserved name gave %v, want ErrUsernameReserved", reservedErr)
		}
		if errors.Is(reservedErr, authn.ErrAccountExists) || errors.Is(reservedErr, authn.ErrInvalidIdentifier) {
			t.Errorf("a reserved name also reads as another refusal: %v", reservedErr)
		}

		h.user("taken", "")
		_, takenErr := h.p.Create(ctx, authn.CreateAccount{Username: "taken", Via: "admin"})
		if !errors.Is(takenErr, authn.ErrAccountExists) {
			t.Fatalf("a taken name gave %v, want ErrAccountExists", takenErr)
		}
		if errors.Is(takenErr, authn.ErrUsernameReserved) {
			t.Errorf("a taken name also reads as reserved: %v", takenErr)
		}
	})
}

// TestTheBuiltInReservedListStandsWhenTheSettingCannotBeRead holds the built-in
// list against a store that fails: the read adds nothing and takes nothing away,
// so a built-in name is still refused and a name outside the list is still
// accepted.
func TestTheBuiltInReservedListStandsWhenTheSettingCannotBeRead(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.withSettings().failOn(reservedPath, errors.New("the store is down"))

		if _, err := h.p.Create(ctx, authn.CreateAccount{Username: "root", Via: "admin"}); !errors.Is(err, authn.ErrUsernameReserved) {
			t.Errorf("a built-in name on a failed read gave %v, want ErrUsernameReserved", err)
		}
		if _, err := h.p.Create(ctx, authn.CreateAccount{Username: "alice", Via: "admin"}); err != nil {
			t.Errorf("a name outside the built-in list on a failed read gave %v, want none", err)
		}
		if lines := h.logs.lines("cannot be read"); len(lines) == 0 {
			t.Errorf("no line reports the failed read: %s", h.logs.String())
		}
	})
}

// TestTheReservedSettingAdmitsOnlyAJSONArrayOfUsernames holds the domain the
// declaration states: an array of names that normalize, and nothing else.
func TestTheReservedSettingAdmitsOnlyAJSONArrayOfUsernames(t *testing.T) {
	for _, c := range []struct {
		text string
		ok   bool
	}{
		{"[]", true},
		{`["acme","partner-support"]`, true},
		{`["ACME"]`, true},
		{"", false},
		{"null", false},
		{"not json", false},
		{`["ok","bad name"]`, false},
		{`{"name":"acme"}`, false},
		{`["acme"] trailing`, false},
	} {
		if got := reservedTextInDomain(c.text); got != c.ok {
			t.Errorf("reservedTextInDomain(%q) = %v, want %v", c.text, got, c.ok)
		}
	}
}

// TestTheReservedSettingRefusesAnElementThatIsNotAUsername holds the write side
// of the same domain: the store refuses the write rather than storing a name
// that would be skipped when the set is read.
func TestTheReservedSettingRefusesAnElementThatIsNotAUsername(t *testing.T) {
	store, _ := deliveredSettings(t)
	err := store.Set(context.Background(), settings.Write{
		Layer: settings.Global(), Key: reservedPath, Value: `["bad name"]`, Actor: "test",
	})
	if !errors.Is(err, settings.ErrInvalidValue) {
		t.Fatalf("writing a name that is not a username gave %v, want ErrInvalidValue", err)
	}
}

// TestRemovingAnAppendedReservedUsernameIsARelaxation holds the direction of a
// change to the appended set: a removed name lets a username through and is
// recorded as a relaxation, while an added name checks more and is silent.
func TestRemovingAnAppendedReservedUsernameIsARelaxation(t *testing.T) {
	for _, c := range []struct {
		name    string
		before  string
		after   string
		del     bool
		relaxed bool
	}{
		{"an appended name removed by the empty array", `["acme"]`, "[]", false, true},
		{"an appended name removed by deleting the row", `["acme"]`, "", true, true},
		{"an appended name added", "[]", `["acme"]`, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			store, bus := deliveredSettings(t)
			h := newHarness(t, db.SQLite)
			h.p.store = store
			audit := h.withAudit()

			if err := store.Set(ctx, settings.Write{
				Layer: settings.Global(), Key: reservedPath, Value: c.before, Actor: "test",
			}); err != nil {
				t.Fatalf("writing the first value: %v", err)
			}
			if c.del {
				if err := store.Delete(ctx, settings.Global(), reservedPath, "test"); err != nil {
					t.Fatalf("deleting the setting: %v", err)
				}
			} else if err := store.Set(ctx, settings.Write{
				Layer: settings.Global(), Key: reservedPath, Value: c.after, Actor: "test",
			}); err != nil {
				t.Fatalf("writing the second value: %v", err)
			}
			deliverChange(t, h, bus.lastChange(t))

			entries := audit.written()
			lines := h.logs.lines("level=WARN")
			if c.relaxed {
				if len(entries) != 1 || entries[0].Object != reservedPath || entries[0].Detail["change"] != moveRelaxed {
					t.Errorf("the audit holds %+v, want one relaxed record on %s with change %s",
						entries, reservedPath, moveRelaxed)
				}
				if len(lines) != 1 || !strings.Contains(lines[0], "setting="+reservedPath) ||
					!strings.Contains(lines[0], "direction="+moveRelaxed) {
					t.Errorf("the Warn is %v, want one naming %s with direction %s:\n%s",
						lines, reservedPath, moveRelaxed, h.logs.String())
				}
				return
			}
			if len(entries) != 0 || len(lines) != 0 {
				t.Errorf("a change that tightens wrote %d audit row(s) and %d Warn line(s), want none:\n%s",
					len(entries), len(lines), h.logs.String())
			}
		})
	}
}
