package config

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/vislake/speed/pkg/core"
)

func declaring(name string, s Schema) core.Module {
	return core.Module{Name: name, Resources: []any{s}}
}

func identifying(name string, h HostIdentity) core.Module {
	return core.Module{Name: name, Resources: []any{h}}
}

func collect(t *testing.T, modules ...core.Module) *manifest {
	t.Helper()
	reg := core.New()
	for _, m := range modules {
		reg.Register(m)
	}
	m, err := newManifest(reg)
	if err != nil {
		t.Fatalf("collecting the manifest failed: %v", err)
	}
	return m
}

func loadOver(t *testing.T, modules ...core.Module) *loader {
	t.Helper()
	reg := core.New()
	for _, m := range modules {
		reg.Register(m)
	}
	l, err := newLoader(reg, io.Discard)
	if err != nil {
		t.Fatalf("collecting the declarations failed: %v", err)
	}
	return l
}

func givePrimary(t *testing.T, l *loader, content map[string]any) error {
	t.Helper()
	if err := validatePrimary(l.m, content); err != nil {
		return err
	}
	return l.v.MergeConfigMap(content)
}

func giveFlags(t *testing.T, l *loader, participants []string, args ...string) error {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := l.BindLayer(fs, participants, participants); err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return l.ClassifyParseError(err, nil)
	}
	return nil
}

func collectRejects(t *testing.T, parties []string, modules ...core.Module) error {
	t.Helper()
	reg := core.New()
	for _, m := range modules {
		reg.Register(m)
	}
	m, err := newManifest(reg)
	if err == nil {
		t.Fatalf("collecting the manifest produced %d items, want a conflict", len(m.items))
	}
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("collection returned %v, want ErrConfigConflict", err)
	}
	for _, party := range parties {
		if !strings.Contains(err.Error(), party) {
			t.Fatalf("conflict reads %q, which does not name %q", err, party)
		}
	}
	return err
}

type addrCarrier struct {
	Addr string
}

type ttlCarrier struct {
	TTL string
}

// TestPathPrefixIntersectionConflict checks that path prefix intersection conflict.
func TestPathPrefixIntersectionConflict(t *testing.T) {
	scalar := struct{ Redis string }{}
	nested := struct{ PoolSize int }{}
	err := collectRejects(t, []string{"leaf", "tree", "cache.redis", "cache.redis.pool-size"},
		declaring("leaf", Schema{Namespace: "cache", Mounts: []Mount{{Value: &scalar}}}),
		declaring("tree", Schema{Namespace: "cache", Mounts: []Mount{{Path: "redis", Value: &nested}}}),
	)
	if !strings.Contains(err.Error(), "prefix") {
		t.Fatalf("conflict reads %q, which does not say the paths intersect", err)
	}
}

// TestPathPrefixIntersectionConflictEitherWayRound checks that path prefix intersection conflict either way round.
func TestPathPrefixIntersectionConflictEitherWayRound(t *testing.T) {
	nested := struct{ PoolSize int }{}
	scalar := struct{ Redis string }{}
	collectRejects(t, []string{"a-tree", "b-leaf", "cache.redis", "cache.redis.pool-size"},
		declaring("a-tree", Schema{Namespace: "cache", Mounts: []Mount{{Path: "redis", Value: &nested}}}),
		declaring("b-leaf", Schema{Namespace: "cache", Mounts: []Mount{{Value: &scalar}}}),
	)
}

// TestIdenticalPathConflict checks that identical path conflict.
func TestIdenticalPathConflict(t *testing.T) {
	collectRejects(t, []string{"alpha", "beta", "cache.addr"},
		declaring("alpha", Schema{Namespace: "cache", Mounts: []Mount{{Value: &addrCarrier{}}}}),
		declaring("beta", Schema{Namespace: "cache", Mounts: []Mount{{Value: &addrCarrier{}}}}),
	)
}

// TestMutuallyExclusiveImplementationsSharingAPathConflict checks that mutually exclusive implementations sharing a path conflict.
func TestMutuallyExclusiveImplementationsSharingAPathConflict(t *testing.T) {
	collectRejects(t, []string{"cache.memory", "cache.remote", "cache.ttl"},
		declaring("cache.memory", Schema{Namespace: "cache", Mounts: []Mount{{Value: &ttlCarrier{}}}}),
		declaring("cache.remote", Schema{Namespace: "cache", Mounts: []Mount{{Value: &ttlCarrier{}}}}),
	)
}

