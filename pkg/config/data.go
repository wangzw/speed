package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// primarySource names the primary config source in the errors a host-owned
// document raises.
const primarySource = "the primary config source"

// validatePrimary refuses a key the primary config source is not allowed to
// give, before any of it reaches the layering tool.
//
// The domain is the whole manifest and not the participants of a command: one
// config source is shared by every command, so judging it by what a single
// command takes in would make it carry unknown keys under every one of them.
//
// The tree it walks is the one the format parsed. The layering tool's own key
// listing mixes the defaults, the bound environment names and the bound
// arguments together, and cannot answer what this one source gave.
//
// Conversion is not done here. A value that does not fit its field is refused
// where the target type is in hand, which is Decode; this pass answers only
// whether the key is one this source may write.
//
// A path may be written as one key holding the dots, nested, or any mixture of
// the two, and every spelling reaches the same item. Two spellings of one path
// in one source are therefore two values for one item, and the read would take
// one and drop the other, so the pair is refused here, where both are still in
// view.
func validatePrimary(m *manifest, content map[string]any) error {
	return validateDocument(m, content, primarySource)
}

// validateDocument refuses a key a host-owned document is not allowed to give,
// before any of it takes effect. label names the document in the errors.
//
// The primary config source and the host defaults document are judged alike:
// both travel with the binary and are written by hand in the host, so a key
// neither may give is a mistake to stop for rather than one to drop. What the
// document is called is the one thing that differs, and the reader has to know
// which of the two to go and fix.
func validateDocument(m *manifest, content map[string]any, label string) error {
	return validateDocumentSection(m, nil, content, make(map[string][]string), label)
}

// validateDocumentSection checks one section. written is the keys that lead to
// it as the document wrote them, and given records the keys that reached each
// declared path.
func validateDocumentSection(m *manifest, written []string, section map[string]any, given map[string][]string, label string) error {
	prefix := strings.Join(written, ".")
	for _, key := range slices.Sorted(maps.Keys(section)) {
		path := joinPath(prefix, key)
		raw := section[key]
		spelled := append(slices.Clone(written), key)

		if item, declared := m.byPath[path]; declared {
			if !item.origins.has(OriginPrimary) {
				return fmt.Errorf("%w: %s gives %q, which module %q "+
					"declares but does not accept from the primary source: it reads %s. Letting "+
					"it through would swallow a value written on purpose",
					ErrUnknownKey, label, path, item.module, originNames(item.origins))
			}
			if earlier, twice := given[path]; twice {
				return fmt.Errorf("%w: %s gives %q twice, once written as "+
					"the keys %q and once as %q, and only one of the two could take effect. Give "+
					"it once", ErrMalformedConfig, label, path, earlier, spelled)
			}
			given[path] = spelled
			// The keys inside a container leaf are not in the manifest and do
			// not take part in this check: the same fact decides that the
			// value is replaced as a whole.
			continue
		}

		if _, interior := m.interior[path]; interior {
			child, isSection := raw.(map[string]any)
			if !isSection {
				return fmt.Errorf("%w: %s gives a value at %q, which is a "+
					"section holding input items rather than an input item of its own",
					ErrUnknownKey, label, path)
			}
			if err := validateDocumentSection(m, spelled, child, given, label); err != nil {
				return err
			}
			continue
		}

		return fmt.Errorf("%w: %s gives %q, which no module declares. "+
			"Check the spelling, and check that the module owning it is imported: the manifest "+
			"is collected from the modules this binary carries",
			ErrUnknownKey, label, path)
	}
	return nil
}

// originNames renders an origin set for an error message.
func originNames(o Origin) string {
	var names []string
	if o.has(OriginPrimary) {
		names = append(names, "the primary config source")
	}
	if o.has(OriginEnv) {
		names = append(names, "the environment")
	}
	if o.has(OriginFlag) {
		names = append(names, "the command line")
	}
	if len(names) == 0 {
		return "no source at all"
	}
	return strings.Join(names, ", ")
}
