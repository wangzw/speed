package core

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
)

// Declaration is a module one drive reaches for its declarations alone.
//
// Such a module is not constructed: it runs no stage callback, takes no
// stance, and its requirements are not expanded, since expanding them is what
// makes a module's providers be constructed alongside it. What it contributes
// is its resource declarations, and only to a consumption point that reads
// the declaring set or wider.
//
// It carries one adjustment and not two, and the one it leaves out is the
// point. Whether a module's input items reach the command line is decided
// where the command line is written, which is not here: a Drive says what the
// drive does, and this package neither writes a command line nor recognises
// one. A field for it on this type would be a field nothing here could act on
// and nothing here could refuse -- read by no one, silently ignored by any
// Drive built by hand, and asking a reader to work out which of the two it
// was.
type Declaration struct {
	// Name is the module, by the name it registered under.
	Name string
	// AllResources puts every declaration this module makes in force,
	// overriding the domain a consumption point would otherwise read. A
	// point reading the enabled set then sees this module's declarations
	// too, even though the module is not running. There is deliberately
	// no check that the module would have been enabled: the host asked
	// for its declarations regardless, and the price is the host's.
	AllResources bool
}

// Drive states one advance through the lifecycle: how far it goes, which
// modules take part, and what to do once it arrives.
type Drive struct {
	// Depth is the stage the advance stops at. The callbacks of the
	// stages beyond it do not run, and a module never learns which depth
	// it is running under. StagePrepare is the zero value, so a Drive
	// left blank advances as far as the stances and constructs nothing.
	Depth Stage
	// Participants lists, by name, the modules this drive includes. It is
	// a literal list: an empty one includes nothing rather than
	// everything. A command that assembles nothing is an ordinary command
	// — printing the version is one — and a zero value that inverted into
	// its opposite would sooner or later be reached by a command that
	// merely forgot to list anything. "Include everything" is expanded
	// into names where the command tree is written, and so are the
	// providers of what the listed modules require — except a provider
	// that declares the capability exclusively: the expansion rule picks
	// no implementation for the host, so one of those has to be named
	// here, and until it is, a capability every provider of which claims
	// it exclusively has no provider in this drive's participating set.
	//
	// Order does not matter and a repeated name counts once, which is
	// what a module included at two levels of one command path produces.
	// A name no module registered is ErrUnknownParticipant.
	Participants []string
	// DeclaringOnly lists the modules this drive reaches for their
	// declarations alone, with whatever the host adjusted on each.
	//
	// It adds to Participants rather than replacing anything: the
	// declaring set is the two of them together, and it is that set a
	// consumption point reading declarations collects from. A module
	// named in both is a participant, and is not recorded here; building
	// a Drive by hand with a name in both is not an error, it is a
	// participant.
	//
	// It is a literal list on the same terms as Participants, and a name
	// no module registered is ErrUnknownParticipant. Nothing is expanded
	// from it: a module reached this way is not built, so it has no
	// construction-time requirements for the closure to follow.
	DeclaringOnly []Declaration
	// Action runs after the drive has reached Depth and before shutdown
	// begins. Its failure is returned as the primary error, and either
	// way every constructed instance is rolled back. A nil Action at
	// StageServe blocks until ctx is cancelled, which is the shape of
	// serving traffic; at any shallower depth it means arriving there was
	// the whole of this command's work.
	Action func(ctx context.Context, reg *Registry) error
}

// Drive advances the lifecycle to d.Depth, runs d.Action, then shuts down in
// reverse order and returns.
//
// A failure in any stage terminates the advance, rolls back every constructed
// instance and returns the original error. Cancellation is the normal way to
// stop: with a clean cleanup the result is nil rather than ctx.Err().
//
// The advance is one indivisible process, and an optional depth does not
// change that: what goes in comes out, whatever stage it reached and whether
// or not the action failed. Releasing the instances therefore never depends
// on the host remembering a second call, which is why the depth is a
// parameter rather than a set of separately callable operations.
//
// A registry is driven once. Run and Drive share that one use, and a shallow
// drive spends it as a full one does: it went in and came out. A process that
// runs two commands builds two registries.
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
		// The action belongs to no module and to no stage, so it is
		// named for what it is rather than wrapped as a stage failure.
		failure = fmt.Errorf("core: action: %w", err)
	}
	return r.shutdown(ctx, failure)
}

// Run is one value of Drive: every registered module takes part, the advance
// goes to StageServe, and there is no action, so it runs until ctx is
// cancelled and then shuts down in reverse order.
//
// Cancellation is the normal way to stop: with a clean cleanup the result is
// nil rather than ctx.Err(). Run may be called once, for the reason Drive
// states.
func (r *Registry) Run(ctx context.Context) error {
	r.mu.RLock()
	participants := slices.Clone(r.order)
	r.mu.RUnlock()

	return r.Drive(ctx, Drive{Depth: StageServe, Participants: participants})
}

