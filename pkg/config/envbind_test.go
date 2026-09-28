package config

import (
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type envOptions struct {
	Addr    string
	Retries int
	Hosts   []string
	Colour  bool
	FlagsOn string
}

// envLoader declares one module under the given prefix, with a pinned name
// that owes nothing to the prefix and one item the environment never gives.
//
// The cases drive it with t.Setenv before building it rather than by handing
// it an environment: the loader reads the process environment once, when it is
// built, and a second environment injected beside it would put the diagnostics
// and the values on different footings. Parallelism goes with it, since
// t.Setenv forbids it.
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

// decodeServer decodes the server mount and fails the test if it cannot.
func decodeServer(t *testing.T, l *loader) envOptions {
	t.Helper()
	var opts envOptions
	if err := l.Reader().Decode("server", &opts); err != nil {
		t.Fatalf("decoding the server mount failed: %v", err)
	}
	return opts
}

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

// TestEmptyEnvVariableCountsAsGiven pins the reading an empty variable gets:
// somebody who set it did give a value, and a string list set to it is the
// empty list rather than a list of one empty element. The layering tool's own
// default is the opposite, so this is a setting rather than a given.
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

// TestUnknownPrefixedEnvPassesAndIsCollected pins the one origin that does not
// fail on a key it does not know. The process environment is not the host's
// alone: an orchestrator injects <PREFIX>_SERVICE_HOST by service name, and
// treating that as a startup failure would let a deployment action unrelated
// to configuration kill the process.
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

// TestItemWithoutEnvOriginIsNotOverridden pins that an item not declaring the
// environment derives no name, so a variable that looks like its name takes
// the pass-and-report path rather than overriding it. It is the executable
// form of the rule that the layering tool is never let loose on the whole
// environment: every name it reads is one this module bound by hand.
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

// TestPinnedEnvNameWorksWithoutPrefix pins that a pinned name is not built out
// of the prefix and therefore does not depend on there being one.
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

func TestStringListFromEnvReplaces(t *testing.T) {
	t.Setenv("MYAPP_SERVER__HOSTS", "x,y,z")
	l := envLoader(t, "MYAPP")
	want := []string{"x", "y", "z"}
	if got := decodeServer(t, l).Hosts; !reflect.DeepEqual(got, want) {
		t.Fatalf("server.hosts decoded to %#v, want %#v", got, want)
	}
}

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

// TestUndeclaredContainerIgnoresTheEnvironment pins the run-time consequence of
// resolving a container leaf to the primary source alone: a map field nobody
// declared does not read MYAPP_LABELS, and a variable of that name is reported
// as unclaimed rather than taken and failed on.
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

// TestTheEnvironmentIsReadOnceWhenLoadingBegins pins the snapshot the
// configuration of a run is. A variable changed in the process after loading
// began -- set, changed or removed -- reaches no value a module decodes, and
// no answer to whether a required item was given: a module that decodes again
// at run time would otherwise see a configuration nobody started the process
// with.
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
