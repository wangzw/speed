package treecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/starters/demo/server/internal/demotest"
)

func genLimit() time.Duration {
	return coretest.Scale(60 * time.Second)
}

func runGen(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), genLimit())
	defer cancel()
	cmd := exec.CommandContext(ctx, hostBinary, append([]string{"sqlite-memory", "gen"}, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = demotest.ChildEnv()
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("gen was still running after the limit and was killed: %w", ctx.Err())
	}
	return stdout.String(), stderr.String(), err
}

const committedMergedDocumentPath = "../../../../contracts/api.json"

const committedMergedDocumentTitle = "speed"

// TestCommittedMergedDocument checks that the committed merged document matches byte for byte what
// this binary's gen command writes.
func TestCommittedMergedDocument(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "api.json")
	stdout, stderr, err := runGen(t, "--title", committedMergedDocumentTitle, "-o", out)
	if err != nil {
		t.Fatalf("gen failed: %v\nstandard output:\n%s\nstandard error:\n%s", err, stdout, stderr)
	}
	generated, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading what gen wrote: %v", err)
	}
	committed, err := os.ReadFile(committedMergedDocumentPath)
	if err != nil {
		t.Fatalf("reading %s: %v (regenerate it with the gen command web/orval.config.ts documents)",
			committedMergedDocumentPath, err)
	}
	if !bytes.Equal(generated, committed) {
		t.Errorf("the committed merged document is out of step with what gen writes: %s\n"+
			"regenerate %s with the gen command web/orval.config.ts documents",
			firstByteDifference(generated, committed), committedMergedDocumentPath)
	}
}

func firstByteDifference(generated, committed []byte) string {
	common := min(len(generated), len(committed))
	for index := range common {
		if generated[index] != committed[index] {
			return fmt.Sprintf("byte %d differs: gen wrote %q, the committed document has %q",
				index, excerpt(generated, index), excerpt(committed, index))
		}
	}
	return fmt.Sprintf("gen wrote %d bytes, the committed document has %d; the first %d are equal",
		len(generated), len(committed), common)
}

func excerpt(document []byte, index int) string {
	const context = 40
	start := max(index-context, 0)
	end := min(index+context, len(document))
	return string(document[start:end])
}

const committedVocabularyPath = "../../../../contracts/features.json"

type vocabularyEntry struct {
	Name        string `json:"name"`
	Module      string `json:"module"`
	Owner       string `json:"owner"`
	Conditional bool   `json:"conditional"`
}

// TestCommittedFeatureVocabulary checks that the committed feature vocabulary matches what gen writes
// with --features, entry for entry.
func TestCommittedFeatureVocabulary(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "features.json")
	stdout, stderr, err := runGen(t, "--features", out)
	if err != nil {
		t.Fatalf("gen --features failed: %v\nstandard output:\n%s\nstandard error:\n%s",
			err, stdout, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading what gen --features wrote: %v", err)
	}
	var written struct {
		Features []vocabularyEntry `json:"features"`
	}
	if err = json.Unmarshal(raw, &written); err != nil {
		t.Fatalf("the vocabulary gen wrote is not JSON: %v\n%s", err, raw)
	}
	committedRaw, err := os.ReadFile(committedVocabularyPath)
	if err != nil {
		t.Fatalf("reading %s: %v (regenerate it with the gen command's --features flag)",
			committedVocabularyPath, err)
	}
	var committed struct {
		Features []vocabularyEntry `json:"features"`
	}
	if err = json.Unmarshal(committedRaw, &committed); err != nil {
		t.Fatalf("%s is not JSON: %v", committedVocabularyPath, err)
	}
	if !reflect.DeepEqual(written.Features, committed.Features) {
		t.Errorf("the committed feature vocabulary is out of step with what gen --features writes:\n"+
			"committed: %+v\nwritten: %+v", committed.Features, written.Features)
	}
	const ownerPlatform = "platform"
	owners := make(map[string]string, len(committed.Features))
	for _, entry := range committed.Features {
		owners[entry.Name] = entry.Owner
	}
	if got := owners["org.personal"]; got != ownerPlatform {
		t.Errorf("org.personal is owned by %q, want %q: the personal-tenant module lives "+
			"under pkg/ and its feature is platform-owned", got, ownerPlatform)
	}
	if got := owners["tenant.admin"]; got != ownerPlatform {
		t.Errorf("tenant.admin is owned by %q, want %q: a platform module's declaration is "+
			"platform-owned", got, ownerPlatform)
	}
}
