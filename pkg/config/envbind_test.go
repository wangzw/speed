package config

import (
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

type envOptions struct {
	Addr    string
	Retries int
	Hosts   []string
	Colour  bool
	FlagsOn string
}

func envLoader(t *testing.T, prefix string) *loader {
	t.Helper()
	defaults := envOptions{Addr: ":8080", Retries: 3, Hosts: []string{"a"}}
	return loadOver(t,
		identifying("host", HostIdentity{Prefix: prefix}),
		declaring("server", Schema{
			Namespace: "server",
			Mounts:    []Mount{{Value: &defaults}},
			Items: map[string]Item{
				"colour":   {Origins: OriginEnv, EnvName: "NO_COLOR"},
				"flags-on": {Origins: OriginFlag, FlagName: "flags-on"},
			},
		}),
	)
}

func decodeServer(t *testing.T, l *loader) envOptions {
	t.Helper()
	var opts envOptions
	if err := l.Reader().Decode("server", &opts); err != nil {
		t.Fatalf("decoding the server mount failed: %v", err)
	}
	return opts
}

// TestEnvOverridesPrimary checks that env overrides primary.
func TestEnvOverridesPrimary(t *testing.T) {
	t.Setenv("MYAPP_SERVER__ADDR", ":9090")
	l := envLoader(t, "MYAPP")
	if err := givePrimary(t, l, map[string]any{"server": map[string]any{"addr": ":7000"}}); err != nil {
		t.Fatalf("applying the primary source failed: %v", err)
	}
	if got := decodeServer(t, l).Addr; got != ":9090" {
		t.Fatalf("server.addr decoded to %q, want the environment's value", got)
	}
}

// TestEnvLayerRecordsProvenance checks that env layer records provenance.
func TestEnvLayerRecordsProvenance(t *testing.T) {
	t.Setenv("MYAPP_SERVER__ADDR", ":9090")
	l := envLoader(t, "MYAPP")
	if !l.given(l.m.byPath["server.addr"]) {
		t.Fatal("a value the environment gave does not count as given, and a required item " +
			"satisfied by it alone would fail")
	}
	if l.given(l.m.byPath["server.retries"]) {
		t.Fatal("an item the environment did not give counts as given")
	}
}

// TestEmptyEnvVariableCountsAsGiven checks that empty env variable counts as given.
func TestEmptyEnvVariableCountsAsGiven(t *testing.T) {
	t.Setenv("MYAPP_SERVER__HOSTS", "")
	l := envLoader(t, "MYAPP")
	if !l.given(l.m.byPath["server.hosts"]) {
		t.Fatal("an empty variable does not count as given, and a required item somebody " +
			"deliberately emptied would be reported as missing")
	}
	if got := decodeServer(t, l).Hosts; len(got) != 0 {
		t.Fatalf("server.hosts decoded to %#v, want the empty list", got)
	}
}

// TestUnknownPrefixedEnvPassesAndIsCollected checks that unknown prefixed env passes and is collected.
func TestUnknownPrefixedEnvPassesAndIsCollected(t *testing.T) {
	m := envLoader(t, "MYAPP").m
	unclaimed := unclaimedEnvNames(m, map[string]string{
		"MYAPP_SERVICE_HOST": "10.0.0.1",
		"MYAPP_PORT":         "8080",
		"MYAPP_SERVER__ADDR": ":9090",
		"MYAPP_CONFIG":       "file:///a.yaml",
		"PATH":               "/usr/bin",
	})
	want := []string{"MYAPP_PORT", "MYAPP_SERVICE_HOST"}
	if !slices.Equal(unclaimed, want) {
		t.Fatalf("the diagnostics list is %v, want %v: the claimed name and the locator are not "+
			"in it, and unrelated variables outside the prefix are not either", unclaimed, want)
	}
}

// TestItemWithoutEnvOriginIsNotOverridden checks that item without env origin is not overridden.
func TestItemWithoutEnvOriginIsNotOverridden(t *testing.T) {
	t.Setenv("MYAPP_SERVER__FLAGS_ON", "given")
	l := envLoader(t, "MYAPP")
	if got := decodeServer(t, l).FlagsOn; got != "" {
		t.Fatalf("server.flags-on decoded to %q, want the default: the item does not read the "+
			"environment", got)
	}
	unclaimed := unclaimedEnvNames(l.m, map[string]string{"MYAPP_SERVER__FLAGS_ON": "given"})
	if !slices.Contains(unclaimed, "MYAPP_SERVER__FLAGS_ON") {
		t.Fatalf("the diagnostics list is %v, and a variable no item reads belongs in it", unclaimed)
	}
}

func unclaimedEnvNames(m *manifest, values map[string]string) []string {
	variables := unclaimedEnvVariables(m, values)
	names := make([]string, 0, len(variables))
	for _, variable := range variables {
		names = append(names, variable.name)
	}
	return names
}

func diagnosticLoader(t *testing.T, prefix string) (*loader, *strings.Builder) {
	t.Helper()
	defaults := envOptions{Addr: ":8080"}
	reg := core.New()
	reg.Register(identifying("host", HostIdentity{Prefix: prefix}))
	reg.Register(declaring("server", Schema{
		Namespace: "server",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{
			"addr": {Origins: OriginPrimary},
		},
	}))
	var sink strings.Builder
	l, err := newLoader(reg, &sink)
	if err != nil {
		t.Fatalf("collecting the declarations failed: %v", err)
	}
	return l, &sink
}

// TestEnvVariableNamingAPrimaryOnlyItemIsReportedByName checks that env variable naming a primary only item is reported by name.
func TestEnvVariableNamingAPrimaryOnlyItemIsReportedByName(t *testing.T) {
	t.Setenv("MYAPP_SERVER__ADDR", ":9090")
	l, diag := diagnosticLoader(t, "MYAPP")
	l.ReportUnclaimed()

	line := diag.String()
	for _, want := range []string{
		"MYAPP_SERVER__ADDR",
		`"server.addr"`,
		"only accepts the primary config source",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("the diagnostic reads %q, want it to contain %q", line, want)
		}
	}
	if strings.Contains(line, "no input item reads it") {
		t.Fatalf("the diagnostic reads %q, and an item is declared at the path the variable spells", line)
	}
	if got := decodeServer(t, l).Addr; got != ":8080" {
		t.Fatalf("server.addr decoded to %q, want the default: the item does not read the "+
			"environment", got)
	}
}

