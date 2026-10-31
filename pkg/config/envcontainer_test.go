package config

import (
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// envSeedAccount is the struct-list element the cases below give as JSON: the
// shape a seeded account would have, with two members the decoder has to match
// by the name each field publishes.
type envSeedAccount struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Age   int    `json:"age"`
}

type envContainerOptions struct {
	Addr   string
	Listen []netip.Addr
	Seeds  []envSeedAccount
	Labels map[string]string
}

// envContainerLoader declares one module under the server namespace, with the
// prototype's values as the defaults and the items map naming the origins under
// test.
func envContainerLoader(t *testing.T, defaults envContainerOptions, items map[string]Item) *loader {
	t.Helper()
	return loadOver(t,
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		declaring("server", Schema{
			Namespace: "server",
			Mounts:    []Mount{{Value: &defaults}},
			Items:     items,
		}),
	)
}

func decodeEnvContainerOptions(t *testing.T, l *loader) envContainerOptions {
	t.Helper()
	var opts envContainerOptions
	if err := l.Reader().Decode("server", &opts); err != nil {
		t.Fatalf("decoding the server mount failed: %v", err)
	}
	return opts
}

// TestScalarListComesFromTheEnvironment pins the first container shape: a list
// of scalars is given as a JSON array and lands in the field whole. The element
// here is a netip.Addr, which reads itself out of a JSON string; a list of
// addresses is the shape a listener needs when no config file exists.
//
// Before the environment was a container origin this declaration failed at
// collection with ErrInvalidSchema, so the case is the end-to-end form of the
// capability.
func TestScalarListComesFromTheEnvironment(t *testing.T) {
	t.Setenv("MYAPP_SERVER__LISTEN", `["127.0.0.1", "2001:db8::1"]`)
	defaults := envContainerOptions{Listen: []netip.Addr{netip.MustParseAddr("192.0.2.1")}}
	l := envContainerLoader(t, defaults, map[string]Item{
		"listen": {Origins: OriginPrimary | OriginEnv},
	})
	got := decodeEnvContainerOptions(t, l)
	want := []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("2001:db8::1")}
	if !slices.Equal(got.Listen, want) {
		t.Fatalf("server.listen decoded to %v, want the array the variable gave, %v", got.Listen, want)
	}
}

// TestStructListComesFromTheEnvironment pins the second shape: the JSON is an
// array of objects, and each member is matched by the name its field publishes.
// A member no field answers for is refused rather than dropped, and the failure
// names the variable and the item, which are what the operator has to go and
// fix.
func TestStructListComesFromTheEnvironment(t *testing.T) {
	items := map[string]Item{"seeds": {Origins: OriginPrimary | OriginEnv}}

	t.Run("decodes every member", func(t *testing.T) {
		t.Setenv("MYAPP_SERVER__SEEDS", `[{"email":"ada@example.com","name":"Ada","age":36}]`)
		l := envContainerLoader(t, envContainerOptions{}, items)
		got := decodeEnvContainerOptions(t, l)
		want := []envSeedAccount{{Email: "ada@example.com", Name: "Ada", Age: 36}}
		if !slices.Equal(got.Seeds, want) {
			t.Fatalf("server.seeds decoded to %+v, want %+v", got.Seeds, want)
		}
	})

	t.Run("refuses an unknown member", func(t *testing.T) {
		t.Setenv("MYAPP_SERVER__SEEDS", `[{"email":"ada@example.com","nickname":"Ada"}]`)
		l := envContainerLoader(t, envContainerOptions{}, items)
		var opts envContainerOptions
		err := l.Reader().Decode("server", &opts)
		if !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("an unknown member returned %v, want ErrTypeMismatch", err)
		}
		for _, want := range []string{"MYAPP_SERVER__SEEDS", "server.seeds", "nickname"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the rejection reads %q, which does not name %q", err, want)
			}
		}
	})

	t.Run("refuses a member in the wrong case", func(t *testing.T) {
		t.Setenv("MYAPP_SERVER__SEEDS", `[{"Email":"ada@example.com"}]`)
		l := envContainerLoader(t, envContainerOptions{}, items)
		var opts envContainerOptions
		err := l.Reader().Decode("server", &opts)
		if !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("a member in the wrong case returned %v, want ErrTypeMismatch: the JSON "+
				"spelling is the one the declaration publishes, not a case-insensitive match", err)
		}
		if !strings.Contains(err.Error(), "Email") {
			t.Fatalf("the rejection reads %q, which does not name the offending member", err)
		}
	})
}

