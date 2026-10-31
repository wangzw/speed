package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type redisOptions struct {
	Addr     string
	PoolSize int
}

type cacheOptions struct {
	Addr   string
	TTL    time.Duration
	Labels map[string]string
	Nodes  []redisOptions
}

// cacheLoader declares one module under the cache namespace with a scalar, a
// duration, a map and a list of structs, which covers every shape a layer has
// to deal with.
func cacheLoader(t *testing.T, items map[string]Item) *loader {
	t.Helper()
	defaults := cacheOptions{
		Addr:   "localhost:6379",
		TTL:    5 * time.Minute,
		Labels: map[string]string{"tier": "hot"},
	}
	return loadOver(t, declaring("cache", Schema{
		Namespace: "cache",
		Mounts:    []Mount{{Value: &defaults}},
		Items:     items,
	}))
}

// decodeCacheOptions decodes the cache mount and fails the test if it cannot.
func decodeCacheOptions(t *testing.T, l *loader) cacheOptions {
	t.Helper()
	var opts cacheOptions
	if err := l.Reader().Decode("cache", &opts); err != nil {
		t.Fatalf("decoding the cache mount failed: %v", err)
	}
	return opts
}

// TestPrimaryOverridesLeafWiseNotSubtree pins that the primary source
// overrides declared leaves one by one: the key set of a section comes from
// the declarations, so giving one key must not knock its siblings back to
// their zero values.
//
// It is the one case that tells a leaf-wise read from a whole-section one.
// Asking the layering tool for the section would answer with the layer that
// holds it, whole, and the sibling nobody gave would come back zero.
func TestPrimaryOverridesLeafWiseNotSubtree(t *testing.T) {
	l := cacheLoader(t, nil)
	if err := givePrimary(t, l, map[string]any{
		"cache": map[string]any{"addr": "redis:6379"},
	}); err != nil {
		t.Fatalf("applying the primary source failed: %v", err)
	}
	opts := decodeCacheOptions(t, l)
	if opts.Addr != "redis:6379" {
		t.Fatalf("cache.addr decoded to %q, want the value the primary source gave", opts.Addr)
	}
	if opts.TTL != 5*time.Minute {
		t.Fatalf("cache.ttl decoded to %v, want the declared default: a sibling key nobody gave "+
			"must not be knocked back", opts.TTL)
	}
	if want := map[string]string{"tier": "hot"}; !reflect.DeepEqual(opts.Labels, want) {
		t.Fatalf("cache.labels decoded to %#v, want the declared default %#v", opts.Labels, want)
	}
}

// TestContainerReplacedWholesale pins the other half of the rule: a map and a
// list of structs have no keys in the manifest, so there is no leaf-wise merge
// to make and the value is replaced entirely.
func TestContainerReplacedWholesale(t *testing.T) {
	l := cacheLoader(t, nil)
	if err := givePrimary(t, l, map[string]any{
		"cache": map[string]any{
			"labels": map[string]any{"zone": "b"},
			"nodes":  []any{map[string]any{"addr": "n1:1", "pool-size": float64(3)}},
		},
	}); err != nil {
		t.Fatalf("applying the primary source failed: %v", err)
	}
	opts := decodeCacheOptions(t, l)
	want := map[string]string{"zone": "b"}
	if !reflect.DeepEqual(opts.Labels, want) {
		t.Fatalf("cache.labels decoded to %#v, want %#v: the default entry is replaced, not "+
			"merged under the new one", opts.Labels, want)
	}
	wantNodes := []redisOptions{{Addr: "n1:1", PoolSize: 3}}
	if !reflect.DeepEqual(opts.Nodes, wantNodes) {
		t.Fatalf("cache.nodes decoded to %#v, want %#v", opts.Nodes, wantNodes)
	}
}

// TestKeysInsideContainerLeafNotValidated pins that a map's own keys do not
// take part in the unknown-key check: they are the user's data, not names this
// module owns. The same fact decides that the value is replaced as a whole;
// using it a second time to judge the keys would make every map-typed item
// unusable at its first key. A member of a struct inside a container is a name
// the declaration owns, and the case below shows it is refused instead.
func TestKeysInsideContainerLeafNotValidated(t *testing.T) {
	l := cacheLoader(t, nil)
	err := givePrimary(t, l, map[string]any{
		"cache": map[string]any{
			"labels": map[string]any{"anything-at-all": "x", "cache.addr": "y"},
		},
	})
	if err != nil {
		t.Fatalf("keys inside a map were checked against the manifest: %v", err)
	}
}

