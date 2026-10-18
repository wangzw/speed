package openapi

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// spec builds one fragment resource the way a module declares it: a module
// name, the endpoint the fragment is for, and the JSON document.
func spec(module, endpoint, document string) core.Resource[speedhttp.Spec] {
	return core.Resource[speedhttp.Spec]{
		Module: module,
		Value:  speedhttp.Spec{Endpoint: endpoint, Document: []byte(document)},
	}
}

// parse reads the merged document into a generic map so a case can assert on
// the shape the readers see.
func parse(t *testing.T, merged []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(merged, &doc); err != nil {
		t.Fatalf("the merged document is not a JSON object: %v (%s)", err, merged)
	}
	return doc
}

// keysOf returns the keys of an object map, sorted, for a failure message.
func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// nested walks a chain of object keys and reports the value there, failing the
// case when any of them is absent or is not an object.
func nested(t *testing.T, doc map[string]any, keys ...string) map[string]any {
	t.Helper()
	at := doc
	for _, key := range keys {
		next, ok := at[key].(map[string]any)
		if !ok {
			t.Fatalf("the document has no object at %v (at %q: %v)", keys, key, at[key])
		}
		at = next
	}
	return at
}

// TestMergeResolvesPathsAgainstTheSpecRoot pins the composition each fragment
// relies on: its paths are relative to the Spec's Prefix and Version, and two
// fragments under different roots keep two distinct operations even when the
// relative path is spelled the same.
func TestMergeResolvesPathsAgainstTheSpecRoot(t *testing.T) {
	merged, err := Merge([]core.Resource[speedhttp.Spec]{
		{Module: "account", Value: speedhttp.Spec{
			Endpoint: "account", Prefix: "api",
			Version: "v1", Document: []byte(`{"paths":{"/auth/me":{"get":{}}}}`),
		}},
		{Module: "unversioned", Value: speedhttp.Spec{
			Endpoint: "api", Prefix: "api",
			Document: []byte(`{"paths":{"/auth/me":{"get":{}}}}`),
		}},
	}, Options{Title: "the platform", Version: "1.2.3"})
	if err != nil {
		t.Fatalf("merging two fragments under different roots failed: %v", err)
	}
	paths := nested(t, parse(t, merged), "paths")
	for _, want := range []string{"/api/v1/auth/me", "/api/auth/me"} {
		if _, ok := paths[want]; !ok {
			t.Errorf("the merged document has no path %q; it holds %v", want, keysOf(paths))
		}
	}
}

// TestMergeJudgesConflictsOnTheResolvedPath holds two fragments declaring one
// operation to be a conflict once their relative paths resolve to the same
// absolute path.
func TestMergeJudgesConflictsOnTheResolvedPath(t *testing.T) {
	_, err := Merge([]core.Resource[speedhttp.Spec]{
		{Module: "one", Value: speedhttp.Spec{
			Endpoint: "account", Prefix: "api",
			Version: "v1", Document: []byte(`{"paths":{"/auth/me":{"get":{}}}}`),
		}},
		{Module: "two", Value: speedhttp.Spec{
			Endpoint: "admin", Prefix: "api",
			Version: "v1", Document: []byte(`{"paths":{"/auth/me":{"get":{}}}}`),
		}},
	}, Options{Title: "the platform", Version: "1.2.3"})
	if !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("two fragments resolving to one path merged with %v, want ErrOperationConflict", err)
	}
	for _, module := range []string{"one", "two"} {
		if !strings.Contains(err.Error(), module) {
			t.Errorf("the conflict %q does not name module %q", err, module)
		}
	}
}

func TestMergeCombinesFragmentsAndNamesTheirSources(t *testing.T) {
	merged, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("beta", "admin", `{"paths":{"/b":{"get":{"summary":"b"}}}}`),
		spec("alpha", "api", `{"paths":{"/a":{"post":{"summary":"a"}}}}`),
	}, Options{Title: "the platform", Version: "1.2.3"})
	if err != nil {
		t.Fatalf("merging two disjoint fragments failed: %v", err)
	}
	doc := parse(t, merged)

	if got := doc["openapi"]; got != openAPIVersion {
		t.Errorf("the document declares openapi %v, want %q", got, openAPIVersion)
	}
	info := nested(t, doc, "info")
	if got := info["title"]; got != "the platform" {
		t.Errorf("info.title is %v, want %q", got, "the platform")
	}
	if got := info["version"]; got != "1.2.3" {
		t.Errorf("info.version is %v, want %q", got, "1.2.3")
	}

	paths := nested(t, doc, "paths")
	if _, ok := paths["/a"]; !ok {
		t.Errorf("the merged paths %v do not carry /a", paths)
	}
	if _, ok := paths["/b"]; !ok {
		t.Errorf("the merged paths %v do not carry /b", paths)
	}

	// Each operation carries the endpoint it was declared for.
	if got := nested(t, doc, "paths", "/a", "post")[endpointExtension]; got != "api" {
		t.Errorf("/a post carries %s %v, want %q", endpointExtension, got, "api")
	}
	if got := nested(t, doc, "paths", "/b", "get")[endpointExtension]; got != "admin" {
		t.Errorf("/b get carries %s %v, want %q", endpointExtension, got, "admin")
	}

	// x-fragments lists each source, in module order.
	fragments, ok := doc[fragmentsExtension].([]any)
	if !ok || len(fragments) != 2 {
		t.Fatalf("%s is %v, want two sources", fragmentsExtension, doc[fragmentsExtension])
	}
	first := fragments[0].(map[string]any)
	if first["module"] != "alpha" || first["endpoint"] != "api" {
		t.Errorf("the first source is %v, want alpha/api", first)
	}
	second := fragments[1].(map[string]any)
	if second["module"] != "beta" || second["endpoint"] != "admin" {
		t.Errorf("the second source is %v, want beta/admin", second)
	}
}

