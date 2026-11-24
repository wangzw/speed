package db

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// OpenedMarkerName is the name the mark Open installs sits under in a handle's plugin
// table, for the external test package: a case listing the declared plugins on a
// connection built with Open leaves it out by this name.
const OpenedMarkerName = openedMarkerName

// EngineMarkerName is the name the engine marker every handle carries sits under in its
// plugin table, left out of a listing of the declared plugins by this name for the same
// reason.
const EngineMarkerName = engineMarkerName

// WithheldStatement is the text a record carries in place of the statement GORM's Scan
// finisher ran, for the external test package: a case reading the records a handle
// writes tells such a statement by it.
const WithheldStatement = withheldStatement

// SlowAfter is a session of handle whose statement logger writes a statement as slow
// once it has run longer than threshold, for the external test package.
func SlowAfter(handle *gorm.DB, threshold time.Duration) *gorm.DB {
	l, ok := handle.Logger.(statementLogger)
	if !ok {
		panic(fmt.Sprintf("db: the handle carries a %T, and only a handle this package opened carries "+
			"the statement logger", handle.Logger))
	}
	l.slow = threshold
	return handle.Session(&gorm.Session{Logger: l})
}
