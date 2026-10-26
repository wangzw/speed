package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
)

// TestServePublishesTheFeatureDocument seeds an administrator, serves the
// baseline and reads GET /api/v1/features: anonymous callers are refused by the
// gateway, and an authenticated one reads exactly the set this process serves.
//
// The expected set is derived from the assembly, not copied: the declarations
// come from the process registry (declaredVocabulary), and a name is expected
// when its declaring module is named by baselineServe. A static feature is
// therefore always expected; a conditional one is expected only if the assembly
// posts it, which the test cannot observe directly -- so it asserts the two
// properties that make a wrong answer impossible instead: every conditional
// name in the answer belongs to a served module, and at least one conditional
// name of a served module is absent (which fails an implementation that posts
// every conditional unconditionally). The two named anchors below are not a
// census but properties: sms.admin is declared by a module the contract imports
// and no command runs, and authn.admin.sso.metadata is declared by a served
// module whose conditional no assembly member posts (no SAML provider here).
//
// The stack is named sqlite-memory and the configuration gives it a database in
// a file: seed and serve are two processes, and only a file survives the first
// one.
func TestServePublishesTheFeatureDocument(t *testing.T) {
	ports := freePorts(t, listenerCount)
	config := writeConfigWith(t, ports, filepath.Join(t.TempDir(), "demo.db"))

	if res := runCommand(t, config, "sqlite-memory", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	serve := start(t, config, "sqlite-memory", serveCommand)
	serve.waitReady(t)

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])

	// Anonymous: the gateway answers its own 401, before any route matters.
	anonymous := getRaw(t, base+"/api/v1/features")
	if anonymous.status != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /api/v1/features answered %d, want 401:\n%s",
			anonymous.status, anonymous.body)
	}

	token := signIn(t, base, seedIdentifier, seedPassword)
	authenticated := authenticatedGet(t, base+"/api/v1/features", token)
	if authenticated.status != http.StatusOK {
		t.Fatalf("GET /api/v1/features answered %d, want 200:\n%s", authenticated.status, authenticated.body)
	}
	if got := authenticated.header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("GET /api/v1/features answered Cache-Control %q, want no-store", got)
	}

	var document struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(authenticated.body, &document); err != nil {
		t.Fatalf("the feature document is not JSON: %v\n%s", err, authenticated.body)
	}

	// The expected sets, derived from this process's own assembly.
	servedModules := make(map[string]bool, len(baselineServe))
	for _, module := range baselineServe {
		servedModules[module] = true
	}
	declared, err := declaredVocabulary()
	if err != nil {
		t.Fatalf("reading this binary's declarations: %v", err)
	}
	staticServed := make(map[string]bool)
	conditionalServed := make(map[string]bool)
	for _, entry := range declared {
		if !servedModules[entry.Module] {
			continue
		}
		if entry.Conditional {
			conditionalServed[entry.Name] = true
		} else {
			staticServed[entry.Name] = true
		}
	}

	answered := make(map[string]bool, len(document.Features))
	for index, name := range document.Features {
		if index > 0 && document.Features[index-1] >= name {
			t.Errorf("the feature document is not strictly ascending at %q (after %q)",
				name, document.Features[index-1])
		}
		answered[name] = true
		if !staticServed[name] && !conditionalServed[name] {
			t.Errorf("the feature document carries %q, which no module this baseline serves declares: "+
				"an answer taken from the available set reports it, and so does a conditional feature "+
				"posted by a module the baseline does not name", name)
		}
	}
	for name := range staticServed {
		if !answered[name] {
			t.Errorf("the feature document omits %q, which its declaring module (named by baselineServe) declares statically",
				name)
		}
	}
	absentConditional := false
	for name := range conditionalServed {
		if !answered[name] {
			absentConditional = true
			break
		}
	}
	if !absentConditional {
		t.Errorf("the feature document carries every conditional feature a served module declares; " +
			"the board appears to post conditionals without checking the assembly")
	}

	// Anchors, not a census: each name stands for a property the derivation
	// above cannot express on its own. sms.admin is declared by an imported
	// module no command runs; authn.admin.sso.metadata has no assembly member
	// that posts it.
	for _, absent := range []string{"sms.admin", "authn.admin.sso.metadata"} {
		if answered[absent] {
			t.Errorf("the feature document carries %q, which this process does not serve: "+
				"an answer taken from the available set, or a conditional feature posted "+
				"without checking the assembly, reports it", absent)
		}
	}
}

// authenticatedGet makes one request carrying a bearer token and keeps the
// answer's headers.
func authenticatedGet(t *testing.T, url, token string) rawResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("building GET %s: %v", url, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the body of GET %s: %v", url, err)
	}
	return rawResponse{status: res.StatusCode, header: res.Header, body: body}
}

// committedVocabularyPath is the feature vocabulary artifact the repository
// commits, beside the merged document. It is relative to this module's
// directory, which is where `go test` runs the package.
const committedVocabularyPath = "../../../contracts/features.json"

// vocabularyEntry is one declaration as contracts/features.json carries it.
// Owner is derived from the declaring module's Go package, the same way gen
// writes it: the platform module's packages are platform-owned, every other
// module path is application-owned.
type vocabularyEntry struct {
	Name        string `json:"name"`
	Module      string `json:"module"`
	Owner       string `json:"owner"`
	Conditional bool   `json:"conditional"`
}

