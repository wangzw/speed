package core

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const thisPackage = "github.com/vislake/speed/pkg/core"

// TestPackageOfReadsTheImportPathFromARuntimeFunctionName pins both halves of the reading: where
// the path ends in the symbol, and what it looks like once it does.
func TestPackageOfReadsTheImportPathFromARuntimeFunctionName(t *testing.T) {
	for _, c := range []struct{ name, function, want string }{
		{"a function", thisPackage + ".Register", thisPackage},
		{"a method", thisPackage + ".(*Registry).Register", thisPackage},
		{"a function literal", thisPackage + "_test.TestX.func1", thisPackage + "_test"},
		{"an init function", "github.com/vislake/speed/pkg/kv/memory.init.0", "github.com/vislake/speed/pkg/kv/memory"},
		{"a generic function", thisPackage + ".Resolve[...]", thisPackage},
		{"the main package", "main.main", "main"},
		{"a dot in the last element", "example.com/lib/gopkg.in/yaml%2ev3.Register", "example.com/lib/gopkg.in/yaml.v3"},
		{"a dot in the last element, a method", "example.com/lib/yaml%2ev3.(*Doc).Method", "example.com/lib/yaml.v3"},
		{"a dot in the last element, a generic function", "example.com/lib/yaml%2ev3.Register[...]", "example.com/lib/yaml.v3"},
		{"a dot in the last element, a function literal", "example.com/lib/yaml%2ev3.Register.func1", "example.com/lib/yaml.v3"},
		{"a path of one element", "gopkg%2ein.Register", "gopkg.in"},
		{"a dot in an earlier element", "example.com/a.b/c.Register", "example.com/a.b/c"},
		{"bytes outside ASCII", "example.com/caf%c3%a9.Register", "example.com/café"},
		{"a percent sign", "example.com/100%25.Register", "example.com/100%"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := packageOf(c.function); got != c.want {
				t.Fatalf("packageOf(%q) is %q, want %q", c.function, got, c.want)
			}
		})
	}
}

// TestUnescapeSymbolLeavesWhatIsNotAnEscapeAlone pins that only a percent sign followed by two
// hexadecimal digits is undone.
func TestUnescapeSymbolLeavesWhatIsNotAnEscapeAlone(t *testing.T) {
	for in, want := range map[string]string{
		"plain/path":  "plain/path",
		"a%2eb":       "a.b",
		"a%2Eb":       "a.b",
		"%2e":         ".",
		"a%2":         "a%2",
		"a%":          "a%",
		"%":           "%",
		"a%zzb":       "a%zzb",
		"a%+1b":       "a%+1b",
		"%%2e":        "%.",
		"%25":         "%",
		"%2e%2e%2f":   "../",
		"trailing%2e": "trailing.",
	} {
		if got := unescapeSymbol(in); got != want {
			t.Errorf("unescapeSymbol(%q) is %q, want %q", in, got, want)
		}
	}
}

// TestCallerPackageIsThePackageOfTheCaller pins what a site is: the package of the function that
// called the exported method, in the form an import writes.
func TestCallerPackageIsThePackageOfTheCaller(t *testing.T) {
	if got := callerPackage(2); got != thisPackage {
		t.Fatalf("callerPackage reports %q for a call made from this test, want %q", got, thisPackage)
	}
}

type sampleStruct struct{ n int }

type sampleMap map[string]int

// TestTypePackageNamesTheDefiningPackageOfTheNamedTypeBehindThePointers pins the package a claim
// compares with the registration packages.
func TestTypePackageNamesTheDefiningPackageOfTheNamedTypeBehindThePointers(t *testing.T) {
	for _, c := range []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{"a named struct", reflect.TypeOf(sampleStruct{}), thisPackage},
		{"a pointer to it", reflect.TypeOf(&sampleStruct{}), thisPackage},
		{"a pointer to a pointer", reflect.TypeOf(new(*sampleStruct)), thisPackage},
		{"a named map", reflect.TypeOf(sampleMap{}), thisPackage},
		{"a type from another package", reflect.TypeOf(&bytes.Buffer{}), "bytes"},
		{"a named type of the standard library", reflect.TypeOf(time.Second), "time"},
		{"a built-in type", reflect.TypeOf(0), ""},
		{"a pointer to a built-in type", reflect.TypeOf(new(int)), ""},
		{"a slice of a named type", reflect.TypeOf([]sampleStruct{}), ""},
		{"a map written out in place", reflect.TypeOf(map[string]sampleStruct{}), ""},
		{"a function type", reflect.TypeOf(func() {}), ""},
		{"a struct written out in place", reflect.TypeOf(struct{ sampleStruct }{}), ""},
		{"a pointer to a struct written out in place", reflect.TypeOf(&struct{ sampleStruct }{}), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := typePackage(c.typ); got != c.want {
				t.Fatalf("typePackage(%s) is %q, want %q", c.typ, got, c.want)
			}
		})
	}
}

