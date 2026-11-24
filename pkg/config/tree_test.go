package config

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

type treeSink interface{ drain() }

type treeProduct struct{ name string }

func (treeProduct) drain() {}

type treeAddr struct{ Addr string }

type treeRun struct{ steps []string }

func (r *treeRun) note(step string) { r.steps = append(r.steps, step) }

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

func treeRegistry(t *testing.T, run *treeRun, content map[string]any) *core.Registry {
	t.Helper()
	reg := core.New()
	reg.Register(Module())
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

func treeApp(reg *core.Registry, args ...string) (*App, *strings.Builder, *strings.Builder) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	a := NewApp(reg).withArgs(args...).withStreams(stdout, stderr)
	a.root.use = "app"
	return a, stdout, stderr
}

func (p *plan) selected(args []string) *planNode {
	found, _, _ := p.root.cmd.Find(args)
	if node, ok := p.byCommand[found]; ok {
		return node
	}
	return p.root
}

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

// TestParticipantsAccumulateDownThePath checks that participants accumulate down the path.
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

// TestParticipantsExpandTheDependencyClosure checks that participants expand the dependency closure.
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

// TestIncludeAllTakesInEveryAvailableModule checks that include all takes in every available module.
func TestIncludeAllTakesInEveryAvailableModule(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	all := a.Root().Sub("all")
	all.IncludeAll()
	all.Do(core.StagePrepare, nil)

	got := planOf(t, a).selected([]string{"all"}).participants
	want := []string{"cache", "config", "format", "greeter", "host", "logging", "source"}
	if !slices.Equal(got, want) {
		t.Fatalf("the command assembles %v, want every registered module %v", got, want)
	}
}

// TestModuleTakenInByTwoLevelsAppearsOnce checks that module taken in by two levels appears once.
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

// TestCommandTakingNothingInHasAnEmptyParticipatingSet checks that command taking nothing in has an empty participating set.
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

// TestIncludeOfAnUnregisteredModuleNamesTheCommand checks that include of an unregistered module names the command.
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

// TestLeafWithoutDoIsRefused checks that leaf without do is refused.
func TestLeafWithoutDoIsRefused(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	a.Root().Sub("serve").Include("cache")

	err := compileFails(t, a)
	if !strings.Contains(err.Error(), "app serve") {
		t.Fatalf("the failure reads %q, which does not name the command missing its Do", err)
	}
}

// TestCommandWithSubcommandsNeedsNoDo checks that command with subcommands needs no do.
func TestCommandWithSubcommandsNeedsNoDo(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil))
	cluster := a.Root().Sub("cluster")
	cluster.Sub("add").Do(core.StagePrepare, nil)

	if got := planOf(t, a).selected([]string{"cluster"}).decl.path(); got != "app cluster" {
		t.Fatalf("the grouping command assembled as %q", got)
	}
}

// TestAllNodesWalksEveryCommandShallowestFirst checks that all nodes walks every command shallowest first.
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

// TestALevelRegistersWhatTheLevelsAboveItDoNot checks that a level registers what the levels above it do not.
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

// TestTheLocatorVariableIsReadFromTheProcessEnvironment checks that the locator variable is read from the process environment.
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

// TestTheReservedArgumentCarriesItsOwnPlaceholder checks that the reserved argument carries its own placeholder.
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
