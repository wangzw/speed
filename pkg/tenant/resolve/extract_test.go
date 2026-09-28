package resolve

import (
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/tenant"
)

func request(t *testing.T, target, host string, header map[string]string) *nethttp.Request {
	t.Helper()
	r := httptest.NewRequest(nethttp.MethodGet, target, nil)
	if host != "" {
		r.Host = host
	}
	for k, v := range header {
		r.Header.Set(k, v)
	}
	return r
}

func TestHeaderExtractor(t *testing.T) {
	e := headerExtractor{name: "X-Tenant"}
	for _, tc := range []struct {
		what   string
		value  string
		want   string
		wantOK bool
	}{
		{"a plain value", "acme", "acme", true},
		{"surrounding space", "  acme  ", "acme", true},
		{"empty", "", "", false},
		{"blanks alone", "   ", "", false},
	} {
		t.Run(tc.what, func(t *testing.T) {
			h := map[string]string{}
			if tc.value != "" {
				h["X-Tenant"] = tc.value
			}
			got, ok := e.Extract(request(t, "/", "api.example.com", h))
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestSubdomainExtractor(t *testing.T) {
	for _, tc := range []struct {
		what  string
		host  string
		index int
		want  string
	}{
		{"the leading label", "acme.example.com", 0, "acme"},
		{"a middle label", "acme.example.com", 1, "example"},
		{"a port is not a label", "acme.example.com:8443", 0, "acme"},
		{"out of range", "acme.example.com", 7, ""},
	} {
		t.Run(tc.what, func(t *testing.T) {
			got, ok := subdomainExtractor{index: tc.index}.
				Extract(request(t, "/", tc.host, nil))
			if tc.want == "" {
				if ok {
					t.Fatalf("got (%q, true), want no name", got)
				}
				return
			}
			if got != tc.want || !ok {
				t.Fatalf("got (%q, %v), want %q", got, ok, tc.want)
			}
		})
	}
}

func TestPathExtractor(t *testing.T) {
	for _, tc := range []struct {
		what  string
		path  string
		index int
		want  string
	}{
		{"the leading segment", "/acme/orders", 0, "acme"},
		{"a later segment", "/acme/orders", 1, "orders"},
		{"a trailing slash is not a segment", "/acme/orders/", 1, "orders"},
		{"out of range", "/acme", 3, ""},
		{"the root", "/", 0, ""},
	} {
		t.Run(tc.what, func(t *testing.T) {
			got, ok := pathExtractor{index: tc.index}.
				Extract(request(t, tc.path, "api.example.com", nil))
			if tc.want == "" {
				if ok {
					t.Fatalf("got (%q, true), want no name", got)
				}
				return
			}
			if got != tc.want || !ok {
				t.Fatalf("got (%q, %v), want %q", got, ok, tc.want)
			}
		})
	}
}

// stub is a stand-in for an extractor another module would register.
type stub struct{ name string }

func (s stub) Extract(*nethttp.Request) (string, bool) { return s.name, s.name != "" }

var _ tenant.Extractor = stub{}

// TestRegisterOutsideTheWindowPanics pins the shut side of the state
// transition and the message a caller acts on: the window runs from this
// module's Migrate callback to its Start callback, and the fixing action is to
// register from the registrant's own Init callback. Registration before that
// window opened is refused rather than dropped.
func TestRegisterOutsideTheWindowPanics(t *testing.T) {
	text := wantPanicMessage(t, func() {
		newResolver(settings{}, nil).Register("late", stub{})
	})
	for _, want := range []struct{ fragment, why string }{
		{"Migrate callback", "where the window that accepts registrations opens"},
		{"Start callback", "where it closes"},
		{"Init callback", "the fixing action the message names"},
	} {
		if !strings.Contains(text, want.fragment) {
			t.Fatalf("the panic text does not name %s (%q): %s", want.why, want.fragment, text)
		}
	}
}

// wantPanicMessage runs f, requires it to panic, and returns the panic's text.
func wantPanicMessage(t *testing.T, f func()) string {
	t.Helper()
	var text string
	func() {
		defer func() {
			raised := recover()
			if raised == nil {
				t.Fatal("the call did not panic")
			}
			s, ok := raised.(string)
			if !ok {
				t.Fatalf("the panic carried %T, want a message string", raised)
			}
			text = s
		}()
		f()
	}()
	return text
}

func TestSealRefusesADuplicateName(t *testing.T) {
	r := newResolver(settings{extractor: "header"}, nil)
	r.open()
	r.Register("header", stub{name: "acme"})
	r.Register("header", headerExtractor{name: "X-Tenant"})

	if err := r.seal(); err == nil {
		t.Fatal("a duplicate registration was accepted")
	}
}

// A name is free of blanks, and a blank is any character Unicode counts as
// space, not the four ASCII ones alone: a name carrying one cannot be written
// into configuration so that it reads back as the same name.
func TestSealRefusesANameCarryingAnyBlank(t *testing.T) {
	for _, name := range []string{"a b", "a\tb", "a\nb", "a\rb", "a\vb", "a\fb", "a\u00a0b", "a\u2028b", "\u3000"} {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			r := newResolver(settings{extractor: HeaderExtractor}, nil)
			r.open()
			r.Register(HeaderExtractor, headerExtractor{name: "X-Tenant"})
			r.Register(name, stub{name: "acme"})
			if err := r.seal(); !errors.Is(err, tenant.ErrInvalidExtractor) {
				t.Fatalf("registering %q gave %v at seal, want ErrInvalidExtractor", name, err)
			}
		})
	}
}

func TestSealRefusesAnUnregisteredSelection(t *testing.T) {
	r := newResolver(settings{extractor: "subdomain"}, nil)
	r.open()
	r.Register(HeaderExtractor, headerExtractor{name: "X-Tenant"})

	err := r.seal()
	if err == nil {
		t.Fatal("selecting an extractor that was never registered was accepted")
	}
	if !errors.Is(err, tenant.ErrNoExtractor) {
		t.Fatalf("got %v, want ErrNoExtractor", err)
	}
}

func TestSealRefusesANilImplementation(t *testing.T) {
	r := newResolver(settings{extractor: "custom"}, nil)
	r.open()
	r.Register("custom", nil)

	if err := r.seal(); err == nil {
		t.Fatal("a nil implementation was accepted")
	}
}

func TestSealSelectsTheConfiguredExtractor(t *testing.T) {
	r := newResolver(settings{extractor: HeaderExtractor}, nil)
	r.open()
	r.Register(HeaderExtractor, headerExtractor{name: "X-Tenant"})
	r.Register(PathExtractor, pathExtractor{index: 0})

	if err := r.seal(); err != nil {
		t.Fatalf("sealing: %v", err)
	}
	if _, ok := r.selected.(headerExtractor); !ok {
		t.Fatalf("selected %T, want the configured one", r.selected)
	}
}
