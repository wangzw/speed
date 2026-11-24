package config

import (
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type envSeedAccount struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Age   int    `json:"age"`
	// Big exists to show that a number is carried at the width it was written
	// with: a float64 would have rounded this one before the field saw it.
	Big int64 `json:"big"`
}

type envContainerOptions struct {
	Addr     string
	Listen   []netip.Addr
	Seeds    []envSeedAccount
	Labels   map[string]string
	Numbered map[int]string
}

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

// TestScalarListComesFromTheEnvironment checks that scalar list comes from the environment.
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

// TestStructListComesFromTheEnvironment checks that struct list comes from the environment.
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

// TestMapComesFromTheEnvironment checks that map comes from the environment.
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

// TestContainerVariableFailuresNameTheVariableAndItem checks that container variable failures name the variable and item.
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

// TestEmptyContainerVariableMeansTheEmptyContainer checks that empty container variable means the empty container.
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

// TestEmptyContainerVariableForAStructIsRefused checks that empty container variable for a struct is refused.
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

// TestSensitiveContainerFailureDoesNotRepeatTheValue checks that sensitive container failure does not repeat the value.
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

// TestContainerKeysAreKeptAsTheJSONWroteThem checks that container keys are kept as the JSON wrote them.
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

// TestEnvContainerReplacesTheWholeValue checks that env container replaces the whole value.
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

// TestContainerJSONRejectsARepeatedKey checks that container JSON rejects a repeated key.
func TestContainerJSONRejectsARepeatedKey(t *testing.T) {
	t.Setenv("MYAPP_SERVER__LABELS", `{"tier":"cold","tier":"hot"}`)
	l := envContainerLoader(t, envContainerOptions{}, map[string]Item{
		"labels": {Origins: OriginPrimary | OriginEnv},
	})
	var opts envContainerOptions
	err := l.Reader().Decode("server", &opts)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("a repeated key returned %v, want ErrTypeMismatch", err)
	}
	for _, want := range []string{"MYAPP_SERVER__LABELS", "server.labels", "tier"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the rejection reads %q, which does not name %q", err, want)
		}
	}
}

// TestContainerJSONRejectsNull checks that container JSON rejects null.
func TestContainerJSONRejectsNull(t *testing.T) {
	cases := map[string]struct {
		variable string
		item     string
		value    string
	}{
		"a bare null":    {"MYAPP_SERVER__LISTEN", "listen", `null`},
		"a list element": {"MYAPP_SERVER__LISTEN", "listen", `["127.0.0.1",null]`},
		"a member value": {"MYAPP_SERVER__SEEDS", "seeds", `[{"email":null}]`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(c.variable, c.value)
			l := envContainerLoader(t, envContainerOptions{}, map[string]Item{
				c.item: {Origins: OriginPrimary | OriginEnv},
			})
			var opts envContainerOptions
			err := l.Reader().Decode("server", &opts)
			if !errors.Is(err, ErrTypeMismatch) {
				t.Fatalf("a null returned %v, want ErrTypeMismatch", err)
			}
			for _, want := range []string{c.variable, "server." + c.item, "null"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the rejection reads %q, which does not say %q", err, want)
				}
			}
		})
	}
}

// TestContainerJSONRejectsWhitespaceOnly checks that container JSON rejects whitespace only.
func TestContainerJSONRejectsWhitespaceOnly(t *testing.T) {
	t.Setenv("MYAPP_SERVER__LISTEN", " \n\t ")
	l := envContainerLoader(t, envContainerOptions{}, map[string]Item{
		"listen": {Origins: OriginPrimary | OriginEnv},
	})
	var opts envContainerOptions
	err := l.Reader().Decode("server", &opts)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("whitespace alone returned %v, want ErrTypeMismatch", err)
	}
	if !strings.Contains(err.Error(), "whitespace") {
		t.Fatalf("the rejection reads %q, which does not say the value holds no JSON", err)
	}
}

// TestContainerJSONRejectsContentAfterTheValue checks that container JSON rejects content after the value.
func TestContainerJSONRejectsContentAfterTheValue(t *testing.T) {
	t.Setenv("MYAPP_SERVER__LISTEN", `["127.0.0.1"] ["192.0.2.1"]`)
	l := envContainerLoader(t, envContainerOptions{}, map[string]Item{
		"listen": {Origins: OriginPrimary | OriginEnv},
	})
	var opts envContainerOptions
	err := l.Reader().Decode("server", &opts)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("trailing content returned %v, want ErrTypeMismatch", err)
	}
	if !strings.Contains(err.Error(), "goes on") {
		t.Fatalf("the rejection reads %q, which does not say the data goes on", err)
	}
}

// TestContainerJSONShapeMustFitTheItem checks that container JSON shape must fit the item.
func TestContainerJSONShapeMustFitTheItem(t *testing.T) {
	t.Setenv("MYAPP_SERVER__LISTEN", `{"addr":"127.0.0.1"}`)
	l := envContainerLoader(t, envContainerOptions{}, map[string]Item{
		"listen": {Origins: OriginPrimary | OriginEnv},
	})
	var opts envContainerOptions
	err := l.Reader().Decode("server", &opts)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("an object for a list returned %v, want ErrTypeMismatch", err)
	}
	if !strings.Contains(err.Error(), "server.listen") {
		t.Fatalf("the rejection reads %q, which does not name the item", err)
	}
}

// TestContainerJSONRejectsANonStringMapKey checks that container JSON rejects a non string map key.
func TestContainerJSONRejectsANonStringMapKey(t *testing.T) {
	t.Setenv("MYAPP_SERVER__NUMBERED", `{"1":"one"}`)
	l := envContainerLoader(t, envContainerOptions{}, map[string]Item{
		"numbered": {Origins: OriginPrimary | OriginEnv},
	})
	var opts envContainerOptions
	err := l.Reader().Decode("server", &opts)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("a map with int keys returned %v, want ErrTypeMismatch", err)
	}
	for _, want := range []string{"MYAPP_SERVER__NUMBERED", "server.numbered", "int"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the rejection reads %q, which does not name %q", err, want)
		}
	}
}

// TestContainerJSONKeepsANumberAtItsWrittenWidth checks that container JSON keeps a number at its written width.
func TestContainerJSONKeepsANumberAtItsWrittenWidth(t *testing.T) {
	t.Setenv("MYAPP_SERVER__SEEDS", `[{"email":"ada@example.com","big":9007199254740993}]`)
	l := envContainerLoader(t, envContainerOptions{}, map[string]Item{
		"seeds": {Origins: OriginPrimary | OriginEnv},
	})
	got := decodeEnvContainerOptions(t, l)
	if len(got.Seeds) != 1 {
		t.Fatalf("server.seeds decoded to %+v, want one element", got.Seeds)
	}
	if want := int64(9007199254740993); got.Seeds[0].Big != want {
		t.Fatalf("server.seeds[0].big decoded to %d, want %d: the number is carried at the "+
			"width it was written with", got.Seeds[0].Big, want)
	}
}

// TestCommandLineOutranksTheEnvironment checks that command line outranks the environment.
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
