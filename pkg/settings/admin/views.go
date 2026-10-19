package admin

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// maskedValue is the fixed string a sensitive item's written text and resolved
// value are replaced with before the view reaches a client. It is the same
// string in both fields, so a client reads one marker and not several.
const maskedValue = "********"

// viewOf builds one item's view from its declaration, the text the selected
// layer holds for it, whether it holds a row at all, and the text it resolves to.
//
// The item's namespace is the module that declared it, which is the key's prefix
// segment. The type is the declared type's text form. The domain is the rule the
// declaration states, empty for an item whose domain admits every value of its
// type.
//
// A sensitive item's default, written and value are all masked here, in one
// place, because the masking is a property of the item rather than of the route
// that read it: a second place building a view is a second place that can forget
// to mask it, and a forgotten mask sends a credential out with the response.
// The mask covers the text only; has_row reports the row's existence unchanged.
func viewOf(item settings.DeclaredItem, written string, hasRow bool, value string) SettingsAdminSetting {
	// The metadata the declaration gave hangs off the embedded Item; the
	// promoted selectors reach it.
	if item.Sensitive {
		return SettingsAdminSetting{
			Key:         item.Key,
			Namespace:   item.Module,
			Type:        item.Type.String(),
			Description: item.Description,
			Domain:      item.Domain.Rule(),
			Sensitive:   true,
			Default:     "",
			Written:     maskedValue,
			HasRow:      hasRow,
			Value:       maskedValue,
		}
	}
	return SettingsAdminSetting{
		Key:         item.Key,
		Namespace:   item.Module,
		Type:        item.Type.String(),
		Description: item.Description,
		Domain:      item.Domain.Rule(),
		Sensitive:   false,
		Default:     item.Default,
		Written:     written,
		HasRow:      hasRow,
		Value:       value,
	}
}

// durationType is the type a text duration is held to, matched by identity so
// that a type defined over time.Duration is treated as the integer it is to
// reflection, exactly as the settings module's declaration does.
var durationType = reflect.TypeFor[time.Duration]()

// textUnmarshalerType is the interface a type uses to decode itself from text.
var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// renderValue writes a resolved value in the declared type's own text form, the
// form the settings module stores a value in and reports a row as. It is what
// makes the view's value field comparable with its written and default fields:
// all three are the text a row of the item would carry.
//
// The rules mirror the settings module's own text form — the one place that form
// is defined — because that module exports no value-to-text function, and its
// classification decides which rule applies: a type that decodes itself from
// text is written through its MarshalText; time.Duration through its own String;
// the scalar kinds through strconv; a float through its shortest round-tripping
// text. A value whose type the settings module would classify otherwise would
// not reach here, since Get hands out a value of the declared type under the
// same classification.
func renderValue(typ reflect.Type, value any) (string, error) {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || rv.Type() != typ {
		// Unreachable in a request: Get hands out a value of exactly the declared
		// type. The guard is here because formatting a value of some other shape
		// would write a text the item's rows never carry.
		return "", fmt.Errorf("%w: the declared type is %s and the value is %T",
			settings.ErrTypeMismatch, typ, value)
	}
	if decodesFromText(typ) {
		held := reflect.New(typ)
		held.Elem().Set(rv)
		marshaler, ok := held.Interface().(encoding.TextMarshaler)
		if !ok {
			// Unreachable: a declared type is one that carries its own text form
			// only when it also writes itself back to text, which the settings
			// module refuses a declaration lacking.
			return "", fmt.Errorf("the declared type %s has no MarshalText", typ)
		}
		text, err := marshaler.MarshalText()
		if err != nil {
			return "", fmt.Errorf("%w: writing a %s value as text: %w", settings.ErrTypeMismatch, typ, err)
		}
		return string(text), nil
	}
	if typ == durationType {
		// rv's type is the declared type, which is time.Duration here, so the
		// integer underneath it is the duration's count.
		return time.Duration(rv.Int()).String(), nil
	}
	switch typ.Kind() {
	case reflect.String:
		return rv.String(), nil
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'g', -1, typ.Bits()), nil
	}
	return "", fmt.Errorf("%w: the declared type %s has no text form", settings.ErrTypeMismatch, typ)
}

// decodesFromText reports whether a type takes its value from a text of its own,
// either directly or through a pointer method set. It is the settings module's
// criterion, restated so that a value is rendered by the same rule it was
// classified under.
func decodesFromText(t reflect.Type) bool {
	if t.Implements(textUnmarshalerType) {
		return true
	}
	return t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(textUnmarshalerType)
}
