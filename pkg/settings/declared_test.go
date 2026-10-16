package settings

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

// listingPolicy and listingNotify are two modules' declarations: the shape an
// administrative surface lists without restating either of them.
type listingPolicy struct {
	Window  time.Duration
	Limit   int
	Opens   time.Time
	Gateway netip.Addr
}

type listingNotify struct {
	Template string
	Token    string
	Ratio    float32
	Channel  listingChannel
}

// listingChannel is a defined string, the ordinary shape of an enumeration.
type listingChannel string

// listingSecret is the sensitive default listingModules declares, which no
// listing may carry.
const listingSecret = "hunter2"

func listingModules() []core.Module {
	policy := listingPolicy{
		Window:  90 * time.Second,
		Limit:   100,
		Opens:   time.Date(2026, 9, 21, 12, 30, 0, 0, time.UTC),
		Gateway: netip.MustParseAddr("10.0.0.1"),
	}
	notify := listingNotify{Template: "Hello {{.Name}}", Token: listingSecret, Ratio: 0.1, Channel: "mail"}
	return []core.Module{
		declaring("test.policy", Schema{
			Namespace: "policy",
			Mounts:    []Mount{{Value: &policy}},
			Items:     map[string]Item{"limit": {Description: "requests per window"}},
		}),
		declaring("test.notify", Schema{
			Namespace: "notify",
			Mounts:    []Mount{{Path: "mail", Value: &notify}},
			Items: map[string]Item{
				"mail.template": {Description: "the message body"},
				"mail.token":    {Description: "the gateway credential", Sensitive: true},
			},
		}),
	}
}

