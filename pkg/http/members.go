package http

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

// checkMembers walks one JSON value against the type it is about to be decoded
// into and refuses the two member names encoding/json accepts without a word.
//
// A name that matches a struct field only when case is ignored fills that
// field: the API has a field "name", and the client sent "NAME". A name
// written twice in one object keeps the last of its values and drops the
// others. Either way the body means one thing here and may mean another to
// whatever read it before this process did — a gateway, a filter, a signature
// check — and a client that misspelled a name or repeated a member gets no
// word of it. Both are malformed bodies.
//
// A name that matches no field at all is left alone: the decoder refuses it
// and names it. So is a value whose type parses its own JSON, since what its
// members mean is that type's business.
func checkMembers(raw []byte, target reflect.Type) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	return walkValue(dec, target, "")
}

var (
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// parsesItself reports whether encoding/json hands a value of type t to the
// type's own method rather than walking it.
func parsesItself(t reflect.Type) bool {
	for _, candidate := range []reflect.Type{t, reflect.PointerTo(t)} {
		if candidate.Implements(jsonUnmarshalerType) || candidate.Implements(textUnmarshalerType) {
			return true
		}
	}
	return false
}

// walkValue consumes one value from dec. t is the type it decodes into, and
// path is where the value sits in the body, for the message.
func walkValue(dec *json.Decoder, t reflect.Type, path string) error {
	for t.Kind() == reflect.Pointer && !parsesItself(t) {
		t = t.Elem()
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, opens := token.(json.Delim)
	if !opens {
		return nil
	}
	if parsesItself(t) {
		return skipRest(dec)
	}
	switch delim {
	case '{':
		return walkObject(dec, t, path)
	case '[':
		return walkArray(dec, t, path)
	}
	return nil
}

// walkObject consumes the members of an object whose opening brace has been
// read, then its closing brace.
func walkObject(dec *json.Decoder, t reflect.Type, path string) error {
	var fields map[string]reflect.Type
	var element reflect.Type
	switch {
	case t.Kind() == reflect.Struct:
		fields = jsonFields(t)
	case t.Kind() == reflect.Map:
		element = t.Elem()
	case t.Kind() == reflect.Interface && t.NumMethod() == 0:
		// An untyped value decodes an object into a map, where a repeated
		// name is a dropped value and two names differing in case are two
		// keys.
		element = t
	default:
		// The decoder refuses an object here with a type mismatch.
		return skipRest(dec)
	}

	seen := make(map[string]bool)
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		name, _ := token.(string)
		at := memberPath(path, name)
		if seen[name] {
			return fmt.Errorf("%w: the member %q is written more than once in one object, and only "+
				"the last of its values would be kept. Send each member once", ErrMalformedBody, at)
		}
		seen[name] = true

		next := element
		if fields != nil {
			field, exact := fields[name]
			if !exact {
				if resembled, found := foldedField(fields, name); found {
					return fmt.Errorf("%w: the member %q is not a field of this body; the field is %q, "+
						"and a member name has to match it exactly, case included",
						ErrMalformedBody, at, memberPath(path, resembled))
				}
				// No field at all: the decoder refuses it and names it.
				if err := skipValue(dec); err != nil {
					return err
				}
				continue
			}
			next = field
		}
		if err := walkValue(dec, next, at); err != nil {
			return err
		}
	}
	_, err := dec.Token()
	return err
}

// walkArray consumes the elements of an array whose opening bracket has been
// read, then its closing bracket.
func walkArray(dec *json.Decoder, t reflect.Type, path string) error {
	var element reflect.Type
	switch {
	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		element = t.Elem()
	case t.Kind() == reflect.Interface && t.NumMethod() == 0:
		element = t
	default:
		return skipRest(dec)
	}
	for i := 0; dec.More(); i++ {
		if err := walkValue(dec, element, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
	}
	_, err := dec.Token()
	return err
}

// skipValue consumes one whole value.
func skipValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if _, opens := token.(json.Delim); opens {
		return skipRest(dec)
	}
	return nil
}

// skipRest consumes the rest of an object or array whose opening delimiter has
// been read.
func skipRest(dec *json.Decoder) error {
	for depth := 1; depth > 0; {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		if delim, is := token.(json.Delim); is {
			switch delim {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}

// memberPath renders where a member sits, for the message.
func memberPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// foldedField returns the field whose name equals name when case is ignored.
func foldedField(fields map[string]reflect.Type, name string) (string, bool) {
	for field := range fields {
		if strings.EqualFold(field, name) {
			return field, true
		}
	}
	return "", false
}

// jsonFields returns the member names encoding/json decodes a struct from,
// each with the type of the field it fills.
//
// It follows encoding/json's own rules, because a name this walk and the
// decoder disagree on would be refused although it decodes, or let through
// although it does not: a field is named by its json tag or else by itself, a
// field tagged "-" takes no part, the fields of an untagged embedded struct
// are promoted into the enclosing one, and among fields of one name the
// shallowest wins — at one depth only a single tagged one, and otherwise
// none.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	type candidate struct {
		name   string
		tagged bool
		depth  int
		typ    reflect.Type
	}

	var candidates []candidate
	visited := make(map[reflect.Type]bool)
	next := map[reflect.Type]int{t: 1}
	for depth := 0; len(next) > 0; depth++ {
		current := next
		next = make(map[reflect.Type]int)
		// The order a level is walked in does not reach the result: the
		// candidates are grouped by name below, and a type appears once per
		// level with its count.
		for st, count := range current {
			if visited[st] {
				continue
			}
			visited[st] = true
			for i := range st.NumField() {
				sf := st.Field(i)
				if sf.Anonymous {
					ft := sf.Type
					if ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					if !sf.IsExported() && ft.Kind() != reflect.Struct {
						continue
					}
				} else if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, _, _ := strings.Cut(tag, ",")
				if !validTagName(name) {
					name = ""
				}
				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					c := candidate{name: name, tagged: name != "", depth: depth, typ: sf.Type}
					if c.name == "" {
						c.name = sf.Name
					}
					candidates = append(candidates, c)
					// A struct embedded more than once at one depth
					// contributes each of its fields twice, so that they
					// cancel out below as encoding/json's do.
					if count > 1 {
						candidates = append(candidates, c)
					}
					continue
				}
				next[ft]++
			}
		}
	}

	byName := make(map[string][]candidate)
	for _, c := range candidates {
		byName[c.name] = append(byName[c.name], c)
	}
	fields := make(map[string]reflect.Type, len(byName))
	for name, group := range byName {
		shallowest := group[0].depth
		for _, c := range group {
			shallowest = min(shallowest, c.depth)
		}
		var top, tagged []candidate
		for _, c := range group {
			if c.depth != shallowest {
				continue
			}
			top = append(top, c)
			if c.tagged {
				tagged = append(tagged, c)
			}
		}
		switch {
		case len(top) == 1:
			fields[name] = top[0].typ
		case len(tagged) == 1:
			fields[name] = tagged[0].typ
		}
	}
	return fields
}

// validTagName is encoding/json's rule for a usable name in a json tag. A tag
// name that breaks it is ignored and the field keeps its own name.
func validTagName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}