// TestMapComesFromTheEnvironment pins the third shape: a map takes its whole
// value from the variable's JSON object.
func TestMapComesFromTheEnvironment(t *testing.T) {
	t.Setenv("MYAPP_SERVER__LABELS", `{"tier":"Cold","Zone":"EU-West"}`)
	l := envContainerLoader(t, envContainerOptions{}, map[string]Item{
		"labels": {Origins: OriginPrimary | OriginEnv},
	})
	got := decodeEnvContainerOptions(t, l)
	want := map[string]string{"tier": "Cold", "Zone": "EU-West"}
	if !reflect.DeepEqual(got.Labels, want) {
		t.Fatalf("server.labels decoded to %#v, want %#v", got.Labels, want)
	}
}

// TestContainerVariableFailuresNameTheVariableAndItem pins the diagnostic of a
// value that is not JSON or does not fit the field: it says which variable was
// read and which item it was for.
func TestContainerVariableFailuresNameTheVariableAndItem(t *testing.T) {
	cases := map[string]string{
		"invalid JSON":       `["127.0.0.1",`,
		"element type fails": `["not-an-address"]`,
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("MYAPP_SERVER__LISTEN", value)
			l := envContainerLoader(t, envContainerOptions{}, map[string]Item{
				"listen": {Origins: OriginPrimary | OriginEnv},
			})
			var opts envContainerOptions
			err := l.Reader().Decode("server", &opts)
			if !errors.Is(err, ErrTypeMismatch) {
				t.Fatalf("the failure returned %v, want ErrTypeMismatch", err)
			}
			for _, want := range []string{"MYAPP_SERVER__LISTEN", "server.listen"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the failure reads %q, which does not name %q", err, want)
				}
			}
		})
	}
}

// TestEmptyContainerVariableMeansTheEmptyContainer pins the reading an empty
// variable gets for the shapes that have one: the empty list and the empty map,
// not the prototype's value left in place. Somebody who wrote the variable did
// give a value.
func TestEmptyContainerVariableMeansTheEmptyContainer(t *testing.T) {
	t.Setenv("MYAPP_SERVER__LISTEN", "")
	t.Setenv("MYAPP_SERVER__LABELS", "")
	defaults := envContainerOptions{
		Listen: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
		Labels: map[string]string{"tier": "hot"},
	}
	l := envContainerLoader(t, defaults, map[string]Item{
		"listen": {Origins: OriginPrimary | OriginEnv},
		"labels": {Origins: OriginPrimary | OriginEnv},
	})
	got := decodeEnvContainerOptions(t, l)
	if got.Listen == nil || len(got.Listen) != 0 {
		t.Fatalf("server.listen decoded to %#v, want the empty list", got.Listen)
	}
	if got.Labels == nil || len(got.Labels) != 0 {
		t.Fatalf("server.labels decoded to %#v, want the empty map", got.Labels)
	}
}

// TestEmptyContainerVariableForAStructIsRefused pins the other half of the
// empty-variable rule. A struct has no empty form, so the diagnostic says so
// instead of silently zeroing it.
//
// A bare struct field is expanded into its leaves, so no declaration produces a
// struct-valued container item; the rule is exercised at the decoder that every
// container value goes through.
func TestEmptyContainerVariableForAStructIsRefused(t *testing.T) {
	item := &manifestItem{
		module:  "server",
		path:    "server.settings",
		key:     "settings",
		item:    Item{Origins: OriginEnv},
		origins: OriginEnv,
		envName: "MYAPP_SERVER__SETTINGS",
		typ:     reflect.TypeOf(struct{ Addr string }{}),
		kind:    kindContainer,
	}
	target := reflect.New(item.typ).Elem()
	err := envContainer(item, "", target)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("an empty variable for a struct returned %v, want ErrTypeMismatch", err)
	}
	for _, want := range []string{"MYAPP_SERVER__SETTINGS", "server.settings", "struct"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the rejection reads %q, which does not say %q", err, want)
		}
	}
}

