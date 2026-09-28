package config

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// treeSink is the capability one fixture module delivers and another
// requires. No case names the provider where it wants the requirer: what a
// participating set does with the dependency between them is the point.
type treeSink interface{ drain() }

// treeProduct is what a fixture module constructs. It answers for treeSink so
// that a drive reaching construction resolves the dependency the same way a
// real one would.
type treeProduct struct{ name string }

func (treeProduct) drain() {}

// treeAddr is the carrier struct every fixture module mounts, so that each of
// them contributes one input item with a command-line name of its own.
type treeAddr struct{ Addr string }

// treeRun records what a drive did. A command's depth and its participating
// set leave no other trace: the set decides who is asked for a stance, and
// the depth decides how far those stances are carried.
type treeRun struct{ steps []string }

func (r *treeRun) note(step string) { r.steps = append(r.steps, step) }

// at lists the modules that reached one stage, in name order.
func (r *treeRun) at(stage string) []string {
	names := []string{}
	for _, step := range r.steps {
		if name, ok := strings.CutSuffix(step, ":"+stage); ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// treeModule builds one fixture module over a shape carrying whatever it
// delivers or requires: it declares an input item under a namespace of its
// own and records every stage it is driven through.
func treeModule(run *treeRun, name string, shape core.Module) core.Module {
	defaults := &treeAddr{Addr: name + ":default"}
	m := shape
	m.Name = name
	m.Resources = append(m.Resources, Schema{
		Namespace: name,
		Mounts:    []Mount{{Value: defaults}},
		Items: map[string]Item{
			"addr": {Origins: OriginPrimary | OriginEnv | OriginFlag, FlagName: name + "-addr"},
		},
	})
	m.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		run.note(name + ":prepare")
		return core.Enablement{State: core.StateEnabled}, nil
	}
	m.New = func(context.Context, *core.Registry) (any, error) {
		run.note(name + ":new")
		return treeProduct{name: name}, nil
	}
	return m
}

// treeRegistry is the fixture the command-tree cases run over: a host
// identity, a transport and a format standing in for the primary source, a
// module delivering the fixture capability, one requiring it, and one
// unrelated to both.
//
// The first three carry resources alone. They are what "every available
// module" has to reach past the ones a host would think to list.
func treeRegistry(t *testing.T, run *treeRun, content map[string]any) *core.Registry {
	t.Helper()
	reg := core.New()
	reg.Register(identifying("host", HostIdentity{Prefix: "MYAPP"}))
	reg.Register(providing("source", fakeSource{scheme: "file", data: []byte("x"), format: "yaml"}))
	reg.Register(providing("format", fakeFormat{name: "yaml", content: content}))
	reg.Register(treeModule(run, "logging", core.Module{
		Provides: []core.Provision{{Token: (*treeSink)(nil)}},
	}))
	reg.Register(treeModule(run, "greeter", core.Module{
		Requires: []core.Requirement{{Token: (*treeSink)(nil)}},
	}))
	reg.Register(treeModule(run, "cache", core.Module{}))
	return reg
}

// treeApp starts a tree over a registry with the process's own arguments and
// streams put aside: a case gives the arguments and reads back what the run
// wrote. The environment stays the process's own, which is where every layer
// reads it; a case that steers a variable writes it with t.Setenv. The root is
// renamed so that the paths a diagnostic renders do not carry the test
// binary's name.
func treeApp(reg *core.Registry, args ...string) (*App, *strings.Builder, *strings.Builder) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	a := NewApp(reg).withArgs(args...).withStreams(stdout, stderr)
	a.root.use = "app"
	return a, stdout, stderr
}

// selected names the node a set of arguments picks, which is how a case
// reads back what one command was assembled with. A run makes the same choice
// through the command-line library, which is the search this borrows. A path
// the arguments do not reach ends at the root.
func (p *plan) selected(args []string) *planNode {
	found, _, _ := p.root.cmd.Find(args)
	if node, ok := p.byCommand[found]; ok {
		return node
	}
	return p.root
}

// planOf assembles the tree the way Execute does, up to the point where the
// command line is read.
func planOf(t *testing.T, a *App) *plan {
	t.Helper()
	l, err := newLoader(a.reg, a.stderr)
	if err != nil {
		t.Fatalf("collecting the declarations failed: %v", err)
	}
	p, err := a.compile(l)
	if err != nil {
		t.Fatalf("assembling the tree failed: %v", err)
	}
	return p
}

