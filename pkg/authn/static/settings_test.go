package static

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// layerStore is a settings store that records the layers it was asked for.
type layerStore struct {
	*fakeSettings
	mu     sync.Mutex
	layers []settings.Layer
}

func (s *layerStore) Get(l settings.Layer, key string, target any) error {
	s.mu.Lock()
	s.layers = append(s.layers, l)
	s.mu.Unlock()
	return s.fakeSettings.Get(l, key, target)
}

// the settings are the global ones.
func TestTheSettingsAreReadFromTheGlobalLayer(t *testing.T) {
	store := &layerStore{fakeSettings: newFakeSettings()}
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.store = store
	b.cacheTTL()
	b.throttleParams()
	if len(store.layers) != 4 {
		t.Fatalf("the four settings were read with %d reads", len(store.layers))
	}
	for _, l := range store.layers {
		if l != settings.Global() {
			t.Errorf("a setting was read from the layer %v, want the global one", l)
		}
	}
}

// a fault that changes while it stays is reported again.
func TestASettingFaultThatChangesIsReportedAgain(t *testing.T) {
	store := newFakeSettings()
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.store = store
	var out capture
	b.logger = out.logger()

	store.set(freeAttemptsPath, 1000)
	b.throttleParams()
	b.throttleParams()
	store.set(freeAttemptsPath, 2000)
	b.throttleParams()
	b.throttleParams()
	if n := strings.Count(out.String(), "setting="+freeAttemptsPath); n != 2 {
		t.Errorf("a setting wrong in one way and then in another is reported %d times, want twice:\n%s", n, out.String())
	}
}

// the groups of names that share a back-off come out once each, in the
// order of their first member.
func TestSharedBackoffGroupsAreEachNamedOnceInTheOrderOfTheirFirstMember(t *testing.T) {
	got := sharedBackoffs([]string{"Zed", "bob", "Carol", "zed", "carol", "CAROL", "dave", "BOB"})
	want := [][]string{{"Zed", "zed"}, {"bob", "BOB"}, {"Carol", "carol", "CAROL"}}
	// The groups follow the first member of each: Zed, bob, Carol.
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sharedBackoffs gives %v, want %v", got, want)
	}
	if got := sharedBackoffs([]string{"a", "b", "c"}); len(got) != 0 {
		t.Errorf("names with no twin give %v, want none", got)
	}
}

// a setting that cannot be read is reported, once, at Error.
func TestASettingThatCannotBeReadIsReportedOnceAtError(t *testing.T) {
	store := newFakeSettings()
	store.err = errors.New("the store is not loaded")
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.store = store
	var out capture
	b.logger = out.logger()
	for range 4 {
		if got := b.cacheTTL(); got != 60*time.Second {
			t.Fatalf("an unreadable setting reads as %s, want the declared default", got)
		}
	}
	text := out.String()
	if n := strings.Count(text, "setting="+cacheTTLPath); n != 1 {
		t.Errorf("an unreadable setting is reported %d times over four reads, want once:\n%s", n, text)
	}
	if !strings.Contains(text, "level=ERROR") || !strings.Contains(text, "cannot be read") {
		t.Errorf("the report is not an Error that says the setting cannot be read:\n%s", text)
	}
}

// requests arrive together, and the settings are read, and a setting that
// is wrong reported, on each of them.
func TestRequestsRunTogetherWithoutARace(t *testing.T) {
	store := newFakeSettings()
	store.set(freeAttemptsPath, 1000)
	store.set(cacheTTLPath, time.Hour)
	b := tableWith(t, map[string]string{"alice": "secret", "bob": "bobs"})
	b.store = store
	var out capture
	b.logger = out.logger()

	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 30 {
				switch (g + i) % 4 {
				case 0:
					b.Verify(context.Background(), basic("alice", "secret"))
				case 1:
					b.Verify(context.Background(), basic("bob", "wrong"))
				case 2:
					b.Verify(context.Background(), basic("ghost"+strconv.Itoa(g), "x"))
				default:
					store.set(failWindowPath, time.Duration(i+1)*time.Hour)
				}
			}
		}()
	}
	wg.Wait()
}
