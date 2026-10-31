package fs

import (
	"path/filepath"

	"github.com/vislake/speed/pkg/config"
)

// rootItem is the one input item this backend accepts, relative to its
// namespace: the local directory an object's bytes are kept under. It is
// required, so the loader refuses a section that omits it before any module
// construction.
const rootItem = "root"

// configDefaults is what this backend is worth without a configuration file.
// There is none: a backend with no directory to keep bytes under has nowhere to
// put them, so root carries no default and is declared required. A host that
// wants a bare run to start names one in its defaults document; this module
// does not invent a path on the operator's disk.
var configDefaults = moduleConfig{}

// moduleConfig is the set of input items this backend accepts.
type moduleConfig struct {
	// Root is the local directory the backend maps every internal key under.
	Root string `config:"root"`
}

// resolved is the configuration this backend actually runs on.
type resolved struct {
	// root is the directory every key is resolved under, cleaned once here so
	// the mapping and its containment hold whatever spelling the host wrote.
	root string
}

// resolve turns decoded configuration into what the backend runs on.
func (c moduleConfig) resolve() resolved {
	return resolved{root: filepath.Clean(c.Root)}
}

// items declares this backend's input items. The module descriptor mounts them
// under this backend's namespace.
//
// The root takes the environment as well as the configuration file: where a
// run keeps its bytes differs between runs of one binary, and a run with no
// configuration file still has to name one. The value is not accepted from the
// command line, which would put a filesystem path in a process listing.
func items() map[string]config.Item {
	return map[string]config.Item{
		rootItem: {
			Origins:     config.OriginPrimary | config.OriginEnv,
			Required:    true,
			Description: "the local directory an object's bytes are kept under",
		},
	}
}
