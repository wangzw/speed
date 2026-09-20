package config

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/vislake/speed/pkg/core"
)

type loadOptions struct {
	Addr     string
	TTL      time.Duration
	Salutate string
}

// loaderSetup is a registry carrying a host identity, one declaring module, a
// transport and a format, together with the diagnostics stream and the
// transport's call counter.
type loaderSetup struct {
	reg    *core.Registry
	stderr *strings.Builder
	// fetches counts what the transport was asked for, which is how a case
	// shows that something happened before the primary source was read.
	fetches *int
}

func newLoaderSetup(t *testing.T, host HostIdentity, content map[string]any, extra ...core.Module) loaderSetup {
	t.Helper()
	fetches := 0
	defaults := loadOptions{Addr: "localhost:6379", TTL: 5 * time.Minute, Salutate: "Hello"}
	reg := core.New()
	reg.Register(identifying("host", host))
	reg.Register(providing("source", fakeSource{scheme: "file", data: []byte("x"), format: "yaml", calls: &fetches}))
	reg.Register(providing("format", fakeFormat{name: "yaml", content: content}))
	reg.Register(declaring("cache", Schema{
		Namespace: "cache",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{
			"salutate": {Origins: OriginPrimary | OriginEnv | OriginFlag, FlagName: "salutation"},
		},
	}))
	for _, m := range extra {
		reg.Register(m)
	}
	return loaderSetup{reg: reg, stderr: &strings.Builder{}, fetches: &fetches}
}

