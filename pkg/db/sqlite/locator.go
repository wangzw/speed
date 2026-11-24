package sqlite

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
)

// connectionScoped reports whether a locator names a database that belongs to
// the one connection opening it: a temporary or in-memory database, or one the
// memdb VFS opens privately, outside shared-cache mode.
func connectionScoped(dsn string) bool {
	l, ok := readLocator(dsn)
	switch {
	case !ok:
		return false
	case l.name == "":
		return true
	case l.sharedCache:
		return false
	default:
		return l.name == ":memory:" || l.memory || (l.vfs == memdbVFS && !memdbShared(l.name))
	}
}

const memdbVFS = "memdb"

// memdbShared reports whether a memdb database is one for every connection of
// the process: the name is longer than one character and begins with a slash or
// a backslash.
func memdbShared(name string) bool {
	return len(name) > 1 && (name[0] == '/' || name[0] == '\\')
}

type sqliteLocator struct {
	// name is the database SQLite opens, with %HH escapes decoded on a file:
	// URI; empty for a temporary database.
	name                          string
	vfs                           string
	sharedCache, memory, readOnly bool
	txlock                        string
}

// readLocator reads a locator the way the driver and SQLite read it, and
// reports false for one either of them refuses to open. A parameter given more
// than once takes its last occurrence.
func readLocator(dsn string) (sqliteLocator, bool) {
	var l sqliteLocator
	name := dsn
	if at := strings.IndexByte(dsn, '?'); at >= 1 {
		query, err := url.ParseQuery(dsn[at+1:])
		if err != nil {
			return l, false
		}
		for _, vfs := range query["vfs"] {
			if l.vfs != "" && l.vfs != vfs {
				return l, false
			}
			l.vfs = vfs
		}
		l.vfs, _, _ = strings.Cut(l.vfs, "\x00")
		l.txlock = query.Get("_txlock")
		if !strings.HasPrefix(dsn, "file:") {
			name = dsn[:at]
		}
	}
	name, _, _ = strings.Cut(name, "\x00")
	if !strings.HasPrefix(name, "file:") {
		l.name = name
		return l, true
	}
	path, options, ok := sqliteURI(name)
	if !ok {
		return l, false
	}
	l.name = path
	for _, option := range options {
		switch name, value := option[0], option[1]; name {
		case "vfs":
			l.vfs = value
		case "cache":
			if value != "shared" && value != "private" {
				return l, false
			}
			l.sharedCache = value == "shared"
		case "mode":
			if value != "ro" && value != "rw" && value != "rwc" && value != "memory" {
				return l, false
			}
			l.memory = value == "memory"
			l.readOnly = value == "ro"
		}
	}
	return l, true
}

// beginStatement is the statement a transaction on a connection to the locator begins with, and an error for a _txlock value the driver does not know. IMMEDIATE is the default so a read-then-write transaction waits for a concurrent writer within the busy timeout instead of being refused at once; the locator's _txlock names another mode, and a read-only locator or transaction begins DEFERRED.
func beginStatement(l sqliteLocator, readOnly bool) (string, error) {
	mode := "IMMEDIATE"
	if l.txlock != "" {
		switch strings.ToLower(l.txlock) {
		case "deferred":
			mode = "DEFERRED"
		case "immediate":
			mode = "IMMEDIATE"
		case "exclusive":
			mode = "EXCLUSIVE"
		default:
			return "", fmt.Errorf("unknown _txlock %q", l.txlock)
		}
	} else if l.readOnly {
		mode = "DEFERRED"
	}
	if readOnly {
		return deferredBegin, nil
	}
	return "BEGIN " + mode, nil
}

const deferredBegin = "BEGIN DEFERRED"

const (
	inName = iota
	inParameter
	inValue
)

// sqliteURI is SQLite's reading of a file: URI, as sqlite3ParseUri reads it.
func sqliteURI(uri string) (string, [][2]string, bool) {
	at := len("file:")
	if strings.HasPrefix(uri[at:], "//") {
		end := at + len("//")
		for end < len(uri) && uri[end] != '/' {
			end++
		}
		if host := uri[at+len("//") : end]; host != "" && host != "localhost" {
			return "", nil, false
		}
		at = end
	}
	state := inName
	var out []byte
	for at < len(uri) && uri[at] != '#' {
		c := uri[at]
		at++
		switch {
		case c == '%' && at+1 < len(uri) && isHexDigit(uri[at]) && isHexDigit(uri[at+1]):
			c = hexValue(uri[at])<<4 | hexValue(uri[at+1])
			at += 2
			if c == 0 {
				at = skipPart(uri, at, state)
				continue
			}
		case state == inParameter && (c == '&' || c == '='):
			if out[len(out)-1] == 0 {
				for at < len(uri) && uri[at] != '#' && uri[at-1] != '&' {
					at++
				}
				continue
			}
			if c == '&' {
				out = append(out, 0)
			} else {
				state = inValue
			}
			c = 0
		case (state == inName && c == '?') || (state == inValue && c == '&'):
			c = 0
			state = inParameter
		}
		out = append(out, c)
	}
	if state == inParameter {
		out = append(out, 0)
	}
	out = append(out, 0, 0, 0, 0)
	field := func(from int) (string, int) {
		end := from + bytes.IndexByte(out[from:], 0)
		return string(out[from:end]), end + 1
	}
	path, next := field(0)
	var options [][2]string
	for out[next] != 0 {
		var name, value string
		name, next = field(next)
		value, next = field(next)
		options = append(options, [2]string{name, value})
	}
	return path, options, true
}

func skipPart(uri string, at, state int) int {
	for ; at < len(uri); at++ {
		switch c := uri[at]; {
		case c == '#',
			state == inName && c == '?',
			state == inParameter && (c == '=' || c == '&'),
			state == inValue && c == '&':
			return at
		}
	}
	return at
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func hexValue(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c <= 'F':
		return c - 'A' + 10
	default:
		return c - 'a' + 10
	}
}
