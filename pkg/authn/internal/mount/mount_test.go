package mount

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
)

// fake is a Mounter written out in full. Its being assignable is what holds
// the method set: a method added to the interface, dropped from it, or given
// another signature stops this file from compiling.
type fake struct{}

func (*fake) Public(string, string, gin.HandlerFunc, ...Option)   {}
func (*fake) Optional(string, string, gin.HandlerFunc, ...Option) {}
func (*fake) Guarded(string, string, gin.HandlerFunc, ...Option)  {}
func (*fake) Callback([]string, string, gin.HandlerFunc)          {}
func (*fake) Base() string                                        { return "" }
func (*fake) PublicURL() string                                   { return "" }
func (*fake) CSRF() *http.CrossOriginProtection                   { return nil }

var _ Mounter = (*fake)(nil)

// TestMounterHasExactlyTheMethodsTheDesignNames holds the other direction of
// the compile-time assertion: an interface that gained a method the design does
// not list would still be satisfied by a fake that had it, and by nothing else.
func TestMounterHasExactlyTheMethodsTheDesignNames(t *testing.T) {
	typ := reflect.TypeFor[Mounter]()

	var got []string
	for m := range typ.Methods() {
		got = append(got, m.Name)
	}
	slices.Sort(got)

	want := []string{"Base", "CSRF", "Callback", "Guarded", "Optional", "Public", "PublicURL"}
	if !slices.Equal(got, want) {
		t.Errorf("Mounter methods = %v, want %v", got, want)
	}
}

// TestNoOptionLeavesTheRoutePlain holds that the zero reading of a
// registration is the route no impersonating identity may pass.
func TestNoOptionLeavesTheRoutePlain(t *testing.T) {
	if got := Resolve(); got.Impersonable {
		t.Errorf("Resolve() = %+v, want Impersonable false", got)
	}
}

// TestImpersonableMarksTheRoute holds that the option reaches the reader: a
// Resolve that ignored its options would answer false here.
func TestImpersonableMarksTheRoute(t *testing.T) {
	if got := Resolve(Impersonable()); !got.Impersonable {
		t.Errorf("Resolve(Impersonable()) = %+v, want Impersonable true", got)
	}
}

// TestANilOptionIsNoAction holds that a nil option neither panics nor changes
// the result, whichever side of a real option it sits on.
func TestANilOptionIsNoAction(t *testing.T) {
	if got := Resolve(nil); got.Impersonable {
		t.Errorf("Resolve(nil) = %+v, want Impersonable false", got)
	}
	if got := Resolve(nil, Impersonable(), nil); !got.Impersonable {
		t.Errorf("Resolve(nil, Impersonable(), nil) = %+v, want Impersonable true", got)
	}
}

// TestResolveStartsFromNothingEachTime holds that one registration's options do
// not leak into the next: the accumulator is per call.
func TestResolveStartsFromNothingEachTime(t *testing.T) {
	_ = Resolve(Impersonable())
	if got := Resolve(); got.Impersonable {
		t.Errorf("a later Resolve() = %+v, want Impersonable false", got)
	}
}