// load runs the whole staged load the way the command tree runs it: collect,
// register the arguments of the participating modules, parse, locate, read the
// primary source, report, hand out the reader.
func (s loaderSetup) load(t *testing.T, args []string) (Reader, error) {
	t.Helper()
	l, err := NewLoader(s.reg, s.stderr)
	if err != nil {
		return nil, err
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	locator := fs.String(locatorFlag, "", "the URI of the primary config source")
	if err := l.BindLayer(fs, []string{"cache"}); err != nil {
		return nil, err
	}
	if err := fs.Parse(args); err != nil {
		return nil, l.ClassifyParseError(err)
	}
	if err := l.ReadPrimary(t.Context(), locate(l.m, environMap(os.Environ()), *locator, fs.Changed(locatorFlag))); err != nil {
		return nil, err
	}
	l.ReportUnclaimed()
	return l.Reader(), nil
}

func decodeCache(t *testing.T, r Reader) loadOptions {
	t.Helper()
	var opts loadOptions
	if err := r.Decode("cache", &opts); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	return opts
}

// TestLoadAppliesEveryLayerInOrder pins the whole stack: the declared
// defaults, then the primary source, then the environment, then the command
// line, each overriding the one below it and no layer touching what a higher
// one already settled. It is the case that says the layering tool's own order
// and the way this module binds into it agree.
func TestLoadAppliesEveryLayerInOrder(t *testing.T) {
	t.Setenv("MYAPP_CACHE__SALUTATE", "from-env")
	t.Setenv("MYAPP_CACHE__ADDR", "from-env:2")
	s := newLoaderSetup(t,
		HostIdentity{Prefix: "MYAPP", DefaultLocator: "file:///app.yaml"},
		map[string]any{"cache": map[string]any{"addr": "from-file:1", "ttl": "1m", "salutate": "from-file"}},
	)
	r, err := s.load(t, []string{"--salutation=from-flag"})
	if err != nil {
		t.Fatalf("loading failed: %v", err)
	}
	opts := decodeCache(t, r)
	if opts.TTL != time.Minute {
		t.Fatalf("cache.ttl decoded to %v, want the primary source's value: no higher layer gave it one", opts.TTL)
	}
	if opts.Addr != "from-env:2" {
		t.Fatalf("cache.addr decoded to %q, want the environment's value over the file's", opts.Addr)
	}
	if opts.Salutate != "from-flag" {
		t.Fatalf("cache.salutate decoded to %q, want the command line's value over the other two", opts.Salutate)
	}
}

// TestNoPrimarySourceStillLoads pins that a run with no locator at all is a
// legal configuration, the other layers carrying it on their own.
func TestNoPrimarySourceStillLoads(t *testing.T) {
	s := newLoaderSetup(t, HostIdentity{Prefix: "MYAPP"}, nil)
	r, err := s.load(t, nil)
	if err != nil {
		t.Fatalf("loading without a primary config source failed: %v", err)
	}
	if *s.fetches != 0 {
		t.Fatalf("the transport was asked for something %d time(s) with no locator given", *s.fetches)
	}
	if got := decodeCache(t, r).Addr; got != "localhost:6379" {
		t.Fatalf("cache.addr decoded to %q, want the declared default", got)
	}
}

// TestLocatorFromEnvVar pins the middle leg of the locator's three steps.
func TestLocatorFromEnvVar(t *testing.T) {
	t.Setenv("MYAPP_CONFIG", "file:///app.yaml")
	s := newLoaderSetup(t,
		HostIdentity{Prefix: "MYAPP"},
		map[string]any{"cache": map[string]any{"addr": "from-file:1"}},
	)
	r, err := s.load(t, nil)
	if err != nil {
		t.Fatalf("loading failed: %v", err)
	}
	if got := decodeCache(t, r).Addr; got != "from-file:1" {
		t.Fatalf("cache.addr decoded to %q, want the file the variable pointed at", got)
	}
}

// TestLocatorKeysNotInConfigData pins that the two reserved names stay out of
// the config data. Were they to go in, the unknown-key check would trip over
// this module's own names first.
func TestLocatorKeysNotInConfigData(t *testing.T) {
	t.Setenv("MYAPP_CONFIG", "file:///app.yaml")
	s := newLoaderSetup(t, HostIdentity{Prefix: "MYAPP"}, map[string]any{})
	r, err := s.load(t, []string{"--config=file:///app.yaml"})
	if err != nil {
		t.Fatalf("loading failed: %v", err)
	}
	held := r.(*reader)
	for _, path := range []string{locatorFlag, helpFlag} {
		if got := held.l.v.Get(path); got != nil {
			t.Fatalf("the config data carries %q as %#v, which is a reserved name and not an "+
				"input item", path, got)
		}
	}
	if strings.Contains(s.stderr.String(), "MYAPP_CONFIG") {
		t.Fatalf("the diagnostics report the locator variable as unread:\n%s", s.stderr)
	}
}

// TestConflictDetectedBeforeFetch pins that collection comes first: two
// modules claiming the same path stop the startup before anything is read.
func TestConflictDetectedBeforeFetch(t *testing.T) {
	clash := declaring("other", Schema{
		Namespace: "cache",
		Mounts:    []Mount{{Value: &struct{ Addr string }{}}},
	})
	s := newLoaderSetup(t,
		HostIdentity{Prefix: "MYAPP", DefaultLocator: "file:///app.yaml"},
		map[string]any{},
		clash,
	)
	_, err := s.load(t, nil)
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("two modules claiming one path returned %v, want ErrConfigConflict", err)
	}
	if *s.fetches != 0 {
		t.Fatalf("the transport was asked for something %d time(s) despite the conflict", *s.fetches)
	}
}

// TestUnknownEnvDiagnosticsOnStderr pins the one origin that reports rather
// than fails, and the shape of the line it writes: it leads with the party
// that produced it, since several writers share a stream the host cannot
// redirect.
func TestUnknownEnvDiagnosticsOnStderr(t *testing.T) {
	t.Setenv("MYAPP_SERVICE_HOST", "10.0.0.1")
	s := newLoaderSetup(t, HostIdentity{Prefix: "MYAPP"}, nil)
	if _, err := s.load(t, nil); err != nil {
		t.Fatalf("a variable under the prefix that no item reads failed the startup: %v", err)
	}
	line := s.stderr.String()
	if !strings.HasPrefix(line, diagnosticPrefix) {
		t.Fatalf("the diagnostic reads %q, and a line on a shared stream has to name its writer", line)
	}
	if !strings.Contains(line, "MYAPP_SERVICE_HOST") {
		t.Fatalf("the diagnostic reads %q, which does not name the variable", line)
	}
}

