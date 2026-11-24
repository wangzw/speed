package http

import (
	"log/slog"
	nethttp "net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// TestSelfReferentialConstraintIsReportedUnderItsOwnCause pins the reporting half, through the
// surface a registrant reaches.
func TestSelfReferentialConstraintIsReportedUnderItsOwnCause(t *testing.T) {
	tr := &trace{}
	logger, records := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{
			Name:     "recover",
			Stage:    StageObserve,
			Provides: []core.Token{(*capRecovery)(nil)},
			After:    []core.Token{(*capRecovery)(nil)},
			Handler:  noteLayer(tr, "recover"),
		})
		e.Route("GET", "/things", filler)
	})

	var reported []string
	for _, line := range records.at(slog.LevelWarn) {
		if strings.Contains(line, "middleware=recover") {
			reported = append(reported, line)
		}
	}
	if len(reported) != 1 {
		t.Fatalf("the constraint of a layer standing only for itself was reported as %v, "+
			"want exactly one diagnostic for it", reported)
	}
	mustContain(t, reported[0], "cause="+string(causeSelfOnly),
		"that the declaring layer is the only layer standing for the capability, which is "+
			"corrected by rewriting the declaration rather than by finding a module")
	mustContain(t, reported[0], typeName(reflect.TypeOf((*capRecovery)(nil)).Elem()),
		"the capability the declaration names")
	mustContain(t, reported[0], "After[0]", "which declaration on that layer placed nothing")
	mustContain(t, reported[0], "endpoint=public", "the endpoint the declaration was made on")

	serveOnce(handler, nethttp.MethodGet, "/things")
	if got := tr.seen(); !equalStrings(got, []string{"recover"}) {
		t.Errorf("the chain ran %v, and a constraint that placed nothing leaves the layer "+
			"running where its stage and position put it", got)
	}
}

// TestSelfReferentialConstraintWithAnotherProviderIsNotReported checks that a self-referential
// constraint is not reported when another layer provides the capability.
func TestSelfReferentialConstraintWithAnotherProviderIsNotReported(t *testing.T) {
	tr := &trace{}
	logger, records := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{
			Name:     "warden",
			Stage:    StageIdentify,
			Provides: []core.Token{(*capAuth)(nil)},
			After:    []core.Token{(*capAuth)(nil)},
			Handler:  noteLayer(tr, "warden"),
		})
		e.Use(Middleware{
			Name:     "auth",
			Stage:    StageIdentify,
			Provides: []core.Token{(*capAuth)(nil)},
			Handler:  noteLayer(tr, "auth"),
		})
		e.Route("GET", "/things", filler)
	})

	reported := unlandedLines(append(records.at(slog.LevelInfo), records.at(slog.LevelWarn)...))
	for _, line := range reported {
		if strings.Contains(line, "middleware=warden") {
			t.Errorf("the constraint of warden was reported as landing on nothing, and another "+
				"layer on this endpoint stands for the capability it names, so the edge to that "+
				"one places it:\n%s", line)
		}
	}

	serveOnce(handler, nethttp.MethodGet, "/things")
	if got, want := tr.seen(), []string{"auth", "warden"}; !equalStrings(got, want) {
		t.Errorf("the chain ran %v, want %v: warden declared itself inside the capability it "+
			"shares with auth, so the constraint places it there however registration order would "+
			"order the two", got, want)
	}
}
