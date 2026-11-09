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

// fakeSettings is a settings store over named rows, with every read recorded and
// one shared failure a case can make it answer, so a case can drive the sending
// switch without the settings module.
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

// TestSendingEnabledGatesResolve covers the switch: false answers ErrNotSendable,
// true resolves, and Resolve reads it on every call, so an operator can turn it
// back without a restart.
func TestSendingEnabledGatesResolve(t *testing.T) {
	r := configuredRig(t)
	store := &fakeSettings{values: map[string]any{}}
	r.s.tuned = newSettingsReader(store, r.logs.logger())

	msg := sms.Message{Template: "welcome", Locale: "en", Params: map[string]string{"name": "Ann"}}
	// The store holds no row, so the declared default applies and the message
	// resolves.
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

// TestSendingEnabledFallsBackToTheDeclaredDefault covers an unreadable switch: it
// reads as its declared default, true, and the fault is logged rather than
// taking the whole capability down.
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

// TestTheModuleDeclaresTheSendingSwitchAsASetting covers the declaration the
// settings module collects: the item is there under the module's namespace and
// its declared default is true.
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