func TestMergeConflictNamesBothModulesAndEndpoints(t *testing.T) {
	_, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("alpha", "api", `{"paths":{"/things":{"get":{"summary":"one"}}}}`),
		spec("beta", "admin", `{"paths":{"/things":{"GET":{"summary":"two"}}}}`),
	}, Options{})
	if !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("two fragments declaring GET /things did not report ErrOperationConflict: %v", err)
	}
	// The conflict names both modules and both endpoints, which is the only
	// place the two fragments are tied back to their sources. The method is
	// uppercased, so a fragment that spelled it differently still collides.
	for _, want := range []string{`"alpha"`, `"api"`, `"beta"`, `"admin"`, "GET /things"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the conflict error %q does not name %s", err.Error(), want)
		}
	}
}

func TestMergeOperationConflictCrossesEndpoints(t *testing.T) {
	// The same route on two endpoints is two operations one path cannot tell
	// apart, so it is a conflict however far apart the endpoints are.
	_, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("alpha", "api", `{"paths":{"/things":{"get":{}}}}`),
		spec("alpha", "admin", `{"paths":{"/things":{"get":{}}}}`),
	}, Options{})
	if !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("one route on two endpoints did not report ErrOperationConflict: %v", err)
	}
}

func TestMergeComponentConflictNamesTheLaterModuleAndTheName(t *testing.T) {
	_, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("alpha", "api", `{"components":{"schemas":{"Error":{"type":"object"}}}}`),
		spec("beta", "admin", `{"components":{"schemas":{"Error":{"type":"string"}}}}`),
	}, Options{})
	if !errors.Is(err, ErrComponentConflict) {
		t.Fatalf("one component declared differently did not report ErrComponentConflict: %v", err)
	}
	if !strings.Contains(err.Error(), `"beta"`) {
		t.Errorf("the conflict error %q does not name the later module", err.Error())
	}
	if !strings.Contains(err.Error(), "schemas.Error") {
		t.Errorf("the conflict error %q does not name the component", err.Error())
	}
}

func TestMergeComponentConflictCountsIntegersPastFloatPrecision(t *testing.T) {
	// Two schemas differing only in an integer past 2^53 are different schemas.
	// Comparing them through a float64 would read them as equal and drop one.
	_, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("alpha", "api", `{"components":{"schemas":{"Count":{"maximum":9007199254740993}}}}`),
		spec("beta", "admin", `{"components":{"schemas":{"Count":{"maximum":9007199254740992}}}}`),
	}, Options{})
	if !errors.Is(err, ErrComponentConflict) {
		t.Fatalf("two schemas differing past float precision reported %v, want ErrComponentConflict", err)
	}
}

func TestMergeComponentWithEqualContentMerges(t *testing.T) {
	// The same schema written with different whitespace is the same schema, so
	// two fragments sharing one error body is not a conflict.
	merged, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("alpha", "api", `{"components":{"schemas":{"Error":{"type":"object","title":"e"}}}}`),
		spec("beta", "admin", `{"components":{"schemas":{"Error": {"title": "e", "type": "object"}}}}`),
	}, Options{})
	if err != nil {
		t.Fatalf("two fragments sharing one component reported %v, want no error", err)
	}
	schemas := nested(t, parse(t, merged), "components", "schemas")
	if _, ok := schemas["Error"]; !ok {
		t.Errorf("the merged components %v do not carry Error", schemas)
	}
}

func TestMergeEmptyYieldsAWellFormedDocument(t *testing.T) {
	merged, err := Merge(nil, Options{Title: "empty"})
	if err != nil {
		t.Fatalf("merging no fragments failed: %v", err)
	}
	doc := parse(t, merged)
	if got := doc["openapi"]; got != openAPIVersion {
		t.Errorf("openapi is %v, want %q", got, openAPIVersion)
	}
	for _, key := range []string{"paths", "components", fragmentsExtension} {
		if _, ok := doc[key]; !ok {
			t.Errorf("the document carries no %q key: %v", key, doc)
		}
	}
}

func TestMergeUnreadableFragmentNamesTheModule(t *testing.T) {
	_, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("alpha", "api", `[1, 2, 3]`),
	}, Options{})
	if !errors.Is(err, ErrUnreadable) {
		t.Fatalf("a fragment that is not an object did not report ErrUnreadable: %v", err)
	}
	if !strings.Contains(err.Error(), `"alpha"`) {
		t.Errorf("the error %q does not name the module that declared the fragment", err.Error())
	}
}

func TestMergeUnreadableOperationNamesTheModule(t *testing.T) {
	_, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("alpha", "api", `{"paths":{"/a":{"get":5}}}`),
	}, Options{})
	if !errors.Is(err, ErrUnreadable) {
		t.Fatalf("an operation that is not an object did not report ErrUnreadable: %v", err)
	}
	if !strings.Contains(err.Error(), `"alpha"`) {
		t.Errorf("the error %q does not name the module that declared the fragment", err.Error())
	}
}

func TestMergeNullFragmentIsUnreadable(t *testing.T) {
	_, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("alpha", "api", `null`),
	}, Options{})
	if !errors.Is(err, ErrUnreadable) {
		t.Fatalf("a null fragment did not report ErrUnreadable: %v", err)
	}
}
