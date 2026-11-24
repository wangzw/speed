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

func spec(module, endpoint, document string) core.Resource[speedhttp.Spec] {
	return core.Resource[speedhttp.Spec]{
		Module: module,
		Value:  speedhttp.Spec{Endpoint: endpoint, Document: []byte(document)},
	}
}

func parse(t *testing.T, merged []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(merged, &doc); err != nil {
		t.Fatalf("the merged document is not a JSON object: %v (%s)", err, merged)
	}
	return doc
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

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

// TestMergeResolvesPathsAgainstTheSpecRoot checks that fragment paths resolve
// against the Spec's prefix and version and stay distinct under different roots.
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

// TestMergeJudgesConflictsOnTheResolvedPath checks that two fragments resolving
// to one absolute path conflict.
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

// TestMergeCombinesFragmentsAndNamesTheirSources checks the merged metadata, the
// endpoint extension and the x-fragments list.
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

	if got := nested(t, doc, "paths", "/a", "post")[endpointExtension]; got != "api" {
		t.Errorf("/a post carries %s %v, want %q", endpointExtension, got, "api")
	}
	if got := nested(t, doc, "paths", "/b", "get")[endpointExtension]; got != "admin" {
		t.Errorf("/b get carries %s %v, want %q", endpointExtension, got, "admin")
	}

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

// TestMergeConflictNamesBothModulesAndEndpoints checks that an operation conflict
// names both modules, both endpoints and the uppercased method and path.
func TestMergeConflictNamesBothModulesAndEndpoints(t *testing.T) {
	_, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("alpha", "api", `{"paths":{"/things":{"get":{"summary":"one"}}}}`),
		spec("beta", "admin", `{"paths":{"/things":{"GET":{"summary":"two"}}}}`),
	}, Options{})
	if !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("two fragments declaring GET /things did not report ErrOperationConflict: %v", err)
	}
	for _, want := range []string{`"alpha"`, `"api"`, `"beta"`, `"admin"`, "GET /things"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the conflict error %q does not name %s", err.Error(), want)
		}
	}
}

// TestMergeOperationConflictCrossesEndpoints checks that one route on two
// endpoints conflicts.
func TestMergeOperationConflictCrossesEndpoints(t *testing.T) {
	_, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("alpha", "api", `{"paths":{"/things":{"get":{}}}}`),
		spec("alpha", "admin", `{"paths":{"/things":{"get":{}}}}`),
	}, Options{})
	if !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("one route on two endpoints did not report ErrOperationConflict: %v", err)
	}
}

// TestMergeComponentConflictNamesTheLaterModuleAndTheName checks that a component
// declared differently conflicts naming the later module and the component.
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

// TestMergeComponentConflictCountsIntegersPastFloatPrecision checks that integers
// differing past 2^53 are different components.
func TestMergeComponentConflictCountsIntegersPastFloatPrecision(t *testing.T) {
	_, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("alpha", "api", `{"components":{"schemas":{"Count":{"maximum":9007199254740993}}}}`),
		spec("beta", "admin", `{"components":{"schemas":{"Count":{"maximum":9007199254740992}}}}`),
	}, Options{})
	if !errors.Is(err, ErrComponentConflict) {
		t.Fatalf("two schemas differing past float precision reported %v, want ErrComponentConflict", err)
	}
}

// TestMergeComponentWithEqualContentMerges checks that like content under one
// name merges.
func TestMergeComponentWithEqualContentMerges(t *testing.T) {
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

// TestMergeEmptyYieldsAWellFormedDocument checks that merging no fragments yields
// a document with the required keys.
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

// TestMergeUnreadableFragmentNamesTheModule checks that a non-object fragment is
// unreadable and names the module.
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

// TestMergeUnreadableOperationNamesTheModule checks that a non-object operation is
// unreadable and names the module.
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

// TestMergeNullFragmentIsUnreadable checks that a null fragment is unreadable.
func TestMergeNullFragmentIsUnreadable(t *testing.T) {
	_, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("alpha", "api", `null`),
	}, Options{})
	if !errors.Is(err, ErrUnreadable) {
		t.Fatalf("a null fragment did not report ErrUnreadable: %v", err)
	}
}

// TestMergeAddsGroupsSecurityAndSchemes checks the merge-owned grouping, default
// security and security schemes.
func TestMergeAddsGroupsSecurityAndSchemes(t *testing.T) {
	merged, err := Merge([]core.Resource[speedhttp.Spec]{
		spec("beta", "admin", `{"paths":{"/b":{"get":{"summary":"b","security":[{"bearerAuth":[]}]}}}}`),
		spec("alpha", "api", `{"paths":{"/a":{"post":{"summary":"a"},"get":{"summary":"g","security":[]}}}}`),
	}, Options{
		Title:       "the platform",
		Version:     "1.2.3",
		Description: "what the document describes",
		Servers:     []string{"https://api.example.com", "https://api.example.net"},
	})
	if err != nil {
		t.Fatalf("merging two fragments failed: %v", err)
	}
	doc := parse(t, merged)

	if got := nested(t, doc, "info")["description"]; got != "what the document describes" {
		t.Errorf("info.description is %v, want the option's description", got)
	}
	servers, ok := doc["servers"].([]any)
	if !ok || len(servers) != 2 {
		t.Fatalf("servers is %v, want the two origins the option named", doc["servers"])
	}
	if got := servers[0].(map[string]any)["url"]; got != "https://api.example.com" {
		t.Errorf("the first server is %v, want the first origin", got)
	}

	tags, ok := doc["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Fatalf("tags is %v, want one entry per declaring module", doc["tags"])
	}
	names := []string{tags[0].(map[string]any)["name"].(string), tags[1].(map[string]any)["name"].(string)}
	if !slices.Equal(names, []string{"alpha", "beta"}) {
		t.Errorf("tags name %v, want the declaring modules in order", names)
	}

	guarded := nested(t, doc, "paths", "/a", "post")
	if got := slicesOfStrings(guarded["tags"]); !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("/a post is tagged %v, want its declaring module", got)
	}
	if got := guarded["security"]; !equalJSONValue(got, []any{
		map[string]any{"bearerAuth": []any{}},
		map[string]any{"sessionCookie": []any{}},
	}) {
		t.Errorf("/a post has security %v, want the guarded requirement", got)
	}

	if got := nested(t, doc, "paths", "/b", "get")["security"]; !equalJSONValue(got, []any{
		map[string]any{"bearerAuth": []any{}},
	}) {
		t.Errorf("/b get has security %v, want the fragment's own requirement", got)
	}
	if got := nested(t, doc, "paths", "/a", "get")["security"]; !equalJSONValue(got, []any{}) {
		t.Errorf("/a get has security %v, want the public array the fragment declared", got)
	}

	schemes := nested(t, doc, "components", "securitySchemes")
	for _, name := range []string{"bearerAuth", "sessionCookie"} {
		if _, ok := schemes[name]; !ok {
			t.Errorf("components.securitySchemes carries no %s: %v", name, keysOf(schemes))
		}
	}
}

func slicesOfStrings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return out
}

func equalJSONValue(a, b any) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	return errLeft == nil && errRight == nil && string(left) == string(right)
}
