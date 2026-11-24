package preferences

import (
	"fmt"
	"sort"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/notification"
)

// taxonomy is the read-only set of notification types collected in New:
// the type-key to notification.TypeSpec mapping, ordered by type key.
type taxonomy struct {
	keys  []string
	byKey map[string]notification.TypeSpec
}

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

func (p *product) Types() []notification.TypeSpec { return p.taxonomy.Types() }

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

func (t taxonomy) Type(key string) (notification.TypeSpec, bool) {
	spec, ok := t.byKey[key]
	if !ok {
		return notification.TypeSpec{}, false
	}
	return copySpec(spec), true
}

func copySpec(spec notification.TypeSpec) notification.TypeSpec {
	spec.DefaultChannels = append([]notification.Channel(nil), spec.DefaultChannels...)
	spec.RecipientVisibleParams = append([]string(nil), spec.RecipientVisibleParams...)
	return spec
}
