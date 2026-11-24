package http

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
)

type (
	capRecovery interface{ recovery() }
	capAuth     interface{ auth() }
	capTenant   interface{ tenant() }
	capQuota    interface{ quota() }
)

func passThrough(c *gin.Context) { c.Next() }

func registered(name string, stage Stage, order int, provides, after, before []core.Token) layer {
	return registeredLayer(Middleware{
		Name:     name,
		Stage:    stage,
		Order:    order,
		Provides: provides,
		After:    after,
		Before:   before,
		Handler:  passThrough,
	})
}

func tokens(list ...core.Token) []core.Token { return list }

func layerNames(layers []layer) []string {
	out := make([]string, 0, len(layers))
	for _, l := range layers {
		out = append(out, l.mw.Name)
	}
	return out
}

func solve(t *testing.T, layers []layer) []layer {
	t.Helper()
	out, report, err := orderLayers("public", layers)
	if err != nil {
		t.Fatalf("ordering %v failed: %v", layerNames(layers), err)
	}
	if len(report) != 0 {
		t.Fatalf("ordering %v reported %+v, and these fixtures declare no constraint that "+
			"landed on nothing", layerNames(layers), report)
	}
	return out
}

func hasCause(report []orderingRemark, layer string, cause remarkCause) bool {
	for _, d := range report {
		if d.layer == layer && d.cause == cause {
			return true
		}
	}
	return false
}

