package config

import "github.com/vislake/speed/pkg/core"

// ModuleName is this module's name in the registry, and the name the reader is
// supplied under: Execute supplies it, and a host that drives the registry
// without a command tree writes it in the Supply call of its own.
//
// It is exported because the name reaches those sites from the package that
// registered it: a literal spelled at one of them would go on compiling after a
// rename and miss the module.
const ModuleName = "config"

// init registers this module with the process registry, so a host that imports
// the package has the module the reader is supplied to.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations: one made with core.New registers it
// by hand, ahead of NewApp.
//
// It delivers Reader and nothing else: no New, because loading runs before the
// drive begins and builds the reader outside it, and no other callback. Execute
// supplies the reader to this module by name; a host that drives the registry
// without a command tree supplies the one Load returned.
//
// It does not claim Reader exclusively. The dependency closure then takes this
// module in on behalf of one that requires the reader, so a reader nobody
// supplied fails the startup with the account of this module itself: it declares
// the capability, has no New and was handed no instance. With a claim the
// closure would leave the module out, and the omission would come up in
// dependency resolution as a capability no participating module delivers.
//
// It declares neither Stop nor Close. The reader holds the values of the run and
// a snapshot of the environment taken when loading began, nothing that has to be
// released, so neither the drive nor the host has anything to release for this
// module, whether or not the drive began.
func Module() core.Module {
	return core.Module{
		Name:     ModuleName,
		Provides: []core.Provision{{Token: (*Reader)(nil)}},
	}
}
