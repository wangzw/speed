package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// application is one of this host's own modules, with what the rest of the
// program needs to know about it and a descriptor does not say: which commands
// run it, which models its code issues statements on, and which of its routes
// are worth a journal row.
//
// Each module file registers itself with registerApplication from its own
// init, and everything that has to list the application modules reads this
// table rather than a list of its own. The command tree takes each module into
// the commands it names; the descriptor-level cases in wiring_test.go hold
// every module to the same declarations; the harness configures the journal
// and checks the tables and release records from it. A module added this way is
// wired and checked without an edit anywhere else.
type application struct {
	module core.Module

	// runsOn names the commands under every stack that take the module in:
	// serveCommand, workerCommand, or the word of a command group. The
	// module is named on each of them one by one, as every module a command
	// runs is; IncludeAll is not what puts it there.
	runsOn []string

	// models are the models the module's code issues statements on: the
	// ones whose tables it owns and the ones it reads or writes on another
	// module's behalf. Nothing in a descriptor says which tables a module's
	// code reaches — that is inside its handlers — so it is said here.
	models []any

	// journalled are the routes the module binds whose requests a deployment
	// records in the journal. The journal captures the requests its
	// configuration lists, and the harness writes that list from these.
	journalled []string
}

// applications is every application module this host registers, in
// registration order.
var applications []application

// registerApplication registers one application module and records it for the
// command tree, the descriptor-level cases and the harness.
func registerApplication(a application) {
	applications = append(applications, a)
	core.ProcessRegistry.Register(a.module)
}

// modulesOn names the application modules a command takes in, in registration
// order.
func modulesOn(command string) []string {
	var names []string
	for _, a := range applications {
		if slices.Contains(a.runsOn, command) {
			names = append(names, a.module.Name)
		}
	}
	return names
}

// The records an application module writes as it is released. They are messages
// rather than free text so that a case finds them by name, and each carries the
// module attribute its Named logger adds.
const (
	stopMsg  = "stop"
	closeMsg = "close"
)

// released is embedded in the product of every application module: it holds
// the logger the module's release records go through.
type released struct {
	log *slog.Logger
}

// releaseLog is what the release callbacks below find the logger through.
func (r *released) releaseLog() *slog.Logger { return r.log }

// releasable is a product carrying a release logger.
type releasable interface {
	releaseLog() *slog.Logger
}

// namedLogger takes the logging capability up and names it after the module.
// Every module that calls it declares the requirement on the capability, which
// is what orders the logging module ahead of its construction and behind its
// release.
func namedLogger(reg *core.Registry, module string) (*slog.Logger, error) {
	logger, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return nil, err
	}
	return logger.Named(module), nil
}

// recordStop and recordClose are the Stop and Close callbacks of every
// application module. They write a record and release nothing: no application
// module holds anything of its own to give back. The database handle, the
// key-value store and the bus belong to the modules that deliver them.
//
// Where they run is the registry's shutdown order. Every module that declares
// Serve — the HTTP entry module is this host's one — is stopped first, and its
// Stop returns once the requests it let in have been answered; the other
// modules' Stop callbacks follow, and every Close comes after all of them,
// each in the reverse of construction order. So an application module is
// stopped and closed after the requests in flight have been answered,
// wherever it was constructed, and one a slice adds may stop taking work in
// its Stop without refusing a request still being answered.
// TestAnInterruptDrainsRequestsInFlight holds each module's stop record to
// that. Work a bus hands a subscriber is outside it: the bus and the modules
// subscribing to it still stop in the reverse of construction order.
func recordStop(_ context.Context, _ *core.Registry, instance any) error {
	if product, ok := instance.(releasable); ok {
		product.releaseLog().Info(stopMsg)
	}
	return nil
}

func recordClose(_ context.Context, _ *core.Registry, instance any) error {
	if product, ok := instance.(releasable); ok {
		product.releaseLog().Info(closeMsg)
	}
	return nil
}

// apiError is the body every refusal and failure this host writes carries. The
// shape is the host's own: the HTTP module defines no error body, so the module
// that provides an API decides what its errors look like.
type apiError struct {
	Error string `json:"error"`
}

// writeError answers with a status and a JSON error body.
func writeError(w nethttp.ResponseWriter, status int, message string) {
	_ = speedhttp.WriteJSON(w, status, apiError{Error: message})
}

// newID generates an application identifier: a prefix naming what it
// identifies, then 96 random bits in hex. Identifiers are generated here rather
// than by the database because a scoped table's key leads with the tenant
// column, which an integer sequence does not compose with on SQLite.
func newID(prefix string) (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

// storeKey builds a key-value key from its segments. The store has no
// namespaces, so every key this host writes starts with crm: and names what it
// holds, and each segment is escaped so that a value taken from data — a tenant
// identifier may carry a colon — cannot run into the next segment and name
// another key.
func storeKey(segments ...string) string {
	escaped := make([]string, len(segments))
	for i, s := range segments {
		escaped[i] = keyEscaper.Replace(s)
	}
	return "crm:" + strings.Join(escaped, ":")
}

var keyEscaper = strings.NewReplacer("%", "%25", ":", "%3A")

// keyDigest is the segment a value of any length and spelling takes in a key: a
// client's idempotency key, a publisher's event identifier. The digest keeps
// every such key the same length and within the bound the tightest backend puts
// on a key.
func keyDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// errForeignInstance reports a callback handed an instance its module did not
// build. Such an instance is one a host supplied for the module, which the
// registry hands to the callbacks as it hands the product, and a checked
// narrowing reports it with this error instead of panicking.
func errForeignInstance(module string, instance any) error {
	return fmt.Errorf("module %q was handed an instance of type %T, which it did not build", module, instance)
}
