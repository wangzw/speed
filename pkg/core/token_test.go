package core

import (
	"bytes"
	"strings"
	"testing"
)

type cacheCap interface{ cacheID() string }

type storeCap interface{ storeID() string }

type readerCap interface{ readerID() string }

type superCacheCap interface {
	cacheCap
	superID() string
}

type product struct{ id string }

func (p *product) cacheID() string  { return p.id }
func (p *product) storeID() string  { return p.id }
func (p *product) readerID() string { return p.id }
func (p *product) superID() string  { return p.id }

type schemaRes struct{ Namespace string }

type specRes struct{ Path string }

type namedRes interface{ resourceName() string }

func (s schemaRes) resourceName() string { return "schema:" + s.Namespace }
func (s specRes) resourceName() string   { return "spec:" + s.Path }

type resNames []string

type otherRes struct{ Label string }

func (o otherRes) resourceName() string { return "other:" + o.Label }

func assertPanics(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if r == nil {
			t.Fatalf("expected a panic mentioning %q, got none", want)
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, want) {
			t.Fatalf("panic message %q does not mention %q", msg, want)
		}
	}()
	fn()
}

// TestCapabilityTokenRejectsNonPointer checks that a capability token panics for a non-pointer
// value and for an untyped nil.
func TestCapabilityTokenRejectsNonPointer(t *testing.T) {
	assertPanics(t, "must be a typed nil pointer", func() {
		capabilityType(42, "test")
	})
	assertPanics(t, "untyped nil", func() {
		capabilityType(cacheCap(nil), "test")
	})
}

// TestCapabilityTokenRejectsNonNilPointer checks that a capability token panics for a non-nil
// pointer.
func TestCapabilityTokenRejectsNonNilPointer(t *testing.T) {
	assertPanics(t, "must be a nil pointer", func() {
		capabilityType(&product{}, "test")
	})
}

// TestCapabilityTokenRejectsConcreteElem checks that a capability token panics when it points to a
// concrete type rather than an interface.
func TestCapabilityTokenRejectsConcreteElem(t *testing.T) {
	assertPanics(t, "must point to an interface", func() {
		capabilityType((*bytes.Buffer)(nil), "test")
	})
	assertPanics(t, "must point to an interface", func() {
		capabilityType((*product)(nil), "test")
	})
}

// TestCapabilityTokenAcceptsInterfaceElem checks that a typed nil pointer to an interface is
// accepted and named after the interface's type.
func TestCapabilityTokenAcceptsInterfaceElem(t *testing.T) {
	got := capabilityType((*cacheCap)(nil), "test")
	if got.Name() != "cacheCap" {
		t.Fatalf("capabilityType returned %s, want cacheCap", got)
	}
}

// TestResourceTokenAcceptsConcreteElem checks that a resource token accepts a typed nil pointer to a
// concrete type and panics for a value or a non-nil pointer.
func TestResourceTokenAcceptsConcreteElem(t *testing.T) {
	got := resourceType((*schemaRes)(nil), "test")
	if got.Name() != "schemaRes" {
		t.Fatalf("resourceType returned %s, want schemaRes", got)
	}
	assertPanics(t, "must be a typed nil pointer", func() {
		resourceType(schemaRes{}, "test")
	})
	assertPanics(t, "must be a nil pointer", func() {
		resourceType(&schemaRes{}, "test")
	})
}
