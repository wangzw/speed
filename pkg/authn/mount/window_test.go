package mount

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/internal/mount"
)

func panicText(f func()) (text string, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			text, panicked = fmt.Sprint(r), true
		}
	}()
	f()
	return "", false
}

// TestARegistrationOutsideTheWindowPanicsAndNamesTheRoute holds the wrong-stage panic and the route in its text.
func TestARegistrationOutsideTheWindowPanicsAndNamesTheRoute(t *testing.T) {
	liveStack{
		register: func(m mount.Mounter) { m.Public("GET", "/early", reachedAs) },
	}.serve(t, func(l *live) {
		for _, c := range []struct {
			name string
			call func(m mount.Mounter)
			want []string
		}{
			{"Public", func(m mount.Mounter) { m.Public("POST", "/late", handler) }, []string{"POST", "/api/v1/auth/late"}},
			{"Optional", func(m mount.Mounter) { m.Optional("GET", "/late-opt", handler) }, []string{"GET", "/api/v1/auth/late-opt"}},
			{"Guarded", func(m mount.Mounter) { m.Guarded("PUT", "/late-guard", handler) }, []string{"PUT", "/api/v1/auth/late-guard"}},
			{"Callback", func(m mount.Mounter) { m.Callback([]string{"GET"}, "/late/:p/cb", handler) }, []string{"GET", "/api/v1/auth/late/:p/cb"}},
		} {
			text, panicked := panicText(func() { c.call(l.mounter) })
			if !panicked {
				t.Errorf("%s: a registration after the window shut did not panic", c.name)
				continue
			}
			for _, w := range c.want {
				if !strings.Contains(text, w) {
					t.Errorf("%s: the panic %q does not name %q", c.name, text, w)
				}
			}
		}
	})
}

// TestARegistrationBeforeTheModuleHasInitialisedPanicsAndNamesTheRoute holds that a pre-Init registration panics rather than dropping the route.
func TestARegistrationBeforeTheModuleHasInitialisedPanicsAndNamesTheRoute(t *testing.T) {
	var s settings
	s.Mount.Bases = defaultBases()
	p, err := newProduct(s)
	if err != nil {
		t.Fatalf("building the product: %v", err)
	}
	if p.Endpoint() != "" {
		t.Errorf("the endpoint is %q before Init, want none", p.Endpoint())
	}
	for name, call := range map[string]func(){
		"Public":   func() { p.Public("POST", "/login", handler) },
		"Optional": func() { p.Optional("POST", "/login", handler) },
		"Guarded":  func() { p.Guarded("POST", "/login", handler) },
		"Callback": func() { p.Callback([]string{"POST"}, "/login", handler) },
	} {
		text, panicked := panicText(call)
		if !panicked {
			t.Errorf("%s: a registration before Init did not panic", name)
			continue
		}
		for _, w := range []string{name, "/login", ModuleName, "Init"} {
			if !strings.Contains(text, w) {
				t.Errorf("%s: the panic %q does not name %q", name, text, w)
			}
		}
	}
}

// TestARegistrationThatCannotMeanWhatItSaysPanicsAndRegistersNothing holds that a contradictory call panics naming the route and writes nothing.
func TestARegistrationThatCannotMeanWhatItSaysPanicsAndRegistersNothing(t *testing.T) {
	l := &ledger{}
	var m mount.Mounter
	err := stack{
		body:     `{}`,
		router:   &standInRouter{names: []string{"main"}, ledger: l},
		gate:     standInGate{ledger: l},
		register: func(got mount.Mounter) { m = got },
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	var nilHandler gin.HandlerFunc
	for _, c := range []struct {
		name string
		call func()
		want []string
	}{
		{"a path with no leading slash", func() { m.Public("GET", "login", handler) }, []string{"GET", `"login"`, "slash"}},
		{"an empty path", func() { m.Guarded("GET", "", handler) }, []string{"GET", "slash"}},
		{"a nil handler", func() { m.Optional("GET", "/login", nilHandler) }, []string{"GET", "/login", "nil handler"}},
		{"a nil handler on a callback", func() { m.Callback([]string{"GET"}, "/cb", nilHandler) }, []string{"/cb", "nil handler"}},
		{"a callback with no method", func() { m.Callback(nil, "/cb", handler) }, []string{"/cb", "no method"}},
		{"a callback with an empty method", func() { m.Callback([]string{"GET", ""}, "/cb", handler) }, []string{"/cb", "empty method"}},
		{"a callback with a method twice", func() { m.Callback([]string{"POST", "POST"}, "/cb", handler) }, []string{"/cb", "POST twice"}},
		{"a route with no method", func() { m.Public("", "/login", handler) }, []string{"/login", "empty method"}},
	} {
		text, panicked := panicText(c.call)
		if !panicked {
			t.Errorf("%s did not panic", c.name)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s: the panic %q does not name %q", c.name, text, w)
			}
		}
	}
	if routes, access := l.snapshot(); len(routes) != 0 || len(access) != 0 {
		t.Errorf("refused registrations left %v and %v behind", routes, access)
	}
}

type refusingGate struct{ standInGate }

func (refusingGate) Exempt(string, string, string) { panic("the registration gate is shut") }

// TestARefusalFromTheGatewayNamesTheRouteToo holds the route in a gateway-side panic.
func TestARefusalFromTheGatewayNamesTheRouteToo(t *testing.T) {
	l := &ledger{}
	var m mount.Mounter
	err := stack{
		body:     `{}`,
		router:   &standInRouter{names: []string{"main"}, ledger: l},
		gate:     refusingGate{standInGate{ledger: l}},
		register: func(got mount.Mounter) { m = got },
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	text, panicked := panicText(func() { m.Public("POST", "/login", handler) })
	if !panicked {
		t.Fatal("a refusal of the gateway's surface did not panic")
	}
	for _, w := range []string{"Public", "POST", "/api/v1/auth/login", "the registration gate is shut"} {
		if !strings.Contains(text, w) {
			t.Errorf("the panic %q does not name %q", text, w)
		}
	}
}
