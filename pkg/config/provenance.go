package config

import (
	"fmt"
	"slices"
	"strings"
)

// ReportSensitive writes one line per sensitive input item, naming the item and
// the source its value came from. The value itself is never written: the line
// is a diagnostic on a shared stream, and a sensitive item is one whose value
// must not appear there.
//
// The report exists so an operator can see where a credential was taken from
// without the credential. An item supplied by an environment variable is
// ordinary -- a platform secret is delivered that way -- and the line says so
// rather than leaving the operator to guess which of the four sources won.
func (l *loader) ReportSensitive() {
	items := slices.Clone(l.m.items)
	slices.SortFunc(items, func(a, b *manifestItem) int { return strings.Compare(a.path, b.path) })
	for _, item := range items {
		if !item.item.Sensitive {
			continue
		}
		// The stream is the diagnostic sink itself: a failed write has nowhere
		// to be reported, and a sensitive-item report is not a failure.
		_, _ = fmt.Fprintf(l.stderr, "%ssensitive item %q takes its value from %s\n",
			diagnosticPrefix, item.path, l.source(item))
	}
}

// source names the layer the item's value comes from, in the precedence order
// Decode reads: the command line, the environment, the primary source, the host
// defaults document, and finally the declared default. It describes the layer
// and never the value.
//
// The primary source and the document are read through primaryValue, the way a
// container's value is, so a path written flat and one written nested name the
// same layer. A key either wrote as nothing is not a value, the same reading
// every other leaf gives it.
func (l *loader) source(item *manifestItem) string {
	if flag := l.bound[item.path]; flag != nil && flag.Changed {
		return "the command line"
	}
	if item.envName != "" {
		if _, set := l.env[item.envName]; set {
			return fmt.Sprintf("the environment variable %s", item.envName)
		}
	}
	for _, layer := range [...]struct {
		tree map[string]any
		name string
	}{
		{l.primary, "the primary config source"},
		{l.defaults, "the host defaults document"},
	} {
		if value, found := primaryValue(layer.tree, item.path); found && value != nil {
			return layer.name
		}
	}
	return "its declared default"
}

// given reports whether any source above the defaults layer gave the item a
// value: the primary config source, the command line, the environment.
//
// The layering tool does not publish which layer a key's value came from, so
// the three sources are asked one by one. Each leg answers for one of them.
// The prototypes are not among them and never count as given -- a carrier
// struct's field always holds something, and counting it would make "nobody
// gave a value" and "somebody gave the zero value" the same answer. The host
// defaults document is not among them either, for a different reason: it is
// an explicit declaration by the host, and documentGiven judges it on its own
// rather than folding it into what this answer means.
//
// The command-line leg reads the argument the item was registered as, which
// exists only once BindLayer has run for the level that carries it.
func (l *loader) given(item *manifestItem) bool {
	if l.v.InConfig(item.path) {
		return true
	}
	if flag := l.bound[item.path]; flag != nil && flag.Changed {
		return true
	}
	// The environment leg reads the snapshot the values come from, so an item
	// counts as given exactly when the environment layer supplies its value.
	_, set := l.envValue(item)
	return set
}

// documentGiven reports whether the host defaults document names the item's
// path with a value.
//
// The document is an explicit declaration by the host, the same act as writing
// the key in the configuration file, so it satisfies a required item the way
// the sources above the defaults layer do. What it is not is a fourth leg of
// given: it sits in the defaults layer, and only the required judgment treats
// it as somebody having given a value.
//
// The read is over the document as the parser gave it, through primaryValue,
// so a path written flat and one written nested mean the same thing. A key
// written as nothing is not a value, for the reason it is not one anywhere
// else: nil is what every layer reads as unset, and the prototype's field
// value stays the default it was.
func (l *loader) documentGiven(item *manifestItem) bool {
	value, found := primaryValue(l.defaults, item.path)
	return found && value != nil
}