// TestPrimarySourceUnknownKeyStopsTheStartup pins that the checks reach
// through the whole load, not only the unit that validates a source.
func TestPrimarySourceUnknownKeyStopsTheStartup(t *testing.T) {
	s := newLoaderSetup(t,
		HostIdentity{Prefix: "MYAPP", DefaultLocator: "file:///app.yaml"},
		//nolint:misspell // "addres" is the unknown-key input under test, for
		// the reason data_test.go's identical case gives.
		map[string]any{"cache": map[string]any{"addres": "typo"}},
	)
	if _, err := s.load(t, nil); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a misspelled key in the primary source returned %v, want ErrUnknownKey", err)
	}
}

// TestLoadWithoutACommandTree pins the exported entry: every registered module
// takes part, the arguments are one flat set, and what comes back is a reader
// over the same layers a command tree would have produced.
//
// The locator reaches it through the process environment, which is the only
// environment it reads. The layering tool pulls the values from there, so a
// second way in for <PREFIX>_CONFIG alone would leave a run taking its primary
// source from one environment and its values from another.
func TestLoadWithoutACommandTree(t *testing.T) {
	t.Setenv("MYAPP_CONFIG", "file:///app.yaml")
	s := newLoaderSetup(t,
		HostIdentity{Prefix: "MYAPP"},
		map[string]any{"cache": map[string]any{"addr": "from-file:1"}},
	)
	r, err := Load(t.Context(), s.reg, []string{"--salutation=from-flag"})
	if err != nil {
		t.Fatalf("loading failed: %v", err)
	}
	opts := decodeCache(t, r)
	if opts.Addr != "from-file:1" {
		t.Fatalf("cache.addr decoded to %q, want the file the variable pointed at", opts.Addr)
	}
	if opts.Salutate != "from-flag" {
		t.Fatalf("cache.salutate decoded to %q, want the command line's value", opts.Salutate)
	}
}

// TestLoadTakesEveryRegisteredModuleIn pins what makes Load an entry of its
// own: the question a command answers -- which modules this run assembles --
// does not arise, so the arguments are one flat set over the whole registry.
// Two modules a tree would never put on the same command are both reachable.
func TestLoadTakesEveryRegisteredModuleIn(t *testing.T) {
	reg := treeRegistry(t, &treeRun{}, nil)
	r, err := Load(t.Context(), reg, []string{"--cache-addr=given", "--logging-addr=also"})
	if err != nil {
		t.Fatalf("loading failed: %v", err)
	}
	if got := decodeAddr(t, r, "cache"); got != "given" {
		t.Errorf("cache.addr reads %q, want the command line's value", got)
	}
	if got := decodeAddr(t, r, "logging"); got != "also" {
		t.Errorf("logging.addr reads %q, want the command line's value", got)
	}
}

// TestPrimarySourceReadTwiceIsAProgrammingError pins the guard on the one
// primary source a run has. The layering tool merges rather than replaces, so
// a second read would stack on the first instead of being refused.
func TestPrimarySourceReadTwiceIsAProgrammingError(t *testing.T) {
	s := newLoaderSetup(t, HostIdentity{Prefix: "MYAPP"}, map[string]any{})
	l, err := NewLoader(s.reg, s.stderr)
	if err != nil {
		t.Fatalf("collecting the declarations failed: %v", err)
	}
	if err := l.ReadPrimary(t.Context(), ""); err != nil {
		t.Fatalf("the first read failed: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("reading the primary source a second time returned, and it would have " +
				"stacked on the first")
		}
	}()
	_ = l.ReadPrimary(t.Context(), "")
}
