package config

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/vislake/speed/pkg/core"
)

// helpLevel prints a word for the number it stores, which is a form no source
// takes back. It is here so the default column can be pinned to the number.
type helpLevel int

func (l helpLevel) String() string { return "debug" }

// helpLabel carries its own text form, and that form holds a space while the
// type is neither a string nor a list of strings. It is the shape the quoting
// rule has to reach: a rule reading the field's Go kind leaves this one bare,
// and the reader who copies the line back hands the program two arguments.
type helpLabel struct{ name string }

func (l helpLabel) MarshalText() ([]byte, error) { return []byte(l.name), nil }

func (l *helpLabel) UnmarshalText(text []byte) error {
	l.name = string(text)
	return nil
}

// helpPercent reads itself from text but writes none: it holds 50 and takes
// "50%" back. It is the shape a leaf has when its stored value and its own
// input form are not the same text.
type helpPercent int

func (p *helpPercent) UnmarshalText(text []byte) error {
	digits, ok := strings.CutSuffix(string(text), "%")
	if !ok {
		return fmt.Errorf("a percentage is written as in \"50%%\"")
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return err
	}
	*p = helpPercent(n)
	return nil
}

func (p helpPercent) String() string { return strconv.Itoa(int(p)) + "%" }

// defaultsCorpus carries one field of every shape the default column has to
// render, so one corpus covers them all: the plain scalars, a named integer
// that prints a word of its own, a duration, three types that carry their own
// text form, one that reads text without writing any, both states of a scalar
// pointer, a sensitive unset one, and a list in both of its states.
//
// The texts are chosen for the quoting rule as much as for the types: a value
// needing no quotes, one carrying a space behind a text form, one carrying the
// characters a shell acts on, one carrying a quote of its own, one carrying a
// character with no typeable form at all, and one opening with a dash.
type defaultsCorpus struct {
	Text    string
	Spaced  string
	Tricky  string
	Quoted  string
	Control string
	Offset  int
	Label   helpLabel
	Flag    bool
	Count   int
	Level   helpLevel
	Ratio   float32
	TTL     time.Duration
	Moment  time.Time
	Amount  big.Int
	Share   helpPercent
	Held    *int
	Missing *int
	Secret  *string
	Hosts   []string
	NoHosts []string
}

// defaultsCorpusLoader collects the corpus into a loader. A fresh one is built
// per case, so a command line given to one does not reach another.
func defaultsCorpusLoader(t *testing.T) *loader {
	t.Helper()
	held := 7
	defaults := defaultsCorpus{
		Text:    "Hello",
		Spaced:  "Hello there",
		Tricky:  `a$b;c\d*e`,
		Quoted:  "it's",
		Control: "a\tb",
		Offset:  -42,
		Label:   helpLabel{name: "west wing"},
		Flag:    true,
		Count:   8080,
		Level:   3,
		Ratio:   0.1,
		TTL:     5 * time.Minute,
		Moment:  time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC),
		Amount:  *big.NewInt(42),
		Share:   50,
		Held:    &held,
		Hosts:   []string{"a", "b"},
	}
	items := make(map[string]Item, 20)
	for _, key := range []string{
		"text", "spaced", "tricky", "quoted", "control", "offset", "label",
		"flag", "count", "level", "ratio", "ttl", "moment",
		"amount", "share", "held", "missing", "no-hosts", "hosts",
	} {
		items[key] = Item{Origins: OriginFlag, FlagName: key}
	}
	items["secret"] = Item{
		Origins:     OriginFlag,
		FlagName:    "secret",
		Sensitive:   true,
		Description: "the credential the greeter presents",
	}
	return loadOver(t, declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &defaults}},
		Items:     items,
	}))
}

// corpusItem picks one item of the corpus out by its command-line name.
func corpusItem(t *testing.T, l *loader, name string) *manifestItem {
	t.Helper()
	item, declared := l.m.byFlag[name]
	if !declared {
		t.Fatalf("the corpus declares no item named --%s", name)
	}
	return item
}

// corpusDefault renders the default of one item of the corpus.
func corpusDefault(t *testing.T, name string) string {
	t.Helper()
	l := defaultsCorpusLoader(t)
	return formatDefault(corpusItem(t, l, name))
}

// helpFlags registers the arguments of one module and returns the flag set
// holding them, which is what the command-line library renders the help
// output out of. Registration happens once per loader - a deeper level passes
// over what a shallower one already registered - so one call per loader
// answers both what the library was handed and what it prints.
func helpFlags(t *testing.T, l *loader, module string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	if err := l.BindLayer(fs, []string{module}, []string{module}); err != nil {
		t.Fatalf("registering the arguments of %q failed: %v", module, err)
	}
	return fs
}

