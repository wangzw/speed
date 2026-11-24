// Package literal builds the log-redaction rule for a secret a module holds as a configuration value.
package literal

import (
	"net/url"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/log"
)

// Register adds a rule that masks each of values under name, and does nothing when none of them is
// a value that could be matched.
func Register(rules log.Redaction, name string, values ...string) {
	if match := Matcher(values...); match != nil {
		rules.AddPattern(name, match)
	}
}

// Matcher reports the spans of a string that hold one of values, in the forms a log line may carry them, or nil when there is nothing to match.
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

func formsOf(value string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return []string{trimmed, url.QueryEscape(trimmed), url.PathEscape(trimmed)}
}