// TestSameNamespaceDisjointPathsAllowed checks that same namespace disjoint paths allowed.
func TestSameNamespaceDisjointPathsAllowed(t *testing.T) {
	m := collect(t,
		declaring("alpha", Schema{Namespace: "cache", Mounts: []Mount{{Value: &addrCarrier{}}}}),
		declaring("beta", Schema{Namespace: "cache", Mounts: []Mount{{Value: &ttlCarrier{}}}}),
	)
	for _, path := range []string{"cache.addr", "cache.ttl"} {
		if m.byPath[path] == nil {
			t.Errorf("manifest has no item at %q", path)
		}
	}
}

// TestOverlappingMountPathsAllowed checks that overlapping mount paths allowed.
func TestOverlappingMountPathsAllowed(t *testing.T) {
	root := addrCarrier{}
	nested := struct{ PoolSize int }{}
	m := collect(t, declaring("cache", Schema{
		Namespace: "cache",
		Mounts:    []Mount{{Value: &root}, {Path: "redis", Value: &nested}},
	}))
	for _, path := range []string{"cache.addr", "cache.redis.pool-size"} {
		if m.byPath[path] == nil {
			t.Errorf("manifest has no item at %q", path)
		}
	}
}

// TestFlagLongNameConflict checks that flag long name conflict.
func TestFlagLongNameConflict(t *testing.T) {
	collectRejects(t, []string{"alpha", "beta", "listen"},
		declaring("alpha", Schema{
			Namespace: "a", Mounts: []Mount{{Value: &addrCarrier{}}},
			Items: map[string]Item{"addr": {Origins: OriginFlag, FlagName: "listen"}},
		}),
		declaring("beta", Schema{
			Namespace: "b", Mounts: []Mount{{Value: &addrCarrier{}}},
			Items: map[string]Item{"addr": {Origins: OriginFlag, FlagName: "listen"}},
		}),
	)
}

// TestFlagShortNameConflict checks that flag short name conflict.
func TestFlagShortNameConflict(t *testing.T) {
	collectRejects(t, []string{"alpha", "beta", "l"},
		declaring("alpha", Schema{
			Namespace: "a", Mounts: []Mount{{Value: &addrCarrier{}}},
			Items: map[string]Item{"addr": {Origins: OriginFlag, FlagName: "listen", FlagShort: "l"}},
		}),
		declaring("beta", Schema{
			Namespace: "b", Mounts: []Mount{{Value: &addrCarrier{}}},
			Items: map[string]Item{"addr": {Origins: OriginFlag, FlagName: "local", FlagShort: "l"}},
		}),
	)
}

// TestEnvNameConflictDerivedVsPinned checks that env name conflict derived vs pinned.
func TestEnvNameConflictDerivedVsPinned(t *testing.T) {
	collectRejects(t, []string{"alpha", "beta", "MYAPP_A__ADDR"},
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		declaring("alpha", Schema{Namespace: "a", Mounts: []Mount{{Value: &addrCarrier{}}}}),
		declaring("beta", Schema{
			Namespace: "b", Mounts: []Mount{{Value: &addrCarrier{}}},
			Items: map[string]Item{"addr": {EnvName: "MYAPP_A__ADDR"}},
		}),
	)
}

// TestEnvNameConflictTwoPinned checks that env name conflict two pinned.
func TestEnvNameConflictTwoPinned(t *testing.T) {
	collectRejects(t, []string{"alpha", "beta", "NO_COLOR"},
		declaring("alpha", Schema{
			Namespace: "a", Mounts: []Mount{{Value: &addrCarrier{}}},
			Items: map[string]Item{"addr": {EnvName: "NO_COLOR"}},
		}),
		declaring("beta", Schema{
			Namespace: "b", Mounts: []Mount{{Value: &addrCarrier{}}},
			Items: map[string]Item{"addr": {EnvName: "NO_COLOR"}},
		}),
	)
}