// TestEnvVariableWithNoDeclaredPathStaysGeneric checks that env variable with no declared path stays generic.
func TestEnvVariableWithNoDeclaredPathStaysGeneric(t *testing.T) {
	t.Setenv("MYAPP_SERVER__ADDR", ":9090")
	t.Setenv("MYAPP_SERVICE_HOST", "10.0.0.1")
	l, diag := diagnosticLoader(t, "MYAPP")
	l.ReportUnclaimed()

	var generic []string
	for _, line := range strings.Split(strings.TrimSpace(diag.String()), "\n") {
		if strings.Contains(line, "no input item reads it") {
			generic = append(generic, line)
		}
	}
	if len(generic) != 1 {
		t.Fatalf("the report reads %q, want exactly the variable with no declared path reported "+
			"as unclaimed", diag.String())
	}
	if !strings.Contains(generic[0], "MYAPP_SERVICE_HOST") {
		t.Fatalf("the unclaimed line reads %q, want it to name MYAPP_SERVICE_HOST", generic[0])
	}
}

// TestPinnedEnvNameWorksWithoutPrefix checks that pinned env name works without prefix.
func TestPinnedEnvNameWorksWithoutPrefix(t *testing.T) {
	t.Setenv("NO_COLOR", "true")
	t.Setenv("MYAPP_SERVER__ADDR", ":9090")
	l := envLoader(t, "")
	opts := decodeServer(t, l)
	if !opts.Colour {
		t.Fatalf("server.colour decoded to %v, want the pinned variable's value", opts.Colour)
	}
	if opts.Addr != ":8080" {
		t.Fatalf("server.addr decoded to %q, want the default: with no prefix there is no "+
			"derived name to match", opts.Addr)
	}
	if got := unclaimedEnvNames(l.m, map[string]string{"MYAPP_SERVER__ADDR": ":9090"}); len(got) != 0 {
		t.Fatalf("the diagnostics list is %v, and with no prefix there is no scan domain", got)
	}
}

