package config

import (
	"fmt"
	"slices"
	"strings"
)

// ReportSensitive writes one line per sensitive input item, naming the item and
// the source its value came from. The value itself is never written.
func (l *loader) ReportSensitive() {
	items := slices.Clone(l.m.items)
	slices.SortFunc(items, func(a, b *manifestItem) int { return strings.Compare(a.path, b.path) })
	for _, item := range items {
		if !item.item.Sensitive {
			continue
		}
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

// EffectiveLayer reports which layer the value at path takes effect from. The
// layer is all that comes back, never the value, so a sensitive item can be
// asked about safely. r must be a Reader this package produced; a path no module
// declares is an error.
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
// caller can ask for, and the name a diagnostic gives that layer.
type provenance struct {
	layer  Layer
	origin string
}

// provenanceOf answers which layer gave the item its effective value, asking the
// layers in Decode's order: command line, environment, primary source, then the
// default layer.
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

// source names the layer the item's value comes from, never the value.
func (l *loader) source(item *manifestItem) string {
	return l.provenanceOf(item).origin
}

// given reports whether any source above the defaults layer gave the item a
// value: the primary config source, the command line or the environment. The
// prototype never counts.
func (l *loader) given(item *manifestItem) bool {
	if l.v.InConfig(item.path) {
		return true
	}
	if flag := l.bound[item.path]; flag != nil && flag.Changed {
		return true
	}
	_, set := l.envValue(item)
	return set
}

// documentGiven reports whether the host defaults document names the item's path
// with a value. It is judged on its own, not as a leg of given.
func (l *loader) documentGiven(item *manifestItem) bool {
	value, found := primaryValue(l.defaults, item.path)
	return found && value != nil
}
