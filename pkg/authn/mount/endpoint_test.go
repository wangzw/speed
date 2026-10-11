package mount

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// mountOn is the configuration body that names the endpoint the routes go on;
// an empty name leaves the key out.
func mountOn(endpoint string) string {
	if endpoint == "" {
		return `{}`
	}
	return `{"authn":{"gate":{"mount":{"endpoint":"` + endpoint + `"}}}}`
}

// TestTheMountEndpointIsTheConfiguredOneOrTheOnlyOneTheEntryPointDeclares holds
// the rule from every side it has. The key written is used as written, whatever
// else the entry point declares; the key empty takes the one endpoint there is;
// and with none or several declared there is nothing to pick, which is
// ErrRoutesUnconfigured naming the key and the endpoints to choose from.
func TestTheMountEndpointIsTheConfiguredOneOrTheOnlyOneTheEntryPointDeclares(t *testing.T) {
	for _, c := range []struct {
		name       string
		declared   []string
		configured string
		want       string   // the endpoint the routes land on, when the start succeeds
		wantErr    error    // otherwise
		inMessage  []string // what the failure names
	}{
		{name: "one endpoint declared, key empty", declared: []string{"main"}, want: "main"},
		{name: "one endpoint declared, the key names it", declared: []string{"main"}, configured: "main", want: "main"},
		{name: "several declared, the key names one", declared: []string{"admin", "public"}, configured: "public", want: "public"},
		{name: "several declared, the key names the other", declared: []string{"admin", "public"}, configured: "admin", want: "admin"},
		{
			name: "no endpoint declared, key empty", wantErr: authn.ErrRoutesUnconfigured,
			inMessage: []string{"authn.gate.mount.endpoint", "no endpoint"},
		},
		{
			name: "two endpoints declared, key empty", declared: []string{"public", "admin"},
			wantErr: authn.ErrRoutesUnconfigured, inMessage: []string{"authn.gate.mount.endpoint", "admin, public"},
		},
		{
			name: "three endpoints declared, key empty", declared: []string{"a", "b", "c"},
			wantErr: authn.ErrRoutesUnconfigured, inMessage: []string{"3 endpoints", "a, b, c"},
		},
		{
			name: "the key names an endpoint the entry point does not have", declared: []string{"main"},
			configured: "ghost", wantErr: authn.ErrUnknownEndpoint, inMessage: []string{"authn.gate.mount.endpoint", `"ghost"`},
		},
		{
			name: "the key names an endpoint and none is declared", configured: "ghost",
			wantErr: authn.ErrUnknownEndpoint, inMessage: []string{`"ghost"`},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := &ledger{}
			var seen mount.Mounter
			err := stack{
				body:   mountOn(c.configured),
				router: &standInRouter{names: c.declared, ledger: l},
				gate:   standInGate{ledger: l}, realGateModule: true,
				register: func(m mount.Mounter) { seen = m; m.Public("GET", "/ping", handler) },
			}.drive(t, nothing)

			routes, access := l.snapshot()
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("the drive returned %v, want %v", err, c.wantErr)
				}
				for _, s := range c.inMessage {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("the failure %q does not name %q", err, s)
					}
				}
				if len(routes) != 0 || len(access) != 0 {
					t.Errorf("a module registered %v and %v although the start failed", routes, access)
				}
				return
			}
			if err != nil {
				t.Fatalf("the drive failed: %v", err)
			}
			if seen.Endpoint() != c.want {
				t.Errorf("the capability reports endpoint %q, want %q", seen.Endpoint(), c.want)
			}
			if len(routes) != 1 || routes[0].endpoint != c.want {
				t.Errorf("the route landed on %v, want the endpoint %q alone", routes, c.want)
			}
			if len(access) != 1 || access[0].endpoint != c.want {
				t.Errorf("the exemption landed on %v, want the endpoint %q alone", access, c.want)
			}
		})
	}
}

// TestACommandWithNoEntryPointFailsWithErrNoRouter holds the module's answer to
// a command that mounts routes and carries nothing to mount them on.
func TestACommandWithNoEntryPointFailsWithErrNoRouter(t *testing.T) {
	err := stack{body: `{}`, gate: standInGate{ledger: &ledger{}}}.drive(t, nothing)
	if !errors.Is(err, authn.ErrNoRouter) {
		t.Fatalf("the drive returned %v, want %v", err, authn.ErrNoRouter)
	}
}

// TestTheStartupDiagnosticNamesTheEndpointAndTheBasePath holds the one line this
// module writes once it has settled where the routes go.
func TestTheStartupDiagnosticNamesTheEndpointAndTheBasePath(t *testing.T) {
	logs := &recorder{}
	l := &ledger{}
	err := stack{
		body:   `{"authn":{"gate":{"mount":{"endpoint":"admin","base-path":"/identity"}}}}`,
		router: &standInRouter{names: []string{"admin", "public"}, ledger: l},
		gate:   standInGate{ledger: l}, realGateModule: true,
		logs: logs,
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	rec, ok := logs.recordNamed("authn: the authentication routes are mounted")
	if !ok {
		t.Fatalf("no diagnostic line was written; the records are %v", logs.recordsOf())
	}
	if got := attrText(rec, "endpoint"); got != "admin" {
		t.Errorf("the line names endpoint %q, want admin", got)
	}
	if got := attrText(rec, "base_path"); got != "/identity" {
		t.Errorf("the line names base path %q, want /identity", got)
	}
}
