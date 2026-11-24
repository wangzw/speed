package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

type reader struct {
	l      *loader
	judged map[string]bool
}

var _ Reader = (*reader)(nil)

func (r *reader) Decode(path string, target any) error {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		panic(fmt.Sprintf("config: Decode was given %T for %s, and it writes into a non-nil "+
			"pointer to a struct. Pass the address of the carrier struct declared for this path",
			target, pathLabel(path)))
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		panic(fmt.Sprintf("config: Decode was given %T for %s, which points at %s, and it "+
			"writes into a struct. Pass the address of the carrier struct declared for this path",
			target, pathLabel(path), v.Kind()))
	}
	return r.decodeStruct(v, path, []reflect.Type{v.Type()})
}

func (r *reader) decodeStruct(v reflect.Value, prefix string, chain []reflect.Type) error {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, tagged, skip := fieldName(f)
		if skip {
			continue
		}
		fv := v.Field(i)
		inner, inline, hollow := inlineEmbedded(f, fv, tagged)
		if hollow {
			continue
		}
		if inline {
			if err := r.descend(inner, prefix, chain, f); err != nil {
				return err
			}
			continue
		}
		key := joinPath(prefix, name)
		switch f.Type.Kind() {
		case reflect.Func, reflect.Chan, reflect.Interface:
			continue
		case reflect.Pointer:
			if f.Type.Elem().Kind() == reflect.Struct && !decodesFromText(f.Type) {
				if fv.IsNil() {
					continue
				}
				if err := r.descend(fv.Elem(), key, chain, f); err != nil {
					return err
				}
				continue
			}
		case reflect.Struct:
			if !decodesFromText(f.Type) {
				if err := r.descend(fv, key, chain, f); err != nil {
					return err
				}
				continue
			}
		}
		if err := r.leaf(key, fv); err != nil {
			return err
		}
	}
	return nil
}

func (r *reader) descend(v reflect.Value, key string, chain []reflect.Type, f reflect.StructField) error {
	st := v.Type()
	if slices.Contains(chain, st) {
		panic(fmt.Sprintf("config: decoding %s reaches %s again through field %s, so walking "+
			"the target does not terminate. A configuration carrier has no back edges: drop "+
			"the field, or mirror the part of it that is meant to be configurable",
			pathLabel(key), typeName(st), f.Name))
	}
	return r.decodeStruct(v, key, append(chain, st))
}

func (r *reader) leaf(path string, fv reflect.Value) error {
	item, declared := r.l.m.byPath[path]
	if declared && item.item.Required && r.judged[item.module] &&
		!r.l.given(item) && !r.l.documentGiven(item) {
		return fmt.Errorf("%w: %q is required and none of the primary config source, the "+
			"environment, the command line and the host defaults document gave it a value. "+
			"The field value in the carrier struct does not count: a value nobody gave and "+
			"a value that happens to be the zero one would otherwise be the same thing",
			ErrMissingRequired, path)
	}
	if declared && !r.l.given(item) && meansUnset(reflect.ValueOf(item.def)) {
		return nil
	}
	// A container leaf comes from the source's unfolded tree, and the environment's JSON replaces the whole container first.
	if declared && item.kind == kindContainer {
		if text, set := r.l.envValue(item); set {
			if !fv.CanSet() {
				return nil
			}
			return envContainer(item, text, fv)
		}
		for _, tree := range [...]map[string]any{r.l.primary, r.l.defaults} {
			given, found := primaryValue(tree, path)
			if !found || given == nil {
				continue
			}
			if !fv.CanSet() {
				return nil
			}
			return convert(item, path, given, fv)
		}
	}
	raw := r.l.raw(item, path)
	if raw == nil || !fv.CanSet() {
		return nil
	}
	return convert(item, path, raw, fv)
}

func (l *loader) raw(item *manifestItem, path string) any {
	if item != nil {
		given := l.bound[item.path]
		if value, set := l.envValue(item); set && (given == nil || !given.Changed) {
			return value
		}
	}
	return l.v.Get(path)
}

// convert writes one leaf's value, keeping a sensitive item's value out of the failure.
func convert(item *manifestItem, path string, raw any, fv reflect.Value) error {
	err := decodeInto(path, raw, fv)
	if err == nil || item == nil || !item.item.Sensitive {
		return err
	}
	return fmt.Errorf("%w: %q takes %s, and the value given does not convert to it. The value "+
		"is not repeated here, since the item is sensitive", ErrTypeMismatch, path, typeName(fv.Type()))
}

func primaryValue(tree map[string]any, path string) (any, bool) {
	if tree == nil {
		return nil, false
	}
	if value, present := tree[path]; present {
		return clonedTree(value), true
	}
	head, rest, nested := strings.Cut(path, ".")
	if !nested {
		return nil, false
	}
	section, isSection := tree[head].(map[string]any)
	if !isSection {
		return nil, false
	}
	return primaryValue(section, rest)
}
