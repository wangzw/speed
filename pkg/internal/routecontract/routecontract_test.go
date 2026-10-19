package routecontract

import (
	"reflect"
	"testing"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The check reads an OpenAPI path that writes its parameter as {name} and a
// registration that writes it as :name. The rewrite is what makes the two
// comparable, so a case that let either form through unread would report every
// parameterised route as both undeclared and unregistered.
func TestGinPathRewritesAnOpenAPIParameter(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/api/v1/objects", "/api/v1/objects"},
		{"/api/v1/objects/{id}", "/api/v1/objects/:id"},
		{"/api/v1/system/auth/accounts/{id}/secrets/{secret}", "/api/v1/system/auth/accounts/:id/secrets/:secret"},
		{"/a/{b}/c/{d}/e", "/a/:b/c/:d/e"},
	} {
		if got := ginPath(c.in); got != c.want {
			t.Errorf("ginPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The check reads only the operations a path item carries. A path item with
// no method in it contributes nothing, and a method is compared case-folded.
func TestOperationsReadsMethodsAndRewritesTheirPath(t *testing.T) {
	got := operations(t, "example", []byte(`{"paths":{
		"/things": {"get": {}, "POST": {}, "parameters": []},
		"/things/{id}": {"delete": {}}
	}}`))
	want := map[string]bool{
		"GET /things":        true,
		"POST /things":       true,
		"DELETE /things/:id": true,
	}
	if len(got) != len(want) {
		t.Fatalf("operations read %v, want the three operations in %v", got, want)
	}
	for _, r := range got {
		if !want[r.key()] {
			t.Errorf("operations read %s, which is not an operation of the fragment", r)
		}
	}
}

// The comparison has to be discriminating in both directions, and it has to
// say which direction each difference is: a route served and undocumented is a
// different defect from an operation a client would call and the process would
// not answer.
func TestDifferencesReportsBothDirections(t *testing.T) {
	registered := []Route{
		{Method: "GET", Path: "/declared"},
		{Method: "GET", Path: "/undocumented"},
	}
	declared := []Route{
		{Method: "GET", Path: "/declared"},
		{Method: "POST", Path: "/declared"},
		{Method: "GET", Path: "/unserved"},
	}
	undeclared, unregistered := differences(registered, declared)
	if !reflect.DeepEqual(undeclared, []string{"GET /undocumented"}) {
		t.Errorf("the routes no fragment declares are %v, want the one that is served and undocumented", undeclared)
	}
	if !reflect.DeepEqual(unregistered, []string{"GET /unserved", "POST /declared"}) {
		t.Errorf("the operations no route registered are %v, want the one unserved path and the one wrong method",
			unregistered)
	}
}

// The comparison over two sets that are one reports nothing, which is what the
// whole check comes to when the fragment and the registrations agree.
func TestDifferencesIsSilentWhenTheTwoSetsAreOne(t *testing.T) {
	routes := []Route{{Method: "GET", Path: "/things/:id"}, {Method: "DELETE", Path: "/things/:id"}}
	undeclared, unregistered := differences(routes, routes)
	if len(undeclared) != 0 || len(unregistered) != 0 {
		t.Errorf("the same set compared against itself reported %v and %v", undeclared, unregistered)
	}
}

// readParams reads an operation's declared query parameters and skips every
// parameter that is not in the query, so that the per-operation set the
// parameter check compares is the query surface alone; a parameter a path item
// writes above its methods is not read either.
func TestReadParamsReadsQueryParametersOnly(t *testing.T) {
	into := map[Route]map[string]bool{}
	readParams(t, "example", speedhttp.Spec{Document: []byte(`{"paths":{
		"/things/{id}": {"get": {"parameters":[
			{"name": "id", "in": "path"},
			{"name": "limit", "in": "query"},
			{"name": "after", "in": "query"}
		]}},
		"/things": {"post": {"parameters": [{"name": "X-Token", "in": "header"}]}}
	}}`)}, into)
	get := into[Route{Method: "GET", Path: "/things/:id"}]
	if !get["limit"] || !get["after"] || get["id"] {
		t.Errorf("the get operation's query parameters are %v, want limit and after and no path parameter", get)
	}
	if _, ok := into[Route{Method: "POST", Path: "/things"}]; ok {
		t.Errorf("a header parameter was read as a query parameter: %v", into)
	}
}

// The parameter comparison is discriminating in both directions, and a route
// present in only one of the two sets is compared against the empty set, so an
// unaccepted declaration and an undeclared acceptance are both caught.
func TestParamDifferencesReportsBothDirections(t *testing.T) {
	list := Route{Method: "GET", Path: "/things"}
	declared := map[Route]map[string]bool{
		list:                            {"limit": true, "after": true, "bogus": true},
		{Method: "GET", Path: "/extra"}: {"q": true},
	}
	accepted := map[Route]map[string]bool{
		list:                                   {"limit": true, "after": true, "kind": true},
		{Method: "GET", Path: "/onlyaccepted"}: {"z": true},
	}
	declaredOnly, acceptedOnly := paramDifferences(declared, accepted)
	if want := []paramSig{{Route: Route{Method: "GET", Path: "/extra"}, Name: "q"}, {Route: list, Name: "bogus"}}; !reflect.DeepEqual(declaredOnly, want) {
		t.Errorf("the declared parameters no handler accepts are %v, want %v", declaredOnly, want)
	}
	if want := []paramSig{{Route: Route{Method: "GET", Path: "/onlyaccepted"}, Name: "z"}, {Route: list, Name: "kind"}}; !reflect.DeepEqual(acceptedOnly, want) {
		t.Errorf("the accepted parameters no fragment declares are %v, want %v", acceptedOnly, want)
	}
}

// The comparison over two per-operation sets that are one reports nothing, which
// is what the whole check comes to when the fragment and the handlers agree.
func TestParamDifferencesIsSilentWhenTheTwoSetsAreOne(t *testing.T) {
	sets := map[Route]map[string]bool{
		{Method: "GET", Path: "/things"}: {"limit": true, "after": true},
	}
	declaredOnly, acceptedOnly := paramDifferences(sets, sets)
	if len(declaredOnly) != 0 || len(acceptedOnly) != 0 {
		t.Errorf("the same set compared against itself reported %v and %v", declaredOnly, acceptedOnly)
	}
}

// Config merges two subtrees that write the same mapping, which is what lets a
// fixture's "authn" section and a case's "authn.session" section live in one
// document, and it panics when the two write the same leaf: one silently
// replacing the other is how a case comes to test a configuration it did not
// write.
func TestConfigMergesMappingsAndRefusesToOverwriteALeaf(t *testing.T) {
	merged := Config(
		map[string]any{"authn": map[string]any{"gate": map[string]any{"public-url": "https://a.example.com"}}},
		map[string]any{"authn": map[string]any{"session": map[string]any{"cookie-name": "sid"}}},
	)
	authn, ok := merged["authn"].(map[string]any)
	if !ok {
		t.Fatalf("the merged document has no authn section: %v", merged)
	}
	if _, ok := authn["gate"]; !ok {
		t.Errorf("the merge dropped the fixture's own section: %v", authn)
	}
	if _, ok := authn["session"]; !ok {
		t.Errorf("the merge dropped the case's section: %v", authn)
	}

	defer func() {
		if recover() == nil {
			t.Error("two parts writing the same leaf merged silently instead of being refused")
		}
	}()
	Config(
		map[string]any{"authn": map[string]any{"session": map[string]any{"cookie-name": "sid"}}},
		map[string]any{"authn": map[string]any{"session": map[string]any{"cookie-name": "other"}}},
	)
}