// advance carries the drive from Prepare to its depth. It returns the first
// failure; the caller rolls back.
//
// Each depth is a place the advance stops, not a variant of it: the earlier
// stages run the same way whatever the depth, and the later ones do not run
// at all.
func (r *Registry) advance(ctx context.Context, d Drive) error {
	participants, err := r.participantModules(d.Participants)
	if err != nil {
		return err
	}
	if err = r.recordDeclarationSets(d.Participants, d.DeclaringOnly); err != nil {
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

	// Dependency resolution is the preliminary to construction, so a
	// drive that constructs nothing has nothing to resolve. A command
	// that only collects stances must not fail over a provider it was
	// never going to build.
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
		final, r.registrationSites(), r.suppliedCapabilities())
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

	// Stage outside, module inside: Serve has to follow every Start, and
	// dependency order gives "after mine", not "after everyone's".
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

// recordDeclarationSets settles the participating and declaring sets for this
// drive and makes them answerable.
//
// Both are recorded before the stances are taken, because both are already
// settled then: the command tree decided them, and nothing in an advance
// changes them. Resolution fills the enablement map later, and that map is a
// narrower thing again -- the participating set, each with the verdict it
// reached.
//
// A module named at both levels is recorded as a participant and not here. It
// runs, so the reading that treats it as declaring alone never arises, and
// keeping it out of the second map is what stops the two from disagreeing.
// A module named twice with different adjustments keeps each of them: the
// host asked for both, and there is no reading under which one of the two
// requests should be dropped.
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

// participantModules turns the names a drive listed into descriptors, in name
// order and without repetition.
//
// An empty list yields no modules. That is the whole of it: the list is
// literal, and reading emptiness as "everything" would turn the command that
// assembles nothing into the one that assembles the most.
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

// unknownParticipantError names what the drive could not find. The guidance is
// the sentinel's own, so that a name refused before it reaches the drive --
// one the command tree checked against the registry itself -- reads the same
// as one refused here.
func unknownParticipantError(name string) error {
	return fmt.Errorf("%w: %q", ErrUnknownParticipant, name)
}

// modulesOutside lists the registered modules this drive did not take in at
// run level, divided into the ones it reached for their declarations alone and
// the rest. A capability whose only provider is among them has no provider
// here, and the failure says so rather than blaming the order.
//
// The division exists for that failure. Both groups declare the capability and
// neither can take it up, and what to do about them differs: one was asked for
// and is not built, so the command's own writing is to be revisited; the other
// was never asked for, so the module belongs on this command.
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

// prepare runs the stance stage over the modules this drive includes.
//
// The stage guarantees no order within itself: a module only reads its own
// configuration and states a stance, and a module that cannot start returns an
// error here, which ends the advance before anything is constructed. No
// module's product exists yet — the only thing to take up is what the host
// supplied before the drive, which is how a module reads its own configuration
// before stating anything.
func (r *Registry) prepare(ctx context.Context, participants []Module) ([]stance, error) {
	stances := make([]stance, 0, len(participants))
	for _, m := range participants {
		state := Enablement{}
		if m.Prepare != nil {
			stated, err := m.Prepare(ctx, r)
			if err != nil {
				return nil, fmt.Errorf("core: module %q Prepare: %w", m.Name, err)
			}
			state = stated
		}
		stances = append(stances, stance{module: m, state: state})
	}
	return stances, nil
}

// construct runs a module's New callback, records the product, and asserts
// that every capability it declared really arrived.
func (r *Registry) construct(ctx context.Context, m Module) error {
	var instance any
	if m.New != nil {
		product, err := m.New(ctx, r)
		if err != nil {
			return fmt.Errorf("core: module %q New: %w", m.Name, err)
		}
		instance = product
	}

	r.mu.Lock()
	if m.New != nil {
		r.instances[m.Name] = instance
	}
	r.lifecycle = append(r.lifecycle, m.Name)
	r.mu.Unlock()

	return assertDelivered(m, instance)
}

// assertDelivered checks the product against the delivery declaration, so a
// declaration that drifted from the code surfaces on the construction that
// produced it rather than on some later module that cannot find a dependency.
//
// A nil instance has two causes, and the message names which one it is: the
// module has no New callback, or its New callback returned no product. The two
// call for different fixes, so reporting a missing callback for both sends half
// the readers after a callback that is not absent.
func assertDelivered(m Module, instance any) error {
	it := reflect.TypeOf(instance)
	for _, ct := range declaredCapabilities(m) {
		if it != nil && it.AssignableTo(ct) {
			continue
		}
		var got string
		switch {
		case it != nil:
			got = typeName(it)
		case m.New == nil:
			got = "nothing, because the module has no New callback"
		default:
			got = "nothing, because the module's New callback returned no product"
		}
		return fmt.Errorf("%w: module %q declares capability %s in Provides but constructed %s",
			ErrUndeliveredCapability, m.Name, typeName(ct), got)
	}
	return nil
}