// TestStructContainerUnknownMemberIsRefusedFromThePrimarySource pins that the
// file side is as strict as the environment side about a member of a struct
// inside a container. Those member names are not in the manifest, so no
// unknown-key pass over the source can see one; the decoder refuses it while
// writing the list, which is the only place left to report it.
func TestStructContainerUnknownMemberIsRefusedFromThePrimarySource(t *testing.T) {
	l := cacheLoader(t, nil)
	if err := givePrimary(t, l, map[string]any{
		"cache": map[string]any{
			"nodes": []any{map[string]any{"addr": "n1:1", "pool": float64(3)}},
		},
	}); err != nil {
		t.Fatalf("giving the primary source failed: %v", err)
	}
	var opts cacheOptions
	err := l.Reader().Decode("cache", &opts)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("an unknown member returned %v, want ErrTypeMismatch", err)
	}
	for _, want := range []string{"cache.nodes", "pool"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the rejection reads %q, which does not name %q", err, want)
		}
	}
}

func TestUnknownKeyNotInManifest(t *testing.T) {
	l := cacheLoader(t, nil)
	//nolint:misspell // "addres" is the test input, not prose: this case pins
	// that a key no module declares is refused, and spelling it correctly would
	// make it a known key and test nothing.
	err := givePrimary(t, l, map[string]any{"cache": map[string]any{"addres": "typo"}})
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a misspelled key returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "no module declares") {
		t.Fatalf("the rejection reads %q, which does not say the manifest has no such key", err)
	}
	if !strings.Contains(err.Error(), "cache.addres") {
		t.Fatalf("the rejection reads %q, which does not name the key", err)
	}
}

// TestUnknownKeyUnderAnItemLessNamespaceIsRefusedByItsFullPath pins that a
// namespace a module declares stays a section even when no input item sits under
// it: a source that still writes a key there is refused by that key's full path,
// not by the namespace alone. A module whose items have all moved to the
// settings store is the case this exists for.
func TestUnknownKeyUnderAnItemLessNamespaceIsRefusedByItsFullPath(t *testing.T) {
	l := loadOver(t, declaring("cache", Schema{Namespace: "cache.sub"}))
	err := givePrimary(t, l, map[string]any{
		"cache": map[string]any{"sub": map[string]any{"ttl": "5m"}},
	})
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a key under a declared namespace returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "cache.sub.ttl") {
		t.Fatalf("the rejection reads %q, which does not name the key", err)
	}
}

// TestAValueAtAnItemLessNamespaceIsRefused holds the other shape at an item-less
// section: a source that puts a scalar on the declared namespace itself is
// refused, because the namespace is a section holding input items rather than an
// input item of its own. The branch is separate from the interior-scalar one, so
// it is pinned on its own.
func TestAValueAtAnItemLessNamespaceIsRefused(t *testing.T) {
	l := loadOver(t, declaring("cache", Schema{Namespace: "cache.sub"}))
	err := givePrimary(t, l, map[string]any{
		"cache": map[string]any{"sub": "just a string"},
	})
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a value on a declared namespace returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "cache.sub") {
		t.Fatalf("the rejection reads %q, which does not name the path", err)
	}
	if !strings.Contains(err.Error(), "section") {
		t.Fatalf("the rejection reads %q, which does not say the path is a section", err)
	}
}

// TestKeyNotAcceptedFromPrimary is the second kind of unknown key, and the
// error text has to tell it from the first: the manifest has this key, and the
// item does not take the primary source. Letting it through would swallow a
// value somebody wrote on purpose.
func TestKeyNotAcceptedFromPrimary(t *testing.T) {
	l := cacheLoader(t, map[string]Item{"addr": {Origins: OriginFlag, FlagName: "cache-addr"}})
	err := givePrimary(t, l, map[string]any{"cache": map[string]any{"addr": "redis:6379"}})
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a key the item does not take from the primary source returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "does not accept from the primary source") {
		t.Fatalf("the rejection reads %q, which does not tell this case from a key the manifest "+
			"has never heard of", err)
	}
}

