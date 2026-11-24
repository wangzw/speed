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

type loaderSetup struct {
	reg     *core.Registry
	stderr  *strings.Builder
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

func (s loaderSetup) load(t *testing.T, args []string) (Reader, error) {
	t.Helper()
	l, err := newLoader(s.reg, s.stderr)
	if err != nil {
		return nil, err
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	locator := fs.String(locatorFlag, "", "the URI of the primary config source")
	if err := l.BindLayer(fs, []string{"cache"}, []string{"cache"}); err != nil {
		return nil, err
	}
	if err := fs.Parse(args); err != nil {
		return nil, l.ClassifyParseError(err, nil)
	}
	if err := l.ReadPrimary(t.Context(), locate(l.m, environMap(os.Environ()), *locator, fs.Changed(locatorFlag))); err != nil {
		return nil, err
	}
	l.ReportUnclaimed()
	l.ReportSensitive()
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

// TestLoadAppliesEveryLayerInOrder checks that load applies every layer in order.
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

// TestNoPrimarySourceStillLoads checks that no primary source still loads.
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

// TestLocatorFromEnvVar checks that locator from env var.
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

// TestLocatorKeysNotInConfigData checks that locator keys not in config data.
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

// TestConflictDetectedBeforeFetch checks that conflict detected before fetch.
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

// TestUnknownEnvDiagnosticsOnStderr checks that unknown env diagnostics on stderr.
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

// TestPrimarySourceUnknownKeyStopsTheStartup checks that primary source unknown key stops the startup.
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

// TestLoadWithoutACommandTree checks that load without a command tree.
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

// TestLoadTakesEveryRegisteredModuleIn checks that load takes every registered module in.
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

// TestLoadReturnsTheReaderWithoutSupplyingIt checks that load returns the reader without supplying it.
func TestLoadReturnsTheReaderWithoutSupplyingIt(t *testing.T) {
	reg := treeRegistry(t, &treeRun{}, nil)
	r, err := Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading failed: %v", err)
	}
	if _, err = reg.Resolve((*Reader)(nil)); !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("looking the reader up after loading returned %v, want %v: Load supplies nothing",
			err, core.ErrMissingProvider)
	}

	reg.Supply(r)
	found, err := reg.Resolve((*Reader)(nil))
	if err != nil {
		t.Fatalf("the reader was supplied and cannot be taken up: %v", err)
	}
	if found != r {
		t.Errorf("the registry hands out %v, want the reader that was supplied", found)
	}
}

// TestPrimarySourceReadTwiceIsAProgrammingError checks that primary source read twice is a programming error.
func TestPrimarySourceReadTwiceIsAProgrammingError(t *testing.T) {
	s := newLoaderSetup(t, HostIdentity{Prefix: "MYAPP"}, map[string]any{})
	l, err := newLoader(s.reg, s.stderr)
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