// TestReservedFlagConfig checks that reserved flag config.
func TestReservedFlagConfig(t *testing.T) {
	collectRejects(t, []string{"alpha", "config"},
		declaring("alpha", Schema{
			Namespace: "a", Mounts: []Mount{{Value: &addrCarrier{}}},
			Items: map[string]Item{"addr": {Origins: OriginFlag, FlagName: "config"}},
		}),
	)
}

// TestReservedFlagHelp checks that reserved flag help.
func TestReservedFlagHelp(t *testing.T) {
	err := collectRejects(t, []string{"alpha", "help"},
		declaring("alpha", Schema{
			Namespace: "a", Mounts: []Mount{{Value: &addrCarrier{}}},
			Items: map[string]Item{"addr": {Origins: OriginFlag, FlagName: "help"}},
		}),
	)
	if !strings.Contains(err.Error(), "command-line library provides it") {
		t.Fatalf("the rejection reads %q, and --help is the library's rather than this module's", err)
	}
}

// TestReservedShortHelpFlag checks that reserved short help flag.
func TestReservedShortHelpFlag(t *testing.T) {
	err := collectRejects(t, []string{"alpha", "h"},
		declaring("alpha", Schema{
			Namespace: "a", Mounts: []Mount{{Value: &addrCarrier{}}},
			Items: map[string]Item{"addr": {Origins: OriginFlag, FlagName: "addr", FlagShort: "h"}},
		}),
	)
	if !strings.Contains(err.Error(), "command-line library provides it") {
		t.Fatalf("the rejection reads %q, and -h is the library's rather than this module's", err)
	}
}

// TestReservedEnvConfig checks that reserved env config.
func TestReservedEnvConfig(t *testing.T) {
	carrier := struct{ Config string }{}
	collectRejects(t, []string{"alpha", "MYAPP_CONFIG"},
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		declaring("alpha", Schema{Mounts: []Mount{{Value: &carrier}}}),
	)
}

// TestReservedEnvConfigAppliesToPinnedNames checks that reserved env config applies to pinned names.
func TestReservedEnvConfigAppliesToPinnedNames(t *testing.T) {
	collectRejects(t, []string{"alpha", "MYAPP_CONFIG"},
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		declaring("alpha", Schema{
			Mounts: []Mount{{Value: &addrCarrier{}}},
			Items:  map[string]Item{"addr": {Origins: OriginEnv, EnvName: "MYAPP_CONFIG"}},
		}),
	)
}

// TestPinnedLocatorNameWithoutPrefixIsAllowed checks that pinned locator name without prefix is allowed.
func TestPinnedLocatorNameWithoutPrefixIsAllowed(t *testing.T) {
	m := collect(t, declaring("alpha", Schema{
		Mounts: []Mount{{Value: &addrCarrier{}}},
		Items:  map[string]Item{"addr": {Origins: OriginEnv, EnvName: "MYAPP_CONFIG"}},
	}))
	if _, read := m.byEnv["MYAPP_CONFIG"]; !read {
		t.Fatal("with no prefix declared, a pinned MYAPP_CONFIG is an ordinary name and stays readable")
	}
}

// TestDuplicateHostIdentityConflict checks that duplicate host identity conflict.
func TestDuplicateHostIdentityConflict(t *testing.T) {
	collectRejects(t, []string{"host", "second-host"},
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		identifying("second-host", HostIdentity{Prefix: "OTHER"}),
	)
}

// TestAnEnvironmentVariableNameAShellCannotSetIsASchemaDefect checks that an environment variable name a shell cannot set is a schema defect.
func TestAnEnvironmentVariableNameAShellCannotSetIsASchemaDefect(t *testing.T) {
	for _, prefix := range []string{"crm-app", "my app", "APP=1", "1APP"} {
		reg := core.New()
		reg.Register(identifying("host", HostIdentity{Prefix: prefix}))
		_, err := newManifest(reg)
		if !errors.Is(err, ErrInvalidSchema) {
			t.Errorf("the prefix %q was collected with %v, want ErrInvalidSchema", prefix, err)
			continue
		}
		if !strings.Contains(err.Error(), `"host"`) {
			t.Errorf("the rejection of %q reads %q, which does not name the module", prefix, err)
		}
	}
	for _, name := range []string{"MY-VAR", "A=B", "9LIVES", "has space"} {
		err := expandFails(t, Schema{
			Mounts: mount(&addrCarrier{}),
			Items:  map[string]Item{"addr": {Origins: OriginEnv, EnvName: name}},
		}, "")
		if !strings.Contains(err.Error(), `"addr"`) {
			t.Errorf("the rejection of %q reads %q, which does not name the item", name, err)
		}
	}

	m := collect(t,
		identifying("host", HostIdentity{Prefix: "crm_app"}),
		declaring("alpha", Schema{
			Namespace: "cache",
			Mounts:    []Mount{{Value: &addrCarrier{}}},
			Items:     map[string]Item{"addr": {Origins: OriginEnv, EnvName: "http_proxy"}},
		}),
	)
	if got := m.byEnv["http_proxy"]; got == nil {
		t.Errorf("the manifest indexes the environment names %v, want the pinned http_proxy", envNames(m))
	}
}

