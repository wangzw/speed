package sqlite

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
)

// connectionScoped reports whether a locator names a database that belongs to
// the one connection opening it, reading it as the driver and SQLite do
// (readLocator): a temporary database, which an empty name opens; an in-memory
// one — :memory:, or mode=memory on a file: URI — outside shared-cache mode;
// and one SQLite's memdb VFS opens under a name of one character or one that
// begins with neither a slash nor a backslash, outside shared-cache mode too.
// SQLite gives each connection that opens such a locator a database of its
// own, so pkg/db holds a handle on one to a single connection. A longer memdb
// name that begins with either is one database for every connection of the
// process (memdbShared), and so is anything in shared-cache mode, a locator
// Dialector refuses.
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

// memdbVFS is the name of SQLite's VFS that keeps a database in memory.
const memdbVFS = "memdb"

// memdbShared reports whether a database the memdb VFS opens under name is one
// for every connection of the process: SQLite shares it when the name is longer
// than one character and begins with a slash or a backslash, and gives each
// connection a database of its own otherwise.
func memdbShared(name string) bool {
	return len(name) > 1 && (name[0] == '/' || name[0] == '\\')
}

// sqliteLocator is a locator as the driver hands it to SQLite and as SQLite
// then reads it.
type sqliteLocator struct {
	// name is the database SQLite opens: the file name, with %HH escapes
	// decoded on a file: URI; empty for a temporary database.
	name string
	// vfs is the VFS it is opened through: the last vfs parameter of a file:
	// URI, or else the one the driver reads from the query, or else none.
	vfs string
	// sharedCache, memory and readOnly are what a file: URI's last cache and
	// mode parameters make of it; all are false outside a file: URI.
	sharedCache, memory, readOnly bool
	// txlock is the driver's _txlock parameter as the driver reads it, the
	// first one the query gives, and empty when there is none.
	txlock string
}

// readLocator reads a locator the way the driver and SQLite read it, and
// reports false for one either of them refuses to open, which then opens
// nothing.
//
// The driver takes everything past the first question mark as its query,
// refuses one net/url cannot parse or one naming two VFSes, and hands the VFS
// it names to SQLite as the default, as a C string, which ends at a NUL. It
// cuts the query off a locator that is not a file: URI and hands a file: URI
// to SQLite whole, as a C string too. SQLite reads a file: URI with its own
// parser (sqliteURI), and takes a parameter given more than once from its last
// occurrence; a cache or mode parameter it does not know fails the open.
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

// beginStatement is the statement a transaction on a connection to the locator
// begins with, and an error for a _txlock parameter the driver does not know.
//
// A transaction begins IMMEDIATE, holding the database's write lock and
// waiting for it within the connection's busy timeout. SQLite's default,
// DEFERRED, takes that lock at the first write, and a transaction that has read
// before it writes is refused it at once, without the busy timeout's wait,
// while another connection holds it, so a check followed by a write — the
// shape most transactions take — would fail under any concurrent writer. The
// locator's _txlock parameter names another mode instead, read the way the
// driver documents it — the first one, deferred, immediate or exclusive in any
// letter case — whatever else the locator carries; the driver itself drops it
// when the locator gives _time_format. A locator naming no mode whose last mode
// parameter is ro begins its transactions DEFERRED: they never write, and
// SQLite begins an IMMEDIATE one on a read-only database as a read transaction
// holding the database's read lock from its BEGIN, which in rollback-journal
// mode keeps another connection's write from committing before the
// transaction has read anything. A transaction database/sql begins read-only
// begins DEFERRED whatever the locator says, as the driver begins one; one
// that holds the write lock from its BEGIN would keep every other writer of
// the file waiting while it reads.
//
// A connection whose query_only is on is the one read-only shape the locator
// does not decide: transactionConn.BeginTx begins DEFERRED there when SQLite
// refuses the statement this gives.
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

// deferredBegin begins a transaction that takes no lock until its first
// statement.
const deferredBegin = "BEGIN DEFERRED"

// The parts of a file: URI sqliteURI reads, in the order they come.
const (
	inName = iota
	inParameter
	inValue
)

// sqliteURI is SQLite's reading of a file: URI, as sqlite3ParseUri reads it: the
// file name, and each query parameter in order as a name and a value. It
// reports false for a URI whose authority SQLite refuses.
//
// SQLite ends the URI at the first number sign, drops an authority that is
// empty or localhost, and decodes %HH escapes in the name, the parameters'
// names and their values alike. An escaped NUL ends the part it is in, and the
// rest of that part is skipped; a parameter whose name is empty is skipped with
// its value; a plus sign stands as it is. In a value, a question mark or an
// equals sign is part of it: only an ampersand ends one.
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
	// out is SQLite's buffer: the name, then each parameter's name and value,
	// each one ended by a NUL.
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

// skipPart is where the part of a file: URI that state names ends, from at: the
// next character that separates it from what follows, or the number sign that
// ends the URI.
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

// isHexDigit reports whether c is a hexadecimal digit.
func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// hexValue is the value of the hexadecimal digit c.
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
