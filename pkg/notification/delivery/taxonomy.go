package delivery

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/notification"
)

// taxonomy is the set of declared notification types, collected once in New and
// read-only afterwards. It is the reference a dispatch is validated against.
//
// The type of a notification is the declaring business module's, not this
// capability's: a module places a notification.TypeSpec in its own module
// descriptor's Resources, and this module — one of the two consumption points —
// collects every declaration here. The domain is the available set and no
// subtraction is applied: the same declaration is a valid reference whether or
// not the declaring module is driven, because "is this type key one this process
// knows" is a vocabulary question and not a question about what this run drives.
type taxonomy struct {
	byKey map[string]notification.TypeSpec
}

// collectTaxonomy gathers every declared type and refuses the set this module
// cannot run on. An inconsistent declaration, or one key two modules both
// declare, is notification.ErrInvalidConfig and ends the startup, naming the key
// and the declaring module rather than surfacing at the first dispatch.
//
// The criterion a declaration is held to is notification.ValidateTypeSpec, the
// one both consumption points of the taxonomy apply; the duplicate key is the
// criterion's set-level rule, which only a collector can apply.
func collectTaxonomy(reg *core.Registry) (taxonomy, error) {
	declared := core.Resources[notification.TypeSpec](reg)
	byKey := make(map[string]notification.TypeSpec, len(declared))
	owners := make(map[string]string, len(declared))
	for _, res := range declared {
		spec := res.Value
		if err := notification.ValidateTypeSpec(spec); err != nil {
			return taxonomy{}, fmt.Errorf("%w: %s declares %q: %w", notification.ErrInvalidConfig, res.Module, spec.Key, err)
		}
		if other, twice := owners[spec.Key]; twice {
			return taxonomy{}, fmt.Errorf("%w: the type key %q is declared by both %s and %s",
				notification.ErrInvalidConfig, spec.Key, other, res.Module)
		}
		owners[spec.Key] = res.Module
		byKey[spec.Key] = spec
	}
	return taxonomy{byKey: byKey}, nil
}

// spec returns the declaration of a type key, and whether it is declared.
func (t taxonomy) spec(key string) (notification.TypeSpec, bool) {
	spec, ok := t.byKey[key]
	return spec, ok
}

// validate runs the checks a dispatch must pass, in the fixed order: shape,
// then taxonomy, then parameters. It is the one routine both the submission seam
// and the job handler run — the handler does not assume the submitter validated
// anything, because a worker can meet a payload the submitter never wrote or a
// row that has since been changed.
func (p *pipeline) validate(d notification.Dispatch) (notification.TypeSpec, error) {
	if strings.TrimSpace(d.TypeKey) == "" {
		return notification.TypeSpec{}, fmt.Errorf("%w: the dispatch names no type", notification.ErrInvalidRequest)
	}
	if d.Recipient.Class != notification.RecipientUser {
		return notification.TypeSpec{}, fmt.Errorf("%w: the recipient class is %q, and a dispatch goes to a user",
			notification.ErrInvalidRequest, d.Recipient.Class)
	}
	if strings.TrimSpace(d.Recipient.UserID) == "" {
		return notification.TypeSpec{}, fmt.Errorf("%w: the dispatch names no recipient", notification.ErrInvalidRequest)
	}
	if strings.TrimSpace(d.Locale) == "" {
		return notification.TypeSpec{}, fmt.Errorf("%w: the dispatch names no locale", notification.ErrInvalidRequest)
	}
	spec, known := p.taxonomy.spec(d.TypeKey)
	if !known {
		return notification.TypeSpec{}, fmt.Errorf("%w: %q is not a declared notification type",
			notification.ErrUnknownType, d.TypeKey)
	}
	if spec.RecipientVisibleParams != nil {
		for name := range d.Params {
			if !visible(spec.RecipientVisibleParams, name) {
				return notification.TypeSpec{}, fmt.Errorf("%w: the parameter %q is not in the type's whitelist",
					notification.ErrParamsNotAllowed, name)
			}
		}
	}
	return spec, nil
}

// visible reports whether a name is in a type's parameter whitelist.
func visible(whitelist []string, name string) bool {
	for _, allowed := range whitelist {
		if allowed == name {
			return true
		}
	}
	return false
}
