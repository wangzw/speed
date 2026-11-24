package admin

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

const maskedValue = settings.MaskedValue

// The one place a view is built; a sensitive item's value, written and default are masked here.
func viewOf(item settings.DeclaredItem, written string, hasRow bool, value string) SettingsAdminSetting {
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

var durationType = reflect.TypeFor[time.Duration]()

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// renderValue writes a resolved value in the declared type's own text form, the
// form the settings module stores, so a view's value compares with its written and
// default.
func renderValue(typ reflect.Type, value any) (string, error) {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || rv.Type() != typ {
		return "", fmt.Errorf("%w: the declared type is %s and the value is %T",
			settings.ErrTypeMismatch, typ, value)
	}
	if decodesFromText(typ) {
		held := reflect.New(typ)
		held.Elem().Set(rv)
		marshaler, ok := held.Interface().(encoding.TextMarshaler)
		if !ok {
			return "", fmt.Errorf("the declared type %s has no MarshalText", typ)
		}
		text, err := marshaler.MarshalText()
		if err != nil {
			return "", fmt.Errorf("%w: writing a %s value as text: %w", settings.ErrTypeMismatch, typ, err)
		}
		return string(text), nil
	}
	if typ == durationType {
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

func decodesFromText(t reflect.Type) bool {
	if t.Implements(textUnmarshalerType) {
		return true
	}
	return t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(textUnmarshalerType)
}
