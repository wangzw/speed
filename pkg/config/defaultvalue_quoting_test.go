package config

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func shellWords(t *testing.T, rendered string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the quoting rule targets a POSIX shell, and Windows has none at /bin/sh")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("this machine has no /bin/sh to put the rendered default through: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aXb"), nil, 0o600); err != nil {
		t.Fatalf("seeding the file a pattern would match failed: %v", err)
	}
	cmd := exec.Command("/bin/sh", "-c", `printf '%s\0' `+rendered)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the shell refused the command line carrying the rendered default %s: %v\n%s",
			rendered, err, stderr.String())
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

func shellWord(t *testing.T, rendered string) string {
	t.Helper()
	words := shellWords(t, rendered)
	if len(words) != 1 {
		t.Fatalf("the rendered default %s reaches the program as %d words %q, and a default "+
			"is one value", rendered, len(words), words)
	}
	return words[0]
}

func labelLoader(t *testing.T, value string) *loader {
	t.Helper()
	type labelOnly struct{ Label helpLabel }
	defaults := labelOnly{Label: helpLabel{name: value}}
	return loadOver(t, declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &defaults}},
		Items:     map[string]Item{"label": {Origins: OriginFlag, FlagName: "label"}},
	}))
}

func labelDefault(t *testing.T, value string) string {
	t.Helper()
	l := labelLoader(t, value)
	return formatDefault(corpusItem(t, l, "label"))
}

func typedBack(t *testing.T, name, typed string) (any, *manifestItem) {
	t.Helper()
	l := defaultsCorpusLoader(t)
	item := corpusItem(t, l, name)
	if err := giveFlags(t, l, []string{"greeter"}, "--"+name+"="+typed); err != nil {
		t.Fatalf("--%s does not take %q back: %v", name, typed, err)
	}
	out := reflect.New(item.typ).Elem()
	if err := decodeInto(item.path, l.v.Get(item.path), out); err != nil {
		t.Fatalf("--%s does not take %q back: %v", name, typed, err)
	}
	return out.Interface(), item
}

// TestHelpQuotesATextFormCarryingASpace checks that help quotes a text form carrying a space.
func TestHelpQuotesATextFormCarryingASpace(t *testing.T) {
	rendered := corpusDefault(t, "label")
	if rendered != `'west wing'` {
		t.Fatalf("the text form renders as %s, and its text holds a space, so it is quoted", rendered)
	}
	if got := shellWord(t, rendered); got != "west wing" {
		t.Fatalf("the rendered default %s reaches the program as %q", rendered, got)
	}
}

// TestHelpDefaultsRoundTripThroughARealShell checks that help defaults round trip through a real shell.
func TestHelpDefaultsRoundTripThroughARealShell(t *testing.T) {
	for _, item := range defaultsCorpusLoader(t).m.items {
		name := item.item.FlagName
		t.Run(name, func(t *testing.T) {
			if reason, skipped := notTypedBack[name]; skipped {
				t.Skipf("--%s stays out of the round trip: %s", name, reason)
			}
			rendered := corpusDefault(t, name)
			got, declared := typedBack(t, name, shellWord(t, rendered))
			if !sameDefault(got, declared.def) {
				t.Fatalf("--%s renders %s, which reads back as %#v while the default is %#v",
					name, rendered, got, declared.def)
			}
		})
	}
}

// TestHelpQuotedDefaultSurvivesEveryASCIIByte checks that help quoted default survives every ascii byte.
func TestHelpQuotedDefaultSurvivesEveryASCIIByte(t *testing.T) {
	for b := 0; b < 0x80; b++ {
		value := "a" + string(rune(b)) + "b"
		t.Run(fmt.Sprintf("0x%02x", b), func(t *testing.T) {
			rendered := labelDefault(t, value)
			if !strconv.IsPrint(rune(b)) {
				if want := strconv.Quote(value); rendered != want {
					t.Fatalf("the default carrying byte 0x%02x renders as %s, and a byte with "+
						"no printable form is rendered as %s", b, rendered, want)
				}
				return
			}
			if got := shellWord(t, rendered); got != value {
				t.Fatalf("the default carrying byte 0x%02x renders as %s, and the shell hands "+
					"the program %q instead of %q", b, rendered, got, value)
			}
		})
	}
}

// TestHelpEscapesADefaultCarryingAControlCharacter checks that help escapes a default carrying a control character.
func TestHelpEscapesADefaultCarryingAControlCharacter(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "newline", value: "a\nb"},
		{name: "escape", value: "a\x1bb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rendered := labelDefault(t, tc.value)
			if strings.Contains(rendered, "\n") {
				t.Fatalf("the default renders as %q, and a value that breaks its line takes "+
					"the layout with it", rendered)
			}
			if strings.ContainsFunc(rendered, func(r rune) bool { return !strconv.IsPrint(r) }) {
				t.Fatalf("the default renders as %q, carrying a raw control character into "+
					"the terminal", rendered)
			}
		})
	}
}

