package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// reader hands each module the values it declared. It holds the load of one
// run and nothing else: configuration is a snapshot taken at startup, so there
// is nothing to refresh and nothing to guard.
type reader struct {
	l *loader
	// judged holds the modules this run takes in at run level, which are
	// the ones whose required items are judged. A module the run only
	// collects from, or does not take in at all, is not among them however
	// its path comes to be decoded: nothing of this run needs its values,
	// and a command that reports the configuration reads them in its own
	// action.
	judged map[string]bool
}

var _ Reader = (*reader)(nil)

// Decode writes the values under path into target.
//
// The walk is over the target rather than over the manifest, which is what
// lets two modules share a namespace: each one decodes the keys its own struct
// names and never sees the other's. Defaults arrive through the config data's
// base layer, so a caller passing a zero struct still gets them, and the
// prototype the module declared is never consulted again.
//
// A path the caller never declared is not an error: target keeps what the
// caller put in it. A required item that no source gave a value for is
// ErrMissingRequired, raised here rather than in a global sweep, because the
// manifest is collected from every registered module, the ones this run does
// not enable included, while a required item binds only where it is decoded
// and only for the modules this run takes in at run level. A sweep over the
// manifest would fail the run over items no participant declared, and judging
// every decoded path would fail a command that reads a module it only collects
// from -- one that reports the configuration -- over a value only the command
// that runs the module needs.
//
// An illegal target panics rather than returning an error: a non-pointer, a
// nil, or a struct that cycles back into itself is a programming error at the
// call site, unrelated to data, showing up deterministically on first
// execution and leaving the host nothing to handle. The panic lands while the
// lifecycle is being driven, so rollback does not run; that is the standing
// cost of a programming error, and the process was going to terminate anyway.
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

// decodeStruct walks one struct down to its leaves, deriving the same paths
// the declaration did: what the manifest expanded and what Decode looks up
// have to be the same names, or a module would declare one key and read
// another. Embedded fields are therefore inlined here exactly as the expansion
// inlines them; the two walks are separate implementations of one rule, and a
// change made to only one of them declares one key and reads another without
// saying so.
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

// descend walks into a nested struct, refusing a type already on the way in.
// A declared mount cannot hold a cycle, collection rejects those; a target the
// caller assembled for itself can, and stopping is better than not returning.
// It panics for the reason Decode's other target checks do: the carrier is
// written at the call site, so a back edge in it is a programming error the
// host cannot act on.
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

// leaf writes one value into the target field and enforces the required rule
// on it.
//
// Every leaf is read on its own rather than the whole path being decoded in
// one go. The layering tool answers a request for a section with the topmost
// layer that holds one, whole, and does not merge the layers below it: a
// primary source giving cache.addr would knock cache.ttl back to its zero
// value, which is the opposite of the leaf-wise overriding the design states.
func (r *reader) leaf(path string, fv reflect.Value) error {
	item, declared := r.l.m.byPath[path]
	if declared && item.item.Required && r.judged[item.module] && !r.l.given(item) {
		return fmt.Errorf("%w: %q is required and neither the primary config source, the "+
			"environment nor the command line gave it a value. The default in the carrier struct "+
			"does not count: a value nobody gave and a value that happens to be the zero one "+
			"would otherwise be the same thing",
			ErrMissingRequired, path)
	}
	// A leaf whose declared value stands for being unset, and that no source
	// gave one, keeps what the caller put in the target. Its default is not in
	// the layering tool either, so the tool would otherwise answer with the
	// rendered default of its command-line argument.
	if declared && !r.l.given(item) && meansUnset(reflect.ValueOf(item.def)) {
		return nil
	}
	// A container leaf is read back from what the source wrote rather than
	// from the layering tool, whose keys have been folded to lower case. Its
	// inner keys are the user's own data: folding them rewrites the
	// configuration instead of normalising a name this module owns. The
	// primary source gives it as a whole and replaces the whole of whatever
	// lay below; with no primary source, the host defaults document supplies
	// the value the same way, and the prototype's own container is the last
	// resort, reached through the default layer below.
	//
	// A value the source wrote as nothing is not a value it gave: every other
	// leaf reaches the same conclusion by the nil below, and a container
	// answering differently would make the meaning of an empty key depend on
	// the type behind it.
	if declared && item.kind == kindContainer {
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

// raw is the value one leaf takes, from the topmost layer that gives it.
//
// The environment is the one layer the layering tool does not hold. It would
// read the process environment at every lookup, and the configuration of a run
// is what the process was started with: a variable changed after loading began
// would otherwise reach a module that decodes again, and a variable changed
// between two modules' decodes would give the two of them different
// configurations. The layer is read out of the snapshot taken when loading
// began instead, and it sits where the layering tool would put it: above the
// primary source and the defaults, below an argument given on the command line.
func (l *loader) raw(item *manifestItem, path string) any {
	if item != nil {
		given := l.bound[item.path]
		if value, set := l.envValue(item); set && (given == nil || !given.Changed) {
			return value
		}
	}
	return l.v.Get(path)
}

// convert writes a source value into the field of one leaf, keeping a
// sensitive item's value out of the failure.
//
// A failed conversion quotes what it was given -- the text that is not a
// number, the text a type refused to read itself from, the element of a
// container that did not fit -- and the failure travels up through the stage
// that decoded it to standard error, where it is a diagnostic like any other.
// A sensitive item's failure names the item and the type it takes, which is
// what the operator has to fix, and leaves the value out. item is nil for a
// path no module declared, which has no sensitivity to keep.
func convert(item *manifestItem, path string, raw any, fv reflect.Value) error {
	err := decodeInto(path, raw, fv)
	if err == nil || item == nil || !item.item.Sensitive {
		return err
	}
	return fmt.Errorf("%w: %q takes %s, and the value given does not convert to it. The value "+
		"is not repeated here, since the item is sensitive", ErrTypeMismatch, path, typeName(fv.Type()))
}

// primaryValue walks the untouched primary source to one path.
//
// A source may write a path flat, as one key holding the dots, or nested, or
// any mixture of the two: the whole remaining path is tried as a key of this
// section before the first segment is descended into. Reaching the value only
// one of those ways would leave the other reading from the folded tree, and
// the same document would then mean two things depending on how it was
// written.
func primaryValue(tree map[string]any, path string) (any, bool) {
	if tree == nil {
		return nil, false
	}
	if value, present := tree[path]; present {
		// The copy is what keeps the promise decodeInto makes: a holder that
		// writes into a container it was given must not reach another
		// holder, nor this tree, which every later read comes from. A value
		// whose target type is any arrives as the node itself, so handing
		// out the node would hand out the tree.
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
