package log

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// MaskedText is what a rule leaves behind: it replaces the value of an attribute whose key path
// carries a registered name, and the spans a value-shape rule reports inside a string.
const MaskedText = "[REDACTED]"

var rootMu sync.Mutex

type pattern struct {
	name  string
	match Matcher
}

type ruleSet struct {
	keys     map[string]struct{}
	patterns []pattern
}

var rules atomic.Pointer[ruleSet]

var emptyRuleSet = &ruleSet{}

func currentRules() *ruleSet {
	if rs := rules.Load(); rs != nil {
		return rs
	}
	return emptyRuleSet
}

func (rs *ruleSet) matchesKey(key string) bool {
	if key == "" || len(rs.keys) == 0 {
		return false
	}
	_, ok := rs.keys[key]
	return ok
}

func (rs *ruleSet) matchesAny(segments []string) bool {
	for _, s := range segments {
		if rs.matchesKey(s) {
			return true
		}
	}
	return false
}

func (rs *ruleSet) maskString(s string) (string, bool) {
	if len(rs.patterns) == 0 || s == "" {
		return s, false
	}
	var spans []Span
	for _, p := range rs.patterns {
		for _, span := range p.match(s) {
			if span.Start < 0 || span.End > len(s) || span.End <= span.Start {
				continue
			}
			spans = append(spans, span)
		}
	}
	if len(spans) == 0 {
		return s, false
	}
	slices.SortFunc(spans, func(a, b Span) int { return a.Start - b.Start })
	merged := spans[:1]
	for _, span := range spans[1:] {
		last := &merged[len(merged)-1]
		if span.Start <= last.End {
			if span.End > last.End {
				last.End = span.End
			}
			continue
		}
		merged = append(merged, span)
	}
	var b strings.Builder
	written := 0
	for _, span := range merged {
		b.WriteString(s[written:span.Start])
		b.WriteString(MaskedText)
		written = span.End
	}
	b.WriteString(s[written:])
	return b.String(), true
}

type redactionRegistry struct{}

var processRedaction Redaction = redactionRegistry{}

func (redactionRegistry) AddKeys(keys ...string) {
	for i, key := range keys {
		if key == "" {
			panic(fmt.Sprintf("log: AddKeys[%d]: the key name is empty. An empty name "+
				"carries no rule, and comparing it against the empty segments of a key "+
				"path would mask everything under an anonymous group; pass the key the "+
				"sensitive attribute is logged under", i))
		}
	}

	rootMu.Lock()
	defer rootMu.Unlock()

	current := currentRules()
	next := &ruleSet{
		keys:     make(map[string]struct{}, len(current.keys)+len(keys)),
		patterns: current.patterns,
	}
	maps.Copy(next.keys, current.keys)
	added := false
	for _, key := range keys {
		if _, ok := next.keys[key]; ok {
			continue
		}
		next.keys[key] = struct{}{}
		added = true
	}
	if !added {
		return
	}
	rules.Store(next)
}

func (redactionRegistry) AddPattern(name string, match Matcher) {
	if match == nil {
		panic(fmt.Sprintf("log: AddPattern(%q): the matcher is nil. A rule needs the "+
			"function that reports which spans of a value to mask", name))
	}
	rootMu.Lock()
	defer rootMu.Unlock()

	current := currentRules()
	next := &ruleSet{
		keys:     current.keys,
		patterns: make([]pattern, len(current.patterns), len(current.patterns)+1),
	}
	copy(next.patterns, current.patterns)
	next.patterns = append(next.patterns, pattern{name: name, match: match})
	rules.Store(next)
}
