package settings

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
)

// bounded is a declaration whose items each carry a domain, over the kinds a
// domain is ordinarily given: an integer, a duration, a defined string and a
// sensitive text.
type bounded struct {
	Limit   int
	Window  time.Duration
	Channel boundedChannel
	Token   string
	Note    string
}

// boundedChannel is a defined string, the ordinary shape of an enumeration.
type boundedChannel string

const (
	boundedNamespace = "bounded"
	positiveRule     = "the positive integers"
	windowRule       = "the durations from one second to one hour"
	channelRule      = "mail and sms"
	tokenRule        = "the texts of at least eight characters"
)

func boundedDefaults() bounded {
	return bounded{Limit: 100, Window: time.Minute, Channel: "mail", Token: "default-token", Note: "free"}
}

func boundedItems() map[string]Item {
	return map[string]Item{
		"limit": {Domain: DomainOf(positiveRule, func(v int) bool { return v > 0 })},
		"window": {Domain: DomainOf(windowRule, func(v time.Duration) bool {
			return v >= time.Second && v <= time.Hour
		})},
		"channel": {Domain: DomainOf(channelRule, func(v boundedChannel) bool {
			return v == "mail" || v == "sms"
		})},
		"token": {Sensitive: true, Domain: DomainOf(tokenRule, func(v string) bool { return len(v) >= 8 })},
	}
}

func boundedModule(defaults bounded, items map[string]Item) core.Module {
	return declaring("test.bounded", Schema{
		Namespace: boundedNamespace,
		Mounts:    []Mount{{Value: &defaults}},
		Items:     items,
	})
}

func boundedTable(t *testing.T) *keyTable {
	t.Helper()
	return tableOf(t, boundedModule(boundedDefaults(), boundedItems()))
}

// A value outside the domain its declaration gives is refused before the row
// is written, as a value of the wrong type is, but under ErrInvalidValue: the
// value is of the declared type, and it is not one the declaring module acts
// on. The refusal names the key and the rule, and quotes the text the value
// would have been stored as. Nothing changes: no row, no change event, and a
// read goes on answering what it answered.
//
// The domain is judged on the value held to the declared type, so the forms an
// administrative endpoint hands the store — a number decoded from JSON, a text
// — are refused as the typed value is. A value the type itself refuses is
// still the type's refusal.
func TestSetRefusesAValueOutsideTheDeclaredDomain(t *testing.T) {
	bus := newRecordingBus()
	store, _ := storeOf(t, boundedTable(t), bus)
	ctx := context.Background()
	layer := ForTenant("acme")

	refused := []struct {
		name, key string
		value     any
		rule      string
		stored    string
	}{
		{"a zero limit", "bounded.limit", 0, positiveRule, `"0"`},
		{"a negative limit", "bounded.limit", -5, positiveRule, `"-5"`},
		{"a zero limit as text", "bounded.limit", "0", positiveRule, `"0"`},
		{"a zero limit decoded from JSON", "bounded.limit", 0.0, positiveRule, `"0"`},
		{"a window too short", "bounded.window", 500 * time.Millisecond, windowRule, `"500ms"`},
		{"a window too long as text", "bounded.window", "90m", windowRule, `"1h30m0s"`},
		{"a channel outside the enumeration", "bounded.channel", "fax", channelRule, `"fax"`},
	}
	for _, c := range refused {
		err := store.Set(ctx, Write{Layer: layer, Key: c.key, Value: c.value})
		if !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%s: Set answered %v, want ErrInvalidValue", c.name, err)
			continue
		}
		for _, part := range []string{c.key, c.rule, c.stored} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("%s: the refusal %q does not carry %s", c.name, err, part)
			}
		}
	}

	if err := store.Set(ctx, Write{Layer: layer, Key: "bounded.limit", Value: "many"}); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("a limit that is not a number answered %v, want ErrTypeMismatch", err)
	}

	rows, err := store.List(ctx, layer)
	if err != nil {
		t.Fatalf("listing the tenant's rows: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("the refused writes left rows behind: %+v", rows)
	}
	if events := bus.events(); len(events) != 0 {
		t.Errorf("the refused writes published %d change events, want none", len(events))
	}
	if got, err := Get[int](store, layer, "bounded.limit"); err != nil || got != 100 {
		t.Errorf("the limit reads %d (%v) after the refused writes, want the declared 100", got, err)
	}

	accepted := []struct {
		key   string
		value any
	}{
		{"bounded.limit", 1},
		{"bounded.limit", 3.0},
		{"bounded.window", "1s"},
		{"bounded.window", time.Hour},
		{"bounded.channel", "sms"},
		{"bounded.channel", boundedChannel("mail")},
		{"bounded.note", "an item with no domain takes any value of its type"},
	}
	for _, c := range accepted {
		if err := store.Set(ctx, Write{Layer: layer, Key: c.key, Value: c.value}); err != nil {
			t.Errorf("writing %v to %s failed: %v, want it stored", c.value, c.key, err)
		}
	}
}

