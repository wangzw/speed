package core

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func diagnosticLines(t *testing.T, final map[string]Enablement) []string {
	t.Helper()
	var buf bytes.Buffer
	writeDiagnostics(&buf, final)
	text := strings.TrimSuffix(buf.String(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// TestDiagnosticsListsNotEnabledModuleWithItsReason checks that a disabled module writes one
// diagnostic line naming it and its reason.
func TestDiagnosticsListsNotEnabledModuleWithItsReason(t *testing.T) {
	lines := diagnosticLines(t, map[string]Enablement{
		"remote": {State: StateDisabled, Reason: "no connection address configured"},
	})
	if len(lines) != 1 {
		t.Fatalf("diagnostics wrote %d lines, want 1: %v", len(lines), lines)
	}
	for _, want := range []string{"remote", "no connection address configured"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("line %q does not mention %q", lines[0], want)
		}
	}
}

// TestDiagnosticsListsResolutionDisabledModuleWithResolutionReason pins that the reason shown is
// the one the verdict carries.
func TestDiagnosticsListsResolutionDisabledModuleWithResolutionReason(t *testing.T) {
	final := mustResolve(t,
		stated("memory", StateAuto, "", exclusive((*cacheCap)(nil))),
		stated("remote", StateEnabled, "", exclusive((*cacheCap)(nil))),
	)
	lines := diagnosticLines(t, final)
	if len(lines) != 1 {
		t.Fatalf("diagnostics wrote %d lines, want only the module that stood down: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "memory") {
		t.Fatalf("line %q does not name the module that stood down", lines[0])
	}
	if !strings.Contains(lines[0], "already has an explicitly enabled provider") {
		t.Fatalf("line %q does not carry the reason resolution gave", lines[0])
	}
}

// TestDiagnosticsCarriesTheReasonAModuleStated pins that a self-disabled
// module's line shows the reason it gave, unchanged.
func TestDiagnosticsCarriesTheReasonAModuleStated(t *testing.T) {
	final := mustResolve(t,
		stated("off", StateDisabled, "GitHub app credentials not configured"),
		stated("on", StateAuto, ""),
	)
	lines := diagnosticLines(t, final)
	if len(lines) != 1 {
		t.Fatalf("diagnostics wrote %d lines, want only the module that stood itself down: %v", len(lines), lines)
	}
	for _, want := range []string{"off", "GitHub app credentials not configured"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("line %q does not carry %q", lines[0], want)
		}
	}
}

// TestDiagnosticsOmitsEnabledModules checks that enabled and auto modules write no diagnostic
// line, while a disabled one does.
func TestDiagnosticsOmitsEnabledModules(t *testing.T) {
	lines := diagnosticLines(t, map[string]Enablement{
		"running": {State: StateEnabled},
		"auto":    {State: StateAuto},
		"off":     {State: StateDisabled, Reason: "off"},
	})
	if len(lines) != 1 || !strings.Contains(lines[0], "off") {
		t.Fatalf("diagnostics wrote %v, want only the disabled module", lines)
	}
}

// TestDiagnosticsLinesAreStableAndSorted checks that diagnostic lines carry the writer's prefix and
// are sorted by module name.
func TestDiagnosticsLinesAreStableAndSorted(t *testing.T) {
	lines := diagnosticLines(t, map[string]Enablement{
		"zeta":  {State: StateDisabled, Reason: "z"},
		"alpha": {State: StateDisabled, Reason: "a"},
		"mid":   {State: StateDisabled, Reason: "m"},
	})
	if len(lines) != 3 {
		t.Fatalf("diagnostics wrote %d lines, want 3: %v", len(lines), lines)
	}
	var names []string
	for _, line := range lines {
		if !strings.HasPrefix(line, diagnosticPrefix) {
			t.Fatalf("line %q does not lead with the writer that produced it", line)
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(line, diagnosticPrefix+"module \""), "\"")
		names = append(names, name)
	}
	if want := []string{"alpha", "mid", "zeta"}; !slices.Equal(names, want) {
		t.Fatalf("lines are ordered %v, want %v", names, want)
	}
}

// TestEnablementQueryMatchesDiagnostics pins the query as the programmatic route to the same
// information the diagnostics put on stderr.
func TestEnablementQueryMatchesDiagnostics(t *testing.T) {
	final := mustResolve(t,
		stated("memory", StateAuto, "", exclusive((*cacheCap)(nil))),
		stated("remote", StateEnabled, "", exclusive((*cacheCap)(nil))),
	)
	reg := New()
	reg.enablement = final

	got, ok := reg.Enablement("memory")
	if !ok {
		t.Fatal("Enablement has no verdict for a module resolution disabled")
	}
	line := diagnosticLines(t, final)[0]
	if !strings.Contains(line, got.Reason) {
		t.Fatalf("query reason %q does not appear in the diagnostic line %q", got.Reason, line)
	}
}