// compileFails assembles a tree that is expected to be refused and returns the
// failure.
func compileFails(t *testing.T, a *App) error {
	t.Helper()
	l, err := newLoader(a.reg, a.stderr)
	if err != nil {
		t.Fatalf("collecting the declarations failed: %v", err)
	}
	p, err := a.compile(l)
	if err == nil {
		t.Fatalf("assembling the tree produced %d commands, want a refusal", len(p.byCommand))
	}
	return err
}

// TestParticipantsAccumulateDownThePath pins that what a level takes in holds
// for every command under it: the root's modules are the program's, and a
// deeper level adds to them rather than replacing them.
func TestParticipantsAccumulateDownThePath(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	a.Root().Include("cache")
	serve := a.Root().Sub("serve")
	serve.Include("logging")
	serve.Do(core.StagePrepare, nil)

	p := planOf(t, a)
	if got := p.root.participants; !slices.Equal(got, []string{"cache"}) {
		t.Fatalf("the root assembles %v, want what the root takes in", got)
	}
	if got := p.selected([]string{"serve"}).participants; !slices.Equal(got, []string{"cache", "logging"}) {
		t.Fatalf("serve assembles %v, want both levels of its path", got)
	}
}

// TestParticipantsExpandTheDependencyClosure pins that taking a module in
// takes in the providers of what it requires. The host names one module and
// the drive is handed two, so a command never has to list the modules its own
// dependencies happen to need.
func TestParticipantsExpandTheDependencyClosure(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	greet := a.Root().Sub("greet")
	greet.Include("greeter")
	greet.Do(core.StageNew, nil)

	got := planOf(t, a).selected([]string{"greet"}).participants
	if !slices.Equal(got, []string{"greeter", "logging"}) {
		t.Fatalf("greet assembles %v, want the provider of what greeter requires as well", got)
	}
}

// TestIncludeAllTakesInEveryAvailableModule pins that "every module" means the
// registry's whole roster, the modules carrying resources alone among them.
// They contribute no argument, so nothing the reader has not heard of turns up
// on the command line.
func TestIncludeAllTakesInEveryAvailableModule(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	all := a.Root().Sub("all")
	all.IncludeAll()
	all.Do(core.StagePrepare, nil)

	got := planOf(t, a).selected([]string{"all"}).participants
	want := []string{"cache", "format", "greeter", "host", "logging", "source"}
	if !slices.Equal(got, want) {
		t.Fatalf("the command assembles %v, want every registered module %v", got, want)
	}
}

// TestModuleTakenInByTwoLevelsAppearsOnce pins that the drive is handed a
// deduplicated list. A module named on the root and again on a subcommand is
// one module, and a repeated name would have it asked for a stance twice.
func TestModuleTakenInByTwoLevelsAppearsOnce(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	a.Root().Include("cache")
	serve := a.Root().Sub("serve")
	serve.Include("cache")
	serve.Do(core.StagePrepare, nil)

	if got := planOf(t, a).selected([]string{"serve"}).participants; !slices.Equal(got, []string{"cache"}) {
		t.Fatalf("serve assembles %v, want the module once", got)
	}
}

// TestCommandTakingNothingInHasAnEmptyParticipatingSet pins that a command
// assembling nothing is handed an empty list rather than none: an absent list
// is how a caller asks for every module, and a command that takes nothing in
// means the opposite of that.
func TestCommandTakingNothingInHasAnEmptyParticipatingSet(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	version := a.Root().Sub("version")
	version.Do(core.StagePrepare, nil)

	got := planOf(t, a).selected([]string{"version"}).participants
	if got == nil {
		t.Fatal("the command assembles nil, which a drive reads as every module")
	}
	if len(got) != 0 {
		t.Fatalf("the command assembles %v, want nothing", got)
	}
}

// TestIncludeOfAnUnregisteredModuleNamesTheCommand pins the diagnostic for a
// host that wrote a module name without importing the package that registers
// it. The registry says which name is unknown; which command wrote it is the
// one thing only the tree knows.
func TestIncludeOfAnUnregisteredModuleNamesTheCommand(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	serve := a.Root().Sub("serve")
	serve.Include("telemetry")
	serve.Do(core.StagePrepare, nil)

	err := compileFails(t, a)
	if !errors.Is(err, core.ErrUnknownParticipant) {
		t.Fatalf("a module nothing registered returned %v, want ErrUnknownParticipant", err)
	}
	if !strings.Contains(err.Error(), "app serve") {
		t.Fatalf("the failure reads %q, which does not name the command that wrote it", err)
	}
	if !strings.Contains(err.Error(), "telemetry") {
		t.Fatalf("the failure reads %q, which does not name the module", err)
	}
}

