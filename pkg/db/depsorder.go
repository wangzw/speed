package db

import (
	"cmp"
	"maps"
	"reflect"
	"slices"

	"github.com/vislake/speed/pkg/core"
)

func inDependencyOrder(rank map[string]int, names []string) {
	slices.SortFunc(names, func(a, b string) int {
		if order := cmp.Compare(rank[a], rank[b]); order != 0 {
			return order
		}
		return cmp.Compare(a, b)
	})
}

func dependencyRank(reg *core.Registry) map[string]int {
	modules := reg.Modules()
	names := make([]string, 0, len(modules))
	byName := make(map[string]core.Module, len(modules))
	providers := make(map[reflect.Type][]string)
	for _, m := range modules {
		names = append(names, m.Name)
		byName[m.Name] = m
		for _, provision := range m.Provides {
			if ct := capabilityOf(provision.Token); ct != nil {
				providers[ct] = append(providers[ct], m.Name)
			}
		}
	}
	slices.Sort(names)

	edges := make(map[string]map[string]bool, len(names))
	indegree := make(map[string]int, len(names))
	for _, name := range names {
		edges[name] = make(map[string]bool)
		indegree[name] = 0
	}
	for _, name := range names {
		for _, requirement := range byName[name].Requires {
			ct := capabilityOf(requirement.Token)
			if ct == nil {
				continue
			}
			for _, provider := range providers[ct] {
				if provider == name || edges[provider][name] {
					continue
				}
				edges[provider][name] = true
				indegree[name]++
			}
		}
	}

	ready := make([]string, 0, len(names))
	for _, name := range names {
		if indegree[name] == 0 {
			ready = append(ready, name)
		}
	}
	rank := make(map[string]int, len(names))
	placed := make(map[string]bool, len(names))
	for len(ready) > 0 {
		slices.Sort(ready)
		name := ready[0]
		ready = ready[1:]
		rank[name] = len(rank)
		placed[name] = true
		for _, to := range slices.Sorted(maps.Keys(edges[name])) {
			indegree[to]--
			if indegree[to] == 0 {
				ready = append(ready, to)
			}
		}
	}
	// A hand-assembled registry can hold a cycle; ordering is not the place to
	// fail a startup over it, so whatever is left keeps its name order.
	for _, name := range names {
		if !placed[name] {
			rank[name] = len(rank)
		}
	}
	return rank
}

func capabilityOf(token core.Token) reflect.Type {
	rt := reflect.TypeOf(token)
	if rt == nil || rt.Kind() != reflect.Pointer {
		return nil
	}
	return rt.Elem()
}
