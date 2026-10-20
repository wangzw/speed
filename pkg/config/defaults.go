package config

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/core"
)

// Defaults is the resource a host declares to carry its platform defaults
// document: one configuration document compiled into the binary, layered below
// the primary config source and above the field values of each module's
// prototype.
//
// It is a second contributor to the default layer, not a second primary
// source. A run still has at most one primary source; what this adds is a
// document that travels with the binary, so that a host's configuration file
// carries only the deltas from it.
//
// The document is a host artifact like the primary source, so it is judged the
// same way: a key no module declares, or one the owning module does not accept
// from the primary source, fails the startup rather than being dropped.
type Defaults interface {
	// Format is the name of a registered Format that parses Data. It is
	// matched exactly, in lower case, the same way the primary source's
	// format name is: "YAML" reaches the parser registered as yaml, and a
	// name no imported Format answers for fails the startup.
	Format() string
	// Data is the document itself.
	Data() []byte
}

// defaultsDocument names the defaults resource in the errors it raises. The
// primary config source and this document are judged alike and reported under
// their own names, so a failure says which of the two to go and fix.
const defaultsDocument = "the host defaults document"

// collectDefaults gives the one defaults document a run may carry, if any.
//
// At most one may be declared: two documents would both claim the same layer
// and there is no ground to choose between them. None is the ordinary case and
// leaves the layer with the prototypes alone, exactly as before the resource
// existed.
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
// manifest's defaults.
//
// The format is looked up the way the primary source's is: folded to lower case
// and matched exactly, with no sniffing of the content. The keys are validated
// against the whole manifest before any of them takes effect, for the reason
// the primary source's are: the document is written by hand in the host, and a
// key nobody reads is a mistake to stop for. The values themselves are not
// converted here; they keep the types the parser gave them and are converted
// where every other source's values are, in Decode.
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
	// The document is kept whole and unfolded: a container leaf is read back
	// from here, and its inner keys are the module's own data rather than
	// names this module owns.
	if cloned, ok := clonedTree(content).(map[string]any); ok {
		l.defaults = cloned
	}
	for _, item := range l.m.items {
		value, found := primaryValue(content, item.path)
		if !found || value == nil {
			// A value written as nothing is not a value: every layer reads a
			// nil the same way, and the prototype's field value stays the
			// default it was.
			continue
		}
		item.def = value
	}
	return nil
}