// TestLeafWithoutDoIsRefused pins that a command a run can select states what
// it does. Falling back to a depth would turn the omission into the deepest
// drive there is, which is the one outcome a host that forgot Do did not ask
// for.
func TestLeafWithoutDoIsRefused(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	a.Root().Sub("serve").Include("cache")

	err := compileFails(t, a)
	if !strings.Contains(err.Error(), "app serve") {
		t.Fatalf("the failure reads %q, which does not name the command missing its Do", err)
	}
}

// TestCommandWithSubcommandsNeedsNoDo pins the other side of that rule: a
// command that exists to group others is complete without one, and a run that
// stops at it is answered with the list of what can follow it.
func TestCommandWithSubcommandsNeedsNoDo(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	cluster := a.Root().Sub("cluster")
	cluster.Sub("add").Do(core.StagePrepare, nil)

	if got := planOf(t, a).selected([]string{"cluster"}).decl.path(); got != "app cluster" {
		t.Fatalf("the grouping command assembled as %q", got)
	}
}

// TestAllNodesWalksEveryCommandShallowestFirst pins what the arguments are
// registered over: every command of the tree, not the selected one's path,
// since the search for the selected command reads a command line whose
// arguments have to be registered already. A command comes after the ones
// above it, which is what lets a level register the modules the levels above
// it have not.
func TestAllNodesWalksEveryCommandShallowestFirst(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	a.Root().Sub("cluster").Sub("add").Do(core.StagePrepare, nil)
	a.Root().Sub("serve").Do(core.StagePrepare, nil)
	p := planOf(t, a)

	var walk []string
	for _, node := range p.allNodes() {
		walk = append(walk, node.decl.path())
	}
	want := []string{"app", "app cluster", "app serve", "app cluster add"}
	if !slices.Equal(walk, want) {
		t.Fatalf("the commands are walked as %v, want %v", walk, want)
	}
}

// TestALevelRegistersWhatTheLevelsAboveItDoNot pins where an argument sits:
// on the shallowest command of a path that takes its module in. A deeper
// command inherits it, and a second definition there would shadow the one the
// layering tool is bound to.
func TestALevelRegistersWhatTheLevelsAboveItDoNot(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	a.Root().Include("cache")
	cluster := a.Root().Sub("cluster")
	cluster.Include("greeter")
	cluster.Sub("add").Do(core.StagePrepare, nil)
	p := planOf(t, a)

	by := map[string][]string{}
	for _, node := range p.allNodes() {
		by[node.decl.path()] = node.added()
	}
	if got := by["app"]; !slices.Equal(got, []string{"cache"}) {
		t.Errorf("the root registers %v, want the module it takes in", got)
	}
	if got := by["app cluster"]; !slices.Equal(got, []string{"greeter", "logging"}) {
		t.Errorf("cluster registers %v, want what it adds to the root", got)
	}
	if got := by["app cluster add"]; len(got) != 0 {
		t.Errorf("add registers %v, want nothing: it adds no module of its own", got)
	}
}

// TestTheLocatorVariableIsReadFromTheProcessEnvironment pins which environment
// a run's locator comes from. The values are read out of a snapshot of the
// process's own environment and the unclaimed-variable diagnostic scans that
// snapshot too, so <PREFIX>_CONFIG is read there as well: were it read anywhere
// else, a run would take its primary source from one environment and its
// values from another, and nothing on a normal path would show it.
func TestTheLocatorVariableIsReadFromTheProcessEnvironment(t *testing.T) {
	t.Setenv("MYAPP_CONFIG", "file:///app.yaml")
	content := map[string]any{"cache": map[string]any{"addr": "from-file:1"}}
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, content), "serve")
	serve := a.Root().Sub("serve")
	serve.Include("cache")
	serve.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
		if got := decodeAddr(t, readerFrom(t, reg), "cache"); got != "from-file:1" {
			t.Errorf("cache.addr reads %q, want the file the variable pointed at", got)
		}
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
}

// TestTheReservedArgumentCarriesItsOwnPlaceholder pins that the one argument
// this module reserves reads like the ones the modules declare. Every other
// argument seats its placeholder in the Value handed to the command-line
// library; this one carries no declaration, so the placeholder goes in the
// usage text, and without it the library would name the Go type instead.
func TestTheReservedArgumentCarriesItsOwnPlaceholder(t *testing.T) {
	a, stdout, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "--help")
	a.Root().Sub("serve").Do(core.StagePrepare, nil)

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("asking for help returned %v", err)
	}
	if !strings.Contains(stdout.String(), "--config LOCATOR") {
		t.Fatalf("the help reads %q, which does not give the reserved argument a placeholder "+
			"of its own", stdout)
	}
}