// TestSensitiveContainerFailureDoesNotRepeatTheValue pins that a sensitive
// container's diagnostic keeps the value out. The decoder's own failure quotes
// what it was given, so the wrapper has to replace it rather than pass it on.
func TestSensitiveContainerFailureDoesNotRepeatTheValue(t *testing.T) {
	t.Setenv("MYAPP_SERVER__SEEDS", `[{"email":"ada@example.com","age":"s3cr3t-age"}]`)
	l := envContainerLoader(t, envContainerOptions{}, map[string]Item{
		"seeds": {
			Origins:     OriginPrimary | OriginEnv,
			Sensitive:   true,
			Description: "the seeded accounts",
		},
	})
	var opts envContainerOptions
	err := l.Reader().Decode("server", &opts)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("the failure returned %v, want ErrTypeMismatch", err)
	}
	for _, want := range []string{"MYAPP_SERVER__SEEDS", "server.seeds"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the failure reads %q, which does not name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "s3cr3t-age") {
		t.Fatalf("the failure reads %q, and it repeats the sensitive value", err)
	}
}

// TestContainerKeysAreKeptAsTheJSONWroteThem pins that the keys of a map
// container keep their case and their punctuation. They are the user's own
// data, not names this module owns, so folding them would rewrite the
// configuration rather than normalise a name.
func TestContainerKeysAreKeptAsTheJSONWroteThem(t *testing.T) {
	t.Setenv("MYAPP_SERVER__LABELS", `{"Zone":"EU-West","TierName":"Cold","a.b c/d":"special","Ünïcode":"u"}`)
	l := envContainerLoader(t, envContainerOptions{}, map[string]Item{
		"labels": {Origins: OriginPrimary | OriginEnv},
	})
	got := decodeEnvContainerOptions(t, l)
	want := map[string]string{
		"Zone":     "EU-West",
		"TierName": "Cold",
		"a.b c/d":  "special",
		"Ünïcode":  "u",
	}
	if !reflect.DeepEqual(got.Labels, want) {
		t.Fatalf("server.labels decoded to %#v, want the keys exactly as the JSON wrote them, %#v",
			got.Labels, want)
	}
}

// TestEnvContainerReplacesTheWholeValue pins the override rule: the variable is
// the container, not a patch on one. A list of one element replaces the
// prototype's list and the primary source's list instead of merging with
// either, so a half-written variable stays half-written.
func TestEnvContainerReplacesTheWholeValue(t *testing.T) {
	t.Setenv("MYAPP_SERVER__LISTEN", `["203.0.113.1"]`)
	defaults := envContainerOptions{
		Listen: []netip.Addr{
			netip.MustParseAddr("192.0.2.1"),
			netip.MustParseAddr("192.0.2.2"),
		},
	}
	l := envContainerLoader(t, defaults, map[string]Item{
		"listen": {Origins: OriginPrimary | OriginEnv},
	})
	givePrimaryWhole(t, l, map[string]any{
		"server": map[string]any{"listen": []any{"198.51.100.1", "198.51.100.2"}},
	})
	got := decodeEnvContainerOptions(t, l)
	want := []netip.Addr{netip.MustParseAddr("203.0.113.1")}
	if !slices.Equal(got.Listen, want) {
		t.Fatalf("server.listen decoded to %v, want exactly the variable's %v: the environment "+
			"replaces the whole container rather than merging with the default or the file",
			got.Listen, want)
	}
}

// TestCommandLineOutranksTheEnvironment pins the order the layers keep: a
// command-line argument wins over the environment, which wins over the primary
// source, which wins over the defaults. The containers above cannot be given on
// the command line in this release, so the case uses a scalar of the same
// mount.
func TestCommandLineOutranksTheEnvironment(t *testing.T) {
	t.Setenv("MYAPP_SERVER__ADDR", ":9090")
	defaults := envContainerOptions{Addr: ":8080"}
	l := envContainerLoader(t, defaults, map[string]Item{
		"addr": {Origins: OriginPrimary | OriginEnv | OriginFlag, FlagName: "addr"},
	})
	if err := giveFlags(t, l, []string{"server"}, "--addr", ":7070"); err != nil {
		t.Fatalf("binding the command line failed: %v", err)
	}
	var got envContainerOptions
	if err := l.Reader().Decode("server", &got); err != nil {
		t.Fatalf("decoding the server mount failed: %v", err)
	}
	if got.Addr != ":7070" {
		t.Fatalf("server.addr decoded to %q, want the command line's :7070", got.Addr)
	}
}
