package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
)

// TestServePublishesTheFeatureDocument seeds an administrator, serves the
// baseline and reads GET /features: anonymous callers are refused by the
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
	anonymous := getRaw(t, base+"/features")
	if anonymous.status != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /features answered %d, want 401:\n%s",
			anonymous.status, anonymous.body)
	}

	token := signIn(t, base, seedIdentifier, seedPassword)
	authenticated := authenticatedGet(t, base+"/features", token)
	if authenticated.status != http.StatusOK {
		t.Fatalf("GET /features answered %d, want 200:\n%s", authenticated.status, authenticated.body)
	}
	if got := authenticated.header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("GET /features answered Cache-Control %q, want no-store", got)
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
	staticServed := make(map[string]bool)
	conditionalServed := make(map[string]bool)
	for _, entry := range declaredVocabulary() {
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
type vocabularyEntry struct {
	Name        string `json:"name"`
	Module      string `json:"module"`
	Conditional bool   `json:"conditional"`
}

// declaredVocabulary is the vocabulary this binary's declarations imply: every
// feature.Spec in the available set, sorted by name. The available set is what
// the imports reach, the same scope the merged document covers.
func declaredVocabulary() []vocabularyEntry {
	resources := core.Resources[feature.Spec](core.ProcessRegistry)
	entries := make([]vocabularyEntry, 0, len(resources))
	for _, resource := range resources {
		entries = append(entries, vocabularyEntry{
			Name:        resource.Value.Name,
			Module:      resource.Module,
			Conditional: resource.Value.Conditional,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

// TestGenWritesTheFeatureVocabulary runs gen with --features and reads what it
// wrote: the same declarations this binary carries, sorted by name. The
// committed artifact is a copy of this output, so a case that only compared the
// two would not say the flag works; this one runs the command.
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
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatalf("the vocabulary gen wrote is not JSON: %v\n%s", err, raw)
	}
	if declared := declaredVocabulary(); !reflect.DeepEqual(written.Features, declared) {
		t.Errorf("gen wrote\n%+v\nwant\n%+v", written.Features, declared)
	}
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
	if err := json.Unmarshal(raw, &committed); err != nil {
		t.Fatalf("%s is not JSON: %v", committedVocabularyPath, err)
	}
	declared := declaredVocabulary()
	if !reflect.DeepEqual(committed.Features, declared) {
		t.Errorf("the committed feature vocabulary is out of step with this binary's declarations:\n"+
			"committed: %+v\ndeclared:  %+v\nregenerate it with the gen command's --features flag",
			committed.Features, declared)
	}
}
