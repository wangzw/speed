package http

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"
	"testing"
)

var sentinels = map[string]error{
	"ErrUnknownEndpoint": ErrUnknownEndpoint,
	"ErrUnknownListener": ErrUnknownListener,
	"ErrInvalidConfig":   ErrInvalidConfig,
	"ErrClaimConflict":   ErrClaimConflict,
	"ErrUnclaimedRoute":  ErrUnclaimedRoute,
	"ErrRouteConflict":   ErrRouteConflict,
	"ErrMiddlewareCycle": ErrMiddlewareCycle,
	"ErrStageConflict":   ErrStageConflict,
	"ErrInvalidSpec":     ErrInvalidSpec,
	"ErrListen":          ErrListen,
	"ErrDrainTimeout":    ErrDrainTimeout,
}

// TestSentinelsAreDistinct checks that no two of them are interchangeable, wrapped or bare, and
// that each carries a message.
func TestSentinelsAreDistinct(t *testing.T) {
	for name, err := range sentinels {
		if err == nil {
			t.Fatalf("%s is nil", name)
		}
		if !strings.HasPrefix(err.Error(), "http: ") {
			t.Errorf("%s reads %q; a sentinel names its package so a caller can tell "+
				"whose failure it is reading", name, err.Error())
		}
		wrapped := fmt.Errorf("assembling endpoint %q: %w", "public", err)
		for otherName, other := range sentinels {
			match := errors.Is(wrapped, other)
			if otherName == name && !match {
				t.Errorf("a wrapped %s does not match %s", name, name)
			}
			if otherName != name && match {
				t.Errorf("a wrapped %s also matches %s", name, otherName)
			}
		}
	}
}

// TestSentinelTableHasNoStrayMember reads the sentinels back out of the source and checks the table
// above accounts for every one of them, in both directions.
func TestSentinelTableHasNoStrayMember(t *testing.T) {
	_, files := parseProductionFiles(t)
	var declared []string
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range value.Names {
					if strings.HasPrefix(name.Name, "Err") && name.IsExported() {
						declared = append(declared, name.Name)
					}
				}
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("no exported Err* variable was found in the source, so the check below proved nothing")
	}
	for _, name := range declared {
		if _, ok := sentinels[name]; !ok {
			t.Errorf("the source declares sentinel %s, which the table here does not list. "+
				"Add it, so that it is checked against every other sentinel", name)
		}
	}
	for name := range sentinels {
		if !slices.Contains(declared, name) {
			t.Errorf("the table lists %s, which the source no longer declares", name)
		}
	}
}