// sameDefault compares what the command line produced with the declared
// default. Lists are compared element by element: a nil list and an empty one
// render the same flat text, so what comes back can only match one of them.
func sameDefault(got, want any) bool {
	gv, wv := reflect.ValueOf(got), reflect.ValueOf(want)
	if gv.Kind() == reflect.Slice && wv.Kind() == reflect.Slice {
		if gv.Len() != wv.Len() {
			return false
		}
		for i := range gv.Len() {
			if gv.Index(i).Interface() != wv.Index(i).Interface() {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(got, want)
}

// TestHelpPointerDefaultShowsThePointee pins that a scalar pointer renders
// what it points at. An address is a number that changes every run and names
// no value the reader could give.
func TestHelpPointerDefaultShowsThePointee(t *testing.T) {
	if got := corpusDefault(t, "held"); got != "7" {
		t.Fatalf("the pointer default renders as %q, and its value is 7", got)
	}
}

// TestHelpTextDefaultUsesTheTypesOwnTextForm pins that a leaf which reads
// itself from a string renders in that same string form. Both of these carry
// their text methods on the pointer receiver, which is the shape a value held
// in an interface hides.
func TestHelpTextDefaultUsesTheTypesOwnTextForm(t *testing.T) {
	if got := corpusDefault(t, "moment"); got != "2026-09-13T10:00:00Z" {
		t.Fatalf("the time default renders as %q, and its own text form is RFC 3339", got)
	}
	if got := corpusDefault(t, "amount"); got != "42" {
		t.Fatalf("the integer default renders as %q, and its value is 42", got)
	}
}

// TestHelpNumericDefaultIgnoresAStringerForm pins that a number renders as a
// number. A type printing a word for its value says what the value means, not
// what a source would have to give to produce it.
func TestHelpNumericDefaultIgnoresAStringerForm(t *testing.T) {
	if got := corpusDefault(t, "level"); got != "3" {
		t.Fatalf("the named integer renders as %q, and its value is 3", got)
	}
}

// TestHelpFloatDefaultKeepsItsPrecision pins that a 32-bit float renders at
// its own width. Widened to 64 bits it grows a tail of digits that is not what
// the module declared.
func TestHelpFloatDefaultKeepsItsPrecision(t *testing.T) {
	if got := corpusDefault(t, "ratio"); got != "0.1" {
		t.Fatalf("the float default renders as %q, and its value is 0.1", got)
	}
}

// TestHelpDurationDefaultIsWrittenAsDuration pins the duration form, which is
// also the only form the parser takes: a bare nanosecond count is refused on
// the way in and would advertise a value that cannot be given.
func TestHelpDurationDefaultIsWrittenAsDuration(t *testing.T) {
	if got := corpusDefault(t, "ttl"); got != "5m0s" {
		t.Fatalf("the duration default renders as %q, and it is five minutes", got)
	}
}

// TestHelpStringListDefaultIsCommaSeparated pins the flat form of a list,
// which is the one the command line and the environment give. It is pinned on
// what the command-line library is handed, not on the rendering alone: a
// second renderer for lists is how the form and the quoting rule come apart.
func TestHelpStringListDefaultIsCommaSeparated(t *testing.T) {
	l := defaultsCorpusLoader(t)
	if got := flagDefaultString(corpusItem(t, l, "hosts")); got != "a,b" {
		t.Fatalf("the list default is handed over as %q, and its flat form is a,b", got)
	}
}

// TestHelpPrintsNoDefaultForAListNobodyGaveAValue pins what a list whose field
// was left alone hands over. An empty list is the zero of its type, and the
// rule that omits every other zero omits this one too: a rendered stand-in
// reads as a value somebody chose.
func TestHelpPrintsNoDefaultForAListNobodyGaveAValue(t *testing.T) {
	var defaults struct{ Hosts []string }
	l := loadOver(t, declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &defaults}},
		Items:     map[string]Item{"hosts": {Origins: OriginFlag, FlagName: "hosts"}},
	}))
	if got := flagDefaultString(corpusItem(t, l, "hosts")); got != "" {
		t.Fatalf("the list nobody gave a value hands over %q, and it has none to hand over", got)
	}
	if usage := helpFlags(t, l, "greeter").FlagUsages(); strings.Contains(usage, "default") {
		t.Fatalf("the help line reads %q, and there is no default to print",
			strings.TrimSpace(usage))
	}
}

// TestHelpQuotesAStringDefaultOnlyWhenItsTextNeedsIt pins that a string is not
// quoted for being a string. The quotes follow the text: a word a command line
// takes as it stands is rendered as it stands, and one the shell would split
// or rewrite is wrapped so the reader copies the whole of it.
func TestHelpQuotesAStringDefaultOnlyWhenItsTextNeedsIt(t *testing.T) {
	for name, want := range map[string]string{
		"text":   "Hello",
		"spaced": `'Hello there'`,
		"quoted": `'it'\''s'`,
	} {
		if got := corpusDefault(t, name); got != want {
			t.Errorf("--%s renders %s, and its text asks for %s", name, got, want)
		}
	}
}

