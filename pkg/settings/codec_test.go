package settings

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// secretTable declares one sensitive item whose type can fail to decode, so
// that the failure path has a value to withhold.
func secretTable(t *testing.T) *keyTable {
	t.Helper()
	defaults := struct {
		Token int
	}{}
	return tableOf(t, declaring("test", Schema{
		Namespace: "secret",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{
			"token": {Description: "a credential", Sensitive: true},
		},
	}))
}

// A declaration may name a defined scalar — an enumeration written as
// `type mode string` is the ordinary way to write one — and a read has to hand
// back that type rather than the builtin it is built from.
//
// The two are not interchangeable: a builtin string is not assignable to a
// defined string type, so a read that built the builtin and assigned it would
// panic inside the library the first time the key held a value.
func TestADefinedScalarRoundTrips(t *testing.T) {
	type mode string
	type toggle bool
	defaults := struct {
		Mode   mode
		Toggle toggle
	}{Mode: "auto", Toggle: true}
	keys := tableOf(t, declaring("defined", Schema{
		Namespace: "defined",
		Mounts:    []Mount{{Value: &defaults}},
	}))
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()

	var got mode
	if err := store.Get(Global(), "defined.mode", &got); err != nil {
		t.Fatalf("reading the declared default: %v", err)
	}
	if got != "auto" {
		t.Errorf("the default read back as %q, want auto", got)
	}

	if err := store.Set(ctx, Write{Layer: Global(), Key: "defined.mode", Value: mode("manual")}); err != nil {
		t.Fatalf("writing the defined string: %v", err)
	}
	if err := store.Get(Global(), "defined.mode", &got); err != nil {
		t.Fatalf("reading a written defined string: %v", err)
	}
	if got != "manual" {
		t.Errorf("the written value read back as %q, want manual", got)
	}

	var flag toggle
	if err := store.Set(ctx, Write{Layer: Global(), Key: "defined.toggle", Value: toggle(false)}); err != nil {
		t.Fatalf("writing the defined bool: %v", err)
	}
	if err := store.Get(Global(), "defined.toggle", &flag); err != nil {
		t.Fatalf("reading a written defined bool: %v", err)
	}
	if flag {
		t.Error("the written defined bool read back as true, want false")
	}
}

// A value of another numeric Go type is converted under the discipline
// configuration shares, so a whole number reaches an integer item and a
// fractional one does not.
func TestANumberIsHeldToTheDeclaredType(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()

	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 10.0}); err != nil {
		t.Fatalf("writing a whole float to an integer item: %v", err)
	}
	if got, _ := Get[int](store, Global(), "demo.limit"); got != 10 {
		t.Errorf("the whole float read back as %v, want 10", got)
	}

	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 10.5}); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("writing a fractional float returned %v, want ErrTypeMismatch", err)
	}
	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: int64(1) << 62}); err != nil {
		t.Errorf("writing a wide integer returned %v, and an int is 64-bit wide", err)
	}
	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: "text"}); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("writing a string returned %v, want ErrTypeMismatch", err)
	}

	// An integer goes into a float item: 1 written from Go source is an int,
	// and configuration takes it into the same field.
	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.ratio", Value: 1}); err != nil {
		t.Fatalf("writing an integer to a float item returned %v, want nil", err)
	}
	if got, _ := Get[float64](store, Global(), "demo.ratio"); got != 1 {
		t.Errorf("the integer read back as %v, want 1", got)
	}
}

