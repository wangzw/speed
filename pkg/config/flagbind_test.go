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

// flagLoader declares one module whose items cover every command-line shape:
// a string with a short name, a boolean, a number, a list of strings, and an
// item that never reaches the command line at all.
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

// parse binds the server module's arguments and parses a command line, then
// decodes the mount: what an argument gave is read back the way a module
// reads it.
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

// parseRejects parses a command line that is expected to be refused, either
// by the library or by the conversion the values go through afterwards.
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

func TestLongFlagBothForms(t *testing.T) {
	for _, args := range [][]string{{"--addr=:9090"}, {"--addr", ":9090"}} {
		if got := parse(t, flagLoader(t), args...).Addr; got != ":9090" {
			t.Fatalf("%v gave server.addr %q, want :9090", args, got)
		}
	}
}

func TestShortFlagBothForms(t *testing.T) {
	for _, args := range [][]string{{"-a=:9090"}, {"-a", ":9090"}} {
		if got := parse(t, flagLoader(t), args...).Addr; got != ":9090" {
			t.Fatalf("%v gave server.addr %q, want :9090", args, got)
		}
	}
}

// TestBoolFlagWithoutValue pins that a boolean may omit its value, which is
// what the value an argument means when written without one settles.
func TestBoolFlagWithoutValue(t *testing.T) {
	if got := parse(t, flagLoader(t), "--verbose").Verbose; !got {
		t.Fatalf("--verbose gave %v, want true", got)
	}
	if got := parse(t, flagLoader(t), "--verbose=false").Verbose; got {
		t.Fatalf("--verbose=false gave %v, want false", got)
	}
}

// TestBoolFlagDoesNotTakeTheNextWord pins the other half of the same rule: the
// word after a boolean is the next argument and never its value.
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

// TestPointerBoolOmitsItsValue pins that a *bool takes the same form: it is
// the shape a module uses when "nobody set it" has to be told from "set to
// false", and reading the next word for it would swallow whatever follows.
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

// TestNilScalarPointerKeepsItsUnsetDefault pins that a leaf whose declared
// value stands for being unset stays unset when nobody gives it one. Its
// default is not in any layer, so the argument's own rendered default must not
// stand in for it.
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

// TestRepeatedFlagKeepsTheLast pins that a scalar written twice takes the last
// occurrence, as a later layer overrides an earlier one.
func TestRepeatedFlagKeepsTheLast(t *testing.T) {
	if got := parse(t, flagLoader(t), "--addr=:1", "--addr=:2").Addr; got != ":2" {
		t.Fatalf("a repeated argument gave %q, want the last occurrence", got)
	}
}

// TestRepeatedListFlagAccumulates pins the other reading a repeated argument
// has: a list written twice within one layer gives two elements. Replacement
// is what happens between layers; accumulation is how one layer writes several
// values.
func TestRepeatedListFlagAccumulates(t *testing.T) {
	want := []string{"x", "y"}
	if got := parse(t, flagLoader(t), "--hosts=x", "--hosts=y").Hosts; !reflect.DeepEqual(got, want) {
		t.Fatalf("a repeated list argument gave %#v, want %#v", got, want)
	}
}

func TestStringListFromFlagReplaces(t *testing.T) {
	want := []string{"x", "y", "z"}
	if got := parse(t, flagLoader(t), "--hosts=x,y,z").Hosts; !reflect.DeepEqual(got, want) {
		t.Fatalf("the list argument gave %#v, want %#v", got, want)
	}
}

// TestFlagOverridesEnv pins the top of the stack: the command line beats the
// environment for one and the same item.
func TestFlagOverridesEnv(t *testing.T) {
	t.Setenv("MYAPP_SERVER__ADDR", ":7000")
	if got := parse(t, flagLoader(t), "--addr=:9090").Addr; got != ":9090" {
		t.Fatalf("server.addr decoded to %q, want the command line's value over the "+
			"environment's", got)
	}
}

// TestFlagLayerRecordsProvenance pins the command-line leg of the question a
// required item asks. It only answers once the level carrying the argument has
// been bound: an item nobody registered has no argument to have changed.
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

func TestFlagTypeMismatch(t *testing.T) {
	err := parseRejects(t, flagLoader(t), "--retries=many")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("an unconvertible argument returned %v, want ErrTypeMismatch", err)
	}
	if !strings.Contains(err.Error(), "server.retries") {
		t.Fatalf("the rejection reads %q, which does not name the path", err)
	}
}

// TestBooleanFlagChecksItsOwnValue pins that a boolean argument refuses text
// that is not one. The layering tool would otherwise read --verbose=maybe as
// false and say nothing.
func TestBooleanFlagChecksItsOwnValue(t *testing.T) {
	err := parseRejects(t, flagLoader(t), "--verbose=maybe")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("a boolean given text that is not one returned %v, want ErrTypeMismatch", err)
	}
	if !strings.Contains(err.Error(), "maybe") {
		t.Fatalf("the rejection reads %q, which does not name the value that was given", err)
	}
}