// TestOnePathGivenTwiceIsRefused covers a primary source that writes the same
// path once as a key holding the dots and once nested. Both spellings are
// accepted, which is what makes the pair two values for one item: the read
// takes one of them and drops the other, and nothing else says a written
// setting had no effect. A section written both ways is not the same thing,
// as long as the two spellings give different items in it.
func TestOnePathGivenTwiceIsRefused(t *testing.T) {
	for name, content := range map[string]map[string]any{
		"scalar": {"cache.addr": "flat:1", "cache": map[string]any{"addr": "nested:1"}},
		"container": {
			"cache.labels": map[string]any{"tier": "cold"},
			"cache":        map[string]any{"labels": map[string]any{"tier": "warm"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := givePrimary(t, cacheLoader(t, nil), content)
			if !errors.Is(err, ErrMalformedConfig) {
				t.Fatalf("one path written twice returned %v, want ErrMalformedConfig", err)
			}
			if !strings.Contains(err.Error(), "twice") {
				t.Errorf("the rejection reads %q, which does not say the path is given twice", err)
			}
		})
	}

	l := cacheLoader(t, nil)
	if err := givePrimary(t, l, map[string]any{
		"cache.addr": "flat:1",
		"cache":      map[string]any{"ttl": "1m"},
	}); err != nil {
		t.Fatalf("a section written both ways with different items in each returned %v", err)
	}
	if got := decodeCacheOptions(t, l); got.Addr != "flat:1" || got.TTL != time.Minute {
		t.Errorf("the section decoded to addr %q and ttl %v, want both spellings' items", got.Addr, got.TTL)
	}
}

// TestSectionGivenAValue covers the shape error the other way round: the path
// is a section holding input items, and the source put a value on it.
func TestSectionGivenAValue(t *testing.T) {
	l := cacheLoader(t, nil)
	err := givePrimary(t, l, map[string]any{"cache": "just a string"})
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a value on a section returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "section") {
		t.Fatalf("the rejection reads %q, which does not say the path is a section", err)
	}
}

// TestPrimaryValueTypeMismatch pins where a value that does not fit its field
// is refused. The check is made where the target type is in hand, which is the
// module's own Decode, so the error has to name the path on its own: the layer
// that gave the value is no longer part of the call.
func TestPrimaryValueTypeMismatch(t *testing.T) {
	l := cacheLoader(t, nil)
	if err := givePrimary(t, l, map[string]any{"cache": map[string]any{"ttl": "abc"}}); err != nil {
		t.Fatalf("applying the primary source failed: %v", err)
	}
	var opts cacheOptions
	err := l.Reader().Decode("cache", &opts)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("an unconvertible value returned %v, want ErrTypeMismatch", err)
	}
	if !strings.Contains(err.Error(), "cache.ttl") {
		t.Fatalf("the rejection reads %q, which does not name the path", err)
	}
}

// TestProvenanceRecordsLayer pins the record the required rule stands on: the
// three sources are asked one by one, and the default layer is not among them,
// so a value nobody gave and a value that happens to be the zero one stay
// apart.
func TestProvenanceRecordsLayer(t *testing.T) {
	l := cacheLoader(t, nil)
	item := l.m.byPath["cache.addr"]
	if l.given(item) {
		t.Fatal("the default layer counts as somebody having given a value")
	}
	if err := givePrimary(t, l, map[string]any{"cache": map[string]any{"addr": "redis:6379"}}); err != nil {
		t.Fatalf("applying the primary source failed: %v", err)
	}
	if !l.given(item) {
		t.Fatal("a value the primary source gave does not count as given")
	}
}

// TestNoPrimarySourceStillYieldsDefaults pins that configuration exists
// without a primary source: assembly is driven by the configuration, not by
// the config source.
func TestNoPrimarySourceStillYieldsDefaults(t *testing.T) {
	l := cacheLoader(t, nil)
	opts := decodeCacheOptions(t, l)
	if opts.TTL != 5*time.Minute {
		t.Fatalf("cache.ttl decoded to %v with no primary source, want the declared default", opts.TTL)
	}
	if opts.Addr != "localhost:6379" {
		t.Fatalf("cache.addr decoded to %q with no primary source, want the declared default", opts.Addr)
	}
}

// TestDefaultsDoNotAliasThePrototype pins that a decoded value cannot be used
// to write back into the struct a module declared, which two registries may
// share.
func TestDefaultsDoNotAliasThePrototype(t *testing.T) {
	defaults := cacheOptions{Labels: map[string]string{"tier": "hot"}}
	l := loadOver(t, declaring("cache", Schema{Namespace: "cache", Mounts: []Mount{{Value: &defaults}}}))
	held := decodeCacheOptions(t, l).Labels
	held["tier"] = "changed"
	if defaults.Labels["tier"] != "hot" {
		t.Fatal("writing through a decoded value reached the prototype the module declared")
	}
}
