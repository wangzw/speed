// Package modules is the slot this product's business modules live in.
//
// A module here is written the way every module under pkg/ is: a descriptor
// with a Name, its Requires, its Provides and its lifecycle callbacks,
// registered with core.ProcessRegistry from an init, and named on a command of
// the command tree in main.go. The import is the wiring: importing a business
// module's package registers it, and the command tree's Include is what puts it
// in a run.
//
// Nothing is in this package yet. It exists so that the slot has a home and the
// first business module has a directory to land in; a copied-out product renames
// it and grows its own modules here.
package modules
