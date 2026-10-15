package preferences

import (
	"fmt"
	"sort"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/notification"
)

// taxonomy is the read-only set of notification types collected in New: the
// type-key to notification.TypeSpec mapping, ordered by type key. It is built
// once, holds nothing from any table, and does not change while the process
// runs.
type taxonomy struct {
	// keys is the type keys in ascending order.
	keys []string
	// byKey is the spec of each key.
	byKey map[string]notification.TypeSpec
}

// buildTaxonomy collects the declarations into a taxonomy and refuses a set that
// cannot be one.
//
// Every declaration is held to the criterion both consumption points of the
// taxonomy apply — notification.ValidateTypeSpec — and two modules claiming one
// key, the criterion's set-level rule, fail the startup too. A refusal names the
// key and the declaring module rather than waiting for a request to find the
// taxonomy unreadable.
//
// The keys are then ordered, so every view of the taxonomy is stable.
func buildTaxonomy(resources []core.Resource[notification.TypeSpec]) (taxonomy, error) {
	tax := taxonomy{byKey: make(map[string]notification.TypeSpec, len(resources))}
	declaredBy := make(map[string]string, len(resources))
	for _, resource := range resources {
		spec := resource.Value
		if err := notification.ValidateTypeSpec(spec); err != nil {
			return taxonomy{}, fmt.Errorf("%w: the type %q declared by %q: %w",
				notification.ErrInvalidConfig, spec.Key, resource.Module, err)
		}
		if first, taken := declaredBy[spec.Key]; taken {
			return taxonomy{}, fmt.Errorf("%w: the type %q is declared by both %q and %q; a type key is "+
				"globally unique", notification.ErrInvalidConfig, spec.Key, first, resource.Module)
		}
		declaredBy[spec.Key] = resource.Module
		tax.byKey[spec.Key] = spec
	}
	tax.keys = make([]string, 0, len(tax.byKey))
	for key := range tax.byKey {
		tax.keys = append(tax.keys, key)
	}
	sort.Strings(tax.keys)
	return tax, nil
}

// Types returns a copy of the taxonomy; see notification.Preferences.
func (p *product) Types() []notification.TypeSpec { return p.taxonomy.Types() }

// Type reports whether a type is declared and, if so, its spec; see
// notification.Preferences.
func (p *product) Type(key string) (notification.TypeSpec, bool) { return p.taxonomy.Type(key) }

// Types returns a copy of the taxonomy, ordered by type key. Each spec's slices
// are copied too, so a caller changing what it was handed does not disturb what
// the module holds.
func (t taxonomy) Types() []notification.TypeSpec {
	out := make([]notification.TypeSpec, len(t.keys))
	for i, key := range t.keys {
		out[i] = copySpec(t.byKey[key])
	}
	return out
}

// Type reports whether a type is in the taxonomy and, if so, its spec as a copy.
func (t taxonomy) Type(key string) (notification.TypeSpec, bool) {
	spec, ok := t.byKey[key]
	if !ok {
		return notification.TypeSpec{}, false
	}
	return copySpec(spec), true
}

// copySpec copies a spec so a value handed out shares no slice with the
// taxonomy.
func copySpec(spec notification.TypeSpec) notification.TypeSpec {
	spec.DefaultChannels = append([]notification.Channel(nil), spec.DefaultChannels...)
	spec.RecipientVisibleParams = append([]string(nil), spec.RecipientVisibleParams...)
	return spec
}
