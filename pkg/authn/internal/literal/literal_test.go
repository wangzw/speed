package literal

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// maskedBy applies spans to s the way the logging capability does, so a case
// reads what a reader of the log would read.
func maskedBy(s string, spans []log.Span) string {
	covered := make([]bool, len(s))
	for _, span := range spans {
		for i := span.Start; i < span.End; i++ {
			covered[i] = true
		}
	}
	var b strings.Builder
	for i := range len(s) {
		if covered[i] {
			b.WriteByte('#')
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// TestTheValueIsMaskedWhereverItAppears holds what the matcher finds in a string:
// every occurrence of the value, and nothing that is not one.
func TestTheValueIsMaskedWhereverItAppears(t *testing.T) {
	const secret = "s3cr3t-Value"
	for _, c := range []struct {
		name string
		in   string
		want string
	}{
		{"the value alone", secret, "############"},
		{"inside a sentence", "dial failed for " + secret + " after 3 tries", "dial failed for ############ after 3 tries"},
		{"twice", secret + " and " + secret, "############ and ############"},
		{"inside a URL query", "https://api.example/token?secret=" + secret + "&x=1", "https://api.example/token?secret=############&x=1"},
		{"glued to other characters", "x" + secret + "y", "x############y"},
		{"another case is not the value", "S3CR3T-VALUE", "S3CR3T-VALUE"},
		{"a prefix of the value is not the value", "s3cr3t-Valu", "s3cr3t-Valu"},
		{"a suffix of the value is not the value", "3cr3t-Value", "3cr3t-Value"},
		{"no value at all", "nothing to hide here", "nothing to hide here"},
		{"the empty string", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			match := Matcher(secret)
			if got := maskedBy(c.in, match(c.in)); got != c.want {
				t.Errorf("%q is read as %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestOverlappingOccurrencesAreAllMasked holds that an occurrence that starts
// inside another is found too: a scan that resumed after each match would leave
// the tail of the second one readable.
func TestOverlappingOccurrencesAreAllMasked(t *testing.T) {
	match := Matcher("abab")
	in := "ababab"
	if got := maskedBy(in, match(in)); got != "######" {
		t.Errorf("%q is read as %q, want every byte masked", in, got)
	}
}

// TestTheFormsALogLineCarriesAreMasked holds the forms besides the one written:
// the value without the whitespace around it, and the value as a URL escapes it
// for a query and for a path. Each is a way the value reaches a log that the
// configured text alone would miss.
func TestTheFormsALogLineCarriesAreMasked(t *testing.T) {
	const secret = " pa ss/w+rd=1&2\n"
	trimmed := strings.TrimSpace(secret)
	for _, c := range []struct {
		name string
		in   string
	}{
		{"as written", "error: " + secret + " rejected"},
		{"trimmed", "error: " + trimmed + " rejected"},
		{"escaped for a query", "Get \"https://api.example/token?secret=" + url.QueryEscape(trimmed) + "\": EOF"},
		{"escaped for a path", "Get \"https://api.example/" + url.PathEscape(trimmed) + "/token\": EOF"},
		{"the untrimmed value escaped for a query", "q=" + url.QueryEscape(secret)},
	} {
		t.Run(c.name, func(t *testing.T) {
			match := Matcher(secret)
			masked := maskedBy(c.in, match(c.in))
			if strings.Contains(masked, "pa") || strings.Contains(masked, "rd") || strings.Contains(masked, "%2F") || strings.Contains(masked, "%2f") {
				t.Errorf("%q is read as %q, which still carries part of the value", c.in, masked)
			}
			if !strings.Contains(masked, "#") {
				t.Errorf("%q is read as %q: nothing was masked", c.in, masked)
			}
		})
	}
}

// TestSeveralValuesAreEachMasked holds that one rule can hold more than one
// value, which is how a module with a client secret and a signing key registers.
func TestSeveralValuesAreEachMasked(t *testing.T) {
	match := Matcher("first-secret", "second-secret")
	in := "a first-secret b second-secret c"
	if got, want := maskedBy(in, match(in)), "a ############ b ############# c"; got != want {
		t.Errorf("%q is read as %q, want %q", in, got, want)
	}
}

// TestNoUsableValueGivesNoMatcher holds the answer for nothing to match: nil,
// which Register reads as "register nothing". A matcher built over an empty
// string would match at every position and mask whole lines, and one that always
// answered with no spans would look registered and protect nothing.
func TestNoUsableValueGivesNoMatcher(t *testing.T) {
	for _, c := range []struct {
		name   string
		values []string
	}{
		{"no value", nil},
		{"an empty value", []string{""}},
		{"only whitespace", []string{" \t\n"}},
		{"several empty values", []string{"", " ", ""}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if match := Matcher(c.values...); match != nil {
				t.Errorf("Matcher(%q) is a matcher, want nil", c.values)
			}
		})
	}
}

// TestAnEmptyValueBesideARealOneIsSkipped holds that the empty value among others
// costs nothing and masks nothing it should not.
func TestAnEmptyValueBesideARealOneIsSkipped(t *testing.T) {
	match := Matcher("", "real-secret")
	in := "x real-secret y"
	if got, want := maskedBy(in, match(in)), "x ########### y"; got != want {
		t.Errorf("%q is read as %q, want %q", in, got, want)
	}
}

// ruleRecorder is a log.Redaction that remembers what was registered.
type ruleRecorder struct{ patterns *[]string }

func (r ruleRecorder) AddKeys(...string)                     {}
func (r ruleRecorder) AddPattern(name string, _ log.Matcher) { *r.patterns = append(*r.patterns, name) }

// TestRegisterAddsOneRuleUnderTheNameAndNoneForNothing holds the registration:
// one pattern under the name given for any usable value, and no call at all for
// none, since the logging capability panics on a nil matcher.
func TestRegisterAddsOneRuleUnderTheNameAndNoneForNothing(t *testing.T) {
	var got []string
	rules := ruleRecorder{patterns: &got}

	Register(rules, "mod.secret", "a", "b")
	if !slices.Equal(got, []string{"mod.secret"}) {
		t.Fatalf("registered %v, want one rule named mod.secret", got)
	}

	got = nil
	Register(rules, "mod.unset", "")
	Register(rules, "mod.none")
	if len(got) != 0 {
		t.Errorf("registered %v for nothing to hide, want no rule", got)
	}
}

// holder is a module that holds a secret configuration value the way a backend
// does: it registers the value in New and only then takes its logger.
func holder(secret string, logger *log.Logger) core.Module {
	return core.Module{
		Name:     "stand-in.holder",
		Requires: []core.Requirement{{Token: (*log.Logger)(nil)}},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logging, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return nil, err
			}
			Register(logging.Redaction(), "stand-in.holder.secret", secret)
			*logger = logging
			return struct{}{}, nil
		},
	}
}

// TestTheValueDoesNotReachTheLogOutput holds the rule end to end through the real
// logging module and its output: a record that carries the value in free text, in
// an error and in the text of a failed request, and one written through a logger
// that was taken after the registration with the value bound to it, all arrive
// with the value masked and the rest of the record intact.
func TestTheValueDoesNotReachTheLogOutput(t *testing.T) {
	const secret = "wx-app-secret/0123456789"
	path := filepath.Join(t.TempDir(), "log.json")
	body := `{"log":{"outputs":[{"to":"file","format":"json","path":"` + path + `"}]}}`

	var logging log.Logger
	mod := holder(secret, &logging)
	reader := authntest.ConfigReaderAll(t, body, log.Module(), mod)

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Register(mod)
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: []string{log.ModuleName, mod.Name},
		Action: func(context.Context, *core.Registry) error {
			named := logging.Named("holder")
			named.Info("a record with the value in free text",
				"note", "token request failed for "+secret+" twice")
			named.Info("a record with the value in an error",
				"err", errors.New("exchange rejected "+secret))
			named.Info("a record with the value in a failed request",
				"err", &url.Error{Op: "Get", URL: "https://api.example/token?secret=" + url.QueryEscape(secret), Err: errors.New("EOF")})
			named.With("bound", secret).Info("a record with the value bound to the logger")
			named.Info("a record with nothing to hide", "note", "all quiet")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log output: %v", err)
	}
	text := string(written)
	for _, message := range []string{"free text", "in an error", "failed request", "bound to the logger", "nothing to hide"} {
		if !strings.Contains(text, message) {
			t.Fatalf("the record about %q is not in the output, so nothing here was checked:\n%s", message, text)
		}
	}
	for _, leaked := range []string{secret, url.QueryEscape(secret), "0123456789"} {
		if strings.Contains(text, leaked) {
			t.Errorf("the output carries %q in the clear:\n%s", leaked, text)
		}
	}
	if !strings.Contains(text, "all quiet") {
		t.Errorf("the output lost the text of a record that held no secret:\n%s", text)
	}
}