// TestHelpQuotesEmbeddedSingleQuotes checks that help quotes embedded single quotes.
func TestHelpQuotesEmbeddedSingleQuotes(t *testing.T) {
	rendered := corpusDefault(t, "quoted")
	if rendered != `'it'\''s'` {
		t.Fatalf("the default carrying a quote renders as %s, and the quote is closed, "+
			"escaped and reopened", rendered)
	}
	if got := shellWord(t, rendered); got != "it's" {
		t.Fatalf("the rendered default %s reaches the program as %q", rendered, got)
	}
}

// TestHelpLeavesAPlainDefaultUnquoted checks that help leaves a plain default unquoted.
func TestHelpLeavesAPlainDefaultUnquoted(t *testing.T) {
	for name, want := range map[string]string{
		"text":   "Hello",
		"hosts":  "a,b",
		"count":  "8080",
		"flag":   "true",
		"ratio":  "0.1",
		"ttl":    "5m0s",
		"moment": "2026-09-13T10:00:00Z",
		"share":  "50%",
	} {
		if got := corpusDefault(t, name); got != want {
			t.Errorf("--%s renders %s, and its text needs no quotes, so it renders as %s",
				name, got, want)
		}
	}
}

// TestHelpQuotesAnEmptyDefault checks that help quotes an empty default.
func TestHelpQuotesAnEmptyDefault(t *testing.T) {
	if got := labelDefault(t, ""); got != `''` {
		t.Errorf("an empty default renders as %q, and an empty value is shown as ''", got)
	}
	defaults := struct{ Hosts []string }{Hosts: []string{}}
	l := loadOver(t, declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &defaults}},
		Items:     map[string]Item{"hosts": {Origins: OriginFlag, FlagName: "hosts"}},
	}))
	if got := flagDefaultString(corpusItem(t, l, "hosts")); got != `''` {
		t.Errorf("the empty list hands over %q, and an empty value is shown as ''", got)
	}
	if usage := helpFlags(t, l, "greeter").FlagUsages(); !strings.Contains(usage, "(default '')") {
		t.Errorf("the help line reads %q, and an empty value is shown as ''",
			strings.TrimSpace(usage))
	}
}

// TestHelpQuotesAListDefaultItsTextNeedsQuotingFor checks that help quotes a list default its text needs quoting for.
func TestHelpQuotesAListDefaultItsTextNeedsQuotingFor(t *testing.T) {
	defaults := struct{ Hosts []string }{Hosts: []string{"a b", "c'd"}}
	l := loadOver(t, declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &defaults}},
		Items: map[string]Item{"hosts": {
			Origins: OriginFlag, FlagName: "hosts", Placeholder: "HOST",
		}},
	}))
	fs := helpFlags(t, l, "greeter")
	rendered := fs.Lookup("hosts").DefValue
	if rendered != `'a b,c'\''d'` {
		t.Fatalf("the list default is handed over as %s, and its text asks for %s",
			rendered, `'a b,c'\''d'`)
	}
	if !strings.Contains(fs.FlagUsages(), "(default "+rendered+")") {
		t.Fatalf("the help line reads %q, and the default it prints is %s",
			strings.TrimSpace(fs.FlagUsages()), rendered)
	}
	if got := shellWord(t, rendered); got != "a b,c'd" {
		t.Fatalf("the rendered list default %s reaches the program as %q, and the flat form "+
			"of the declared list is %q", rendered, got, "a b,c'd")
	}
	if err := fs.Parse([]string{"--hosts=" + shellWord(t, rendered)}); err != nil {
		t.Fatalf("--hosts does not take its own rendered default back: %v", err)
	}
	out := reflect.New(corpusItem(t, l, "hosts").typ).Elem()
	if err := decodeInto("greeter.hosts", l.v.Get("greeter.hosts"), out); err != nil {
		t.Fatalf("--hosts does not take its own rendered default back: %v", err)
	}
	if !sameDefault(out.Interface(), defaults.Hosts) {
		t.Fatalf("the rendered list default reads back as %#v while the default is %#v",
			out.Interface(), defaults.Hosts)
	}
}

// TestHelpLeavesALeadingDashDefaultBare checks that help leaves a leading dash default bare.
func TestHelpLeavesALeadingDashDefaultBare(t *testing.T) {
	rendered := corpusDefault(t, "offset")
	if rendered != "-42" {
		t.Fatalf("the negative default renders as %s, and its text needs no quotes", rendered)
	}
	got, _ := typedBack(t, "offset", shellWord(t, rendered))
	if got != -42 {
		t.Fatalf("the command line reads the rendered default back as %#v", got)
	}
}

var notTypedBack = map[string]string{
	"share": "the type reads itself from text and writes none, so the column " +
		"carries Go's own form rather than a value the parser takes back",
	"control": "its text carries a tab, and a command line has no single-line " +
		"literal standing for one, so the column renders an escaped form meant " +
		"to be read rather than copied",
	"missing": "its value stands for being unset, so there is no default to type back",
	"secret":  "a sensitive default is never handed to the command line at all",
}
