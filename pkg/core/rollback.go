package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

func (r *Registry) shutdown(ctx context.Context, primary error) error {
	ctx = context.WithoutCancel(ctx)

	order := r.constructionOrder()
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

func (r *Registry) constructionOrder() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	order := slices.Clone(r.lifecycle)
	var unreached []Module
	for _, name := range sortedKeys(r.supplied) {
		if !slices.Contains(order, name) {
			unreached = append(unreached, r.mods[name].module)
		}
	}
	return append(order, dependencyOrder(unreached)...)
}

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

func (r *Registry) instanceOf(name string) any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.instances[name]
}
