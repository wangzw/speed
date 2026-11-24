package http

import (
	"fmt"
	"reflect"

	"github.com/vislake/speed/pkg/core"
)

func capabilityKey(t core.Token, where string) reflect.Type {
	v := reflect.ValueOf(t)
	if !v.IsValid() {
		panic(fmt.Sprintf("http: %s: capability token is an untyped nil. "+
			"Write it as a typed nil pointer, for example (*auth.Authenticator)(nil); "+
			"Authenticator(nil) converts to an untyped nil interface and designates nothing", where))
	}
	if v.Kind() != reflect.Pointer {
		panic(fmt.Sprintf("http: %s: capability token must be a typed nil pointer, got %s. "+
			"Write it as (*%s)(nil)", where, v.Type(), v.Type()))
	}
	if !v.IsNil() {
		panic(fmt.Sprintf("http: %s: capability token must be a nil pointer, got a non-nil %s. "+
			"A token designates a capability, not an instance", where, v.Type()))
	}
	elem := v.Type().Elem()
	if elem.Kind() != reflect.Interface {
		panic(fmt.Sprintf("http: %s: capability token must point to an interface, got %s. "+
			"A capability is the set of methods modules take up from each other, "+
			"so a pointer to a concrete type such as (*%s)(nil) cannot designate one",
			where, typeName(elem), elem.Name()))
	}
	return elem
}

func typeName(t reflect.Type) string {
	if t.PkgPath() != "" && t.Name() != "" {
		return t.PkgPath() + "." + t.Name()
	}
	return t.String()
}
