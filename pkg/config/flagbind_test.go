package config

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

type serverOptions struct {
	Addr     string
	Verbose  bool
	Retries  int
	Hosts    []string
	FileOnly string
}

func flagLoader(t *testing.T) *loader {
	t.Helper()
	defaults := serverOptions{Addr: ":8080", Retries: 3, Hosts: []string{"a"}}
	return loadOver(t, identifying("host", HostIdentity{Prefix: "MYAPP"}), declaring("server", Schema{
		Namespace: "server",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{
			"addr":    {Origins: OriginFlag | OriginEnv, FlagName: "addr", FlagShort: "a"},
			"verbose": {Origins: OriginFlag, FlagName: "verbose", FlagShort: "v"},
			"retries": {Origins: OriginFlag, FlagName: "retries"},
			"hosts":   {Origins: OriginFlag, FlagName: "hosts"},
		},
	}))
}

func parse(t *testing.T, l *loader, args ...string) serverOptions {
	t.Helper()
	if err := giveFlags(t, l, []string{"server"}, args...); err != nil {
		t.Fatalf("parsing %v failed: %v", args, err)
	}
	var opts serverOptions
	if err := l.Reader().Decode("server", &opts); err != nil {
		t.Fatalf("decoding after %v failed: %v", args, err)
	}
	return opts
}

func parseRejects(t *testing.T, l *loader, args ...string) error {
	t.Helper()
	if err := giveFlags(t, l, []string{"server"}, args...); err != nil {
		return err
	}
	var opts serverOptions
	err := l.Reader().Decode("server", &opts)
	if err == nil {
		t.Fatalf("parsing %v produced %#v, want a rejection", args, opts)
	}
	return err
}

// TestLongFlagBothForms checks that long flag both forms.
func TestLongFlagBothForms(t *testing.T) {
	for _, args := range [][]string{{"--addr=:9090"}, {"--addr", ":9090"}} {
		if got := parse(t, flagLoader(t), args...).Addr; got != ":9090" {
			t.Fatalf("%v gave server.addr %q, want :9090", args, got)
		}
	}
}

// TestShortFlagBothForms checks that short flag both forms.
func TestShortFlagBothForms(t *testing.T) {
	for _, args := range [][]string{{"-a=:9090"}, {"-a", ":9090"}} {
		if got := parse(t, flagLoader(t), args...).Addr; got != ":9090" {
			t.Fatalf("%v gave server.addr %q, want :9090", args, got)
		}
	}
}

// TestBoolFlagWithoutValue checks that bool flag without value.
func TestBoolFlagWithoutValue(t *testing.T) {
	if got := parse(t, flagLoader(t), "--verbose").Verbose; !got {
		t.Fatalf("--verbose gave %v, want true", got)
	}
	if got := parse(t, flagLoader(t), "--verbose=false").Verbose; got {
		t.Fatalf("--verbose=false gave %v, want false", got)
	}
}

// TestBoolFlagDoesNotTakeTheNextWord checks that bool flag does not take the next word.
func TestBoolFlagDoesNotTakeTheNextWord(t *testing.T) {
	opts := parse(t, flagLoader(t), "--verbose", "--addr=:9090")
	if !opts.Verbose {
		t.Fatalf("server.verbose gave %v, want true", opts.Verbose)
	}
	if opts.Addr != ":9090" {
		t.Fatalf("server.addr gave %q, and the argument after a boolean is an argument of its "+
			"own", opts.Addr)
	}
}

