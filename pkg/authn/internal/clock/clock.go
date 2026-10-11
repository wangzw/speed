// Package clock is the one place the authentication capability reads the time
// from, and it reads it in UTC.
//
// Every time that is written to a database, and every time that is bound as a
// query parameter, comes through here and is in UTC. SQLite stores a time as
// text in the zone it carries and compares it as a string, so a value in any
// other zone makes a comparison such as "has this session expired" come out
// wrong without any error; PostgreSQL's timestamptz is not affected, and the
// same rule applies to both. The local zone is not a safe default for the same
// reason: it is whatever the host machine says.
//
// A consumer that needs to be driven by a test takes a [Source] and reads the
// time through it rather than calling [Now] directly.
//
// A reading from here is a wall-clock instant for storing and for comparing
// against stored instants: converting to UTC drops the monotonic clock reading
// that time.Now carries. Logic that measures elapsed time or the end of a
// lifetime held in memory (a cache entry, a backoff window) takes its readings
// from time.Now itself, so that a step of the wall clock cannot stretch or
// shorten it.
package clock

import "time"

// Now is the current time in UTC.
func Now() time.Time {
	return time.Now().UTC()
}

// UTC is t in UTC. The zero time is returned as it is, so a value that was
// never set stays recognisable by [time.Time.IsZero].
func UTC(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC()
}

// Source is a way of reading the time, for a consumer to hold and a test to
// replace. The zero value reads the system clock.
type Source func() time.Time

// Now is the time the source reads, in UTC. A source that answers in another
// zone still yields the same instant expressed in UTC.
func (s Source) Now() time.Time {
	if s == nil {
		return Now()
	}
	return UTC(s())
}
