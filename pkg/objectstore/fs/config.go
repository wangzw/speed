package fs

import (
	"path/filepath"

	"github.com/vislake/speed/pkg/config"
)

const rootItem = "root"

var configDefaults = moduleConfig{}

type moduleConfig struct {
	Root string `config:"root"`
}

type resolved struct {
	root string
}

func (c moduleConfig) resolve() resolved {
	return resolved{root: filepath.Clean(c.Root)}
}

func items() map[string]config.Item {
	return map[string]config.Item{
		rootItem: {
			Origins:     config.OriginPrimary,
			Required:    true,
			Description: "the local directory an object's bytes are kept under",
		},
	}
}
