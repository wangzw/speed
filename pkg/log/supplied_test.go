package log

import (
	"context"
	"log/slog"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

type hostLogger struct{}

func (hostLogger) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (hostLogger) Redaction() Redaction      { return processRedaction }

var _ Logger = hostLogger{}

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

// TestASuppliedInstanceTakesThePlaceOfPrepare pins that an instance a host supplies for this
// module leaves the bootstrap chain's level where it was.
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

// TestASuppliedInstanceBuiltByNewIsReleasedByTheDrive checks that a drive that takes over an
// instance built by New runs Close on it, dropping its reference.
func TestASuppliedInstanceBuiltByNewIsReleasedByTheDrive(t *testing.T) {
	built, err := Module().New(context.Background(), registryWithReader(&stubReader{cfg: Config{
		Level:   "info",
		Outputs: []Output{{To: destStdout, Format: formatText}},
	}}))
	if err != nil {
		t.Fatalf("building the instance with this module's New failed: %v", err)
	}
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
