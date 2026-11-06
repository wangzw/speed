package resolve

import (
	"errors"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/slug"
)

func TestNewHoldsItsDependenciesAndSwitchesToTheirSets(t *testing.T) {
	s := newStore()
	good := Config{
		Accounts: accountsView{s: s}, Admin: adminView{s: s}, Memberships: membershipsView{s: s},
		NormalizeUsername: slug.Normalize,
	}
	if _, err := New(good); err != nil {
		t.Fatalf("New(good) = %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"no accounts":    func(c *Config) { c.Accounts = nil },
		"no admin":       func(c *Config) { c.Admin = nil },
		"no memberships": func(c *Config) { c.Memberships = nil },
		"no normalizer":  func(c *Config) { c.NormalizeUsername = nil },
		"signup unknown": func(c *Config) { c.Signup = "invite" },
	} {
		c := good
		mutate(&c)
		if _, err := New(c); !errors.Is(err, authn.ErrInvalidConfig) {
			t.Errorf("%s: err = %v, want ErrInvalidConfig", name, err)
		}
	}
}

func TestZeroSwitchReadsAsClosed(t *testing.T) {
	e := newEnv(t)
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, email: "ann@home.org", emailVia: "idp:google"})
	if e.r.signup == SignupOpen {
		t.Fatalf("the zero Config opened the switch: %q", e.r.signup)
	}
	if _, err := e.resolve(social("google", "g1", "ann@home.org", true)); !errors.Is(err, authn.ErrLinkRequired) {
		t.Errorf("err = %v, want ErrLinkRequired: a verified address on an account always needs an explicit link", err)
	}
	if _, err := e.resolve(social("google", "g2", "", false)); !errors.Is(err, authn.ErrSignupClosed) {
		t.Errorf("err = %v, want ErrSignupClosed", err)
	}
}

// A failure after the account was made deletes it, so that the next attempt is
// not met by an address or a key a half-made account holds.
func TestAFailureAfterTheAccountIsMadeUnwindsIt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*env)
		proof authn.Proof
		opts  []option
		want  error
	}{
		{"binding fails", func(e *env) { e.s.failLink = authn.ErrUnavailable }, social("google", "g1", "ann@home.org", true), nil, authn.ErrUnavailable},
		{"membership fails", func(e *env) { e.s.failPut = authn.ErrUnavailable }, enterprise("u1", "ann@acme.com"), []option{withProvider(authn.ProvisioningJIT, false)}, authn.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, append([]option{withSignup(SignupOpen)}, tc.opts...)...)
			tc.setup(e)
			_, err := e.resolve(tc.proof)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if live := e.s.live(); len(live) != 0 {
				t.Errorf("accounts = %v, want the half-made one deleted", live)
			}
			if _, ok := e.s.boundTo(tc.proof.External.Provider, tc.proof.External.Subject); ok {
				t.Error("a binding survived the unwind")
			}
			// The next attempt, with the dependency back, is a clean first login.
			e.s.failLink, e.s.failPut = nil, nil
			if res, err := e.resolve(tc.proof); err != nil || res.Outcome != OutcomeCreated {
				t.Errorf("retry = %+v, %v, want an account created", res, err)
			}
		})
	}
}

func TestAnUnwindSurvivesTheLogInsCancelledContext(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	pending, err := e.pending(social("google", "g1", "", false))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	ctx, cancel := contextWithCancelOnLink(e)
	defer cancel()
	e.s.failLink = authn.ErrUnavailable
	_, err = e.r.Create(ctx, *pending.Pending, testUsername, passwordOf(*pending.Pending), "")
	if err == nil {
		t.Fatal("want a failure")
	}
	if len(e.s.deleted) != 1 {
		t.Errorf("deleted %v, want the account unwound although the context was cancelled", e.s.deleted)
	}
}

func TestACreateFailureIsPassedOn(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	e.s.failCreate = authn.ErrUnavailable
	if _, err := e.resolve(social("google", "g1", "", false)); !errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestDisplayNameIsCutToHundredCharactersNotBytes(t *testing.T) {
	long := strings.Repeat("名", 150)
	got := displayNameOrDefault(long, "")
	if utf8.RuneCountInString(got) != 100 || !utf8.ValidString(got) {
		t.Errorf("got %d characters, valid=%v, want 100 whole characters", utf8.RuneCountInString(got), utf8.ValidString(got))
	}
	if got := displayNameOrDefault(strings.Repeat("a", 100), ""); len(got) != 100 {
		t.Errorf("a name of exactly 100 characters came out %d long", len(got))
	}
	if got := displayNameOrDefault("  Ann\x00 \u202eLee\n ", ""); strings.ContainsAny(got, "\x00\n") || strings.HasPrefix(got, " ") {
		t.Errorf("got %q, want control characters and the ends trimmed", got)
	}
	if got := displayNameOrDefault("a\xffb", ""); got != "ab" {
		t.Errorf("displayNameOrDefault of an invalid byte = %q, want %q: the byte is dropped, not replaced", got, "ab")
	}
	if got := displayNameOrDefault(" \x00\n ", "ann"); got != "ann" {
		t.Errorf("displayNameOrDefault of a name that empties out = %q, want the username %q", got, "ann")
	}
}

func TestEmailDomain(t *testing.T) {
	for in, want := range map[string]string{
		"ann@acme.com":     "acme.com",
		" Ann@ACME.com ":   "acme.com",
		"ann@":             "",
		"@acme.com":        "",
		"ann":              "",
		"a@b@acme.com":     "",
		"ann@acme.cöm":     "",
		"ann@ac me.com":    "",
		"ann@acme.com\x00": "",
		"ann@acme.com\x80": "",
	} {
		if got := emailDomain(in); got != want {
			t.Errorf("emailDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

// The package imports the root package and the standard library, and nothing
// else: not the login engine, a login method, the tenant module or gin.
func TestPackageImportsOnlyTheRootPackage(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob: %v %v", files, err)
	}
	const root = "github.com/vislake/speed/pkg/authn"
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range src.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			first, _, _ := strings.Cut(path, "/")
			if !strings.Contains(first, ".") || path == root {
				continue
			}
			t.Errorf("%s imports %s, want the standard library and %s alone", f, path, root)
		}
	}
}
