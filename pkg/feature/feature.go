// Package feature is the declaration surface of the feature document: the
// resource type a module registers to declare a feature it serves, the Board a
// module posts a conditional feature to, the rules a feature name has to meet,
// and the sentinels the publisher fails with.
//
// It imports the standard library alone. A module that declares a feature
// imports this package, and a third-party dependency reached from here would
// enter the closure of every such module.
package feature

import (
	"errors"
	"regexp"
)

// Spec declares one feature the declaring module serves. A static feature is
// present whenever the module is in force; a conditional one only once the
// module posts it to the Board during Init.
//
// A module declares a Spec by putting it in its descriptor's Resources. A
// framework module and a host's application module declare features the same
// way.
type Spec struct {
	// Name is the feature's name. It has at least two dot-separated segments,
	// and its first segment equals the first segment of the declaring module's
	// name. A name is declared once in the available set.
	Name string
	// Conditional marks a feature that is present only when the declaring
	// module posts it during Init, in the same judgement that decides whether
	// the surface it describes is mounted. A static feature is present as soon
	// as the declaring module is in force.
	Conditional bool
}

// Board collects the conditional features running modules serve. Post is legal
// from the board's Migrate until its Start, a window that contains the whole
// Init stage; outside it Post panics. Modules post in Init, and what they post
// is judged in Start.
//
// Post is safe for concurrent use. A module takes the Board up with
// core.Resolve and does not list it in Requires, so a process without the
// board — a worker, for instance — skips the post silently.
type Board interface {
	// Post reports that the module serves name in this run. module is the
	// posting module's own name, and name has to be one it declared as a
	// conditional feature.
	Post(module, name string)
}

// The sentinels a caller decides on with errors.Is.
var (
	// ErrInvalidName reports a feature name whose shape is not valid: fewer
	// than two dot-separated segments, a segment that does not begin with a
	// lower-case letter, or a segment carrying a character outside lower-case
	// letters, digits and dashes.
	ErrInvalidName = errors.New("feature: a feature name is not a valid shape")
	// ErrForeignName reports a feature name whose first segment differs from
	// the first segment of the declaring module's name.
	ErrForeignName = errors.New("feature: a feature name's first segment does not match the declaring module's")
	// ErrDuplicate reports one feature name declared by two modules.
	ErrDuplicate = errors.New("feature: two modules declare one feature name")
	// ErrUndeclared reports a post of a name the posting module did not
	// declare as a conditional feature.
	ErrUndeclared = errors.New("feature: a module posted a name it did not declare as a conditional feature")
	// ErrNoGate reports an assembly with no provider of the authentication
	// gateway, which the feature document needs because it is published to
	// authenticated callers alone.
	ErrNoGate = errors.New("feature: the process provides no authentication gateway")
)

// nameShape is the rule a feature name's shape is held to: two or more
// dot-separated segments, each beginning with a lower-case letter and
// continuing with lower-case letters, digits or dashes.
var nameShape = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)

// ValidName reports whether name has the shape a feature name must have. It
// does not check the namespace rule — a name's first segment must equal the
// declaring module's first segment — because that rule needs the declaring
// module, which the publisher has and a Spec alone does not.
func ValidName(name string) bool {
	return nameShape.MatchString(name)
}
