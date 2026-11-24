package static

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// TestNoResolverErrorRepeatsTheSubjectTable holds the one thing a failure
// about this module's credentials must not do: repeat the table.
func TestNoResolverErrorRepeatsTheSubjectTable(t *testing.T) {
	entry := cheapEntry(t, "x")

	t.Run("a name that does not fit the format", func(t *testing.T) {
		const name = "not a valid subject name #1"
		_, err := moduleConfig{Subjects: map[string]string{name: entry}, CacheSize: 16, Concurrency: 2}.resolve()
		if err == nil {
			t.Fatal("a subject outside the format resolved without complaint")
		}
		if strings.Contains(err.Error(), name) {
			t.Errorf("the failure repeats the subject name: %v", err)
		}
	})

	t.Run("an entry nothing can read", func(t *testing.T) {
		const entryValue = "$argon2id$v=19$m=64,t=1,p=1$SENTINELSALT0SENTINELSALT$SENTINELHASHVALUE0"
		_, err := moduleConfig{Subjects: map[string]string{"alice": entryValue}, CacheSize: 16, Concurrency: 2}.resolve()
		if err == nil {
			t.Fatal("an unreadable entry resolved without complaint")
		}
		if strings.Contains(err.Error(), entryValue) || strings.Contains(err.Error(), "SENTINEL") {
			t.Errorf("the failure repeats the stored entry: %v", err)
		}
	})
}

// TestTheModuleRequiresTheLogCapabilityOptionally holds the optional edge that orders the log module before this one.
func TestTheModuleRequiresTheLogCapabilityOptionally(t *testing.T) {
	var found bool
	for _, r := range Module().Requires {
		if r.Token == core.Token((*log.Logger)(nil)) {
			found = true
			if !r.Optional {
				t.Error("the logging capability is required, and an assembly that never took logging in must still run")
			}
		}
	}
	if !found {
		t.Error("the module does not declare the logging capability it registers its table with")
	}
}

// TestTheSubjectTableIsRegisteredAsARedactionRule holds that entries are masked through the real logging chain.
func TestTheSubjectTableIsRegisteredAsARedactionRule(t *testing.T) {
	entry := cheapEntry(t, "alices")
	path := filepath.Join(t.TempDir(), "log.json")
	body := fmt.Sprintf(`{"authn":{"static":{"subjects":{"alice":%q}}},`+
		`"log":{"outputs":[{"to":"file","format":"json","path":%q}]}}`, entry, path)

	reader := authntest.ConfigReaderAll(t, body, log.Module(), Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(log.Module())
	reg.Register(Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{ModuleName, log.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			lg, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return err
			}
			lg.Named("probe").Info("a failed request carried the entry", "entry", entry)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the module: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log output: %v", err)
	}
	out := string(written)
	if !strings.Contains(out, log.MaskedText) {
		t.Errorf("the destination carries no %q, so the entry was not masked:\n%s", log.MaskedText, out)
	}
	if strings.Contains(out, entry) {
		t.Errorf("the entry reached the destination in the clear:\n%s", out)
	}
}