// The predicate is handed a value of exactly the declared type, whatever form
// the write arrived in: a []string written to a textList item — a value only
// assignable to the declared type, which the write accepts as it is — reaches
// it as a textList, and so does a text the type decodes. A predicate typed
// for the declared type would otherwise never see the value it admits, and
// every write in that form would be refused.
func TestADomainIsHandedTheValueInTheDeclaredType(t *testing.T) {
	var seen []reflect.Type
	prototype := struct{ Addrs textList }{Addrs: textList{"x"}}
	keys := tableOf(t, declaring("test.list", Schema{
		Namespace: "list",
		Mounts:    []Mount{{Value: &prototype}},
		Items: map[string]Item{"addrs": {Domain: DomainOf("the lists of one or two addresses",
			func(v textList) bool {
				seen = append(seen, reflect.TypeOf(v))
				return len(v) >= 1 && len(v) <= 2
			})}},
	}))
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()
	want := reflect.TypeFor[textList]()

	for _, value := range []any{[]string{"x", "y"}, "x,y", textList{"x", "y"}} {
		seen = nil
		if err := store.Set(ctx, Write{Layer: Global(), Key: "list.addrs", Value: value}); err != nil {
			t.Errorf("writing %#v, which the domain admits, failed: %v", value, err)
		}
		if len(seen) != 1 || seen[0] != want {
			t.Errorf("writing %#v handed the predicate %v, want one value of %v", value, seen, want)
		}
	}
	if err := store.Set(ctx, Write{Layer: Global(), Key: "list.addrs", Value: []string{"x", "y", "z"}}); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("writing three addresses answered %v, want ErrInvalidValue", err)
	}
}

// A sensitive item's value outside its domain is refused like any other, and
// the refusal carries neither the value nor the text it would have been
// stored as: the error reaches an administrative endpoint's response and the
// logs, where the marker exists to keep a credential out.
func TestARefusalOfASensitiveValueWithholdsIt(t *testing.T) {
	store, _ := storeOf(t, boundedTable(t), newRecordingBus())
	const secret = "hunter2"
	err := store.Set(context.Background(), Write{Layer: Global(), Key: "bounded.token", Value: secret})
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("a token too short answered %v, want ErrInvalidValue", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the refusal %q quotes the sensitive value", err)
	}
	for _, part := range []string{"bounded.token", tokenRule} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("the refusal %q does not carry %s", err, part)
		}
	}
}

// The domain is judged when a value is written, and nowhere else. A row that
// lies outside it — stored before the domain was declared, or written around
// this module — reads back as it is, rather than as a failure or as the
// default: a consumer that cannot act on such a value judges what it reads.
func TestARowOutsideTheDomainReadsAsItIs(t *testing.T) {
	store, handle := storeOf(t, boundedTable(t), newRecordingBus())
	insert := "INSERT INTO " + tableName + " (tenant_id, key, value, version, updated_at) VALUES (?, ?, ?, 1, ?)"
	if err := handle.Exec(insert, "acme", "bounded.limit", "0", time.Now().UTC()).Error; err != nil {
		t.Fatalf("writing the row around the store: %v", err)
	}
	if err := store.load(context.Background()); err != nil {
		t.Fatalf("reloading the snapshot: %v", err)
	}
	if got, err := Get[int](store, ForTenant("acme"), "bounded.limit"); err != nil || got != 0 {
		t.Errorf("the stored limit reads %d (%v), want the 0 the row holds", got, err)
	}
}

