// Package feature is the declaration surface of the feature document: the Spec
// a module registers, the Board it posts a conditional feature to, the name
// rules, and the sentinels the publisher fails with.
package feature

import (
	"errors"
	"regexp"
)

// Spec declares one feature the declaring module serves. A static feature is
// present whenever the module is in force; a conditional one only once the
// module posts it to the Board during Init.
type Spec struct {
	// Name is the feature's name: at least two dot-separated segments, its first
	// equal to the first segment of the declaring module's name, and declared
	// once in the available set.
	Name string
	// Conditional marks a feature present only when the declaring module posts
	// it during Init. A static feature is present whenever the module is in
	// force.
	Conditional bool
}

// Board collects the conditional features running modules serve. Post is legal
// from the board's Migrate until its Start, a window that contains the whole
// Init stage; outside it Post panics. Post is safe for concurrent use.
type Board interface {
	// Post reports that the module serves name in this run. module is the
	// posting module's name, and name must be one it declared as a conditional
	// feature.
	Post(module, name string)
}

// The sentinels a caller decides on with errors.Is.
var (
	// ErrInvalidName reports a feature name whose shape is not valid: fewer than
	// two dot-separated segments, or a segment not matching [a-z][a-z0-9-]*.
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
	// gateway, because the document is published to authenticated callers alone.
	ErrNoGate = errors.New("feature: the process provides no authentication gateway")
)

var nameShape = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)

// ValidName reports whether name has the shape a feature name must have. It
// does not check that a name's first segment equals the declaring module's,
// because that check needs the declaring module.
func ValidName(name string) bool {
	return nameShape.MatchString(name)
}
