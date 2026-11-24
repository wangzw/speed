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

// TestServePublishesTheFeatureDocument seeds an administrator, serves the baseline and holds that
// anonymous callers are refused and an authenticated one reads exactly the set this process serves.
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

	for _, absent := range []string{"authn.admin.sso", "authn.admin.sso.metadata"} {
		if answered[absent] {
			t.Errorf("the feature document carries %q, which this process does not serve: "+
				"an answer taken from the available set, or a conditional feature posted "+
				"without checking the assembly, reports it", absent)
		}
	}
}

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

type vocabularyEntry struct {
	Name        string `json:"name"`
	Module      string `json:"module"`
	Owner       string `json:"owner"`
	Conditional bool   `json:"conditional"`
}

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

// TestGenWritesTheFeatureVocabulary runs gen with --features and reads the declarations it wrote,
// sorted by name.
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