// TestArgumentEndsWithoutItsValue pins the classification of a syntax error
// the command-line library reports.
func TestArgumentEndsWithoutItsValue(t *testing.T) {
	err := parseRejects(t, flagLoader(t), "--addr")
	if !errors.Is(err, ErrMalformedCommandLine) {
		t.Fatalf("an argument that ends without its value returned %v, want "+
			"ErrMalformedCommandLine", err)
	}
}

func TestUnknownFlagRejected(t *testing.T) {
	err := parseRejects(t, flagLoader(t), "--nope=1")
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an argument no item declares returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "--nope") {
		t.Fatalf("the rejection reads %q, which does not name the argument", err)
	}
}

// TestUndeclaredSingleCharShortNameStaysUnknownKey pins that a short name
// nobody declared is a name the syntax admits rather than a syntax error.
func TestUndeclaredSingleCharShortNameStaysUnknownKey(t *testing.T) {
	err := parseRejects(t, flagLoader(t), "-q")
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an undeclared short name returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "-q") {
		t.Fatalf("the rejection reads %q, which does not name the argument", err)
	}
}

// TestItemWithoutFlagOriginHasNoFlag pins that declaring no command-line
// origin keeps the item off the command line entirely.
func TestItemWithoutFlagOriginHasNoFlag(t *testing.T) {
	l := flagLoader(t)
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := l.BindLayer(fs, []string{"server"}); err != nil {
		t.Fatalf("binding failed: %v", err)
	}
	if fs.Lookup("file-only") != nil {
		t.Fatal("an item that declares no command-line origin was registered as an argument")
	}
}

// TestFlagNameWithoutFlagOriginRejected pins the diagnostic the library cannot
// give: the name is in the manifest, so the reader is told the origin is
// missing rather than that the name is unknown.
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

// TestArgumentOfAModuleOutsideTheCommandRejected pins the third reading of an
// unknown argument: the item declares the command line, and this command does
// not take its module in. The fix is a different command, which neither of the
// other two texts would lead the reader to.
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

// TestOneItemIsOneArgumentOnEveryCommandThatTakesItIn pins what a second
// command taking the same module in gets. It carries the argument, since the
// module is in its participating set and a run selects it as readily as any
// other command; and it carries the one the first command defined, since a
// second definition would hold a second value and only the one the layering
// tool is bound to is ever read.
func TestOneItemIsOneArgumentOnEveryCommandThatTakesItIn(t *testing.T) {
	l := flagLoader(t)
	first := pflag.NewFlagSet("first", pflag.ContinueOnError)
	first.SetOutput(io.Discard)
	if err := l.BindLayer(first, []string{"server"}); err != nil {
		t.Fatalf("binding the first command failed: %v", err)
	}
	second := pflag.NewFlagSet("second", pflag.ContinueOnError)
	second.SetOutput(io.Discard)
	if err := l.BindLayer(second, []string{"server"}); err != nil {
		t.Fatalf("binding the second command failed: %v", err)
	}
	if second.Lookup("addr") == nil {
		t.Fatal("the second command carries no argument for an item of the module it takes in")
	}
	if first.Lookup("addr") != second.Lookup("addr") {
		t.Fatal("the two commands carry two arguments for one item")
	}
	if err := second.Parse([]string{"--addr=:9090"}); err != nil {
		t.Fatalf("parsing the second command failed: %v", err)
	}
	var opts serverOptions
	if err := l.Reader().Decode("server", &opts); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if opts.Addr != ":9090" {
		t.Fatalf("server.addr decoded to %q, want the value the second command was given", opts.Addr)
	}
}

// TestReservedConfigFlagAcceptedAndNotInData pins that the config locator is
// an argument the program takes and not an input item: it selects the primary
// source and never reaches the config data, where the unknown-key check would
// trip over this module's own name.
func TestReservedConfigFlagAcceptedAndNotInData(t *testing.T) {
	l := flagLoader(t)
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	locator := fs.String(locatorFlag, "", "the URI of the primary config source")
	if err := l.BindLayer(fs, []string{"server"}); err != nil {
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

// TestHelpGivesAListItsDeclaredPlaceholder pins that a list of strings shows
// the placeholder its declaration chose. Taking the library's own list
// argument names the argument after that type instead, so the one word the
// reader is shown would be the library's rather than the declaration's.
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
	if err := l.BindLayer(fs, []string{"server"}); err != nil {
		t.Fatalf("binding failed: %v", err)
	}
	usage := fs.FlagUsages()
	if !strings.Contains(usage, "--host HOST") {
		t.Fatalf("the help line reads %q, and the declared placeholder HOST is what names the value",
			strings.TrimSpace(usage))
	}
}

// TestListArgumentAccumulatesWithinOneLayer pins that a repeated argument adds
// to what came before it. Giving several elements in one layer is written
// either way, and the separated form and the repeated form reach the same
// list.
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
			if err := l.BindLayer(fs, []string{"server"}); err != nil {
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