// TestHelpKeepsTheResidualFormForATextTypeWithoutAnOutputForm pins what a leaf
// gets when it reads itself from text but writes none: Go's own printed form.
// That form is not one the parser is known to take back, which is why this
// argument stays out of the round trip; the alternatives - the number it
// stores, or a spelling made up here - would both be untrue.
func TestHelpKeepsTheResidualFormForATextTypeWithoutAnOutputForm(t *testing.T) {
	if got := corpusDefault(t, "share"); got != "50%" {
		t.Fatalf("the text-reading type renders as %q, and its printed form is 50%%", got)
	}
}

// TestHelpPrintsNoDefaultForAnUnsetLeaf pins what the command-line library is
// handed for a leaf whose value stands for being unset. A nil scalar pointer
// is how a module tells "nobody gave it" from "it was given the zero value",
// so neither the zero nor Go's <nil> is the truth about it, and a rendered
// stand-in would become the value every silent layer falls back on.
func TestHelpPrintsNoDefaultForAnUnsetLeaf(t *testing.T) {
	l := defaultsCorpusLoader(t)
	if got := flagDefaultString(corpusItem(t, l, "missing")); got != "" {
		t.Fatalf("the unset leaf hands over %q, and it has no default to hand over", got)
	}
}

// TestHelpPrintsNoDefaultForAnUnsetSensitiveLeaf pins that the two rules do
// not cancel each other out: an item that is both still hands over nothing.
func TestHelpPrintsNoDefaultForAnUnsetSensitiveLeaf(t *testing.T) {
	l := defaultsCorpusLoader(t)
	if got := flagDefaultString(corpusItem(t, l, "secret")); got != "" {
		t.Fatalf("the unset sensitive leaf hands over %q", got)
	}
}

// TestHelpOmitsSensitiveDefault pins the one thing this module decides about a
// sensitive item: the command-line library prints what it receives, so the
// real default is never handed to it and reaches the run through the default
// layer alone.
func TestHelpOmitsSensitiveDefault(t *testing.T) {
	defaults := struct{ Token string }{Token: "s3cr3t"}
	l := loadOver(t, declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{"token": {
			Origins:     OriginFlag,
			FlagName:    "remote-token",
			Sensitive:   true,
			Description: "the credential of the remote greeter",
		}},
	}))
	item := corpusItem(t, l, "remote-token")
	if got := flagDefaultString(item); got != "" {
		t.Fatalf("the sensitive item hands over %q, and a credential in the help output is a leak", got)
	}
	if got := l.v.Get(item.path); got != "s3cr3t" {
		t.Fatalf("the default layer holds %#v, and the real default still has to reach the run", got)
	}
}

// TestHelpGivesEitherBooleanNoPlaceholder pins that the placeholder looks
// through the pointer exactly as the argument form does. Offering a value for
// --trace would advertise a spelling written without one.
func TestHelpGivesEitherBooleanNoPlaceholder(t *testing.T) {
	var defaults struct {
		Verbose bool
		Trace   *bool
	}
	l := loadOver(t, declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{
			"verbose": {Origins: OriginFlag, FlagName: "verbose", Description: "say more"},
			"trace":   {Origins: OriginFlag, FlagName: "trace", Description: "record the calls"},
		},
	}))
	for _, name := range []string{"verbose", "trace"} {
		if got := placeholder(corpusItem(t, l, name)); got != "" {
			t.Fatalf("--%s takes the placeholder %q, and a boolean is written without a value",
				name, got)
		}
	}
}

// TestPlaceholderCollidingWithAValueTypeNameRejected pins the one placeholder
// a declaration may not choose. The placeholder is what the layering tool
// reads a changed argument by, and one of its own type names would have it
// convert the text itself and swallow whatever failed.
func TestPlaceholderCollidingWithAValueTypeNameRejected(t *testing.T) {
	defaults := struct{ Count string }{}
	reg := core.New()
	reg.Register(declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{"count": {
			Origins: OriginFlag, FlagName: "count", Placeholder: "int",
		}},
	}))
	_, err := newManifest(reg)
	if !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("a placeholder spelled as one of the layering tool's type names returned %v, "+
			"want ErrInvalidSchema", err)
	}
	if !strings.Contains(err.Error(), "int") {
		t.Fatalf("the rejection reads %q, which does not name the placeholder", err)
	}
}

