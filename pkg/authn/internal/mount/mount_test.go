package mount

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"

	speedhttp "github.com/vislake/speed/pkg/http"
)

type fake struct{}

func (*fake) Public(string, string, gin.HandlerFunc, ...Option)   {}
func (*fake) Optional(string, string, gin.HandlerFunc, ...Option) {}
func (*fake) Guarded(string, string, gin.HandlerFunc, ...Option)  {}
func (*fake) Callback([]string, string, gin.HandlerFunc)          {}
func (*fake) BaseIn(speedhttp.Scope) string                       { return "" }
func (*fake) Endpoint() string                                    { return "" }
func (*fake) PublicURL() string                                   { return "" }
func (*fake) CSRF() *http.CrossOriginProtection                   { return nil }

var _ Mounter = (*fake)(nil)

// TestMounterHasExactlyTheMethodsTheDesignNames holds that Mounter's method set is
// exactly the fixed list, the other direction of the compile-time assertion.
func TestMounterHasExactlyTheMethodsTheDesignNames(t *testing.T) {
	typ := reflect.TypeFor[Mounter]()

	var got []string
	for m := range typ.Methods() {
		got = append(got, m.Name)
	}
	slices.Sort(got)

	want := []string{"BaseIn", "CSRF", "Callback", "Endpoint", "Guarded", "Optional", "Public", "PublicURL"}
	if !slices.Equal(got, want) {
		t.Errorf("Mounter methods = %v, want %v", got, want)
	}
}

// TestNoOptionLeavesTheRoutePlain holds that a registration with no option is not
// impersonable.
func TestNoOptionLeavesTheRoutePlain(t *testing.T) {
	if got := Resolve(); got.Impersonable {
		t.Errorf("Resolve() = %+v, want Impersonable false", got)
	}
}

// TestImpersonableMarksTheRoute holds that the option reaches the reader.
func TestImpersonableMarksTheRoute(t *testing.T) {
	if got := Resolve(Impersonable()); !got.Impersonable {
		t.Errorf("Resolve(Impersonable()) = %+v, want Impersonable true", got)
	}
}

// TestANilOptionIsNoAction holds that a nil option neither panics nor changes the
// result, whichever side of a real option it sits on.
func TestANilOptionIsNoAction(t *testing.T) {
	if got := Resolve(nil); got.Impersonable {
		t.Errorf("Resolve(nil) = %+v, want Impersonable false", got)
	}
	if got := Resolve(nil, Impersonable(), nil); !got.Impersonable {
		t.Errorf("Resolve(nil, Impersonable(), nil) = %+v, want Impersonable true", got)
	}
}

// TestResolveStartsFromNothingEachTime holds that one call's options do not leak
// into the next.
func TestResolveStartsFromNothingEachTime(t *testing.T) {
	_ = Resolve(Impersonable())
	if got := Resolve(); got.Impersonable {
		t.Errorf("a later Resolve() = %+v, want Impersonable false", got)
	}
}