// Declared lists every item of every declaring module, in the keys' byte
// order, with the declarer, the declared type, the default in the text a row
// would hold, and the metadata.
//
// Each field is one a surface over the settings cannot get elsewhere without
// restating the declaration: the type is what a read's target has to be, the
// default is what a key with no row reads as, and the description is written
// nowhere else. The default is the declared type's own text — a time as its
// MarshalText writes it, not as it prints — because that text is what Set
// takes back and what List reports for a row holding the same value.
func TestDeclaredListsEveryItemOfEveryModule(t *testing.T) {
	keys := tableOf(t, listingModules()...)
	store, _ := storeOf(t, keys, newRecordingBus())

	want := []DeclaredItem{
		{Key: "notify.mail.channel", Module: "test.notify", Type: reflect.TypeFor[listingChannel](), Default: "mail"},
		{Key: "notify.mail.ratio", Module: "test.notify", Type: reflect.TypeFor[float32](), Default: "0.1"},
		{
			Key: "notify.mail.template", Module: "test.notify", Type: reflect.TypeFor[string](),
			Default: "Hello {{.Name}}", Item: Item{Description: "the message body"},
		},
		{
			Key: "notify.mail.token", Module: "test.notify", Type: reflect.TypeFor[string](),
			Item: Item{Description: "the gateway credential", Sensitive: true},
		},
		{Key: "policy.gateway", Module: "test.policy", Type: reflect.TypeFor[netip.Addr](), Default: "10.0.0.1"},
		{
			Key: "policy.limit", Module: "test.policy", Type: reflect.TypeFor[int](),
			Default: "100", Item: Item{Description: "requests per window"},
		},
		{Key: "policy.opens", Module: "test.policy", Type: reflect.TypeFor[time.Time](), Default: "2026-09-21T12:30:00Z"},
		{Key: "policy.window", Module: "test.policy", Type: reflect.TypeFor[time.Duration](), Default: "1m30s"},
	}
	got := store.Declared()
	if len(got) != len(want) {
		t.Fatalf("Declared listed %d items, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("item %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A sensitive item is listed with its metadata and without its default.
//
// The listing exists to reach a client: an administrative endpoint's response,
// a command's output. A credential compiled in as a default does not earn that
// reach by being declared, so the listing carries the fact that the item is
// sensitive and nothing it holds.
func TestDeclaredWithholdsASensitiveDefault(t *testing.T) {
	store, _ := storeOf(t, tableOf(t, listingModules()...), newRecordingBus())
	listed := store.Declared()
	if rendered := fmt.Sprintf("%#v", listed); strings.Contains(rendered, listingSecret) {
		t.Errorf("the listing carries the sensitive default: %s", rendered)
	}
	for _, item := range listed {
		if item.Key == "notify.mail.token" && !item.Sensitive {
			t.Errorf("the credential is listed as an ordinary item, so nothing tells a surface to "+
				"mask the rows List hands out for it: %+v", item)
		}
	}
}

// listingSecrets carries one item of every kind the type set admits, each with
// a default a credential could be: a PIN is an integer, a rotation period a
// duration, a peer an address, a signing key a big number.
type listingSecrets struct {
	Password string
	Channel  listingChannel
	Pin      int
	Serial   uint64
	Armed    bool
	Seed     float64
	Rotation time.Duration
	Issued   time.Time
	Peer     netip.Addr
	Key      big.Int
}

// listingSecretsModule declares a listingSecrets under namespace, every item
// marked sensitive or none of them.
func listingSecretsModule(namespace string, sensitive bool) core.Module {
	secrets := listingSecrets{
		Password: listingSecret,
		Channel:  "pager",
		Pin:      482915,
		Serial:   9007199254740993,
		Armed:    true,
		Seed:     0.4142,
		Rotation: 97 * time.Minute,
		Issued:   time.Date(2031, 3, 4, 5, 6, 7, 0, time.UTC),
		Peer:     netip.MustParseAddr("192.0.2.77"),
	}
	secrets.Key.Lsh(big.NewInt(1), 70)
	items := make(map[string]Item)
	for _, key := range []string{
		"password", "channel", "pin", "serial", "armed", "seed", "rotation", "issued", "peer", "key",
	} {
		items[key] = Item{Sensitive: sensitive}
	}
	return declaring("test."+namespace, Schema{
		Namespace: namespace,
		Mounts:    []Mount{{Value: &secrets}},
		Items:     items,
	})
}

// A sensitive item's default is withheld whatever its declared type is.
//
// The marker is a property of the declaration, not of a type: a PIN compiled in
// as an integer default, a rotation period as a duration or a peer as an
// address reaches a client through the listing exactly as a string credential
// would. The same carrier declared without the marker is the control: every one
// of those defaults is listed there, so each one withheld is one that was there
// to withhold.
func TestDeclaredWithholdsTheDefaultOfASensitiveItemOfEveryKind(t *testing.T) {
	keys := tableOf(t, listingSecretsModule("secret", true), listingSecretsModule("plain", false))
	store, _ := storeOf(t, keys, newRecordingBus())

	kinds := reflect.TypeFor[listingSecrets]().NumField()
	listed := map[string][]DeclaredItem{}
	for _, item := range store.Declared() {
		namespace, _, _ := strings.Cut(item.Key, ".")
		listed[namespace] = append(listed[namespace], item)
	}
	if len(listed["secret"]) != kinds || len(listed["plain"]) != kinds {
		t.Fatalf("listed %d sensitive and %d ordinary items, want %d of each: %+v",
			len(listed["secret"]), len(listed["plain"]), kinds, listed)
	}
	for _, item := range listed["plain"] {
		if item.Default == "" {
			t.Errorf("the ordinary item %q is listed with no default, so the case cannot tell a "+
				"withheld default from an absent one", item.Key)
		}
	}
	for _, item := range listed["secret"] {
		if !item.Sensitive || item.Default != "" {
			t.Errorf("the sensitive %v item %q is listed with default %q (sensitive = %v), want "+
				"none", item.Type, item.Key, item.Default, item.Sensitive)
		}
	}
}

// The listing answers before the snapshot is loaded, where a read does not.
//
// A read before the load is refused because the snapshot is not there to say
// what the table holds; the listing reads the key table alone, which is
// complete once the store is constructed. A module that lists the settings in
// its own Init, or a command whose drive stops short of this module's Init,
// gets the full listing either way.
func TestDeclaredAnswersBeforeTheSnapshotLoads(t *testing.T) {
	store := newSettingsStore(tableOf(t, listingModules()...),
		stubDatabase{handle: open(t), dialect: db.SQLite}, newRecordingBus(), moduleSettings{})
	if _, err := Get[int](store, Global(), "policy.limit"); !errors.Is(err, ErrNotLoaded) {
		t.Fatalf("a read before the load returned %v, want ErrNotLoaded", err)
	}
	if listed := store.Declared(); len(listed) == 0 {
		t.Error("the listing is empty before the snapshot loads")
	}
}

// The listing handed out is the caller's own: modifying it changes nothing
// the next caller receives.
func TestDeclaredHandsOutTheCallersOwnListing(t *testing.T) {
	store, _ := storeOf(t, tableOf(t, listingModules()...), newRecordingBus())
	first := store.Declared()
	first[0].Key = "changed"
	first[0].Default = "changed"
	if second := store.Declared(); second[0].Key == "changed" || second[0].Default == "changed" {
		t.Errorf("a caller's change to its listing reached the next one: %+v", second[0])
	}
}

// A surface built on nothing but the listing reads every item under its
// listed type, writes the listed default back as text, and finds that text in
// the rows, on either dialect.
//
// This is the use the listing is for: an endpoint that knows no declaration
// of its own. A read's target has to be exactly the declared type, which only
// the listed Type lets such a surface build; a default in any form other than
// the declared type's own text would read as a different value than the one
// the key serves, or be refused when written back.
func TestAListingDrivesAnAdministrativeSurfaceOnEveryDialect(t *testing.T) {
	for _, d := range dialectsUnderTest {
		t.Run(d.name, func(t *testing.T) {
			keys := tableOf(t, listingModules()...)
			store := storeOn(t, d.open(t), d.dialect, keys, newRecordingBus())
			ctx := context.Background()
			tenant := ForTenant("t1")

			listed := store.Declared()
			for _, item := range listed {
				if item.Sensitive {
					continue
				}
				target := reflect.New(item.Type)
				if err := store.Get(tenant, item.Key, target.Interface()); err != nil {
					t.Fatalf("reading %q under its listed type %v: %v", item.Key, item.Type, err)
				}
				served, err := encodeValue(keys.entries[item.Key], target.Elem().Interface())
				if err != nil || served != item.Default {
					t.Errorf("%q serves %q (%v), and the listing gives its default as %q",
						item.Key, served, err, item.Default)
				}
				err = store.Set(ctx, Write{Layer: tenant, Key: item.Key, Value: item.Default, Actor: "admin"})
				if err != nil {
					t.Errorf("writing the listed default of %q back returned %v", item.Key, err)
				}
			}

			entries, err := store.List(ctx, tenant)
			if err != nil {
				t.Fatalf("listing the rows: %v", err)
			}
			rows := make(map[string]string, len(entries))
			for _, e := range entries {
				rows[e.Key] = e.Value
			}
			for _, item := range listed {
				if item.Sensitive {
					continue
				}
				if text, ok := rows[item.Key]; !ok || text != item.Default {
					t.Errorf("the row of %q holds %q (present = %v), want the listed default %q",
						item.Key, text, ok, item.Default)
				}
			}
		})
	}
}

// listingNested is a carrier of every shape the key rule expands rather than
// takes as a leaf: an embedded struct inlined, a nested struct and a pointer to
// one under a segment of their own, a segment named by a tag, a field kept out.
// The embedded type is exported: an unexported one is skipped whole, by
// configuration as by this module.
type listingNested struct {
	ListingBase
	Retry    listingRetry
	Backup   *listingRetry `config:"fallback"`
	MaxConns int           `json:"max-conns"`
	Scratch  string        `config:"-"`
}

type ListingBase struct{ Region string }

type listingRetry struct {
	Attempts int
	Backoff  time.Duration
}

// Declared lists the keys a carrier's expansion derives, however deep the
// carrier is, with each key's own type and marker: the listing is the key
// table's, not a second derivation of it. A surface that restated the key rule
// to find another module's keys would have to follow every one of these
// shapes, and a sensitive marker given to a nested item is what it would need
// to mask that item's rows.
func TestDeclaredListsTheKeysOfANonFlatCarrier(t *testing.T) {
	carrier := listingNested{
		ListingBase: ListingBase{Region: "eu"},
		Retry:       listingRetry{Attempts: 3, Backoff: time.Second},
		Backup:      &listingRetry{Attempts: 1, Backoff: time.Minute},
		MaxConns:    8,
	}
	keys := tableOf(t, declaring("test.nested", Schema{
		Namespace: "nested",
		Mounts:    []Mount{{Path: "pool", Value: &carrier}},
		Items:     map[string]Item{"pool.retry.attempts": {Sensitive: true}},
	}))
	store, _ := storeOf(t, keys, newRecordingBus())

	want := map[string]struct {
		typ       reflect.Type
		def       string
		sensitive bool
	}{
		"nested.pool.region":            {reflect.TypeFor[string](), "eu", false},
		"nested.pool.retry.attempts":    {reflect.TypeFor[int](), "", true},
		"nested.pool.retry.backoff":     {reflect.TypeFor[time.Duration](), "1s", false},
		"nested.pool.fallback.attempts": {reflect.TypeFor[int](), "1", false},
		"nested.pool.fallback.backoff":  {reflect.TypeFor[time.Duration](), "1m0s", false},
		"nested.pool.max-conns":         {reflect.TypeFor[int](), "8", false},
	}
	listed := store.Declared()
	if len(listed) != len(want) {
		t.Fatalf("Declared lists %d items, want %d: %+v", len(listed), len(want), listed)
	}
	for _, item := range listed {
		w, ok := want[item.Key]
		if !ok {
			t.Errorf("Declared lists %q, which the carrier does not derive", item.Key)
			continue
		}
		if item.Type != w.typ || item.Default != w.def || item.Sensitive != w.sensitive {
			t.Errorf("%q is listed as %v with default %q (sensitive %v), want %v, %q, %v",
				item.Key, item.Type, item.Default, item.Sensitive, w.typ, w.def, w.sensitive)
		}
		if err := store.Get(Global(), item.Key, reflect.New(item.Type).Interface()); err != nil {
			t.Errorf("reading the listed %q under its listed type: %v", item.Key, err)
		}
	}
}