type fieldFirstSample struct {
	first sampleStruct
}

type zeroSizeSample struct{}

// TestObjectOfGivesAnIdentityOnlyToAReferenceToAnObject pins which instances the same-object check
// can speak about.
func TestObjectOfGivesAnIdentityOnlyToAReferenceToAnObject(t *testing.T) {
	shared := &sampleStruct{n: 1}
	sharedMap := sampleMap{"k": 1}
	sharedChan := make(chan int)

	for _, c := range []struct {
		name     string
		instance any
		has      bool
	}{
		{"a pointer to a struct", shared, true},
		{"a pointer to a pointer", new(*sampleStruct), true},
		{"a non-nil map", sharedMap, true},
		{"a non-nil channel", sharedChan, true},
		{"a pointer to a zero-size struct", &zeroSizeSample{}, false},
		{"a pointer to a zero-size array", new([0]int), false},
		{"a nil pointer", (*sampleStruct)(nil), false},
		{"a nil map", sampleMap(nil), false},
		{"a nil channel", (chan int)(nil), false},
		{"a struct value", sampleStruct{n: 1}, false},
		{"a zero-size value", zeroSizeSample{}, false},
		{"a slice", []int{1}, false},
		{"a function", func() {}, false},
		{"an integer", 7, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, got := objectOf(c.instance); got != c.has {
				t.Fatalf("objectOf reports an identity for %s: %v, want %v", c.name, got, c.has)
			}
		})
	}

	first, _ := objectOf(shared)
	again, _ := objectOf(shared)
	if first != again {
		t.Error("one pointer gave two identities")
	}
	if other, _ := objectOf(&sampleStruct{n: 1}); other == first {
		t.Error("two pointers to equal structs share an identity: equal contents are not one object")
	}
	if a, _ := objectOf(sharedMap); a != mustObject(t, sharedMap) {
		t.Error("one map gave two identities")
	}
	if other := mustObject(t, sampleMap{"k": 1}); other == mustObject(t, sharedMap) {
		t.Error("two equal maps share an identity")
	}
	if a, b := mustObject(t, sharedChan), mustObject(t, make(chan int)); a == b {
		t.Error("two channels share an identity")
	}
}

func mustObject(t *testing.T, instance any) objectKey {
	t.Helper()
	key, ok := objectOf(instance)
	if !ok {
		t.Fatalf("objectOf reports no identity for %#v", instance)
	}
	return key
}

// TestObjectOfSeparatesAStructFromItsFirstField pins the case the dynamic type is part of the
// identity for.
func TestObjectOfSeparatesAStructFromItsFirstField(t *testing.T) {
	outer := &fieldFirstSample{}
	inner := &outer.first
	if reflect.ValueOf(outer).Pointer() != reflect.ValueOf(inner).Pointer() {
		t.Fatal("the first field of a struct does not share its address, so this test pins nothing")
	}

	if mustObject(t, outer) == mustObject(t, inner) {
		t.Fatal("a pointer to a struct and a pointer to its first field share an identity")
	}
}

