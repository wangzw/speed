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

type listingChannel string

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

// TestDeclaredListsEveryItemOfEveryModule checks that Declared lists every item of every
// module in key order with its declarer, type, default text and metadata.
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

// TestDeclaredWithholdsASensitiveDefault checks that a sensitive item is listed with its metadata and without its default.
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

// TestDeclaredWithholdsTheDefaultOfASensitiveItemOfEveryKind checks that a sensitive item's default is withheld whatever its type.
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

// TestDeclaredAnswersBeforeTheSnapshotLoads checks that the listing answers before the snapshot is loaded, where a read does not.
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

// TestDeclaredHandsOutTheCallersOwnListing checks that the listing handed out is the caller's own.
func TestDeclaredHandsOutTheCallersOwnListing(t *testing.T) {
	store, _ := storeOf(t, tableOf(t, listingModules()...), newRecordingBus())
	first := store.Declared()
	first[0].Key = "changed"
	first[0].Default = "changed"
	if second := store.Declared(); second[0].Key == "changed" || second[0].Default == "changed" {
		t.Errorf("a caller's change to its listing reached the next one: %+v", second[0])
	}
}

// TestAListingDrivesAnAdministrativeSurfaceOnEveryDialect checks that a surface
// built on the listing alone reads and writes every item on either dialect.
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

type listingNested struct {
	ListingBase
	Retry    listingRetry
	Backup   *listingRetry `config:"fallback"`
	MaxConns int           `json:"max-conns"`
	Scratch  string        `config:"-"`
}

// ListingBase is embedded to pin the inlining of an anonymous field.
type ListingBase struct {
	// Region is a field of the inlined embedded struct.
	Region string
}

type listingRetry struct {
	Attempts int
	Backoff  time.Duration
}

// TestDeclaredListsTheKeysOfANonFlatCarrier checks that Declared lists the keys
// a nested carrier's expansion derives, with each key's type and marker.
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
