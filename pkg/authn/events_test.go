package authn

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// eventTable is the design's event table: for each type, its name, its payload
// and the top-level fields of the payload, in the JSON names the design writes.
// The names are the contract with subscribers, so they are written out here and
// not derived from the code under test.
var eventTable = []struct {
	name    string
	typ     reflect.Type
	fields  []string
	omitted []string // fields that a zero value leaves out of the JSON
}{
	{EventAccountCreated, reflect.TypeFor[AccountCreated](), []string{"account_id", "kind", "via"}, nil},
	{EventAccountStatusChanged, reflect.TypeFor[AccountStatusChanged](), []string{"account_id", "from", "to", "reason_class"}, nil},
	{EventAccountDeleted, reflect.TypeFor[AccountDeleted](), []string{"account_id"}, nil},
	{EventIdentifierChanged, reflect.TypeFor[IdentifierChanged](), []string{"account_id", "kind", "change"}, nil},
	{EventBindingChanged, reflect.TypeFor[BindingChanged](), []string{"account_id", "provider_class", "change", "source"}, nil},
	{EventMembershipChanged, reflect.TypeFor[MembershipChanged](), []string{"account_id", "change", "source"}, nil},
	{EventPasswordChanged, reflect.TypeFor[PasswordChanged](), []string{"account_id", "by", "channel"}, nil},
	{EventFactorChanged, reflect.TypeFor[FactorChanged](), []string{"account_id", "factor_id", "kind", "change", "by"}, nil},
	{EventLoginSucceeded, reflect.TypeFor[LoginSucceeded](), []string{"account_id", "session_id", "methods", "assurance", "delivery"}, nil},
	{EventSessionRevoked, reflect.TypeFor[SessionRevoked](), []string{"session_id", "account_id", "scope", "reason_class"}, nil},
	{EventThrottleEngaged, reflect.TypeFor[ThrottleEngaged](), []string{"scope", "account_id"}, []string{"account_id"}},
	{EventTokenReuseDetected, reflect.TypeFor[TokenReuseDetected](), []string{"session_id", "account_id"}, nil},
	{EventImpersonationStarted, reflect.TypeFor[ImpersonationStarted](), []string{"grant_id", "actor_id", "account_id", "session_id", "expires_at"}, nil},
	{EventImpersonationEnded, reflect.TypeFor[ImpersonationEnded](), []string{"grant_id", "ended_by"}, nil},
	{EventIDPChanged, reflect.TypeFor[IDPChanged](), []string{"idp_id", "change"}, nil},
}

// TestTheEventTypesAreTheDesignsFifteen holds the names of the types, which a
// subscriber matches as strings.
func TestTheEventTypesAreTheDesignsFifteen(t *testing.T) {
	want := []string{
		"authn.account.created",
		"authn.account.status-changed",
		"authn.account.deleted",
		"authn.identifier.changed",
		"authn.binding.changed",
		"authn.membership.changed",
		"authn.password.changed",
		"authn.factor.changed",
		"authn.login.succeeded",
		"authn.session.revoked",
		"authn.throttle.engaged",
		"authn.token.reuse-detected",
		"authn.impersonation.started",
		"authn.impersonation.ended",
		"authn.idp.changed",
	}
	var got []string
	for _, e := range eventTable {
		got = append(got, e.name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("event types = %q, want %q", got, want)
	}
	nameShape := regexp.MustCompile(`^authn(\.[a-z]+(-[a-z]+)*){2}$`)
	seen := map[string]bool{}
	for _, n := range got {
		if !nameShape.MatchString(n) {
			t.Errorf("event type %q is not authn.<area>.<what>, lower case with hyphens", n)
		}
		if seen[n] {
			t.Errorf("event type %q is declared twice", n)
		}
		seen[n] = true
	}
}

// constNames returns the names of the constants of events.go that start with
// prefix and are of the untyped string kind, read from the source so that one
// added without an entry in the tables above is found.
func constNames(t *testing.T, prefix string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "events.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			for _, n := range s.(*ast.ValueSpec).Names {
				if strings.HasPrefix(n.Name, prefix) && n.IsExported() {
					out = append(out, n.Name)
				}
			}
		}
	}
	return out
}

