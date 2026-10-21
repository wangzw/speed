package config

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/core"
)

// The names a declaration may not take. --config is this module's own, and
// --help and -h come from the command-line library, which puts them on every
// command; a declaration taking any of them would silently hijack the config
// locator or the help output.
const (
	locatorFlag      = "config"
	helpFlag         = "help"
	helpShort        = "h"
	locatorEnvSuffix = "_CONFIG"
)

// manifest is the complete set of input items of a run, collected from every
// registered module. Everything downstream reads it: the environment variable
// names are derived from it, the command line is assembled from it, and the
// unknown-key and type checks are made against it.
type manifest struct {
	// items are the input items in collection order: by module name, and
	// within a module in declaration order.
	items []*manifestItem
	// byPath, byFlag, byShort and byEnv index the items by the names each
	// layer addresses them with. An item missing a name is absent from the
	// corresponding index.
	byPath  map[string]*manifestItem
	byFlag  map[string]*manifestItem
	byShort map[string]*manifestItem
	byEnv   map[string]*manifestItem
	// interior maps every path an item's ancestors occupy to the item that
	// put it there, which is what makes a path that is a leaf for one module
	// and an interior node for another detectable.
	interior map[string]*manifestItem
	// sections maps every namespace a module declares, and its ancestors, to the
	// module that declared it. A namespace is a section even when it carries no
	// input items, which is what lets a source that still writes a key under it
	// be refused by that key's full path rather than by the namespace.
	sections map[string]string
	// host is the declared host identity, zero when no module declared one.
	host HostIdentity
	// hasHost records whether a host identity was declared at all, which
	// tells an absent declaration from one that declares empty strings.
	hasHost bool
}

// newManifest collects the declarations of every registered module and merges
// them into one manifest, failing on the first conflict.
//
// The domain is every registered module, including the ones this run will not
// enable: which modules run is not known until they state it, and two
// implementations of the same capability must divide the paths between them
// even though only one of them ever runs.
func newManifest(reg *core.Registry) (*manifest, error) {
	m := &manifest{
		byPath:   make(map[string]*manifestItem),
		byFlag:   make(map[string]*manifestItem),
		byShort:  make(map[string]*manifestItem),
		byEnv:    make(map[string]*manifestItem),
		interior: make(map[string]*manifestItem),
		sections: make(map[string]string),
	}
	if err := m.collectHost(reg); err != nil {
		return nil, err
	}
	for _, declared := range core.Resources[Schema](reg) {
		if err := m.declareSections(declared.Module, declared.Value.Namespace); err != nil {
			return nil, err
		}
		items, err := expandSchema(declared.Module, declared.Value, m.host.Prefix)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if err := m.add(item); err != nil {
				return nil, err
			}
		}
	}
	return m, nil
}

// collectHost takes the host identity, of which there may be at most one: two
// prefixes or two default locators give no ground to choose between them.
func (m *manifest) collectHost(reg *core.Registry) error {
	declared := core.Resources[HostIdentity](reg)
	if len(declared) > 1 {
		return fmt.Errorf("%w: modules %q and %q both declare a HostIdentity, and a run has one "+
			"environment variable prefix and one default config locator. Leave the declaration "+
			"to the host module",
			ErrConfigConflict, declared[0].Module, declared[1].Module)
	}
	if len(declared) == 1 {
		if d := envNameDefect(declared[0].Value.Prefix); d != "" {
			return fmt.Errorf("%w: module %q declares the environment variable prefix %q: %s. "+
				"Every derived name and the config locator's variable begin with it",
				ErrInvalidSchema, declared[0].Module, declared[0].Value.Prefix, d)
		}
		m.host, m.hasHost = declared[0].Value, true
	}
	return nil
}

// declareSections records a module's namespace, and its ancestors, as sections
// of the config data. A namespace is a section whether or not any item sits
// under it: a module whose items have all moved elsewhere still owns the
// section, and a source that writes a key under it is refused by that key's full
// path.
//
// A section and an input item may not share a path: the same path would be a
// value for one module and a section for the other, and no primary source can
// express both. Two modules sharing a namespace is not a conflict, so a section
// meeting a section is allowed; a section meeting a leaf is not.
func (m *manifest) declareSections(module, namespace string) error {
	if namespace == "" {
		return nil
	}
	for _, path := range append(ancestors(namespace), namespace) {
		if item, taken := m.byPath[path]; taken {
			return sectionLeafConflict(item.module, item.path, module, path)
		}
		if _, taken := m.sections[path]; !taken {
			m.sections[path] = module
		}
	}
	return nil
}

// add puts one input item into the manifest, refusing it when it collides with
// an item already there. The item already there is always the one from the
// earlier module in name order, so the two parties of a conflict are named in
// the same order however the modules were registered.
func (m *manifest) add(item *manifestItem) error {
	if err := m.checkPath(item); err != nil {
		return err
	}
	if err := m.checkFlagNames(item); err != nil {
		return err
	}
	if err := m.checkEnvName(item); err != nil {
		return err
	}
	m.items = append(m.items, item)
	m.byPath[item.path] = item
	for _, ancestor := range ancestors(item.path) {
		if _, taken := m.interior[ancestor]; !taken {
			m.interior[ancestor] = item
		}
	}
	if item.item.FlagName != "" {
		m.byFlag[item.item.FlagName] = item
	}
	if item.item.FlagShort != "" {
		m.byShort[item.item.FlagShort] = item
	}
	if item.envName != "" {
		m.byEnv[item.envName] = item
	}
	return nil
}