// TestHelpOmitsTheDefaultOfARequiredItem pins that an item nobody may omit
// hands over no default. The zero of a string rendered in the form it would be
// typed back is ”, and the library prints that as a default the item does not
// have: the reader takes "leave it out and you get the empty string" from a
// line that means the opposite.
func TestHelpOmitsTheDefaultOfARequiredItem(t *testing.T) {
	defaults := struct{ Addr string }{}
	l := loadOver(t, declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{"addr": {
			Origins:     OriginFlag,
			FlagName:    "remote-addr",
			Placeholder: "HOST:PORT",
			Required:    true,
			Description: "the address of the greeting service",
		}},
	}))
	if got := flagDefaultString(corpusItem(t, l, "remote-addr")); got != "" {
		t.Fatalf("the required item hands over %q, and an item nobody may omit has no default", got)
	}
	if usage := helpFlags(t, l, "greeter").FlagUsages(); strings.Contains(usage, "default") {
		t.Fatalf("the help line reads %q, and a required item has no default to print",
			strings.TrimSpace(usage))
	}
}

// TestHelpMarksARequiredItem pins that the help output tells an argument
// nobody may omit from an optional one. The default column is what separates
// them everywhere else, and a required item leaves that column empty, so the
// mark rides in the usage text the command-line library prints as it stands.
// Without it the reader is left to guess which arguments the run needs.
func TestHelpMarksARequiredItem(t *testing.T) {
	defaults := struct {
		Addr string
		Port int
	}{Port: 8080}
	l := loadOver(t, declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{
			"addr": {
				Origins:     OriginFlag,
				FlagName:    "remote-addr",
				Placeholder: "HOST:PORT",
				Required:    true,
				Description: "the address of the greeting service",
			},
			"port": {
				Origins:     OriginFlag,
				FlagName:    "port",
				Description: "the port the greeter listens on",
			},
		},
	}))
	usage := helpFlags(t, l, "greeter").FlagUsages()
	if !strings.Contains(usage, "the address of the greeting service (required)") {
		t.Errorf("the help output reads\n%s\nand a required argument says it is one", usage)
	}
	if strings.Contains(usage, "the port the greeter listens on (required)") {
		t.Errorf("the help output reads\n%s\nand an optional argument carries no mark", usage)
	}
}

// TestHelpDoesNotMarkACollectedModulesArgumentAsRequired pins that the mark
// follows the module a run asks for its configuration rather than the item
// that declares itself required.
//
// A module taken in for its declarations alone is never constructed, so it
// never decodes its configuration and no run comes up short of the value. The
// marker says a run will not start without it, and it is printed on the one
// page a reader uses to decide exactly that.
func TestHelpDoesNotMarkACollectedModulesArgumentAsRequired(t *testing.T) {
	defaults := struct{ Addr string }{}
	l := loadOver(t, declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{"addr": {
			Origins:     OriginFlag,
			FlagName:    "remote-addr",
			Description: "the address of the greeting service",
			Required:    true,
		}},
	}))

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	// Named among the modules taking part and not among the ones running:
	// it contributes the argument and nothing here runs it.
	if err := l.BindLayer(fs, []string{"greeter"}, nil); err != nil {
		t.Fatalf("registering the arguments failed: %v", err)
	}
	usage := fs.FlagUsages()
	if !strings.Contains(usage, "the address of the greeting service") {
		t.Errorf("the help output reads\n%s\nand the argument is not there at all", usage)
	}
	if strings.Contains(usage, "(required)") {
		t.Errorf("the help output reads\n%s\nand marks required an argument of a module this "+
			"run never constructs", usage)
	}
}

// TestHelpMarksARequiredItemWithoutADescription pins that the mark stands on
// its own when the item declares no description. It is the whole of what the
// line says about the argument, so dropping it would leave the line blank.
func TestHelpMarksARequiredItemWithoutADescription(t *testing.T) {
	defaults := struct{ Addr string }{}
	l := loadOver(t, declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{"addr": {
			Origins: OriginFlag, FlagName: "remote-addr", Placeholder: "HOST:PORT", Required: true,
		}},
	}))
	if usage := helpFlags(t, l, "greeter").FlagUsages(); !strings.Contains(usage, "(required)") {
		t.Errorf("the help output reads\n%s\nand a required argument says it is one", usage)
	}
}

// TestHelpOmitsADefaultThatIsTheZeroValue pins that a zero default is left
// out, which is the test the library applies to its own types. A rendered zero
// reads as a value somebody chose.
func TestHelpOmitsADefaultThatIsTheZeroValue(t *testing.T) {
	defaults := struct {
		Addr    string
		Retries int
	}{}
	l := loadOver(t, declaring("server", Schema{
		Namespace: "server",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{
			"addr":    {Origins: OriginFlag, FlagName: "addr"},
			"retries": {Origins: OriginFlag, FlagName: "retries"},
		},
	}))
	for _, name := range []string{"addr", "retries"} {
		if got := flagDefaultString(corpusItem(t, l, name)); got != "" {
			t.Fatalf("--%s hands over %q for a zero default", name, got)
		}
	}
}