// TestEveryEventConstantIsInTheTable holds the table above against the source:
// an event constant with no entry has no payload checked.
func TestEveryEventConstantIsInTheTable(t *testing.T) {
	if got := len(constNames(t, "Event")); got != len(eventTable) {
		t.Errorf("events.go declares %d Event constants, the table has %d entries", got, len(eventTable))
	}
	// Every payload struct of events.go is in the table too.
	f, err := parser.ParseFile(token.NewFileSet(), "events.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	structs := 0
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.TYPE {
			for _, s := range gd.Specs {
				if _, ok := s.(*ast.TypeSpec).Type.(*ast.StructType); ok {
					structs++
				}
			}
		}
	}
	if structs != len(eventTable) {
		t.Errorf("events.go declares %d structs, the table has %d payloads", structs, len(eventTable))
	}
}

var (
	tagShape = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	// secretWords are words a payload field's name may not contain: what they
	// name is a credential, free text, or a handle a person typed.
	secretWords = []string{"secret", "password", "token", "code", "otp", "email", "phone", "identifier", "username", "assertion", "state", "key"}
	// freeTextNames are whole names a field may not have, because what they hold
	// is free text; reason_class is a class and is allowed.
	freeTextNames = []string{"reason", "note", "message", "text", "display_name", "name", "label", "detail", "purpose"}
)

// TestEveryPayloadHasTheDesignsShape holds the shape each payload is held to:
// JSON names in lower case with underscores, top-level scalars only, and no field
// whose name says it holds a secret, free text or a typed handle.
func TestEveryPayloadHasTheDesignsShape(t *testing.T) {
	for _, e := range eventTable {
		t.Run(e.name, func(t *testing.T) {
			if e.typ.Kind() != reflect.Struct {
				t.Fatalf("%s is not a struct", e.typ)
			}
			var tags []string
			for f := range e.typ.Fields() {
				tag, ok := f.Tag.Lookup("json")
				if !ok {
					t.Errorf("%s has no json tag", f.Name)
					continue
				}
				name, _, _ := strings.Cut(tag, ",")
				tags = append(tags, name)
				if !tagShape.MatchString(name) {
					t.Errorf("%s: the JSON name %q is not lower case with underscores", f.Name, name)
				}
				switch f.Type.Kind() {
				case reflect.String, reflect.Bool, reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint32, reflect.Uint64, reflect.Float64:
				default:
					t.Errorf("%s is a %s, and a payload's top-level fields are scalars", f.Name, f.Type.Kind())
				}
				for _, w := range secretWords {
					for _, part := range strings.Split(name, "_") {
						if part == w {
							t.Errorf("%s: the JSON name %q holds the word %q, which names something a payload does not carry", f.Name, name, w)
						}
					}
				}
				if slices.Contains(freeTextNames, name) {
					t.Errorf("%s: the JSON name %q names free text", f.Name, name)
				}
				for _, w := range []string{"secret", "password", "token", "otp", "email", "phone"} {
					if strings.Contains(strings.ToLower(f.Name), w) {
						t.Errorf("the Go field name %q holds %q, which names something a payload does not carry", f.Name, w)
					}
				}
			}
			if !slices.Equal(tags, e.fields) {
				t.Errorf("JSON names = %q, want the design's %q", tags, e.fields)
			}
		})
	}
}