// checkPath refuses two paths that intersect. Intersecting covers more than
// being equal: one path being a prefix of the other makes the same path a leaf
// for one module and an interior node for the other, and no primary source can
// express both at once.
func (m *manifest) checkPath(item *manifestItem) error {
	if other, taken := m.byPath[item.path]; taken {
		return fmt.Errorf("%w: modules %q and %q both declare the input item %q. Paths carry no "+
			"module name, so two modules under the same namespace have to divide the paths "+
			"between them",
			ErrConfigConflict, other.module, item.module, item.path)
	}
	if section, taken := m.sections[item.path]; taken {
		return sectionLeafConflict(section, item.path, item.module, item.path)
	}
	if other, taken := m.interior[item.path]; taken {
		return pathIntersects(other, other.path, item, item.path)
	}
	for _, ancestor := range ancestors(item.path) {
		if other, taken := m.byPath[ancestor]; taken {
			return pathIntersects(other, ancestor, item, item.path)
		}
	}
	return nil
}

func pathIntersects(first *manifestItem, firstPath string, second *manifestItem, secondPath string) error {
	return fmt.Errorf("%w: module %q declares the input item %q while module %q declares %q, and "+
		"one path is a prefix of the other, so the same path is a value for one of them and a "+
		"section for the other. Give the two declarations separate namespaces or prefixes",
		ErrConfigConflict, first.module, firstPath, second.module, secondPath)
}

// sectionLeafConflict refuses a path one module declares as a section and
// another as an input item. The two parties are named in module-name order so
// that the message reads the same however the modules were registered.
func sectionLeafConflict(sectionModule, sectionPath, itemModule, itemPath string) error {
	first, second := sectionModule, itemModule
	if second < first {
		first, second = second, first
	}
	return fmt.Errorf("%w: modules %q and %q both claim %q, one as a section and the other as an "+
		"input item, so the same path is a value for one of them and a section for the other. "+
		"Give the two declarations separate namespaces or prefixes",
		ErrConfigConflict, first, second, sectionPath)
}

// checkFlagNames refuses a command-line name that is taken, or reserved. The
// command line is one flat namespace, so two items conflict there however far
// apart their paths are.
func (m *manifest) checkFlagNames(item *manifestItem) error {
	switch item.item.FlagName {
	case "":
	case locatorFlag:
		return fmt.Errorf("%w: module %q declares %q as the command-line name of %q, which this "+
			"module keeps for the config locator. Choose another name",
			ErrConfigConflict, item.module, item.item.FlagName, item.path)
	case helpFlag:
		return fmt.Errorf("%w: module %q declares %q as the command-line name of %q, and the "+
			"command-line library provides it on every command. Choose another name",
			ErrConfigConflict, item.module, item.item.FlagName, item.path)
	default:
		if other, taken := m.byFlag[item.item.FlagName]; taken {
			return fmt.Errorf("%w: modules %q and %q both declare the command-line name %q, for "+
				"%q and %q. Command-line names are global, whatever namespaces the items sit in",
				ErrConfigConflict, other.module, item.module, item.item.FlagName, other.path, item.path)
		}
	}
	if item.item.FlagShort == "" {
		return nil
	}
	// The reserved short name is judged before the duplicate is. Judging it
	// second would let the first module to declare -h through and report the
	// second, naming a different party on each run.
	if item.item.FlagShort == helpShort {
		return fmt.Errorf("%w: module %q declares %q as the command-line short name of %q, and "+
			"the command-line library provides it on every command as the short name of --%s. "+
			"Choose another short name",
			ErrConfigConflict, item.module, item.item.FlagShort, item.path, helpFlag)
	}
	if other, taken := m.byShort[item.item.FlagShort]; taken {
		return fmt.Errorf("%w: modules %q and %q both declare the command-line short name %q, for "+
			"%q and %q. Short names are global, whatever namespaces the items sit in",
			ErrConfigConflict, other.module, item.module, item.item.FlagShort, other.path, item.path)
	}
	return nil
}

// checkEnvName refuses an environment variable name that is taken, or reserved.
// A derived name and a pinned one meet in the same namespace, so either pair of
// them can collide.
//
// The reserved name is the name itself, however the item arrived at it: a
// pinned <PREFIX>_CONFIG hijacks the config locator exactly as a derived one
// does. The prefix has to be there for the rule to apply at all, since
// <PREFIX>_CONFIG is derived from it and there is no such name without one.
func (m *manifest) checkEnvName(item *manifestItem) error {
	if item.envName == "" {
		return nil
	}
	if m.host.Prefix != "" && item.envName == locatorEnvName(m.host.Prefix) {
		return fmt.Errorf("%w: module %q declares %q, which reads the environment variable %s, "+
			"the name this module reserves for the config locator. Rename the input item or read "+
			"another name with EnvName",
			ErrConfigConflict, item.module, item.path, item.envName)
	}
	if other, taken := m.byEnv[item.envName]; taken {
		return fmt.Errorf("%w: modules %q and %q both read the environment variable %s, for %q and "+
			"%q. Environment variable names are global, whatever namespaces the items sit in",
			ErrConfigConflict, other.module, item.module, item.envName, other.path, item.path)
	}
	return nil
}

// locatorEnvName is the environment variable the config locator is read from.
func locatorEnvName(prefix string) string {
	return strings.ToUpper(prefix + locatorEnvSuffix)
}

// ancestors lists the proper prefixes of a path at its segment boundaries,
// shortest first.
func ancestors(path string) []string {
	var out []string
	for i, r := range path {
		if r == '.' {
			out = append(out, path[:i])
		}
	}
	return out
}