// TestPointerBoolOmitsItsValue checks that pointer bool omits its value.
func TestPointerBoolOmitsItsValue(t *testing.T) {
	type pointerCarrier struct {
		Colour *bool
		Addr   string
	}
	defaults := pointerCarrier{}
	l := loadOver(t, declaring("server", Schema{
		Namespace: "server",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{
			"colour": {Origins: OriginFlag, FlagName: "colour"},
			"addr":   {Origins: OriginFlag, FlagName: "addr"},
		},
	}))
	if err := giveFlags(t, l, []string{"server"}, "--colour", "--addr=:9090"); err != nil {
		t.Fatalf("parsing failed: %v", err)
	}
	var opts pointerCarrier
	if err := l.Reader().Decode("server", &opts); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if opts.Colour == nil || !*opts.Colour {
		t.Fatalf("server.colour decoded to %v, want a pointer to true", opts.Colour)
	}
	if opts.Addr != ":9090" {
		t.Fatalf("server.addr decoded to %q, and the argument after a boolean is one of its own",
			opts.Addr)
	}
}

// TestNilScalarPointerKeepsItsUnsetDefault checks that nil scalar pointer keeps its unset default.
func TestNilScalarPointerKeepsItsUnsetDefault(t *testing.T) {
	type pointerCarrier struct {
		Colour *bool
	}
	defaults := pointerCarrier{}
	l := loadOver(t, declaring("server", Schema{
		Namespace: "server",
		Mounts:    []Mount{{Value: &defaults}},
		Items:     map[string]Item{"colour": {Origins: OriginFlag, FlagName: "colour"}},
	}))
	if err := giveFlags(t, l, []string{"server"}); err != nil {
		t.Fatalf("parsing failed: %v", err)
	}
	var opts pointerCarrier
	if err := l.Reader().Decode("server", &opts); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if opts.Colour != nil {
		t.Fatalf("server.colour decoded to a pointer to %v, want nil: nobody gave it a value",
			*opts.Colour)
	}
}

// TestRepeatedFlagKeepsTheLast checks that repeated flag keeps the last.
func TestRepeatedFlagKeepsTheLast(t *testing.T) {
	if got := parse(t, flagLoader(t), "--addr=:1", "--addr=:2").Addr; got != ":2" {
		t.Fatalf("a repeated argument gave %q, want the last occurrence", got)
	}
}

// TestRepeatedListFlagAccumulates checks that repeated list flag accumulates.
func TestRepeatedListFlagAccumulates(t *testing.T) {
	want := []string{"x", "y"}
	if got := parse(t, flagLoader(t), "--hosts=x", "--hosts=y").Hosts; !reflect.DeepEqual(got, want) {
		t.Fatalf("a repeated list argument gave %#v, want %#v", got, want)
	}
}

// TestStringListFromFlagReplaces checks that string list from flag replaces.
func TestStringListFromFlagReplaces(t *testing.T) {
	want := []string{"x", "y", "z"}
	if got := parse(t, flagLoader(t), "--hosts=x,y,z").Hosts; !reflect.DeepEqual(got, want) {
		t.Fatalf("the list argument gave %#v, want %#v", got, want)
	}
}

// TestFlagOverridesEnv checks that flag overrides env.
func TestFlagOverridesEnv(t *testing.T) {
	t.Setenv("MYAPP_SERVER__ADDR", ":7000")
	if got := parse(t, flagLoader(t), "--addr=:9090").Addr; got != ":9090" {
		t.Fatalf("server.addr decoded to %q, want the command line's value over the "+
			"environment's", got)
	}
}

// TestFlagLayerRecordsProvenance checks that flag layer records provenance.
func TestFlagLayerRecordsProvenance(t *testing.T) {
	l := flagLoader(t)
	if err := giveFlags(t, l, []string{"server"}, "--addr=:9090"); err != nil {
		t.Fatalf("parsing failed: %v", err)
	}
	if !l.given(l.m.byPath["server.addr"]) {
		t.Fatal("a value the command line gave does not count as given")
	}
	if l.given(l.m.byPath["server.retries"]) {
		t.Fatal("an item the command line did not give counts as given")
	}
}

