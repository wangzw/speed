package main

import (
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

const envPrefix = "DEMO"

const hostModuleName = "host"

func init() { core.ProcessRegistry.Register(hostModule()) }

// hostModule carries the host's own inputs: the environment prefix and the
// defaults document compiled into the binary. DefaultLocator is empty so a
// command, version included, need not depend on a file being installed.
func hostModule() core.Module {
	return core.Module{
		Name:      hostModuleName,
		Resources: []any{config.HostIdentity{Prefix: envPrefix}, platformDefaults{}},
	}
}