// TestStringListFromEnvReplaces checks that string list from env replaces.
func TestStringListFromEnvReplaces(t *testing.T) {
	t.Setenv("MYAPP_SERVER__HOSTS", "x,y,z")
	l := envLoader(t, "MYAPP")
	want := []string{"x", "y", "z"}
	if got := decodeServer(t, l).Hosts; !reflect.DeepEqual(got, want) {
		t.Fatalf("server.hosts decoded to %#v, want %#v", got, want)
	}
}

// TestEnvTypeMismatch checks that env type mismatch.
func TestEnvTypeMismatch(t *testing.T) {
	t.Setenv("MYAPP_SERVER__RETRIES", "many")
	l := envLoader(t, "MYAPP")
	var opts envOptions
	err := l.Reader().Decode("server", &opts)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("an unconvertible variable returned %v, want ErrTypeMismatch", err)
	}
	if !strings.Contains(err.Error(), "server.retries") {
		t.Fatalf("the rejection reads %q, which does not name the path", err)
	}
}

// TestUndeclaredContainerIgnoresTheEnvironment checks that undeclared container ignores the environment.
func TestUndeclaredContainerIgnoresTheEnvironment(t *testing.T) {
	type carrier struct {
		Labels map[string]string
	}
	t.Setenv("MYAPP_SERVER__LABELS", "a=b")
	defaults := carrier{}
	l := loadOver(t,
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		declaring("server", Schema{Namespace: "server", Mounts: []Mount{{Value: &defaults}}}),
	)
	unclaimed := unclaimedEnvNames(l.m, map[string]string{"MYAPP_SERVER__LABELS": "a=b"})
	if !slices.Contains(unclaimed, "MYAPP_SERVER__LABELS") {
		t.Fatalf("the unclaimed names are %v, want the map's derived name among them", unclaimed)
	}
	if l.given(l.m.byPath["server.labels"]) {
		t.Fatal("the map took a value from the environment, which has no form to give it one")
	}
}

// TestTheEnvironmentIsReadOnceWhenLoadingBegins checks that the environment is read once when loading begins.
func TestTheEnvironmentIsReadOnceWhenLoadingBegins(t *testing.T) {
	t.Setenv("MYAPP_SERVER__ADDR", ":1")
	t.Setenv("MYAPP_SERVER__RETRIES", "5")
	l := envLoader(t, "MYAPP")
	before := decodeServer(t, l)

	t.Setenv("MYAPP_SERVER__ADDR", ":2")
	if err := os.Unsetenv("MYAPP_SERVER__RETRIES"); err != nil {
		t.Fatalf("removing the variable failed: %v", err)
	}
	t.Setenv("MYAPP_SERVER__HOSTS", "late")
	after := decodeServer(t, l)

	if after.Addr != ":1" || after.Retries != 5 || !reflect.DeepEqual(after.Hosts, before.Hosts) {
		t.Errorf("the second decode read %+v after the process environment changed, want the "+
			"values the run was loaded with, %+v", after, before)
	}
	if !l.given(l.m.byPath["server.retries"]) {
		t.Error("a variable removed after loading began no longer counts as given")
	}
	if l.given(l.m.byPath["server.hosts"]) {
		t.Error("a variable set after loading began counts as given")
	}
}
