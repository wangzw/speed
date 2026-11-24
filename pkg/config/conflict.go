package config

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/core"
)

const (
	locatorFlag      = "config"
	helpFlag         = "help"
	helpShort        = "h"
	locatorEnvSuffix = "_CONFIG"
)

type manifest struct {
	items    []*manifestItem
	byPath   map[string]*manifestItem
	byFlag   map[string]*manifestItem
	byShort  map[string]*manifestItem
	byEnv    map[string]*manifestItem
	interior map[string]*manifestItem
	sections map[string]string
	host     HostIdentity
	hasHost  bool
}

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

func locatorEnvName(prefix string) string {
	return strings.ToUpper(prefix + locatorEnvSuffix)
}

func ancestors(path string) []string {
	var out []string
	for i, r := range path {
		if r == '.' {
			out = append(out, path[:i])
		}
	}
	return out
}
