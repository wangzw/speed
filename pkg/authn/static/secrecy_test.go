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

// TestNoResolverErrorRepeatsTheSubjectTable holds the one thing a failure about
// this module's credentials must not do: repeat the table. The subjects item is
// the module's secret — the names it answers for and the password entries it
// holds — so a refusal names the key to correct and never the stored material.
//
// Two parts of the table are driven here. A name that does not fit the format is
// not echoed, because the table is a secret as a whole and a name outside the
// format can be anything at all. An entry the module cannot read is not echoed
// either, because it is the stored secret the table exists to hold. The entry
// case repeats TestAnUnusableEntryNeverReachesAnErrorMessage on purpose: this
// test states the whole-table rule in one place, and naming only the format rule
// would leave the two halves of the table looking as though only one is guarded.
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

// TestTheModuleRequiresTheLogCapabilityOptionally holds the declaration New's
// registration depends on: without the edge nothing orders the log module before
// this one, and the registration happens against a capability that is not there.
// It is optional, because a backend has to run in an assembly that never took
// logging in.
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

// TestTheSubjectTableIsRegisteredAsARedactionRule holds the table's entries to
// being masked wherever they turn up in a log line, through the real logging
// chain rather than a stand-in: the module is driven beside the log module
// writing JSON to a file, and a record that carries an entry is written through
// the delivered capability.
//
// The assertion is that the destination shows the mask, not merely that it lacks
// the entry: the entry might never have reached the record at all, and an
// absence alone would pass for the wrong reason.
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
			// Written while the destination is open: the drive closes it after
			// the action returns.
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
