package delivery

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/notification"
)

// taxonomy is the set of declared notification types, collected once in
// New and read-only afterwards.
type taxonomy struct {
	byKey map[string]notification.TypeSpec
}

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

func (t taxonomy) spec(key string) (notification.TypeSpec, bool) {
	spec, ok := t.byKey[key]
	return spec, ok
}

// validate runs the checks a dispatch must pass, in the fixed order:
// shape, then taxonomy, then parameters.
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

// validateParams refuses the parameter shapes the queue codec cannot
// carry unchanged.
func validateParams(params map[string]any) error {
	return walkParamMap(params)
}

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

func visible(whitelist []string, name string) bool {
	for _, allowed := range whitelist {
		if allowed == name {
			return true
		}
	}
	return false
}
