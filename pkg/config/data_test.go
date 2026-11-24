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

func decodeCacheOptions(t *testing.T, l *loader) cacheOptions {
	t.Helper()
	var opts cacheOptions
	if err := l.Reader().Decode("cache", &opts); err != nil {
		t.Fatalf("decoding the cache mount failed: %v", err)
	}
	return opts
}

// TestPrimaryOverridesLeafWiseNotSubtree checks that primary overrides leaf wise not subtree.
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

// TestContainerReplacedWholesale checks that container replaced wholesale.
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

// TestKeysInsideContainerLeafNotValidated checks that keys inside container leaf not validated.
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

// TestStructContainerUnknownMemberIsRefusedFromThePrimarySource checks that struct container unknown member is refused from the primary source.
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

// TestUnknownKeyNotInManifest checks that unknown key not in manifest.
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

// TestUnknownKeyUnderAnItemLessNamespaceIsRefusedByItsFullPath checks that unknown key under an item less namespace is refused by its full path.
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

// TestAValueAtAnItemLessNamespaceIsRefused checks that a value at an item less namespace is refused.
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

// TestKeyNotAcceptedFromPrimary checks that key not accepted from primary.
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

// TestOnePathGivenTwiceIsRefused checks that one path given twice is refused.
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

// TestSectionGivenAValue checks that section given a value.
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

// TestPrimaryValueTypeMismatch checks that primary value type mismatch.
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

// TestProvenanceRecordsLayer checks that provenance records layer.
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

// TestNoPrimarySourceStillYieldsDefaults checks that no primary source still yields defaults.
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

// TestDefaultsDoNotAliasThePrototype checks that defaults do not alias the prototype.
func TestDefaultsDoNotAliasThePrototype(t *testing.T) {
	defaults := cacheOptions{Labels: map[string]string{"tier": "hot"}}
	l := loadOver(t, declaring("cache", Schema{Namespace: "cache", Mounts: []Mount{{Value: &defaults}}}))
	held := decodeCacheOptions(t, l).Labels
	held["tier"] = "changed"
	if defaults.Labels["tier"] != "hot" {
		t.Fatal("writing through a decoded value reached the prototype the module declared")
	}
}
