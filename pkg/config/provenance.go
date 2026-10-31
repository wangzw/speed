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

// Layer names one of the layers a configuration value can come from, in the
// order the layers stack: the default layer at the bottom and the command line
// at the top. It answers where a path's effective value came from, and it
// carries no value of its own.
type Layer int

const (
	// LayerDefault is the default layer: the host defaults document when the
	// document names the item, otherwise the field value the declaring
	// module's prototype carries.
	LayerDefault Layer = iota + 1
	// LayerPrimary is the primary config source.
	LayerPrimary
	// LayerEnvironment is the environment variable layer.
	LayerEnvironment
	// LayerCommandLine is the command line.
	LayerCommandLine
)

// EffectiveLayer reports which layer the value at path takes effect from: the
// default layer, the primary config source, the environment, or the command
// line. The path is the complete one Decode takes, and the answer is read the
// same way, through the same per-layer questions Decode's order rests on.
//
// The layer is all that comes back, never the value, so the question can be
// asked about a sensitive item: the caller learns where a credential came from
// without the credential entering the answer.
//
// r is a Reader this package produced, as Load returns and Execute supplies. A
// path no module declares is an error rather than the default layer: an
// undeclared path has no input item, so no layer gave it a value.
func EffectiveLayer(r Reader, path string) (Layer, error) {
	lr, ok := r.(*reader)
	if !ok {
		return 0, fmt.Errorf("config: EffectiveLayer was given %T, which this package did not "+
			"produce. Ask with the Reader Load returned or Execute supplied", r)
	}
	item, declared := lr.l.m.byPath[path]
	if !declared {
		return 0, fmt.Errorf("config: EffectiveLayer was given %s, which is not an input item any "+
			"module declares, so it has no effective value and no layer to report", pathLabel(path))
	}
	return lr.l.provenanceOf(item).layer, nil
}

// provenance is one answer about where an item's value comes from: the layer a
// caller can ask for, and the name a diagnostic gives that layer. The two
// differ only inside the default layer, whose two segments -- the host defaults
// document and the prototype's field value -- the report tells apart.
type provenance struct {
	layer  Layer
	origin string
}

// provenanceOf answers which layer gave the item its effective value, asking
// the layers one by one in the order Decode reads them: the command line, the
// environment, the primary source, and the default layer. This is the one place
// that order is written down for this question, so the exported query and the
// sensitive-item report cannot disagree.
//
// The primary source and the document are read through primaryValue, the way a
// container's value is, so a path written flat and one written nested name the
// same layer. A key either wrote as nothing is not a value, the same reading
// every other leaf gives it.
func (l *loader) provenanceOf(item *manifestItem) provenance {
	if flag := l.bound[item.path]; flag != nil && flag.Changed {
		return provenance{layer: LayerCommandLine, origin: "the command line"}
	}
	if item.envName != "" {
		if _, set := l.env[item.envName]; set {
			return provenance{
				layer:  LayerEnvironment,
				origin: fmt.Sprintf("the environment variable %s", item.envName),
			}
		}
	}
	if value, found := primaryValue(l.primary, item.path); found && value != nil {
		return provenance{layer: LayerPrimary, origin: "the primary config source"}
	}
	if value, found := primaryValue(l.defaults, item.path); found && value != nil {
		return provenance{layer: LayerDefault, origin: "the host defaults document"}
	}
	return provenance{layer: LayerDefault, origin: "its declared default"}
}

// source names the layer the item's value comes from, in the precedence order
// Decode reads: the command line, the environment, the primary source, the host
// defaults document, and finally the declared default. It describes the layer
// and never the value.
func (l *loader) source(item *manifestItem) string {
	return l.provenanceOf(item).origin
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
