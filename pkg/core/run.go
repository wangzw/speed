package core

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
)

// Declaration is a module one drive reaches for its declarations alone.
type Declaration struct {
	// Name is the module, by the name it registered under.
	Name string
	// AllResources puts every declaration this module makes in force, overriding the domain a
	// consumption point would otherwise read.
	AllResources bool
}

// Drive states one advance through the lifecycle: how far it goes, which modules take part, and
// what to do once it arrives.
type Drive struct {
	// Depth is the stage the advance stops at.
	// The callbacks of the stages beyond it do not run, and a module never learns which depth it is
	// running under.
	Depth Stage
	// Participants lists, by name, the modules this drive includes.
	// It is a literal list: an empty one includes nothing rather than everything.
	Participants []string
	// DeclaringOnly lists the modules this drive reaches for their declarations alone, with whatever
	// the host adjusted on each.
	DeclaringOnly []Declaration
	// Action runs after the drive has reached Depth and before shutdown begins.
	// Its failure is returned as the primary error, and either way every constructed instance is
	// rolled back.
	Action func(ctx context.Context, reg *Registry) error
}

// Drive advances the lifecycle to d.Depth, runs d.Action, then shuts down and returns.
func (r *Registry) Drive(ctx context.Context, d Drive) error {
	if d.Depth > StageServe {
		panic(fmt.Sprintf("core: Drive asked to advance to stage %d, which is not one of "+
			"them. The depth is a Stage constant, StagePrepare through StageServe; Stop "+
			"and Close are not depths, they run at the end of every drive", uint8(d.Depth)))
	}

	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		panic("core: the registry has been driven more than once. Run and Drive share the " +
			"one drive a registry has, and a shallow one spends it just as a full one " +
			"does; a registry that has been through the lifecycle cannot have its " +
			"instances reused. To run a second command, build another registry with New")
	}
	r.running = true
	r.mu.Unlock()

	if err := r.advance(ctx, d); err != nil {
		return r.shutdown(ctx, err)
	}
	if d.Action == nil {
		if d.Depth == StageServe {
			<-ctx.Done()
		}
		return r.shutdown(ctx, nil)
	}
	var failure error
	if err := d.Action(ctx, r); err != nil {
		failure = fmt.Errorf("core: action: %w", err)
	}
	return r.shutdown(ctx, failure)
}

// Run is one value of Drive: every registered module takes part, the advance goes to StageServe,
// and there is no action, so it runs until ctx is cancelled and then shuts down in the order Drive
// states.
func (r *Registry) Run(ctx context.Context) error {
	r.mu.RLock()
	participants := slices.Clone(r.order)
	r.mu.RUnlock()

	return r.Drive(ctx, Drive{Depth: StageServe, Participants: participants})
}

func (r *Registry) advance(ctx context.Context, d Drive) error {
	names := r.withSupplied(d.Participants)
	participants, err := r.participantModules(names)
	if err != nil {
		return err
	}
	if err = r.recordDeclarationSets(names, d.DeclaringOnly); err != nil {
		return err
	}

	stances, err := r.prepare(ctx, participants)
	if err != nil {
		return err
	}

	final, err := resolveEnablement(stances)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.enablement = final
	r.mu.Unlock()
	writeDiagnostics(os.Stderr, final)

	if d.Depth == StagePrepare {
		return nil
	}

	enabled := make([]Module, 0, len(participants))
	for _, m := range participants {
		if final[m.Name].State != StateDisabled {
			enabled = append(enabled, m)
		}
	}
	declarationsOnly, outside := r.modulesOutside(participants)
	order, err := planConstruction(enabled, participants, declarationsOnly, outside,
		final, r.origins())
	if err != nil {
		return err
	}

	r.mu.Lock()
	r.constructing = true
	r.mu.Unlock()
	for _, name := range order {
		if err := r.construct(ctx, moduleByName(enabled, name)); err != nil {
			return err
		}
	}
	if d.Depth == StageNew {
		return nil
	}

	for stage := StageMigrate; stage <= d.Depth; stage++ {
		for _, name := range order {
			callback := callbackFor(moduleByName(enabled, name), stage)
			if callback == nil {
				continue
			}
			if err := callback(ctx, r, r.instanceOf(name)); err != nil {
				return fmt.Errorf("core: module %q %s: %w", name, stage, err)
			}
		}
	}
	return nil
}

