package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

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
func validatePrimary(m *manifest, content map[string]any) error {
	return validatePrimarySection(m, "", content)
}

func validatePrimarySection(m *manifest, prefix string, section map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(section)) {
		path := joinPath(prefix, key)
		raw := section[key]

		if item, declared := m.byPath[path]; declared {
			if !item.origins.has(OriginPrimary) {
				return fmt.Errorf("%w: the primary config source gives %q, which module %q "+
					"declares but does not accept from the primary source: it reads %s. Letting "+
					"it through would swallow a value written on purpose",
					ErrUnknownKey, path, item.module, originNames(item.origins))
			}
			// The keys inside a container leaf are not in the manifest and do
			// not take part in this check: the same fact decides that the
			// value is replaced as a whole.
			continue
		}

		if _, interior := m.interior[path]; interior {
			child, isSection := raw.(map[string]any)
			if !isSection {
				return fmt.Errorf("%w: the primary config source gives a value at %q, which is a "+
					"section holding input items rather than an input item of its own",
					ErrUnknownKey, path)
			}
			if err := validatePrimarySection(m, path, child); err != nil {
				return err
			}
			continue
		}

		return fmt.Errorf("%w: the primary config source gives %q, which no module declares. "+
			"Check the spelling, and check that the module owning it is imported: the manifest "+
			"is collected from the modules this binary carries",
			ErrUnknownKey, path)
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
