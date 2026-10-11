package admission

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// ownLines are the records this module wrote, told from the entry point's by
// the module name in the message.
func ownLines(logs *recorder) []record {
	var out []record
	for _, rec := range logs.all() {
		if strings.Contains(rec.msg, ModuleName) {
			out = append(out, rec)
		}
	}
	return out
}

func TestStartupWritesTheGuardedEndpointsAndEveryLineStartsWithTheDirectoryPrefix(t *testing.T) {
	logs := newRecorder()
	body := `{
		"http": {"endpoints": {"public": {"address": "127.0.0.1:0"}, "admin": {"address": "127.0.0.1:0"}}},
		"tenant": {"admission": {"endpoints": ["public", "admin"]}}
	}`
	if err := (fixture{body: body, supplied: []supply{withLogs(logs)}}).drive(t, core.StageInit, nil); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	lines := ownLines(logs)
	if len(lines) == 0 {
		t.Fatal("the module wrote nothing at startup")
	}
	for _, rec := range lines {
		if !strings.HasPrefix(rec.msg, "tenant: ") {
			t.Errorf("the diagnostic %q does not start with %q", rec.msg, "tenant: ")
		}
	}
	var in []record
	for _, rec := range lines {
		if strings.Contains(rec.msg, "admission in force") {
			in = append(in, rec)
		}
	}
	if len(in) != 1 {
		t.Fatalf("%d lines say the layer is in force, want 1: %+v", len(in), lines)
	}
	guarded := in[0].attrs["guarded"]
	if !strings.Contains(guarded, "public") || !strings.Contains(guarded, "admin") {
		t.Fatalf("the line lists the guarded endpoints as %q, want both", guarded)
	}
	if in[0].level != levelInfo {
		t.Errorf("the line is at %v, want Info", in[0].level)
	}
}

func TestTheWarningForAnAbsentRegistryOfProviders(t *testing.T) {
	warned := func(t *testing.T, supplied ...supply) []record {
		t.Helper()
		logs := newRecorder()
		f := fixture{supplied: append([]supply{withLogs(logs)}, supplied...)}
		if err := f.drive(t, core.StageInit, nil); err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		var out []record
		for _, rec := range ownLines(logs) {
			if rec.level == levelWarn {
				out = append(out, rec)
			}
		}
		return out
	}

	t.Run("with settings that can switch the requirement on and no registry it warns", func(t *testing.T) {
		got := warned(t, withStore(newSettingsStub()))
		if len(got) != 1 {
			t.Fatalf("the Warn records are %+v, want one", got)
		}
		if !strings.HasPrefix(got[0].msg, "tenant: ") || !strings.Contains(got[0].msg, requireSSOKey) {
			t.Fatalf("the warning %q does not start with the prefix and name %s", got[0].msg, requireSSOKey)
		}
	})
	t.Run("with a registry it does not", func(t *testing.T) {
		if got := warned(t, withStore(newSettingsStub()), withProviders(standardProviders())); len(got) != 0 {
			t.Fatalf("the Warn records are %+v, want none", got)
		}
	})
	t.Run("with no settings the requirement cannot be switched on and it does not", func(t *testing.T) {
		if got := warned(t); len(got) != 0 {
			t.Fatalf("the Warn records are %+v, want none", got)
		}
	})
}

func TestTheSupplyOfAnOptionalCapabilityIsSaidInTheStartupLine(t *testing.T) {
	logs := newRecorder()
	f := fixture{supplied: []supply{withLogs(logs), withDecider(&deciderStub{}), withProviders(standardProviders())}}
	if err := f.drive(t, core.StageInit, nil); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	for _, rec := range ownLines(logs) {
		if strings.Contains(rec.msg, "in force") {
			if rec.attrs["decider"] != "true" || rec.attrs["identity_providers"] != "true" || rec.attrs["settings"] != "false" {
				t.Fatalf("the line says %v, want the decider and the providers in and the settings out", rec.attrs)
			}
			return
		}
	}
	t.Fatal("no line says the layer is in force")
}