// The largest finite float32 goes back in as float32 writes it.
//
// float32's shortest text for math.MaxFloat32 is 3.4028235e+38, and that text
// names a number a little above the value itself. It is still a number the item
// holds by the round-trip rule: its nearest float32 is math.MaxFloat32, which
// float32 writes as the same text. So the default the listing gives for such an
// item, and the row List reports for it, are texts Set takes back, as they are
// for every other value. A number whose nearest float32 is an infinity, and one
// the item would hold as its largest value while writing it as a different
// number, are refused as every other neighbour is.
func TestTheLargestFloat32GoesBackInAsItIsWritten(t *testing.T) {
	defaults := struct {
		Ceiling float32
		Floor   float32
	}{Ceiling: math.MaxFloat32, Floor: -math.MaxFloat32}
	keys := tableOf(t, declaring("test.edge", Schema{Namespace: "edge", Mounts: []Mount{{Value: &defaults}}}))
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()

	for _, item := range store.Declared() {
		want, err := Get[float32](store, Global(), item.Key)
		if err != nil {
			t.Fatalf("reading the default of %q: %v", item.Key, err)
		}
		if err := store.Set(ctx, Write{Layer: Global(), Key: item.Key, Value: item.Default}); err != nil {
			t.Errorf("writing the listed default %q of %q back returned %v", item.Default, item.Key, err)
			continue
		}
		if got, err := Get[float32](store, Global(), item.Key); err != nil || got != want {
			t.Errorf("%q read back as %v (%v) after its default went back in, want %v",
				item.Key, got, err, want)
		}
	}
	entries, err := store.List(ctx, Global())
	if err != nil || len(entries) != 2 {
		t.Fatalf("listing the rows gave %+v (%v), want the two written", entries, err)
	}
	for _, e := range entries {
		if err := store.Set(ctx, Write{Layer: Global(), Key: e.Key, Value: e.Value}); err != nil {
			t.Errorf("writing the row %q of %q back returned %v", e.Value, e.Key, err)
		}
	}

	halfway := 0x1p128 - 0x1p103 // between math.MaxFloat32 and the next power of two
	cases := []struct {
		value    any
		accepted bool
	}{
		{3.4028235e38, true},
		{"3.4028235e+38", true},
		{-3.4028235e38, true},
		{"-3.4028235e+38", true},
		{float64(math.MaxFloat32), true},
		{3.40282356e38, false},
		{"3.40282356e+38", false},
		{math.Nextafter(halfway, 0), false},
		{halfway, false},
		{3.4028236e38, false},
		{"3.4028236e+38", false},
		{-1e39, false},
	}
	for _, c := range cases {
		err := store.Set(ctx, Write{Layer: Global(), Key: "edge.ceiling", Value: c.value})
		switch {
		case c.accepted && err != nil:
			t.Errorf("writing %v into a float32 item returned %v, want it held as math.MaxFloat32", c.value, err)
		case !c.accepted && !errors.Is(err, ErrTypeMismatch):
			t.Errorf("writing %v into a float32 item returned %v, want ErrTypeMismatch", c.value, err)
		}
	}
}

// A duration item takes duration text and refuses a bare number.
//
// A duration counts nanoseconds underneath, so a bare number is the one value
// that reads as one thing and means another: an endpoint receiving
// {"value": 60} for a window it shows in seconds would store sixty
// nanoseconds, and a consumer checking only that the window is positive would
// act on it. The text is the form configuration takes for the same item.
func TestADurationIsWrittenAsText(t *testing.T) {
	keys := demoTable(t)
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()

	for _, number := range []any{300, int64(300), uint(300), float64(300)} {
		err := store.Set(ctx, Write{Layer: Global(), Key: "demo.window", Value: number})
		if !errors.Is(err, ErrTypeMismatch) {
			t.Errorf("writing the %T %v to a duration item returned %v, want ErrTypeMismatch",
				number, number, err)
		}
	}
	if rows := countRows(t, store); rows != 0 {
		t.Fatalf("a refused duration left %d rows behind", rows)
	}

	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.window", Value: "300s"}); err != nil {
		t.Fatalf("writing duration text returned %v, want nil", err)
	}
	if got, _ := Get[time.Duration](store, Global(), "demo.window"); got != 5*time.Minute {
		t.Errorf("the duration read back as %v, want 5m0s", got)
	}
	if err := store.Set(ctx, Write{Layer: Global(), Key: "demo.window", Value: "soon"}); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("writing text that is not a duration returned %v, want ErrTypeMismatch", err)
	}
}

