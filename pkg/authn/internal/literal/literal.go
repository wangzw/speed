// Package literal builds the redaction rule for a secret a module holds as a
// configuration value: the value itself, wherever it turns up inside a string
// that is logged.
//
// The shape rules the gateway registers catch what a credential looks like. A
// client secret or a vendor key has no shape, so the one thing known about it is
// the value, and the rule is an exact substring. Marking an item Sensitive does
// not do this: it keeps the value out of error texts and help output and
// registers nothing with the logging capability.
//
// A module registers in its own New, before it takes its Named logger, because
// Named binds an attribute and an attribute bound before the rule exists is
// never judged by it.
package literal

import (
	"net/url"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/log"
)

// Register adds a rule that masks each of values under name, and does nothing
// when none of them is a value that could be matched. Registering nothing is
// right for an item that was not configured: there is no secret to hide, and the
// logging capability refuses a rule with no matcher.
//
// Every value is masked wherever it occurs in the process's logs, not only in
// the records of the module that registered it. A short value therefore masks
// every occurrence of itself, which is the safe direction and the registrant's
// to weigh.
func Register(rules log.Redaction, name string, values ...string) {
	if match := Matcher(values...); match != nil {
		rules.AddPattern(name, match)
	}
}

// Matcher reports the spans of a string that are one of values, each in the forms
// a log line may carry it: as written, without the whitespace around it (a value
// read from a file often ends in a newline that the code using it dropped), and
// URL-escaped as a query component or a path segment (the text of a failed
// request holds the address it was sent to, with the value in the escaped form it
// was built in). It returns nil when there is nothing to match, which is the case
// for no value and for values that are all empty or only whitespace.
//
// Matching is exact and case-sensitive, and every occurrence is reported,
// overlapping ones included; the logging capability merges the spans. The
// matcher runs on every string value and error text of every record in the
// process, so it is a scan per form with no expression engine behind it.
func Matcher(values ...string) log.Matcher {
	var needles []string
	for _, value := range values {
		for _, form := range formsOf(value) {
			if !slices.Contains(needles, form) {
				needles = append(needles, form)
			}
		}
	}
	if len(needles) == 0 {
		return nil
	}
	return func(s string) []log.Span {
		var spans []log.Span
		for _, needle := range needles {
			for from := 0; from < len(s); {
				at := strings.Index(s[from:], needle)
				if at < 0 {
					break
				}
				at += from
				spans = append(spans, log.Span{Start: at, End: at + len(needle)})
				from = at + 1
			}
		}
		return spans
	}
}

// formsOf lists the forms one value is looked for in: without the whitespace
// around it, and escaped. The form with the whitespace left on contains these, so
// it is not looked for separately. A value with nothing but whitespace in it has
// no form: it is not a secret anyone could have chosen, and as a rule it would
// mask every space the process logs.
func formsOf(value string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return []string{trimmed, url.QueryEscape(trimmed), url.PathEscape(trimmed)}
}