// declaredVocabulary is the vocabulary this binary's declarations imply: every
// feature.Spec in the available set, sorted by name. The available set is what
// the imports reach, the same scope the merged document covers.
func declaredVocabulary() ([]vocabularyEntry, error) {
	resources := core.Resources[feature.Spec](core.ProcessRegistry)
	entries := make([]vocabularyEntry, 0, len(resources))
	for _, resource := range resources {
		owner, err := moduleOwner(core.ProcessRegistry, resource.Module)
		if err != nil {
			return nil, err
		}
		entries = append(entries, vocabularyEntry{
			Name:        resource.Value.Name,
			Module:      resource.Module,
			Owner:       owner,
			Conditional: resource.Value.Conditional,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// holdBaselineVocabulary holds one baseline artifact — the file gen wrote, or
// the committed one — to this binary's declarations. The artifact describes the
// baseline build, so under the review tag the only names this binary declares
// beyond it must be exactly reviewFeatureNames; the entries the two share are
// compared whole. In a default build the list is empty, so both comparisons are
// the exact ones they always were: the artifact is neither more nor less than
// the baseline build's vocabulary.
func holdBaselineVocabulary(t *testing.T, artifact []vocabularyEntry) {
	t.Helper()
	declared, err := declaredVocabulary()
	if err != nil {
		t.Fatalf("reading this binary's declarations: %v", err)
	}
	reviewOnly := make(map[string]bool, len(reviewFeatureNames))
	for _, name := range reviewFeatureNames {
		if reviewOnly[name] {
			t.Errorf("reviewFeatureNames names %q more than once", name)
		}
		reviewOnly[name] = true
	}
	inArtifact := make(map[string]bool, len(artifact))
	for _, entry := range artifact {
		inArtifact[entry.Name] = true
	}
	var added []string
	baseline := make([]vocabularyEntry, 0, len(declared))
	for _, entry := range declared {
		if !inArtifact[entry.Name] {
			added = append(added, entry.Name)
		}
		if reviewOnly[entry.Name] {
			continue
		}
		baseline = append(baseline, entry)
	}
	wantAdded := slices.Clone(reviewFeatureNames)
	slices.Sort(added)
	slices.Sort(wantAdded)
	if !slices.Equal(added, wantAdded) {
		t.Errorf("this binary declares %v beyond the baseline artifact, want exactly reviewFeatureNames %v: "+
			"a review control's feature must be named in reviewFeatureNames beside reviewModules, "+
			"and every name there must be one the build declares", added, wantAdded)
	}
	if !reflect.DeepEqual(baseline, artifact) {
		t.Errorf("the baseline vocabulary is out of step with this binary's baseline declarations:\n"+
			"artifact: %+v\ndeclared: %+v", artifact, baseline)
	}
}

// TestGenWritesTheFeatureVocabulary runs gen with --features and reads what it
// wrote: the same declarations the baseline build carries, sorted by name. The
// committed artifact is a copy of this output, so a case that only compared the
// two would not say the flag works; this one runs the command. Under the review
// tag the binary declares the review-only features as well;
// holdBaselineVocabulary holds that difference to its own list.
func TestGenWritesTheFeatureVocabulary(t *testing.T) {
	config := writeConfig(t, freePorts(t, listenerCount))
	out := filepath.Join(t.TempDir(), "features.json")
	res := runCommand(t, config, "sqlite-memory", genCommand, "--features", out)
	if res.err != nil {
		t.Fatalf("gen --features failed: %v\n%s", res.err, res.combined())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading what gen wrote: %v", err)
	}
	var written struct {
		Features []vocabularyEntry `json:"features"`
	}
	if err = json.Unmarshal(raw, &written); err != nil {
		t.Fatalf("the vocabulary gen wrote is not JSON: %v\n%s", err, raw)
	}
	holdBaselineVocabulary(t, written.Features)
}

// TestCommittedFeatureVocabulary holds the committed vocabulary to this
// binary's declarations. gen writes the artifact from the same available set,
// and the frontend's feature-name type is generated from the artifact; a
// declaration added, removed or renamed without regenerating leaves the two out
// of step, which this case reports rather than letting the frontend's set drift
// from the backend's.
func TestCommittedFeatureVocabulary(t *testing.T) {
	raw, err := os.ReadFile(committedVocabularyPath)
	if err != nil {
		t.Fatalf("reading %s: %v (regenerate it with the gen command's --features flag)",
			committedVocabularyPath, err)
	}
	var committed struct {
		Features []vocabularyEntry `json:"features"`
	}
	if err = json.Unmarshal(raw, &committed); err != nil {
		t.Fatalf("%s is not JSON: %v", committedVocabularyPath, err)
	}
	holdBaselineVocabulary(t, committed.Features)
	// The ownership split is the reason the artifact carries an owner at all:
	// the host's own module must be application-owned so the platform package
	// does not register its name, and a platform module must be
	// platform-owned. The comparison above already holds every entry to this
	// criterion; these two anchors name the property so a reader sees what the
	// split is for.
	owners := make(map[string]string, len(committed.Features))
	for _, entry := range committed.Features {
		owners[entry.Name] = entry.Owner
	}
	if got := owners["workspace.personal"]; got != ownerApplication {
		t.Errorf("workspace.personal is owned by %q, want %q: the host's own module must not "+
			"enter the platform package", got, ownerApplication)
	}
	if got := owners["tenant.admin"]; got != ownerPlatform {
		t.Errorf("tenant.admin is owned by %q, want %q: a platform module's declaration is "+
			"platform-owned", got, ownerPlatform)
	}
}
