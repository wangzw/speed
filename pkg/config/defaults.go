package config

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/core"
)

// Defaults is the resource a host declares to carry its platform defaults
// document: one configuration document compiled into the binary, layered below
// the primary config source and above each module's prototype field values.
//
// A document here satisfies a required item, and it is judged like the primary
// source: a key no module declares fails the startup.
type Defaults interface {
	// Format is the name of a registered Format that parses Data, matched in
	// lower case.
	Format() string
	// Data is the document itself.
	Data() []byte
}

// defaultsDocument names the defaults resource in the errors it raises.
const defaultsDocument = "the host defaults document"

// collectDefaults gives the one defaults document a run may carry, if any. At
// most one may be declared.
func collectDefaults(reg *core.Registry) (Defaults, string, bool, error) {
	declared := core.Resources[Defaults](reg)
	switch len(declared) {
	case 0:
		return nil, "", false, nil
	case 1:
		return declared[0].Value, declared[0].Module, true, nil
	default:
		return nil, "", false, fmt.Errorf("%w: modules %q and %q both declare a Defaults "+
			"document, and a run carries one platform defaults document. Leave the declaration "+
			"to the host", ErrConfigConflict, declared[0].Module, declared[1].Module)
	}
}

// applyDefaults parses the host defaults document and folds its values into the
// manifest's defaults. The keys are validated against the whole manifest; the
// values are converted in Decode.
func (l *loader) applyDefaults(d Defaults, module string) error {
	name := strings.ToLower(d.Format())
	if name == "" {
		return fmt.Errorf("%w: %s declared by module %q names no format, so there is nothing to "+
			"parse it with. Name a registered Format, for example \"yaml\"",
			ErrUnknownFormat, defaultsDocument, module)
	}
	format, known := l.t.formats[name]
	if !known {
		return fmt.Errorf("%w: %s declared by module %q names the format %q, which no imported "+
			"Format parses. Import a parser for it: by convention that is "+
			"github.com/vislake/speed/pkg/config/format/%s. The convention is a lead, not a "+
			"guarantee: an implementation is free to live elsewhere, and this module cannot "+
			"enumerate the ones that exist",
			ErrUnknownFormat, defaultsDocument, module, name, name)
	}
	content, err := format.Unmarshal(d.Data())
	if err != nil {
		return fmt.Errorf("%w: %s declared by module %q does not parse as %s: %w",
			ErrMalformedConfig, defaultsDocument, module, name, err)
	}
	if content == nil {
		content = map[string]any{}
	}
	if err := validateDocument(l.m, content, defaultsDocument); err != nil {
		return err
	}
	if cloned, ok := clonedTree(content).(map[string]any); ok {
		l.defaults = cloned
	}
	for _, item := range l.m.items {
		value, found := primaryValue(content, item.path)
		if !found || value == nil {
			continue
		}
		item.def = value
		item.documented = true
	}
	return nil
}