// TestStageOrdering pins the two steps that decide a layer's position, as a table: the stage
// boundary first, then the Order and the constraints within a stage.
func TestStageOrdering(t *testing.T) {
	tests := []struct {
		name      string
		layers    []layer
		want      []string
		conflict  []string
		satisfied string
	}{
		{
			name: "registration order against the stage order still assembles in stage order",
			layers: []layer{
				registered("authorize-first", StageAuthorize, 0, nil, nil, nil),
				registered("observe-second", StageObserve, 0, nil, nil, nil),
			},
			want: []string{"observe-second", "authorize-first"},
		},
		{
			name: "a same-stage After is honoured however the two registered",
			layers: []layer{
				registered("inside", StageIdentify, -100, nil, tokens((*capAuth)(nil)), nil),
				registered("auth", StageIdentify, 0, tokens((*capAuth)(nil)), nil, nil),
			},
			want: []string{"auth", "inside"},
		},
		{
			name: "an agreeing cross-stage constraint draws no edge and does not change the chain",
			layers: []layer{
				registered("throttle", StageThrottle, 0, nil, tokens((*capAuth)(nil)), nil),
				registered("auth", StageIdentify, 0, tokens((*capAuth)(nil)), nil, nil),
			},
			want:      []string{"auth", "throttle"},
			satisfied: "throttle",
		},
		{
			name: "a contradicting cross-stage constraint fails, naming both layers",
			layers: []layer{
				registered("observe", StageObserve, 0, nil, tokens((*capAuth)(nil)), nil),
				registered("auth", StageAuthorize, 0, tokens((*capAuth)(nil)), nil, nil),
			},
			conflict: []string{"observe", "auth"},
		},
		{
			name: "Order -100 and 100 place a layer within its stage",
			layers: []layer{
				registered("middle", StageGuard, 0, nil, nil, nil),
				registered("last", StageGuard, 100, nil, nil, nil),
				registered("first", StageGuard, -100, nil, nil, nil),
			},
			want: []string{"first", "middle", "last"},
		},
		{
			name: "an Order -100 layer yields to a same-stage constraint",
			layers: []layer{
				registered("first", StageGuard, -100, nil, tokens((*capAuth)(nil)), nil),
				registered("auth", StageGuard, 0, tokens((*capAuth)(nil)), nil, nil),
			},
			want: []string{"auth", "first"},
		},
		{
			name: "an Order does not move a layer across a stage boundary",
			layers: []layer{
				registered("observe-late-number", StageObserve, 100, nil, nil, nil),
				registered("guard-early-number", StageGuard, -100, nil, nil, nil),
			},
			want: []string{"observe-late-number", "guard-early-number"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, report, err := orderLayers("public", tt.layers)

			if len(tt.conflict) > 0 {
				if !errors.Is(err, ErrStageConflict) {
					t.Fatalf("ordering %v returned %v, want one wrapping ErrStageConflict: a "+
						"constraint across stages that points against the stage order is a "+
						"startup failure, not an edge", layerNames(tt.layers), err)
				}
				if out != nil {
					t.Errorf("a failed ordering returned a chain of %v, want none", layerNames(out))
				}
				for _, name := range tt.conflict {
					if !strings.Contains(err.Error(), `"`+name+`"`) {
						t.Errorf("the failure does not name %q, so the reader cannot see which two "+
							"layers the constraint tied together: %v", name, err)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("ordering %v failed: %v", layerNames(tt.layers), err)
			}
			if got := layerNames(out); !slices.Equal(got, tt.want) {
				t.Errorf("chain is %v, want %v", got, tt.want)
			}
			if tt.satisfied != "" {
				if !hasCause(report, tt.satisfied, causeStageSatisfied) {
					t.Errorf("the report %+v does not record %q's constraint as already given by "+
						"the stage order, which is the only place the redundancy shows",
						report, tt.satisfied)
				}
			} else if len(report) != 0 {
				t.Errorf("the ordering reported %+v, and this fixture declares nothing that needs "+
					"a diagnostic", report)
			}
		})
	}
}

// TestStageConflictNamesWhereTheStagesPlacedTheLayer pins the direction in the failure text: the
// constraint asked to sit inside the layer it named.
func TestStageConflictNamesWhereTheStagesPlacedTheLayer(t *testing.T) {
	layers := []layer{
		registered("outer", StageObserve, 0, nil, tokens((*capAuth)(nil)), nil),
		registered("inner", StageAuthorize, 0, tokens((*capAuth)(nil)), nil, nil),
	}

	_, _, err := orderLayers("public", layers)
	if !errors.Is(err, ErrStageConflict) {
		t.Fatalf("error is %v, want one wrapping ErrStageConflict", err)
	}
	mustContain(t, err.Error(), "places",
		"that the stage order, and not the constraint, decides which side the layer lands on")
	mustContain(t, err.Error(), "outside",
		"the side the stage order put the declaring layer on, which is the opposite of the side "+
			"the After asked for")
}

// TestOrderDecidesWithinReadySet pins where Order applies: inside the ready set, and nowhere else.
func TestOrderDecidesWithinReadySet(t *testing.T) {
	layers := []layer{
		registered("p", StageAuthorize, 100, tokens((*capRecovery)(nil)), nil, nil),
		registered("q", StageAuthorize, -100, nil, tokens((*capRecovery)(nil)), nil),
		registered("r", StageAuthorize, 0, nil, nil, nil),
	}

	got := layerNames(solve(t, layers))
	want := []string{"r", "p", "q"}
	if !slices.Equal(got, want) {
		t.Errorf("chain is %v, want %v: Order picks within the ready set, so q cannot be "+
			"reached before p leaves it", got, want)
	}
}

// TestConstraintBeatsOrder pins that a constraint outranks Order.
func TestConstraintBeatsOrder(t *testing.T) {
	layers := []layer{
		registered("guarded", StageGuard, -100, nil, tokens((*capAuth)(nil)), nil),
		registered("auth", StageGuard, 0, tokens((*capAuth)(nil)), nil, nil),
	}

	got := layerNames(solve(t, layers))
	want := []string{"auth", "guarded"}
	if !slices.Equal(got, want) {
		t.Errorf("chain is %v, want %v: After is a hard constraint within the stage, and neither "+
			"a small Order nor an earlier registration may place guarded outside auth", got, want)
	}
}

// TestBeforeEdgeReversesDirection pins that Before draws the same edge as After with its ends
// swapped.
func TestBeforeEdgeReversesDirection(t *testing.T) {
	after := []layer{
		registered("auth", StageIdentify, 0, tokens((*capAuth)(nil)), nil, nil),
		registered("peer", StageIdentify, 0, nil, tokens((*capAuth)(nil)), nil),
	}
	before := []layer{
		registered("auth", StageIdentify, 0, tokens((*capAuth)(nil)), nil, nil),
		registered("peer", StageIdentify, 0, nil, nil, tokens((*capAuth)(nil))),
	}

	gotAfter := layerNames(solve(t, after))
	if want := []string{"auth", "peer"}; !slices.Equal(gotAfter, want) {
		t.Errorf("with After the chain is %v, want %v", gotAfter, want)
	}
	gotBefore := layerNames(solve(t, before))
	if want := []string{"peer", "auth"}; !slices.Equal(gotBefore, want) {
		t.Errorf("with Before the chain is %v, want %v: Before places the layer outside the "+
			"capability's providers, which is the After edge reversed", gotBefore, want)
	}
}

// TestEqualOrderKeepsRegistrationOrder pins the final tie-break: layers with equal Order and no
// constraint between them stay in the order they were registered in.
func TestEqualOrderKeepsRegistrationOrder(t *testing.T) {
	layers := []layer{
		registered("gamma", StageRecord, 0, nil, nil, nil),
		registered("alpha", StageRecord, 0, nil, nil, nil),
		registered("beta", StageRecord, 0, nil, nil, nil),
	}

	got := layerNames(solve(t, layers))
	want := []string{"gamma", "alpha", "beta"}
	if !slices.Equal(got, want) {
		t.Errorf("chain is %v, want %v: equal Order falls back to the order the endpoint "+
			"recorded the registrations in", got, want)
	}
}

// TestEqualOrderIsNotReported pins that two layers of one stage with the same Order are not a
// failure and produce no remark at all: the value is a setting with no order between equal ones.
func TestEqualOrderIsNotReported(t *testing.T) {
	layers := []layer{
		registered("outer-claim", StageGuard, -100, nil, nil, nil),
		registered("inner-claim", StageGuard, -100, nil, nil, nil),
	}

	out, report, err := orderLayers("public", layers)
	if err != nil {
		t.Fatalf("two layers sharing an Order is not a failure, got: %v", err)
	}
	if got, want := layerNames(out), []string{"outer-claim", "inner-claim"}; !slices.Equal(got, want) {
		t.Errorf("chain is %v, want %v: equal Order is separated by registration order, which "+
			"is the one thing that breaks the tie", got, want)
	}
	if len(report) != 0 {
		t.Errorf("two layers sharing an Order were reported as %+v, and equal Order carries no "+
			"order to report", report)
	}
}

// TestMissingProviderDropsConstraint pins that a constraint naming a capability no
// layer on this endpoint stands for disappears.
func TestMissingProviderDropsConstraint(t *testing.T) {
	layers := []layer{
		registered("trace", StageObserve, 100, nil, tokens((*capRecovery)(nil)), nil),
		registered("metrics", StageObserve, -100, nil, nil, nil),
	}

	out, report, err := orderLayers("public", layers)
	if err != nil {
		t.Fatalf("a constraint with no provider must not fail the assembly, got: %v", err)
	}
	got := layerNames(out)
	want := []string{"metrics", "trace"}
	if !slices.Equal(got, want) {
		t.Errorf("chain is %v, want %v: with no provider for the capability the constraint "+
			"draws no edge, leaving Order to decide", got, want)
	}

	if len(report) != 1 {
		t.Fatalf("the dropped constraint was reported as %+v, want exactly the one that "+
			"landed on nothing", report)
	}
	only := report[0]
	if only.endpoint != "public" || only.layer != "trace" || only.field != "After" || only.at != 0 {
		t.Errorf("the report locates the constraint at %+v, and it was declared as "+
			"endpoint public, middleware trace, After[0]", only)
	}
	if want := typeName(reflect.TypeOf((*capRecovery)(nil)).Elem()); only.capability != want {
		t.Errorf("the report names the capability %q, want %q: without the type name the "+
			"reader cannot tell which module was meant to stand for it", only.capability, want)
	}
	if only.cause != causeNoProvider {
		t.Errorf("the report gives the cause as %q, and no layer on this endpoint stands for "+
			"the capability: the reader has to go and find the module that should have been here, "+
			"not correct a declaration", only.cause)
	}
}

// TestLandedConstraintIsNotReported is the other half.
func TestLandedConstraintIsNotReported(t *testing.T) {
	layers := []layer{
		registered("auth", StageIdentify, 0, tokens((*capAuth)(nil)), nil, nil),
		registered("tenant", StageIdentify, 0, nil, tokens((*capAuth)(nil)), nil),
		registered("warden", StageIdentify, 0, tokens((*capAuth)(nil)), tokens((*capAuth)(nil)), nil),
	}

	_, report, err := orderLayers("public", layers)
	if err != nil {
		t.Fatalf("ordering failed: %v", err)
	}
	if len(report) != 0 {
		t.Errorf("a constraint with a provider on this endpoint was reported as landing on "+
			"nothing: %+v", report)
	}
}

// TestSelfProvidedConstraintIsReportedUnderItsOwnCause pins a layer that stands for
// the capability it names and is the only layer that does.
func TestSelfProvidedConstraintIsReportedUnderItsOwnCause(t *testing.T) {
	layers := []layer{
		registered("recover", StageGuard, 0,
			tokens((*capRecovery)(nil)),
			tokens((*capRecovery)(nil)),
			nil),
	}

	out, report, err := orderLayers("public", layers)
	if err != nil {
		t.Fatalf("ordering failed: %v", err)
	}
	if got := layerNames(out); !slices.Equal(got, []string{"recover"}) {
		t.Errorf("chain is %v, and the layer still runs whatever its constraint did", got)
	}
	if len(report) != 1 {
		t.Fatalf("the constraint of a layer standing only for itself was reported as %+v, want "+
			"exactly that one", report)
	}
	only := report[0]
	if only.layer != "recover" || only.field != "After" || only.at != 0 {
		t.Errorf("the report locates the constraint at %+v, and it was declared as "+
			"endpoint public, middleware recover, After[0]", only)
	}
	if only.cause != causeSelfOnly {
		t.Errorf("the report gives the cause as %q, and the only layer standing for this "+
			"capability is the one that declared the constraint: its fixing action is to "+
			"correct the declaration, not to go looking for an absent module", only.cause)
	}
}

// TestSelfConstraintIsNotACycle pins that a layer naming a capability it delivers itself is not
// ordered against itself.
func TestSelfConstraintIsNotACycle(t *testing.T) {
	layers := []layer{
		registered("recover", StageGuard, 0,
			tokens((*capRecovery)(nil)),
			tokens((*capRecovery)(nil)),
			tokens((*capRecovery)(nil))),
		registered("trace", StageGuard, 0, nil, nil, nil),
	}

	out, report, err := orderLayers("public", layers)
	if err != nil {
		t.Fatalf("a layer naming a capability it stands for itself must not fail the ordering: %v", err)
	}
	got := layerNames(out)
	want := []string{"recover", "trace"}
	if !slices.Equal(got, want) {
		t.Errorf("chain is %v, want %v", got, want)
	}

	if len(report) != 2 {
		t.Errorf("the layer's two self-referring constraints were reported as %+v, want both "+
			"of them under the self-only cause", report)
	}
	for _, u := range report {
		if u.cause != causeSelfOnly {
			t.Errorf("the constraint on %q was reported with the cause %q, and the layer "+
				"standing only for itself is the case this fixture is", u.layer, u.cause)
		}
	}
}

// TestDuplicateEdgeIsCountedOnce pins that one relation declared from both ends is one edge.
func TestDuplicateEdgeIsCountedOnce(t *testing.T) {
	layers := []layer{
		registered("inner", StageIdentify, 0, tokens((*capAuth)(nil)), tokens((*capTenant)(nil)), nil),
		registered("outer", StageIdentify, 0, tokens((*capTenant)(nil)), nil, tokens((*capAuth)(nil))),
	}

	out, _, err := orderLayers("public", layers)
	if err != nil {
		t.Fatalf("one relation declared from both ends must solve, got: %v", err)
	}
	got := layerNames(out)
	want := []string{"outer", "inner"}
	if !slices.Equal(got, want) {
		t.Errorf("chain is %v, want %v", got, want)
	}
}

// TestCycleNamesEveryMemberAndTheirDeclarations pins what the failure has to carry.
func TestCycleNamesEveryMemberAndTheirDeclarations(t *testing.T) {
	layers := []layer{
		registered("auth", StageAuthorize, 0, tokens((*capAuth)(nil)), tokens((*capTenant)(nil)), nil),
		registered("tenant", StageAuthorize, 0, tokens((*capTenant)(nil)), tokens((*capQuota)(nil)), nil),
		registered("quota", StageAuthorize, 0, tokens((*capQuota)(nil)), tokens((*capAuth)(nil)), nil),
		registered("offcycle", StageAuthorize, 0, nil, nil, nil),
	}

	out, report, err := orderLayers("public", layers)
	if !errors.Is(err, ErrMiddlewareCycle) {
		t.Fatalf("error is %v, want one wrapping ErrMiddlewareCycle", err)
	}
	if out != nil {
		t.Errorf("a failed solve returned a chain of %v, want none: a partial order is not a "+
			"chain anyone may assemble", layerNames(out))
	}
	if report != nil {
		t.Errorf("a failed solve reported %+v; the assembly is being abandoned, and the failure "+
			"is the only thing worth reading", report)
	}

	text := err.Error()
	for _, name := range []string{"auth", "tenant", "quota"} {
		if !strings.Contains(text, `"`+name+`"`) {
			t.Errorf("the cycle report does not name %q, so the reader cannot tell who is on "+
				"the cycle: %s", name, text)
		}
	}
	for _, token := range []core.Token{(*capAuth)(nil), (*capTenant)(nil), (*capQuota)(nil)} {
		declared := typeName(reflect.TypeOf(token).Elem())
		if !strings.Contains(text, declared) {
			t.Errorf("the cycle report does not carry the declaration %s, which is the one the "+
				"reader has to drop: %s", declared, text)
		}
	}
	if strings.Contains(text, "offcycle") {
		t.Errorf("the cycle report names offcycle, which is not on the cycle: %s", text)
	}
}

// TestCycleReportIsDeterministic pins that one graph yields one text.
func TestCycleReportIsDeterministic(t *testing.T) {
	layers := []layer{
		registered("auth", StageAuthorize, 0, tokens((*capAuth)(nil)), tokens((*capTenant)(nil)), nil),
		registered("tenant", StageAuthorize, 0, tokens((*capTenant)(nil)), tokens((*capQuota)(nil)), nil),
		registered("quota", StageAuthorize, 0, tokens((*capQuota)(nil)), tokens((*capAuth)(nil)), nil),
		registered("offcycle", StageAuthorize, 0, nil, nil, nil),
	}

	_, _, err := orderLayers("public", layers)
	if err == nil {
		t.Fatal("the fixture no longer forms a cycle, so nothing is being pinned")
	}
	first := err.Error()
	for i := range 100 {
		_, _, again := orderLayers("public", layers)
		if again == nil {
			t.Fatalf("run %d solved a cyclic graph", i)
		}
		if again.Error() != first {
			t.Fatalf("run %d reported\n\t%s\nand the first run reported\n\t%s", i, again.Error(), first)
		}
	}
}

// TestEqualOrdersSolveTheSameWayEveryRun pins the reproducibility the tie-break buys.
func TestEqualOrdersSolveTheSameWayEveryRun(t *testing.T) {
	build := func() []layer {
		return []layer{
			registered("gamma", StageRecord, 0, nil, nil, nil),
			registered("alpha", StageRecord, 0, nil, nil, nil),
			registered("beta", StageRecord, 0, nil, nil, nil),
			registered("delta", StageRecord, 0, nil, nil, nil),
		}
	}

	first := layerNames(solve(t, build()))
	if want := []string{"gamma", "alpha", "beta", "delta"}; !slices.Equal(first, want) {
		t.Fatalf("the first run solved the equal Orders as %v, want the registration order %v",
			first, want)
	}
	for i := range 100 {
		got := layerNames(solve(t, build()))
		if !slices.Equal(got, first) {
			t.Fatalf("run %d solved the equal Orders as %v, and the first run solved them as %v",
				i, got, first)
		}
	}
}

// TestIllegalConstraintTokenPanicsNamingTheDeclaration pins that an illegal capability token panics
// where it was written.
func TestIllegalConstraintTokenPanicsNamingTheDeclaration(t *testing.T) {
	defer func() {
		raised := recover()
		if raised == nil {
			t.Fatal("an untyped nil in After was accepted, so the layer is ordered against a " +
				"capability that designates nothing")
		}
		text, ok := raised.(string)
		if !ok {
			t.Fatalf("panicked with %T, want the string the capability rules raise", raised)
		}
		for _, want := range []string{`endpoint "public"`, `middleware "trace"`, "After[0]"} {
			if !strings.Contains(text, want) {
				t.Errorf("the panic does not carry %s, so the reader cannot find the "+
					"declaration: %s", want, text)
			}
		}
	}()

	_, _, _ = orderLayers("public", []layer{
		registered("trace", StageGuard, 0, nil, tokens(nil), nil),
	})
}

// TestZeroStagePanicsAtRegistration pins that a layer which left Stage out is refused where it
// registered, naming the layer and the values to write.
func TestZeroStagePanicsAtRegistration(t *testing.T) {
	defer func() {
		raised := recover()
		if raised == nil {
			t.Fatal("a middleware with no Stage was accepted, so the layer has no category and " +
				"would run wherever a default put it")
		}
		text, ok := raised.(string)
		if !ok {
			t.Fatalf("panicked with %T, want the string the registration raises", raised)
		}
		for _, want := range []string{`middleware "unstaged"`, "StageObserve"} {
			if !strings.Contains(text, want) {
				t.Errorf("the panic does not carry %s, so the reader cannot find the call or the "+
					"value to write: %s", want, text)
			}
		}
	}()

	logger, _ := newRecordingLogger()
	_ = mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "unstaged", Handler: passThrough})
	})
}

// TestZeroOrderIsLegal is the counterpart of TestZeroStagePanicsAtRegistration for the other field.
func TestZeroOrderIsLegal(t *testing.T) {
	logger, _ := newRecordingLogger()
	_ = mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "no-order", Stage: StageGuard, Handler: passThrough})
	})
}

// TestNoLayersIsNotAFailure pins the endpoint that nobody registered a middleware on: it assembles,
// with the routing engine as the whole chain.
func TestNoLayersIsNotAFailure(t *testing.T) {
	out, _, err := orderLayers("public", nil)
	if err != nil {
		t.Fatalf("an endpoint with no middleware must solve, got: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("solving no layers produced %v", layerNames(out))
	}
}
