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

var builtinCases = []string{"api", "well-known", "root"}

// A built-in reserved username is refused at every write surface: management create or first login, self-registration and identifier add.
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

// The personal prefix is reserved besides the literal names.
func TestAPersonalPrefixedUsernameIsReserved(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		if _, err := h.p.Create(ctx, authn.CreateAccount{Username: "personal-owner", Via: "admin"}); !errors.Is(err, authn.ErrUsernameReserved) {
			t.Errorf("a personal-prefixed name gave %v, want ErrUsernameReserved", err)
		}
		if got := h.count(tableAccounts, ""); got != 0 {
			t.Errorf("a refused Create wrote %d account row(s), want none", got)
		}
		if _, err := h.p.Create(ctx, authn.CreateAccount{Username: "personal", Via: "admin"}); err != nil {
			t.Errorf("the prefix's stem was refused: %v", err)
		}
	})
}

// A missing or blank username is the field's own 400 username-invalid at every write surface, not the body's generic refusal.
func TestAnEmptyUsernameIsAShapeFailureAtEveryWriteSurface(t *testing.T) {
	for _, name := range []string{"", "   "} {
		t.Run("a blank name", func(t *testing.T) {
			forEachDialect(t, func(t *testing.T, h *harness) {
				ctx := context.Background()

				if _, err := h.p.Create(ctx, authn.CreateAccount{Username: name, Via: "admin"}); !authn.IsUsernameShape(err) {
					t.Errorf("Create with %q gave %v, want the username's shape failure", name, err)
				}
				if got := h.count(tableAccounts, ""); got != 0 {
					t.Errorf("a refused Create wrote %d account row(s), want none", got)
				}

				id := h.user("holder", "")
				if err := h.p.AddIdentifier(ctx, id, authn.IdentifierUsername, name, admin); !authn.IsUsernameShape(err) {
					t.Errorf("AddIdentifier with %q gave %v, want the username's shape failure", name, err)
				}
			})
		})
	}
}

// A deployment-appended reserved name is refused like a built-in one, matched by its normalized form whatever case it is written in.
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

// A reserved name and a taken name are distinct refusals, each only itself.
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

// A failed settings read leaves the built-in reserved list standing, refusing built-ins and accepting names outside it.
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

// The reserved setting admits a JSON array of usernames and nothing else.
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
		{`["ok","bad name"]`, true},
		{`{"name":"acme"}`, false},
		{`["acme"] trailing`, false},
		{`["acme",1]`, false},
	} {
		if got := reservedTextInDomain(c.text); got != c.ok {
			t.Errorf("reservedTextInDomain(%q) = %v, want %v", c.text, got, c.ok)
		}
	}
}

// The store takes the array as written and skips an element that is not a username when the set is read.
func TestTheReservedSettingTakesAnElementThatIsNotAUsername(t *testing.T) {
	store, _ := deliveredSettings(t)
	err := store.Set(context.Background(), settings.Write{
		Layer: settings.Global(), Key: reservedPath, Value: `["bad name"]`, Actor: "test",
	})
	if err != nil {
		t.Fatalf("writing an array with a name that is not a username gave %v, want it stored", err)
	}
}

// An appended name is normalized on read, so an element written in capitals refuses every spelling of it.
func TestAnAppendedNameIsNormalizedWhenTheSetIsRead(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.withSettings().set(reservedPath, `["ACME"]`)

		for _, name := range []string{"acme", "ACME", "Acme"} {
			if _, err := h.p.Create(ctx, authn.CreateAccount{Username: name, Via: "admin"}); !errors.Is(err, authn.ErrUsernameReserved) {
				t.Errorf("the spelling %q gave %v, want ErrUsernameReserved", name, err)
			}
		}
		if _, err := h.p.Create(ctx, authn.CreateAccount{Username: "alice", Via: "admin"}); err != nil {
			t.Errorf("a name outside the set gave %v, want none", err)
		}
	})
}

// A bad element is skipped with one Warn while the rest of the array stands, and a repeated read does not repeat the line.
func TestAnElementThatIsNotAUsernameIsSkippedWhenTheSetIsRead(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.withSettings().set(reservedPath, `["acme","a b"]`)

		if _, err := h.p.Create(ctx, authn.CreateAccount{Username: "acme", Via: "admin"}); !errors.Is(err, authn.ErrUsernameReserved) {
			t.Errorf("the valid element gave %v, want ErrUsernameReserved", err)
		}
		if _, err := h.p.Create(ctx, authn.CreateAccount{Username: "alice", Via: "admin"}); err != nil {
			t.Errorf("a name outside the set gave %v, want none", err)
		}
		lines := h.logs.lines("level=WARN")
		if len(lines) != 1 || !strings.Contains(lines[0], "setting="+reservedPath) {
			t.Fatalf("the Warn is %v, want one naming %s:\n%s", lines, reservedPath, h.logs.String())
		}
		if _, err := h.p.Create(ctx, authn.CreateAccount{Username: "bob", Via: "admin"}); err != nil {
			t.Fatalf("creating beside the skipped element: %v", err)
		}
		if lines := h.logs.lines("level=WARN"); len(lines) != 1 {
			t.Errorf("the Warn repeated on a second read: %v", lines)
		}
	})
}

// Removing an appended reserved name is recorded as a relaxation, while adding one checks more and is silent.
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

// The reserved answer seam reports the built-in names, the appended names and the personal prefix, not a free name, and outside the username shape is the normalizer's refusal.
func TestTheReservedAnswerSeamReportsTheEffectiveSet(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.withSettings().set(reservedPath, `["partner-support"]`)

		cases := map[string]bool{
			"api":             true,
			"personal-alice":  true,
			"partner-support": true,
			"acme":            false,
		}
		for name, want := range cases {
			got, err := h.p.ReservedUsername(ctx, name)
			if err != nil {
				t.Fatalf("ReservedUsername(%q): %v", name, err)
			}
			if got != want {
				t.Errorf("ReservedUsername(%q) = %v, want %v", name, got, want)
			}
		}
		if _, err := h.p.ReservedUsername(ctx, "Not A Name"); !errors.Is(err, authn.ErrInvalidIdentifier) {
			t.Errorf("a name outside the shape gave %v, want ErrInvalidIdentifier", err)
		}
	})
}