// TestHostIdentityReachesDerivation checks that host identity reaches derivation.
func TestHostIdentityReachesDerivation(t *testing.T) {
	m := collect(t,
		identifying("host", HostIdentity{Prefix: "MYAPP", DefaultLocator: "file:///etc/myapp.yaml"}),
		declaring("alpha", Schema{Namespace: "cache", Mounts: []Mount{{Value: &addrCarrier{}}}}),
	)
	if !m.hasHost || m.host.DefaultLocator != "file:///etc/myapp.yaml" {
		t.Fatalf("manifest carries host identity %+v (declared %v), want the declared one", m.host, m.hasHost)
	}
	if got := m.byEnv["MYAPP_CACHE__ADDR"]; got == nil {
		t.Fatalf("manifest indexes the environment names %v, want MYAPP_CACHE__ADDR", envNames(m))
	}
}

// TestConflictMessageStableAcrossRegistrationOrders checks that conflict message stable across registration orders.
func TestConflictMessageStableAcrossRegistrationOrders(t *testing.T) {
	alpha := declaring("alpha", Schema{Namespace: "cache", Mounts: []Mount{{Value: &addrCarrier{}}}})
	zeta := declaring("zeta", Schema{Namespace: "cache", Mounts: []Mount{{Value: &addrCarrier{}}}})

	first := collectRejects(t, []string{"alpha", "zeta"}, alpha, zeta)
	second := collectRejects(t, []string{"alpha", "zeta"}, zeta, alpha)
	if first.Error() != second.Error() {
		t.Fatalf("registration order changed the conflict message:\n%v\n%v", first, second)
	}
	if strings.Index(first.Error(), "alpha") > strings.Index(first.Error(), "zeta") {
		t.Fatalf("conflict reads %q, want the parties in module-name order", first)
	}
}

// TestASectionAndAnInputItemMayNotShareAPath checks that a section and an input item may not share a path.
func TestASectionAndAnInputItemMayNotShareAPath(t *testing.T) {
	item := struct{ Sub string }{}
	collectRejects(t, []string{"alpha", "beta", "cache.sub"},
		declaring("alpha", Schema{Namespace: "cache.sub"}),
		declaring("beta", Schema{Namespace: "cache", Mounts: []Mount{{Value: &item}}}),
	)
}

// TestASectionAndAnInputItemConflictEitherWayRound checks that a section and an input item conflict either way round.
func TestASectionAndAnInputItemConflictEitherWayRound(t *testing.T) {
	item := struct{ Sub string }{}
	first := collectRejects(t, []string{"alpha", "zeta", "cache.sub"},
		declaring("alpha", Schema{Namespace: "cache", Mounts: []Mount{{Value: &item}}}),
		declaring("zeta", Schema{Namespace: "cache.sub"}),
	)
	second := collectRejects(t, []string{"alpha", "zeta", "cache.sub"},
		declaring("zeta", Schema{Namespace: "cache.sub"}),
		declaring("alpha", Schema{Namespace: "cache", Mounts: []Mount{{Value: &item}}}),
	)
	if first.Error() != second.Error() {
		t.Fatalf("registration order changed the conflict message:\n%v\n%v", first, second)
	}
}

func envNames(m *manifest) []string {
	out := make([]string, 0, len(m.byEnv))
	for name := range m.byEnv {
		out = append(out, name)
	}
	return out
}