// TestCapabilityListReadsAsAPhrase pins the wording a message builds from the capabilities an
// instance lacks, one and several.
func TestCapabilityListReadsAsAPhrase(t *testing.T) {
	cache := reflect.TypeOf((*cacheCap)(nil)).Elem()
	store := reflect.TypeOf((*storeCap)(nil)).Elem()
	reader := reflect.TypeOf((*readerCap)(nil)).Elem()
	name := func(ct reflect.Type) string { return typeName(ct) }

	for _, c := range []struct {
		cts  []reflect.Type
		want string
	}{
		{[]reflect.Type{cache}, "capability " + name(cache)},
		{[]reflect.Type{cache, store}, "capabilities " + name(cache) + " and " + name(store)},
		{[]reflect.Type{cache, store, reader}, "capabilities " + name(cache) + ", " + name(store) + " and " + name(reader)},
	} {
		if got := capabilityList(c.cts); got != c.want {
			t.Errorf("capabilityList is %q, want %q", got, c.want)
		}
	}
}

// TestInstanceTypeNameKeepsTheImportPathBehindPointers checks that the instance type name keeps the
// package path and adds one * per pointer level.
func TestInstanceTypeNameKeepsTheImportPathBehindPointers(t *testing.T) {
	for _, c := range []struct {
		typ  reflect.Type
		want string
	}{
		{reflect.TypeOf(sampleStruct{}), thisPackage + ".sampleStruct"},
		{reflect.TypeOf(&sampleStruct{}), "*" + thisPackage + ".sampleStruct"},
		{reflect.TypeOf(new(*sampleStruct)), "**" + thisPackage + ".sampleStruct"},
		{reflect.TypeOf(&bytes.Buffer{}), "*bytes.Buffer"},
	} {
		if got := instanceTypeName(c.typ); got != c.want {
			t.Errorf("instanceTypeName(%s) is %q, want %q", c.typ, got, c.want)
		}
	}
}

// TestAssertDeliveredTellsAMissingCallbackFromAnEmptyOne checks that a module with no New is told to
// supply an instance, while a New that returned nothing is told it returned no product.
func TestAssertDeliveredTellsAMissingCallbackFromAnEmptyOne(t *testing.T) {
	withoutNew := Module{Name: "host-only", Provides: provides((*cacheCap)(nil))}
	hollow := Module{
		Name:     "hollow",
		Provides: provides((*cacheCap)(nil)),
		New:      func(context.Context, *Registry) (any, error) { return nil, nil },
	}

	err := assertDelivered(withoutNew, nil)
	if !errors.Is(err, ErrUndeliveredCapability) {
		t.Fatalf("assertDelivered returned %v for a module without New, want ErrUndeliveredCapability", err)
	}
	for _, want := range []string{`"host-only"`, "no New callback", "supplied no instance", "Supply"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "returned no product") {
		t.Errorf("error %q describes a New callback that returned nothing, for a module that has none", err)
	}

	err = assertDelivered(hollow, nil)
	if !errors.Is(err, ErrUndeliveredCapability) {
		t.Fatalf("assertDelivered returned %v for a New that returned nothing, want ErrUndeliveredCapability", err)
	}
	if !strings.Contains(err.Error(), "returned no product") {
		t.Errorf("error %q does not say the New callback returned no product", err)
	}
	for _, wrong := range []string{"no New callback", "Supply"} {
		if strings.Contains(err.Error(), wrong) {
			t.Errorf("error %q says %q of a module whose New callback exists, which sends the reader "+
				"after a fix that changes nothing", err, wrong)
		}
	}
}

// TestAssertDeliveredAcceptsAnInstanceThatDeliversEveryCapability checks that an instance
// delivering every declared capability passes, while one delivering none is refused.
func TestAssertDeliveredAcceptsAnInstanceThatDeliversEveryCapability(t *testing.T) {
	both := Module{Name: "both", Provides: provides((*cacheCap)(nil), (*storeCap)(nil))}
	if err := assertDelivered(both, &product{id: "p"}); err != nil {
		t.Errorf("assertDelivered refused an instance that implements both capabilities: %v", err)
	}
	if err := assertDelivered(both, sampleStruct{}); !errors.Is(err, ErrUndeliveredCapability) {
		t.Errorf("assertDelivered returned %v for an instance that implements neither, want "+
			"ErrUndeliveredCapability", err)
	}
	if err := assertDelivered(Module{Name: "declares-nothing"}, nil); err != nil {
		t.Errorf("assertDelivered refused a nil instance for a module that declares nothing: %v", err)
	}
}
