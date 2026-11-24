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

type helpLevel int

func (l helpLevel) String() string { return "debug" }

type helpLabel struct{ name string }

func (l helpLabel) MarshalText() ([]byte, error) { return []byte(l.name), nil }

func (l *helpLabel) UnmarshalText(text []byte) error {
	l.name = string(text)
	return nil
}

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

func corpusItem(t *testing.T, l *loader, name string) *manifestItem {
	t.Helper()
	item, declared := l.m.byFlag[name]
	if !declared {
		t.Fatalf("the corpus declares no item named --%s", name)
	}
	return item
}

func corpusDefault(t *testing.T, name string) string {
	t.Helper()
	l := defaultsCorpusLoader(t)
	return formatDefault(corpusItem(t, l, name))
}

func helpFlags(t *testing.T, l *loader, module string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	if err := l.BindLayer(fs, []string{module}, []string{module}); err != nil {
		t.Fatalf("registering the arguments of %q failed: %v", module, err)
	}
	return fs
}

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

// TestHelpPointerDefaultShowsThePointee checks that help pointer default shows the pointee.
func TestHelpPointerDefaultShowsThePointee(t *testing.T) {
	if got := corpusDefault(t, "held"); got != "7" {
		t.Fatalf("the pointer default renders as %q, and its value is 7", got)
	}
}

// TestHelpTextDefaultUsesTheTypesOwnTextForm checks that help text default uses the types own text form.
func TestHelpTextDefaultUsesTheTypesOwnTextForm(t *testing.T) {
	if got := corpusDefault(t, "moment"); got != "2026-09-13T10:00:00Z" {
		t.Fatalf("the time default renders as %q, and its own text form is RFC 3339", got)
	}
	if got := corpusDefault(t, "amount"); got != "42" {
		t.Fatalf("the integer default renders as %q, and its value is 42", got)
	}
}

// TestHelpNumericDefaultIgnoresAStringerForm checks that help numeric default ignores a stringer form.
func TestHelpNumericDefaultIgnoresAStringerForm(t *testing.T) {
	if got := corpusDefault(t, "level"); got != "3" {
		t.Fatalf("the named integer renders as %q, and its value is 3", got)
	}
}

// TestHelpFloatDefaultKeepsItsPrecision checks that help float default keeps its precision.
func TestHelpFloatDefaultKeepsItsPrecision(t *testing.T) {
	if got := corpusDefault(t, "ratio"); got != "0.1" {
		t.Fatalf("the float default renders as %q, and its value is 0.1", got)
	}
}

// TestHelpDurationDefaultIsWrittenAsDuration checks that help duration default is written as duration.
func TestHelpDurationDefaultIsWrittenAsDuration(t *testing.T) {
	if got := corpusDefault(t, "ttl"); got != "5m0s" {
		t.Fatalf("the duration default renders as %q, and it is five minutes", got)
	}
}

// TestHelpStringListDefaultIsCommaSeparated checks that help string list default is comma separated.
func TestHelpStringListDefaultIsCommaSeparated(t *testing.T) {
	l := defaultsCorpusLoader(t)
	if got := flagDefaultString(corpusItem(t, l, "hosts")); got != "a,b" {
		t.Fatalf("the list default is handed over as %q, and its flat form is a,b", got)
	}
}

// TestHelpPrintsNoDefaultForAListNobodyGaveAValue checks that help prints no default for a list nobody gave a value.
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

// TestHelpQuotesAStringDefaultOnlyWhenItsTextNeedsIt checks that help quotes a string default only when its text needs it.
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

// TestHelpKeepsTheResidualFormForATextTypeWithoutAnOutputForm checks that help keeps the residual form for a text type without an output form.
func TestHelpKeepsTheResidualFormForATextTypeWithoutAnOutputForm(t *testing.T) {
	if got := corpusDefault(t, "share"); got != "50%" {
		t.Fatalf("the text-reading type renders as %q, and its printed form is 50%%", got)
	}
}

// TestHelpPrintsNoDefaultForAnUnsetLeaf checks that help prints no default for an unset leaf.
func TestHelpPrintsNoDefaultForAnUnsetLeaf(t *testing.T) {
	l := defaultsCorpusLoader(t)
	if got := flagDefaultString(corpusItem(t, l, "missing")); got != "" {
		t.Fatalf("the unset leaf hands over %q, and it has no default to hand over", got)
	}
}

// TestHelpPrintsNoDefaultForAnUnsetSensitiveLeaf checks that help prints no default for an unset sensitive leaf.
func TestHelpPrintsNoDefaultForAnUnsetSensitiveLeaf(t *testing.T) {
	l := defaultsCorpusLoader(t)
	if got := flagDefaultString(corpusItem(t, l, "secret")); got != "" {
		t.Fatalf("the unset sensitive leaf hands over %q", got)
	}
}

// TestHelpOmitsSensitiveDefault checks that help omits sensitive default.
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

// TestHelpGivesEitherBooleanNoPlaceholder checks that help gives either boolean no placeholder.
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

// TestPlaceholderCollidingWithAValueTypeNameRejected checks that placeholder colliding with a value type name rejected.
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

// TestHelpOmitsTheDefaultOfARequiredItem checks that help omits the default of a required item.
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

// TestHelpRendersTheDocumentDefaultOfARequiredItem checks that help renders the document default of a required item.
func TestHelpRendersTheDocumentDefaultOfARequiredItem(t *testing.T) {
	defaults := struct{ Addr string }{}
	l := loadOver(t,
		providing("format.yaml", yamlParser(map[string]any{
			"greeter": map[string]any{"addr": "defaults:1"},
		})),
		declaring("greeter", Schema{
			Namespace: "greeter",
			Mounts:    []Mount{{Value: &defaults}},
			Items: map[string]Item{"addr": {
				Origins:     OriginPrimary | OriginFlag,
				FlagName:    "remote-addr",
				Placeholder: "HOST:PORT",
				Required:    true,
				Description: "the address of the greeting service",
			}},
		}),
		withDefaults("yaml", "ignored"),
	)
	if got := flagDefaultString(corpusItem(t, l, "remote-addr")); got != "defaults:1" {
		t.Fatalf("the required item the document supplies hands over %q, want the document's value",
			got)
	}
	usage := helpFlags(t, l, "greeter").FlagUsages()
	if strings.Contains(usage, "(required)") {
		t.Errorf("the help output reads\n%s\nand marks required an argument whose omission starts "+
			"the run", usage)
	}
}

// TestHelpMarksARequiredItem checks that help marks a required item.
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

// TestHelpDoesNotMarkACollectedModulesArgumentAsRequired checks that help does not mark a collected modules argument as required.
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

// TestHelpMarksARequiredItemWithoutADescription checks that help marks a required item without a description.
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

// TestHelpOmitsADefaultThatIsTheZeroValue checks that help omits a default that is the zero value.
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
