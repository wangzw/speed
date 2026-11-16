package delivery

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	// The parameter shape is checked before the whitelist, so a map carrying the
	// codec's reserved key or a container the codec cannot walk is reported as
	// the unreadable shape it is, not as an unlisted name.
	if err := validateParams(d.Params); err != nil {
		return notification.TypeSpec{}, err
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

// validateParams refuses the parameter shapes the queue codec cannot carry
// unchanged. The codec walks map[string]any and []any, turning a time.Time into
// a marked string at any depth and restoring it when the job is read; a typed
// container, a pointer or a struct reaches the consumer as a different shape,
// so it is refused where the dispatch is submitted and again where the job is
// consumed. A bare time.Time is the one container the codec does handle, at any
// depth.
//
// A scalar keeps no Go type across the queue: encoding/json decodes every
// number as float64, so an integer parameter reaches the consumer as a whole
// float64. That is admitted — the renderer reads a whole float as a number and
// the sms stringifier writes it without a fraction — because the round trip
// changes the scalar's Go type, not the value or the text it renders to. A
// shape the codec cannot walk is refused instead: it would reach the consumer
// as a different structure.
//
// The codec's own wire key is reserved: a producer-supplied map carrying it is
// refused at every level, because accepting one would let a producer choose
// what the consumer restores.
func validateParams(params map[string]any) error {
	return walkParamMap(params)
}

// walkParamMap checks one map and the values in it.
func walkParamMap(params map[string]any) error {
	for name, value := range params {
		if name == deliveryTimeMarker {
			return fmt.Errorf("%w: a parameter map carries the queue codec's reserved key %q",
				notification.ErrInvalidRequest, deliveryTimeMarker)
		}
		if err := walkParamValue(name, value); err != nil {
			return err
		}
	}
	return nil
}

// walkParamValue checks one parameter value, descending into the two containers
// the codec walks.
func walkParamValue(name string, value any) error {
	switch v := value.(type) {
	case nil, string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number, time.Time:
		return nil
	case map[string]any:
		return walkParamMap(v)
	case []any:
		for _, item := range v {
			if err := walkParamValue(name, item); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: the parameter %q is a %T, a shape the queue codec cannot carry unchanged",
			notification.ErrInvalidRequest, name, value)
	}
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