// TestFlagTypeMismatch checks that flag type mismatch.
func TestFlagTypeMismatch(t *testing.T) {
	err := parseRejects(t, flagLoader(t), "--retries=many")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("an unconvertible argument returned %v, want ErrTypeMismatch", err)
	}
	if !strings.Contains(err.Error(), "server.retries") {
		t.Fatalf("the rejection reads %q, which does not name the path", err)
	}
}

// TestBooleanFlagChecksItsOwnValue checks that boolean flag checks its own value.
func TestBooleanFlagChecksItsOwnValue(t *testing.T) {
	err := parseRejects(t, flagLoader(t), "--verbose=maybe")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("a boolean given text that is not one returned %v, want ErrTypeMismatch", err)
	}
	if !strings.Contains(err.Error(), "maybe") {
		t.Fatalf("the rejection reads %q, which does not name the value that was given", err)
	}
}

// TestArgumentEndsWithoutItsValue checks that argument ends without its value.
func TestArgumentEndsWithoutItsValue(t *testing.T) {
	err := parseRejects(t, flagLoader(t), "--addr")
	if !errors.Is(err, ErrMalformedCommandLine) {
		t.Fatalf("an argument that ends without its value returned %v, want "+
			"ErrMalformedCommandLine", err)
	}
}

// TestUnknownFlagRejected checks that unknown flag rejected.
func TestUnknownFlagRejected(t *testing.T) {
	err := parseRejects(t, flagLoader(t), "--nope=1")
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an argument no item declares returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "--nope") {
		t.Fatalf("the rejection reads %q, which does not name the argument", err)
	}
}

// TestUndeclaredSingleCharShortNameStaysUnknownKey checks that undeclared single char short name stays unknown key.
func TestUndeclaredSingleCharShortNameStaysUnknownKey(t *testing.T) {
	err := parseRejects(t, flagLoader(t), "-q")
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an undeclared short name returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "-q") {
		t.Fatalf("the rejection reads %q, which does not name the argument", err)
	}
}

// TestItemWithoutFlagOriginHasNoFlag checks that item without flag origin has no flag.
func TestItemWithoutFlagOriginHasNoFlag(t *testing.T) {
	l := flagLoader(t)
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := l.BindLayer(fs, []string{"server"}, []string{"server"}); err != nil {
		t.Fatalf("binding failed: %v", err)
	}
	if fs.Lookup("file-only") != nil {
		t.Fatal("an item that declares no command-line origin was registered as an argument")
	}
}

// TestFlagNameWithoutFlagOriginRejected checks that flag name without flag origin rejected.
func TestFlagNameWithoutFlagOriginRejected(t *testing.T) {
	defaults := serverOptions{}
	l := loadOver(t, declaring("server", Schema{
		Namespace: "server",
		Mounts:    []Mount{{Value: &defaults}},
		Items:     map[string]Item{"file-only": {Origins: OriginPrimary, FlagName: "file-only"}},
	}))
	err := giveFlags(t, l, []string{"server"}, "--file-only=x")
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an argument whose item does not take the command line returned %v, want "+
			"ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "does not accept from the command line") {
		t.Fatalf("the rejection reads %q, which does not tell this case from a name the "+
			"manifest has never heard of", err)
	}
}

// TestArgumentOfAModuleOutsideTheCommandRejected checks that argument of a module outside the command rejected.
func TestArgumentOfAModuleOutsideTheCommandRejected(t *testing.T) {
	l := flagLoader(t)
	err := giveFlags(t, l, nil, "--addr=:9090")
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an argument of a module outside the command returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "does not take that module in") {
		t.Fatalf("the rejection reads %q, which does not say the module is outside this "+
			"command", err)
	}
}

