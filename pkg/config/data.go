package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

const primarySource = "the primary config source"

func validatePrimary(m *manifest, content map[string]any) error {
	return validateDocument(m, content, primarySource)
}

func validateDocument(m *manifest, content map[string]any, label string) error {
	return validateDocumentSection(m, nil, content, make(map[string][]string), label)
}

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

		if module, section := m.sections[path]; section {
			child, isSection := raw.(map[string]any)
			if !isSection {
				return fmt.Errorf("%w: %s gives a value at %q, which module %q declares as a "+
					"section holding input items rather than an input item of its own",
					ErrUnknownKey, label, path, module)
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
