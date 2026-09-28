package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// shutdown runs Stop and then Close over every constructed instance and
// returns the primary error with whatever the cleanup added.
//
// Stop runs over the entry points first -- the modules that declare a Serve
// callback -- and over every other module after them, each group in reverse
// construction order. Close runs once every Stop has returned, in reverse
// construction order with no grouping.
//
// The entry points go first for the reason Serve is a stage of its own. A
// request an entry point admitted can reach any module in the process, not
// only the ones the entry point depends on, and the modules it reaches have
// no construction-time edge that puts them on either side of it: a registrant
// requiring the entry point's capability is constructed after it, and one that
// takes it up in Init lands wherever the registry linearises what the
// declarations leave open. Reverse construction order alone would stop some of
// them while the entry point is still draining, and they would refuse the work
// of a request it admitted. The Serve field is how an entry point is
// recognised: the registry interprets that field already, and needs no
// module's name to do it. Within each group a module is still stopped before
// the modules it depends on.
//
// It closes every drive, whatever depth that drive asked for and whether it
// arrived or failed on the way: the instances a drive built are the
// instances it releases.
//
// Every constructed instance is covered whatever stage the failure happened
// in. Rolling back only the modules that completed the failing stage would
// leave behind the ones constructed earlier that never got that far, holding
// connections and goroutines nobody releases. That is also why Stop and Close
// have to tolerate an instance that was never initialised or started.
//
// A supplied product is not in range. It was constructed by the party that
// handed it in, never entered the lifecycle list, and is released by that
// same party; closing it here would shut down something the host still holds.
//
// Stop and Close run on a context stripped of cancellation. On the normal
// path shutdown is reached because the host's context was cancelled, and a
// drain that honours its context ends the moment it is handed one that is
// already done: a module whose Stop is an http.Server.Shutdown(ctx) would
// report ctx.Err() without draining anything, and an ordinary stop would come
// back as a failure. Values carry over, so a module still reaches whatever the
// host attached to the context it passed to Run.
//
// A Stop failure does not abort the stage: a failed drain notice must not hold
// back the Close that follows. Cleanup failures are aggregated behind the
// primary error rather than replacing it, because what terminated the startup
// is the first failure, not a secondary one on the way out. With no primary
// error and a clean cleanup the result is nil: cancellation is the normal way
// to stop, and reporting it as an error would make every host special-case a
// move it just made itself.
func (r *Registry) shutdown(ctx context.Context, primary error) error {
	ctx = context.WithoutCancel(ctx)

	r.mu.RLock()
	order := slices.Clone(r.lifecycle)
	r.mu.RUnlock()
	slices.Reverse(order)

	failures := []error{primary}
	for _, name := range r.entryPointsFirst(order) {
		m, ok := r.Lookup(name)
		if !ok || m.Stop == nil {
			continue
		}
		if err := m.Stop(ctx, r, r.instanceOf(name)); err != nil {
			failures = append(failures, fmt.Errorf("core: module %q Stop: %w", name, err))
		}
	}
	for _, name := range order {
		m, ok := r.Lookup(name)
		if !ok || m.Close == nil {
			continue
		}
		if err := m.Close(ctx, r, r.instanceOf(name)); err != nil {
			failures = append(failures, fmt.Errorf("core: module %q Close: %w", name, err))
		}
	}
	return errors.Join(failures...)
}

// entryPointsFirst moves the modules that declare a Serve callback ahead of
// every other module, keeping the given order within each group.
func (r *Registry) entryPointsFirst(order []string) []string {
	entries := make([]string, 0, len(order))
	rest := make([]string, 0, len(order))
	for _, name := range order {
		if m, ok := r.Lookup(name); ok && m.Serve != nil {
			entries = append(entries, name)
			continue
		}
		rest = append(rest, name)
	}
	return append(entries, rest...)
}

// instanceOf returns the product a module was constructed with, or nil for a
// module that has no New callback.
func (r *Registry) instanceOf(name string) any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.instances[name]
}