// TestOneItemIsOneArgumentOnEveryCommandThatTakesItIn checks that one item is one argument on every command that takes it in.
func TestOneItemIsOneArgumentOnEveryCommandThatTakesItIn(t *testing.T) {
	l := flagLoader(t)
	first := pflag.NewFlagSet("first", pflag.ContinueOnError)
	first.SetOutput(io.Discard)
	if err := l.BindLayer(first, []string{"server"}, []string{"server"}); err != nil {
		t.Fatalf("binding the first command failed: %v", err)
	}
	second := pflag.NewFlagSet("second", pflag.ContinueOnError)
	second.SetOutput(io.Discard)
	if err := l.BindLayer(second, []string{"server"}, []string{"server"}); err != nil {
		t.Fatalf("binding the second command failed: %v", err)
	}
	if second.Lookup("addr") == nil {
		t.Fatal("the second command carries no argument for an item of the module it takes in")
	}
	if err := second.Parse([]string{"--addr=:9090"}); err != nil {
		t.Fatalf("parsing the second command failed: %v", err)
	}
	if got := first.Lookup("addr").Value.String(); got != ":9090" {
		t.Fatalf("the first command's argument holds %q after the second was given :9090, "+
			"so the two commands carry two values for one item", got)
	}
	var opts serverOptions
	if err := l.Reader().Decode("server", &opts); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if opts.Addr != ":9090" {
		t.Fatalf("server.addr decoded to %q, want the value the second command was given", opts.Addr)
	}
}

// TestReservedConfigFlagAcceptedAndNotInData checks that reserved config flag accepted and not in data.
func TestReservedConfigFlagAcceptedAndNotInData(t *testing.T) {
	l := flagLoader(t)
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	locator := fs.String(locatorFlag, "", "the URI of the primary config source")
	if err := l.BindLayer(fs, []string{"server"}, []string{"server"}); err != nil {
		t.Fatalf("binding failed: %v", err)
	}
	if err := fs.Parse([]string{"--config=file:///a.yaml", "--addr=:9090"}); err != nil {
		t.Fatalf("parsing failed: %v", err)
	}
	if *locator != "file:///a.yaml" {
		t.Fatalf("the locator argument gave %q", *locator)
	}
	if got := l.v.Get(locatorFlag); got != nil {
		t.Fatalf("the config data carries %q as %#v, and a reserved name is not an input item",
			locatorFlag, got)
	}
}

// TestHelpGivesAListItsDeclaredPlaceholder checks that help gives a list its declared placeholder.
func TestHelpGivesAListItsDeclaredPlaceholder(t *testing.T) {
	defaults := struct{ Hosts []string }{Hosts: []string{"a", "b"}}
	l := loadOver(t, declaring("server", Schema{
		Namespace: "server",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{"hosts": {
			Origins:     OriginFlag,
			FlagName:    "host",
			Placeholder: "HOST",
			Description: "an upstream to try",
		}},
	}))
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := l.BindLayer(fs, []string{"server"}, []string{"server"}); err != nil {
		t.Fatalf("binding failed: %v", err)
	}
	usage := fs.FlagUsages()
	if !strings.Contains(usage, "--host HOST") {
		t.Fatalf("the help line reads %q, and the declared placeholder HOST is what names the value",
			strings.TrimSpace(usage))
	}
}

// TestListArgumentAccumulatesWithinOneLayer checks that list argument accumulates within one layer.
func TestListArgumentAccumulatesWithinOneLayer(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"repeated", []string{"--hosts", "x", "--hosts", "y"}, "x,y"},
		{"separated", []string{"--hosts", "x,y"}, "x,y"},
		{"replaces the declared default", []string{"--hosts", "x"}, "x"},
		{"empty text is an empty list", []string{"--hosts", ""}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := flagLoader(t)
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			fs.SetOutput(io.Discard)
			if err := l.BindLayer(fs, []string{"server"}, []string{"server"}); err != nil {
				t.Fatalf("binding failed: %v", err)
			}
			if err := fs.Parse(c.args); err != nil {
				t.Fatalf("parsing %v failed: %v", c.args, err)
			}
			if got := l.v.GetString("server.hosts"); got != c.want {
				t.Fatalf("%v gave %q, want %q", c.args, got, c.want)
			}
		})
	}
}