// TestAPayloadMarshalsToExactlyItsFields marshals each payload with every field
// set and reads back the keys, so a field that carries a tag the reflection above
// read and the encoder does not honour, or one the encoder leaves out, is found.
func TestAPayloadMarshalsToExactlyItsFields(t *testing.T) {
	for _, e := range eventTable {
		full := reflect.New(e.typ).Elem()
		for f := range full.NumField() {
			full.Field(f).SetString("v")
		}
		raw, err := json.Marshal(full.Interface())
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]string
		if err = json.Unmarshal(raw, &keys); err != nil {
			t.Fatalf("%s marshals to %s, which is not an object of strings: %v", e.name, raw, err)
		}
		var got []string
		for k := range keys {
			got = append(got, k)
		}
		slices.Sort(got)
		want := slices.Clone(e.fields)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s marshals the keys %q, want %q", e.name, got, want)
		}

		// A field the design leaves out when empty is left out, and every other
		// one is written even when empty: a subscriber reads a missing key as
		// "not applicable".
		raw, err = json.Marshal(reflect.New(e.typ).Elem().Interface())
		if err != nil {
			t.Fatal(err)
		}
		var empty map[string]string
		if err := json.Unmarshal(raw, &empty); err != nil {
			t.Fatal(err)
		}
		for _, f := range e.fields {
			_, present := empty[f]
			if present == slices.Contains(e.omitted, f) {
				t.Errorf("%s: the empty field %q is present = %v, want %v", e.name, f, present, !slices.Contains(e.omitted, f))
			}
		}
	}
}

// TestAThrottleEventCarriesAnAccountOnlyForTheScopesThatHaveOne is the one
// payload with an optional field, which is there for account and factor and
// absent for the keys that are not accounts.
func TestAThrottleEventCarriesAnAccountOnlyForTheScopesThatHaveOne(t *testing.T) {
	raw, _ := json.Marshal(ThrottleEngaged{Scope: string(ScopeIdentifier)})
	if strings.Contains(string(raw), "account_id") {
		t.Errorf("an identifier-scope payload is %s, which names an account", raw)
	}
	raw, _ = json.Marshal(ThrottleEngaged{Scope: string(ScopeAccount), AccountID: "a"})
	if !strings.Contains(string(raw), `"account_id":"a"`) {
		t.Errorf("an account-scope payload is %s, which lacks the account", raw)
	}
}

// reasonClasses is the design's closed set, in the table's order.
var reasonClasses = []string{
	"admin", "self", "password-change", "factor-reset", "refresh-reuse",
	"expired", "account-disabled", "account-deleted", "evicted", "throttled",
}

// TestTheReasonClassesAreTheDesignsTen holds the closed set against the design
// and against the source, and that a value outside it is refused.
func TestTheReasonClassesAreTheDesignsTen(t *testing.T) {
	consts := map[string]string{
		"ReasonAdmin": ReasonAdmin, "ReasonSelf": ReasonSelf, "ReasonPasswordChange": ReasonPasswordChange,
		"ReasonFactorReset": ReasonFactorReset, "ReasonRefreshReuse": ReasonRefreshReuse,
		"ReasonExpired": ReasonExpired, "ReasonAccountDisabled": ReasonAccountDisabled,
		"ReasonAccountDeleted": ReasonAccountDeleted, "ReasonEvicted": ReasonEvicted, "ReasonThrottled": ReasonThrottled,
	}
	var got []string
	for _, v := range consts {
		got = append(got, v)
	}
	slices.Sort(got)
	want := slices.Clone(reasonClasses)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the Reason constants are %q, want the design's %q", got, want)
	}
	if n := len(constNames(t, "Reason")); n != len(reasonClasses) {
		t.Errorf("events.go declares %d Reason constants, the design has %d classes", n, len(reasonClasses))
	}

	for _, c := range reasonClasses {
		if !ValidReasonClass(c) {
			t.Errorf("ValidReasonClass(%q) = false, want true", c)
		}
		if got, want := ValidPayloadReasonClass(c), c != "throttled"; got != want {
			t.Errorf("ValidPayloadReasonClass(%q) = %v, want %v: throttled belongs to a log line alone", c, got, want)
		}
	}
	for _, c := range []string{
		"", " ", "Admin", "ADMIN", "admin ", " admin", "admin\n", "password_change", "passwordchange",
		"other", "unknown", "account", "expire", "evict", "throttled ", "a free text reason", "admin,self",
	} {
		if ValidReasonClass(c) || ValidPayloadReasonClass(c) {
			t.Errorf("%q is accepted as a reason class, which is outside the closed set", c)
		}
	}
}