// A domain the declaration cannot be held to is the declaration's own defect,
// found when the key table is built: one over another type than the item's,
// which no write could hand a value of; one with no rule, which a refusal
// could not name; one with no predicate; and one that does not admit the
// item's own default, which a write could never store back once a row had
// replaced it. The last names the default unless the item is sensitive.
func TestADomainTheDeclarationCannotBeHeldToIsADeclarationDefect(t *testing.T) {
	cases := []struct {
		name     string
		defaults bounded
		item     string
		domain   Item
		carries  []string
		withheld string
	}{
		{
			name:    "a domain over another type",
			item:    "limit",
			domain:  Item{Domain: DomainOf(positiveRule, func(v int64) bool { return v > 0 })},
			carries: []string{`"limit"`, "int64", "DomainOf[int]"},
		},
		{
			name:    "a domain with no rule",
			item:    "limit",
			domain:  Item{Domain: DomainOf("", func(v int) bool { return v > 0 })},
			carries: []string{"limit", "no rule"},
		},
		{
			name:    "a domain with no predicate",
			item:    "limit",
			domain:  Item{Domain: DomainOf[int](positiveRule, nil)},
			carries: []string{"limit", "no predicate"},
		},
		{
			name:     "a default outside its domain",
			defaults: bounded{Limit: 0, Window: time.Minute, Channel: "mail", Token: "default-token"},
			item:     "limit",
			domain:   Item{Domain: DomainOf(positiveRule, func(v int) bool { return v > 0 })},
			carries:  []string{"limit", `"0"`, positiveRule},
		},
		{
			name:     "a sensitive default outside its domain",
			defaults: bounded{Limit: 100, Window: time.Minute, Channel: "mail", Token: "short"},
			item:     "token",
			domain:   Item{Sensitive: true, Domain: DomainOf(tokenRule, func(v string) bool { return len(v) >= 8 })},
			carries:  []string{"token", tokenRule, "withheld"},
			withheld: "short",
		},
	}
	for _, c := range cases {
		defaults := c.defaults
		if defaults == (bounded{}) {
			defaults = boundedDefaults()
		}
		items := boundedItems()
		items[c.item] = c.domain
		_, err := buildKeyTable(registryWith(t, boundedModule(defaults, items)))
		if !errors.Is(err, ErrInvalidDeclaration) {
			t.Errorf("%s: building the key table answered %v, want ErrInvalidDeclaration", c.name, err)
			continue
		}
		for _, part := range append([]string{"test.bounded"}, c.carries...) {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("%s: the defect %q does not carry %s", c.name, err, part)
			}
		}
		if c.withheld != "" && strings.Contains(err.Error(), c.withheld) {
			t.Errorf("%s: the defect %q quotes the sensitive default", c.name, err)
		}
	}
}

// Declared reports each item's domain with the rest of its metadata, so a
// surface over the settings can state the values a key takes without
// restating the declaration. An item with no domain reports the zero Domain,
// whose rule is empty.
func TestDeclaredReportsTheRuleOfEachDomain(t *testing.T) {
	store, _ := storeOf(t, boundedTable(t), newRecordingBus())
	want := map[string]string{
		"bounded.limit":   positiveRule,
		"bounded.window":  windowRule,
		"bounded.channel": channelRule,
		"bounded.token":   tokenRule,
		"bounded.note":    "",
	}
	listed := store.Declared()
	if len(listed) != len(want) {
		t.Fatalf("Declared lists %d items, want %d: %+v", len(listed), len(want), listed)
	}
	for _, item := range listed {
		rule, ok := want[item.Key]
		if !ok {
			t.Errorf("Declared lists %s, which the declaration does not carry", item.Key)
			continue
		}
		if got := item.Domain.Rule(); got != rule {
			t.Errorf("%s reports the rule %q, want %q", item.Key, got, rule)
		}
	}
}

// Item stays comparable with a Domain in it, and two Domains are equal when
// they come from one call to DomainOf: a caller that compared two Items, or
// kept one as a map key, still compiles and still means what it meant.
func TestADomainKeepsAnItemComparable(t *testing.T) {
	domain := DomainOf(positiveRule, func(v int) bool { return v > 0 })
	same, other := Item{Domain: domain}, Item{Domain: DomainOf(positiveRule, func(v int) bool { return v > 0 })}
	if (Item{Domain: domain}) != same {
		t.Error("two Items carrying one Domain compare unequal")
	}
	if same == other {
		t.Error("Items carrying Domains from two calls to DomainOf compare equal")
	}
	if (Item{}) != (Item{Domain: Domain{}}) {
		t.Error("the zero Domain is not the Domain an Item carries by default")
	}
}