func (r *Registry) recordDeclarationSets(participants []string, declared []Declaration) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	participating := make(map[string]bool, len(participants))
	for _, name := range participants {
		participating[name] = true
	}

	declaringOnly := make(map[string]Declaration, len(declared))
	for _, d := range declared {
		if participating[d.Name] {
			continue
		}
		if _, ok := r.mods[d.Name]; !ok {
			return unknownParticipantError(d.Name)
		}
		at := declaringOnly[d.Name]
		at.Name = d.Name
		at.AllResources = at.AllResources || d.AllResources
		declaringOnly[d.Name] = at
	}

	r.participating = participating
	r.declaringOnly = declaringOnly
	return nil
}

func (r *Registry) withSupplied(names []string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append(slices.Clone(names), sortedKeys(r.supplied)...)
}

func (r *Registry) isSupplied(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.supplied[name]
	return ok
}

func (r *Registry) participantModules(names []string) ([]Module, error) {
	unique := slices.Clone(names)
	slices.Sort(unique)
	unique = slices.Compact(unique)

	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Module, 0, len(unique))
	for _, name := range unique {
		reg, ok := r.mods[name]
		if !ok {
			return nil, unknownParticipantError(name)
		}
		out = append(out, reg.module)
	}
	return out, nil
}

func unknownParticipantError(name string) error {
	return fmt.Errorf("%w: %q", ErrUnknownParticipant, name)
}

func (r *Registry) modulesOutside(participants []Module) (declarationsOnly, outside []Module) {
	taking := make(map[string]bool, len(participants))
	for _, m := range participants {
		taking[m.Name] = true
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, name := range r.sortedNamesLocked() {
		if taking[name] {
			continue
		}
		if r.declarationOf(name) {
			declarationsOnly = append(declarationsOnly, r.mods[name].module)
			continue
		}
		outside = append(outside, r.mods[name].module)
	}
	return declarationsOnly, outside
}

func (r *Registry) prepare(ctx context.Context, participants []Module) ([]stance, error) {
	stances := make([]stance, 0, len(participants))
	for _, m := range participants {
		state := Enablement{}
		supplied := r.isSupplied(m.Name)
		switch {
		case supplied:
			state = Enablement{State: StateEnabled}
		case m.Prepare != nil:
			stated, err := m.Prepare(ctx, r)
			if err != nil {
				return nil, fmt.Errorf("core: module %q Prepare: %w", m.Name, err)
			}
			state = stated
		}
		stances = append(stances, stance{module: m, state: state, supplied: supplied})
	}
	return stances, nil
}

func (r *Registry) construct(ctx context.Context, m Module) error {
	var instance any
	supplied := r.isSupplied(m.Name)
	switch {
	case supplied:
		instance = r.instanceOf(m.Name)
	case m.New != nil:
		product, err := m.New(ctx, r)
		if err != nil {
			return fmt.Errorf("core: module %q New: %w", m.Name, err)
		}
		instance = product
	}

	r.mu.Lock()
	if !supplied && m.New != nil {
		r.instances[m.Name] = instance
	}
	r.lifecycle = append(r.lifecycle, m.Name)
	r.mu.Unlock()

	return assertDelivered(m, instance)
}

func assertDelivered(m Module, instance any) error {
	it := reflect.TypeOf(instance)
	for _, ct := range declaredCapabilities(m) {
		if implements(it, ct) {
			continue
		}
		var got string
		switch {
		case it != nil:
			got = typeName(it)
		case m.New == nil:
			got = "nothing, because the module has no New callback and the host supplied no " +
				"instance for it: its instance can only come from the host, which hands it " +
				"in with Supply before the drive"
		default:
			got = "nothing, because the module's New callback returned no product"
		}
		return fmt.Errorf("%w: module %q declares capability %s in Provides but constructed %s",
			ErrUndeliveredCapability, m.Name, typeName(ct), got)
	}
	return nil
}
