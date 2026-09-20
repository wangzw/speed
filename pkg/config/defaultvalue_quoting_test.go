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

// shellWords hands one rendered default to a real shell and returns the words
// the program would receive in argv. The rendered text is pasted into a
// command of its own, the shell does the splitting and the expansions, and
// printf writes the arguments it ended up with back, NUL-separated.
//
// A non-interactive shell is all this covers: word splitting, quote removal,
// parameter, command and arithmetic expansion, and pathname expansion. History
// expansion and job specs live in the interactive layer, and no driver without
// a terminal can rule on them.
//
// The working directory holds a file the corpus patterns match, because an
// unmatched pattern is left standing: without that file a rendered * would
// come back as itself and a missing pair of quotes would pass unnoticed.
//
// One limit is written down here and worked around by the callers: printf
// writes the same single NUL for no arguments at all as for one empty
// argument, so "the word disappeared" reads as one empty word. The empty
// default is therefore pinned on the rendered text rather than through here.
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

// shellWord is the common case: a default is one value, so it has to reach the
// program as one word. The count is compared before the text, or a value the
// shell split in two would be reported as a mismatched first word.
func shellWord(t *testing.T, rendered string) string {
	t.Helper()
	words := shellWords(t, rendered)
	if len(words) != 1 {
		t.Fatalf("the rendered default %s reaches the program as %d words %q, and a default "+
			"is one value", rendered, len(words), words)
	}
	return words[0]
}

// labelLoader collects a single item whose leaf carries its own text form.
// The census below rides this shape rather than a plain string because it is
// the shape the rule is about: what the column prints is whatever the type
// wrote, and the decision to quote it can only be read off that text.
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

// labelDefault renders the default of that single item.
func labelDefault(t *testing.T, value string) string {
	t.Helper()
	l := labelLoader(t, value)
	return formatDefault(corpusItem(t, l, "label"))
}

// typedBack writes one rendered default onto a command line and reads back the
// value that arrives in the item's own type. It is the second half of the
// criterion: the shell decides what word the program receives, and this
// decides what that word converts to.
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

// TestHelpQuotesATextFormCarryingASpace pins the case the whole rule exists
// for: a type that carries its own text form, is neither a string nor a list
// of strings, and whose form holds a space. Decided by the field's Go kind it
// is printed bare, and the reader copying the line back hands the program two
// arguments instead of one value.
func TestHelpQuotesATextFormCarryingASpace(t *testing.T) {
	rendered := corpusDefault(t, "label")
	if rendered != `'west wing'` {
		t.Fatalf("the text form renders as %s, and its text holds a space, so it is quoted", rendered)
	}
	if got := shellWord(t, rendered); got != "west wing" {
		t.Fatalf("the rendered default %s reaches the program as %q", rendered, got)
	}
}

// TestHelpDefaultsRoundTripThroughARealShell pins the rule the default column
// rests on, over every shape the corpus carries: the reader copies the value
// out of the help output into a command line and gets that same default back.
// The shell is the real one, so the step the rendering has to survive - word
// splitting and the expansions - is performed rather than imagined.
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

// TestHelpQuotedDefaultSurvivesEveryASCIIByte is the criterion put one byte at
// a time: every byte a default can carry is rendered, and what the shell hands
// the program is that same text. Each byte is judged on its own: rendered in
// one batch, a byte the rule left bare turns the whole command into a syntax
// error and the report names no byte at all.
//
// A byte with no printable form is held to the other half of the rule: there
// is no single-line command-line literal standing for it, so the column
// renders an escaped form meant to be read, and the raw byte stays out of the
// reader's terminal.
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

// TestHelpEscapesADefaultCarryingAControlCharacter pins the two properties the
// escaped form is chosen for. The help output is one line per item, so a
// default carrying a newline may not break its line; and an escape character
// reaching the terminal raw is read by the terminal rather than by the reader.
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

// TestHelpQuotesEmbeddedSingleQuotes pins the one escape the quoted form has.
// Nothing is special inside single quotes, the quote itself included, so a
// value carrying one leaves the quotes and comes back into them.
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

// TestHelpLeavesAPlainDefaultUnquoted pins the other half of the rule. A value
// whose text a command line takes as it stands is rendered as it stands:
// quoting it would be a pair of characters the reader has to know to drop.
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

// TestHelpQuotesAnEmptyDefault pins the one value quoted for being unreadable
// rather than unsafe: an empty column leaves the reader unable to tell an
// empty default from one that failed to print.
//
// The list is held to the same rule, and is pinned on the line the reader
// actually sees. A list holding nothing is not the zero of its type once the
// module wrote it - the zero is the list nobody wrote, and that one is
// omitted before any rendering happens - so this is the shape an empty list
// reaches the column in.
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

// TestHelpQuotesAListDefaultItsTextNeedsQuotingFor pins that the flat form of
// a list is held to the criterion the whole column rests on: the reader
// copies the value off the help line onto a command line and the program
// receives that same text. An element carrying a space arrives as two
// arguments without the quotes, so what the reader typed back is not the list
// that was printed.
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

// TestHelpLeavesALeadingDashDefaultBare pins that a negative number is not
// quoted for opening with a dash. Quotes would change nothing about it: the
// shell removes them and the program still receives a word starting with a
// dash, and the parser reads it as a value either way.
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

// notTypedBack names the arguments the round trip leaves out, with the reason
// each one is left out. A skip is written down rather than silently passed
// over, so the set of shapes that cannot be typed back stays visible.
var notTypedBack = map[string]string{
	"share": "the type reads itself from text and writes none, so the column " +
		"carries Go's own form rather than a value the parser takes back",
	"control": "its text carries a tab, and a command line has no single-line " +
		"literal standing for one, so the column renders an escaped form meant " +
		"to be read rather than copied",
	"missing": "its value stands for being unset, so there is no default to type back",
	"secret":  "a sensitive default is never handed to the command line at all",
}
