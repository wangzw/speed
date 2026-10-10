package log

import (
	"context"
	"log/slog"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// The tests in this file are about an instance a host supplies for this module
// itself, to stand where the one its New builds would stand. Prepare and New do
// not run for it, and Close does.

// hostLogger is the smallest instance a host can supply: it delivers the Logger
// capability and is not the product this module builds.
type hostLogger struct{}

func (hostLogger) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (hostLogger) Redaction() Redaction      { return processRedaction }

var _ Logger = hostLogger{}

// driveTo drives reg as far as depth, with this module named, the way a command
// that takes it in would.
func driveTo(t *testing.T, reg *core.Registry, depth core.Stage) {
	t.Helper()
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        depth,
		Participants: []string{ModuleName},
	})
	if err != nil {
		t.Fatalf("a drive to %v over a registry holding this module failed: %v", depth, err)
	}
}

// TestASuppliedInstanceTakesThePlaceOfPrepare pins that an instance a host
// supplies for this module leaves the bootstrap chain's level where it was. The
// level is written in Prepare alone, and Prepare does not run for a module whose
// instance the host supplied.
//
// The two cases differ in the supply and in nothing else. Without the supply the
// drive runs this module's Prepare, which reads the section and sets the level
// the configuration names; that is what shows both observations below can move.
func TestASuppliedInstanceTakesThePlaceOfPrepare(t *testing.T) {
	cases := []struct {
		name      string
		supplied  bool
		wantLevel slog.Level
		wantRead  bool
	}{
		{"without a supplied instance Prepare sets the configured level", false, slog.LevelDebug, true},
		{"with a supplied instance Prepare does not run", true, slog.LevelWarn, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keepBootstrapLevel(t)
			bootstrapLevel.Set(slog.LevelWarn)

			reader := &stubReader{cfg: Config{Level: "debug"}}
			reg := registryWithReader(reader)
			reg.Register(Module())
			if c.supplied {
				reg.Supply(hostLogger{}, ModuleName)
			}
			driveTo(t, reg, core.StagePrepare)

			if got := bootstrapLevel.Level(); got != c.wantLevel {
				t.Errorf("the bootstrap level is %v after the drive, want %v", got, c.wantLevel)
			}
			if read := reader.path != ""; read != c.wantRead {
				t.Errorf("the configuration section was read: %t, want %t (the path asked for was %q)",
					read, c.wantRead, reader.path)
			}
		})
	}
}

// TestASuppliedInstanceBuiltByNewIsReleasedByTheDrive pins the other half: the
// host built the instance with this module's own New and supplied it, so Close
// runs on it and gives up the references New took on the destinations.
//
// The registry the drive runs over holds no reader, and the drive goes as far as
// construction. This module's Prepare and New would fail on that, so the drive
// finishing is also what shows neither ran.
func TestASuppliedInstanceBuiltByNewIsReleasedByTheDrive(t *testing.T) {
	built, err := Module().New(context.Background(), registryWithReader(&stubReader{cfg: Config{
		Level:   "info",
		Outputs: []Output{{To: destStdout, Format: formatText}},
	}}))
	if err != nil {
		t.Fatalf("building the instance with this module's New failed: %v", err)
	}
	// Releasing is idempotent, so this only matters when the drive below fails to.
	t.Cleanup(func() { _ = closeLogger(built) })
	if got := refsFor("stdout"); got != 2 {
		t.Fatalf("standard output has %d references while the host's instance is up, want 2", got)
	}

	reg := core.New()
	reg.Register(Module())
	reg.Supply(built, ModuleName)
	driveTo(t, reg, core.StageNew)

	if got := refsFor("stdout"); got != 1 {
		t.Errorf("standard output has %d references after the drive that carried the host's "+
			"instance, want the bootstrap one: the drive did not run Close on it", got)
	}
}
