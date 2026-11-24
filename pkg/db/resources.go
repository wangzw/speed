package db

import (
	"io/fs"

	"gorm.io/gorm"
)

// Migrations is the resource a module with tables declares to hand its
// migration files over.
type Migrations struct {
	// FS holds one subdirectory per dialect, named by that dialect's value,
	// with that dialect's migration files inside it.
	//
	// Files are applied in name order, so a 0001_ prefix makes lexical order
	// agree with intended order. A declaration without a subdirectory for the
	// running dialect is zero migrations for it, not an error.
	FS fs.FS
}

// Plugin is the resource a module declares to have a GORM plugin installed on
// every handle this module hands out. It is installed as given.
type Plugin struct {
	// Plugin is the plugin itself, installed with (*gorm.DB).Use.
	Plugin gorm.Plugin
}
