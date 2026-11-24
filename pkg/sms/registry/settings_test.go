package registry

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
)

type fakeSettings struct {
	mu     sync.Mutex
	values map[string]any
	err    error
	reads  []string
}

func (f *fakeSettings) Get(_ settings.Layer, key string, target any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, key)
	if f.err != nil {
		return f.err
	}
	value, ok := f.values[key]
	if !ok {
		return settings.ErrUnknownKey
	}
	reflect.ValueOf(target).Elem().Set(reflect.ValueOf(value))
	return nil
}

func (f *fakeSettings) Set(context.Context, settings.Write) error { return nil }

func (f *fakeSettings) Delete(context.Context, settings.Layer, string, string) error { return nil }

func (f *fakeSettings) List(context.Context, settings.Layer) ([]settings.Entry, error) {
	return nil, nil
}

func (f *fakeSettings) Declared() []settings.DeclaredItem { return nil }

func (f *fakeSettings) WrittenAt(settings.Layer, string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (f *fakeSettings) Read(key string, reads ...settings.LayerRead) ([]settings.LayerReadResult, error) {
	results := make([]settings.LayerReadResult, len(reads))
	for i, read := range reads {
		if err := f.Get(read.Layer, key, read.Target); err != nil {
			return nil, err
		}
		results[i] = settings.LayerReadResult{}
	}
	return results, nil
}

func (f *fakeSettings) set(key string, value any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[key] = value
}

func (f *fakeSettings) readKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reads...)
}

// TestSendingEnabledGatesResolve checks that false answers ErrNotSendable, true resolves, and the switch is read on every call.
func TestSendingEnabledGatesResolve(t *testing.T) {
	r := configuredRig(t)
	store := &fakeSettings{values: map[string]any{}}
	r.s.tuned = newSettingsReader(store, r.logs.logger())

	msg := sms.Message{Template: "welcome", Locale: "en", Params: map[string]string{"name": "Ann"}}
	if _, err := r.s.Resolve(msg); err != nil {
		t.Fatalf("resolving with the switch at its default: %v", err)
	}

	store.set(sendingEnabledPath, false)
	if _, err := r.s.Resolve(msg); !errors.Is(err, sms.ErrNotSendable) {
		t.Errorf("resolving with the switch off returned %v, want ErrNotSendable", err)
	}

	store.set(sendingEnabledPath, true)
	if _, err := r.s.Resolve(msg); err != nil {
		t.Errorf("resolving after the switch was turned back on returned %v", err)
	}

	reads := store.readKeys()
	if len(reads) != 3 {
		t.Fatalf("Resolve read the settings %d times, want once per call", len(reads))
	}
	for _, key := range reads {
		if key != sendingEnabledPath {
			t.Errorf("Resolve read %q, want %q", key, sendingEnabledPath)
		}
	}
}

// TestSendingEnabledFallsBackToTheDeclaredDefault checks that an unreadable
// switch reads as its declared default and the fault is logged.
func TestSendingEnabledFallsBackToTheDeclaredDefault(t *testing.T) {
	r := configuredRig(t)
	store := &fakeSettings{values: map[string]any{}, err: errors.New("the store is unreachable")}
	r.s.tuned = newSettingsReader(store, r.logs.logger())

	if _, err := r.s.Resolve(sms.Message{
		Template: "welcome", Locale: "en", Params: map[string]string{"name": "Ann"},
	}); err != nil {
		t.Errorf("resolving with an unreadable switch returned %v, want the declared default", err)
	}
	if len(r.logs.lines("switch cannot be read")) == 0 {
		t.Errorf("the unreadable switch was not logged; the log holds:\n%s", r.logs.buf.String())
	}
}

// TestTheModuleDeclaresTheSendingSwitchAsASetting checks that the item is
// declared under the module's namespace with a default of true.
func TestTheModuleDeclaresTheSendingSwitchAsASetting(t *testing.T) {
	var declared settings.Schema
	found := false
	for _, resource := range Module().Resources {
		if schema, ok := resource.(settings.Schema); ok {
			declared, found = schema, true
		}
	}
	if !found {
		t.Fatal("the module declares no settings.Schema, so the sending switch is not a setting")
	}
	if _, ok := declared.Items[sendingEnabledItem]; !ok {
		t.Errorf("the settings schema declares %v, want the %q item", declared.Items, sendingEnabledItem)
	}
	if declared.Namespace != ConfigNamespace {
		t.Errorf("the settings namespace is %q, want %q", declared.Namespace, ConfigNamespace)
	}
	if !settingDefaultValues.SendingEnabled {
		t.Errorf("the switch's declared default is false, want true")
	}
	if sendingEnabledPath != ConfigNamespace+"."+sendingEnabledItem {
		t.Errorf("the switch's path is %q, want %q", sendingEnabledPath, ConfigNamespace+"."+sendingEnabledItem)
	}
}
