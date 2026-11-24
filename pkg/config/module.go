package config

import "github.com/vislake/speed/pkg/core"

// ModuleName is this module's name in the registry, and the name the reader is
// supplied under.
const ModuleName = "config"

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for a registry made with
// core.New, which registers it by hand ahead of NewApp. It delivers Reader and
// nothing else, not exclusively, and declares neither Stop nor Close.
func Module() core.Module {
	return core.Module{
		Name:     ModuleName,
		Provides: []core.Provision{{Token: (*Reader)(nil)}},
	}
}
