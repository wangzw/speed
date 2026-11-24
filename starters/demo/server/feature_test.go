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
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
)

// TestServePublishesTheFeatureDocument seeds an administrator, serves the
// baseline and reads GET /api/v1/features: anonymous callers are refused by the
// gateway, and an authenticated one reads exactly the set this process serves.
//
// The expected set is derived from the assembly, not copied: the declarations
// come from the process registry (declaredVocabulary), and a name is expected
// when its declaring module is named by the serve roster — the platform core and
// the capability ceiling. A static feature is therefore
// always expected; a conditional one is expected only if the assembly posts it,
// which the test cannot observe directly -- so it asserts the two properties
// that make a wrong answer impossible instead: every conditional name in the
// answer belongs to a served module, and at least one conditional name of a
// served module is absent (which fails an implementation that posts every
// conditional unconditionally). The named anchor below is not a census but a
// property: authn.admin.sso and authn.admin.sso.metadata are declared by a
// served module whose conditional this baseline does not post (the SSO
// management group is off by configuration, and the SAML method is named but
// stands down, so no metadata refresher exists here).
//
// The stack is named sqlite-memory and the configuration gives it a database in
// a file: seed and serve are two processes, and only a file survives the first
// one.
func TestServePublishesTheFeatureDocument(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "demo.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)

	if res := runCommand(t, config, "sqlite-memory", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	serve, ports := startServing(t, func(ports []int) *process {
		return start(t, writeConfigWith(t, ports, dsn), "sqlite-memory", serveCommand)
	})

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

	// The expected sets, derived from this process's own assembly. A module in
	// the roster is not necessarily enabled: each states its own stance in
	// Prepare, and the startup diagnostics name every one that stood down. The
	// document is read before the run is stopped so that every HTTP answer is
	// collected first; stopping it drains the diagnostics the enabled set is
	// read from.
	serve.stop(t)
	disabled := modulesTheRunDidNotEnable(t, serve.stderr())
	servedModules := make(map[string]bool)
	for _, module := range serveRoster() {
		if !disabled[module] {
			servedModules[module] = true
		}
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
			t.Errorf("the feature document omits %q, which its declaring module (named by the serve roster) declares statically",
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

	// An anchor, not a census: authn.admin.sso is off in the baseline
	// configuration (no authn.admin.sso.enabled), so its group and the metadata
	// refresh inside it are never posted. A name taken from the available set
	// would report both.
	for _, absent := range []string{"authn.admin.sso", "authn.admin.sso.metadata"} {
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
	res, err := (&http.Client{Timeout: probeTimeout()}).Do(req)
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

// modulesTheRunDidNotEnable reads the startup diagnostics and answers the
// modules the run reported as not enabled. The line is core's own:
// `module "<name>" is not enabled: <reason>`; the reason is free text and is
// never parsed.
func modulesTheRunDidNotEnable(t *testing.T, diagnostics string) map[string]bool {
	t.Helper()
	const (
		marker = `module "`
		suffix = `" is not enabled`
	)
	disabled := make(map[string]bool)
	for _, line := range strings.Split(diagnostics, "\n") {
		at := strings.Index(line, marker)
		if at < 0 {
			continue
		}
		rest := line[at+len(marker):]
		end := strings.Index(rest, suffix)
		if end < 0 {
			continue
		}
		disabled[rest[:end]] = true
	}
	return disabled
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

// holdBaselineVocabulary holds the file gen wrote to this binary's
// declarations. The artifact describes the baseline build, so it is exactly
// this binary's vocabulary: neither more nor less.
func holdBaselineVocabulary(t *testing.T, artifact []vocabularyEntry) {
	t.Helper()
	declared, err := declaredVocabulary()
	if err != nil {
		t.Fatalf("reading this binary's declarations: %v", err)
	}
	if !reflect.DeepEqual(declared, artifact) {
		t.Errorf("the vocabulary is out of step with this binary's declarations:\n"+
			"artifact: %+v\ndeclared: %+v", artifact, declared)
	}
}

// TestGenWritesTheFeatureVocabulary runs gen with --features and reads what it
// wrote: the same declarations the baseline build carries, sorted by name. The
// committed artifact is a copy of this output, so a case that only compared the
// two would not say the flag works; this one runs the command.
func TestGenWritesTheFeatureVocabulary(t *testing.T) {
	t.Parallel()
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