// A read hands out a copy of what the module holds, so a caller that modifies
// what it read cannot reach the declaration's default or the snapshot's value.
func TestAReadHandsOutItsOwnCopy(t *testing.T) {
	prototype := struct {
		Addrs textList
	}{Addrs: textList{"a", "b"}}
	keys := tableOf(t, declaring("copy", Schema{Namespace: "copy", Mounts: []Mount{{Value: &prototype}}}))
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()

	// The declared default: writing through what a read returned must not
	// change the declaration the next read resolves against.
	var got textList
	if err := store.Get(Global(), "copy.addrs", &got); err != nil {
		t.Fatalf("reading the default: %v", err)
	}
	got[0] = "changed"
	if err := store.Get(Global(), "copy.addrs", &got); err != nil {
		t.Fatalf("reading the default again: %v", err)
	}
	if got[0] != "a" {
		t.Errorf("the second read returned %q, so the default was modified through the first",
			got[0])
	}
	if prototype.Addrs[0] != "a" {
		t.Errorf("the prototype's field became %q", prototype.Addrs[0])
	}

	// The snapshot's value: the same has to hold once a row exists.
	if err := store.Set(ctx, Write{Layer: Global(), Key: "copy.addrs", Value: textList{"x", "y"}}); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if err := store.Get(Global(), "copy.addrs", &got); err != nil {
		t.Fatalf("reading the written value: %v", err)
	}
	got[0] = "changed"
	if err := store.Get(Global(), "copy.addrs", &got); err != nil {
		t.Fatalf("reading the written value again: %v", err)
	}
	if got[0] != "x" {
		t.Errorf("the second read returned %q, so the snapshot's value was modified through "+
			"the first", got[0])
	}
}

// A sensitive item's stored value stays out of the error text, including the
// error that reports it failing to decode.
//
// The error a read returns is the module's own writing, and it travels to
// wherever the caller logs it. A credential reaches a table and a snapshot
// legitimately; it does not reach a log line, which is what the sensitive
// marker is for.
func TestASensitiveValueStaysOutOfTheErrorText(t *testing.T) {
	keys := secretTable(t)
	handle := open(t)
	const planted = "hunter2-not-a-number"
	if err := handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
		"VALUES ('', 'secret.token', ?, 1, ?)", planted, time.Now().UTC()).Error; err != nil {
		t.Fatalf("planting a bad value: %v", err)
	}
	store := newSettingsStore(keys, stubDatabase{handle: handle, dialect: demoDialect()},
		newRecordingBus(), moduleSettings{})
	store.logger = discardLogger()
	if err := store.load(context.Background()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	var target int
	err := store.Get(Global(), "secret.token", &target)
	if err == nil {
		t.Fatal("reading a value that does not decode returned no error")
	}
	if strings.Contains(err.Error(), planted) {
		t.Errorf("the error text carries the sensitive value: %v", err)
	}
	if !strings.Contains(err.Error(), "secret.token") {
		t.Errorf("the error text does not name the key, and it should: %v", err)
	}
}

// An ordinary item's value is not withheld: the text is what the reader needs
// to reconcile the declaration with the row.
func TestAnOrdinaryValueIsNamedInTheErrorText(t *testing.T) {
	keys := demoTable(t)
	handle := open(t)
	const planted = "not-a-number"
	if err := handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
		"VALUES ('', 'demo.limit', ?, 1, ?)", planted, time.Now().UTC()).Error; err != nil {
		t.Fatalf("planting a bad value: %v", err)
	}
	store := newSettingsStore(keys, stubDatabase{handle: handle, dialect: demoDialect()},
		newRecordingBus(), moduleSettings{})
	store.logger = discardLogger()
	if err := store.load(context.Background()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	var target int
	err := store.Get(Global(), "demo.limit", &target)
	if err == nil {
		t.Fatal("reading a value that does not decode returned no error")
	}
	if !strings.Contains(err.Error(), planted) {
		t.Errorf("the error text omits the stored value, and this item is not sensitive: %v", err)
	}
}
