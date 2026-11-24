package db

import (
	"context"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

type dependencyProvider struct{}

func (dependencyProvider) probe() {}

func providerModule(name string) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: (*probeCapability)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return dependencyProvider{}, nil },
	}
}

func dependantModule(name string) core.Module {
	return core.Module{
		Name:     name,
		Requires: []core.Requirement{{Token: (*probeCapability)(nil)}},
	}
}

// TestTheOrderComesFromRequiresNotFromNames pins what decides the order: a module's dependency on
// another, not its name.
func TestTheOrderComesFromRequiresNotFromNames(t *testing.T) {
	rank := dependencyRank(registryOf(dependantModule("alpha"), providerModule("zeta")))

	if rank["zeta"] >= rank["alpha"] {
		t.Errorf("zeta is ranked %d and alpha %d, want the module whose capability alpha took up "+
			"placed first", rank["zeta"], rank["alpha"])
	}

	names := []string{"alpha", "zeta"}
	inDependencyOrder(rank, names)
	if want := []string{"zeta", "alpha"}; !slices.Equal(names, want) {
		t.Errorf("the modules come out as %v, want %v", names, want)
	}
}

// TestTheLoggingModuleIsPlacedAheadOfTheDatabaseModuleThatTakesItUp pins the edge the database
// module's optional requirement of the logging capability puts in the order.
func TestTheLoggingModuleIsPlacedAheadOfTheDatabaseModuleThatTakesItUp(t *testing.T) {
	app := core.Module{
		Name:     "app",
		Requires: []core.Requirement{{Token: (*Database)(nil)}},
	}

	rank := dependencyRank(registryOf(app, NewModule(probeSpec()), log.Module()))

	if len(rank) != 3 {
		t.Fatalf("the ordering placed %d of the 3 registered modules: %v", len(rank), rank)
	}
	names := []string{"app", probeNamespace, log.ModuleName}
	inDependencyOrder(rank, names)
	if want := []string{log.ModuleName, probeNamespace, "app"}; !slices.Equal(names, want) {
		t.Errorf("the modules come out as %v, want %v: the logging module first, the database module "+
			"that takes its logger up second, and the module that requires the database last", names, want)
	}
}

// TestModulesWithNoRelationBetweenThemKeepNameOrder pins the fallback, which is the whole of the
// order between two modules that declared nothing about each other.
func TestModulesWithNoRelationBetweenThemKeepNameOrder(t *testing.T) {
	rank := map[string]int{"alpha": 0, "middle": 0, "zeta": 0}
	names := []string{"zeta", "middle", "alpha"}

	inDependencyOrder(rank, names)

	if want := []string{"alpha", "middle", "zeta"}; !slices.Equal(names, want) {
		t.Errorf("modules at the same place come out as %v, want the name order %v", names, want)
	}
}

type cycleCapability interface{ cycle() }

// TestAModuleOnACycleIsPlacedBehindEverythingElse pins what the ordering does with a registry it
// cannot fully order.
func TestAModuleOnACycleIsPlacedBehindEverythingElse(t *testing.T) {
	alpha := core.Module{
		Name:     "alpha",
		Provides: []core.Provision{{Token: (*probeCapability)(nil)}},
		Requires: []core.Requirement{{Token: (*cycleCapability)(nil)}},
	}
	zeta := core.Module{
		Name:     "zeta",
		Provides: []core.Provision{{Token: (*cycleCapability)(nil)}},
		Requires: []core.Requirement{{Token: (*probeCapability)(nil)}},
	}

	rank := dependencyRank(registryOf(alpha, zeta))

	if len(rank) != 2 {
		t.Fatalf("the ordering placed %d of the 2 registered modules: %v. A module left out of it "+
			"keeps its migrations and its plugins out of the run without a word", len(rank), rank)
	}
	names := []string{"zeta", "alpha"}
	inDependencyOrder(rank, names)
	if want := []string{"alpha", "zeta"}; !slices.Equal(names, want) {
		t.Errorf("the modules on the cycle come out as %v, want the name order %v", names, want)
	}
}
