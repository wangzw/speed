// Package static carries a credential-verifying backend whose subject table
// comes from configuration.
//
// It is the smallest backend that is actually usable: a fixed set of subjects,
// each with a password hash, read once at startup and never changed afterwards.
// That makes it right for the accounts a deployment has before it has a user
// store — an operator, a probe, a first administrator — and wrong for accounts
// that come and go, which need a backend backed by something writable.
//
// A host takes it in by writing ModuleName into a command alongside the
// gateway. It delivers the verification capability non-exclusively, so it can
// stand beside other backends, each the authority for its own subjects.
package static

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.static"

// ConfigNamespace is where this module's input items are mounted. A backend
// hangs under authn.<backend>, so that every backend's section sits beside the
// gateway's under one heading.
const ConfigNamespace = "authn.static"

// basicScheme is the authentication scheme this backend reads. It is the one
// whose credential carries both the subject and the password in a place a
// standard fixes (RFC 7617).
const basicScheme = "Basic"

// backend is this module's product.
type backend struct {
	settings settings
	// claimed is the enumerated claim, built once in New and read-only
	// afterwards. The gateway reads it during Init, and the mechanism does
	// not stop a backend from writing to what it handed over; this one does
	// not.
	claimed []string
}

// backend must satisfy the capability it delivers.
var _ authn.Verifier = (*backend)(nil)

// init registers this module with the process registry.
//
// A host chooses its backends by importing them, and what enters its go.mod
// follows the packages it imported: a host that takes only this one carries a
// password hash and no other backend's library — no directory client and no
// database provider in it. The go.sum and the `go mod tidy` fetch are not held
// to that face — they follow the test closures of the imported packages — and
// whole-graph reads see every library an implementation binds
// (ADR-module-packaging-2026-09-22).
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Exclusive is left empty, which is what lets several backends run at once,
// each the authority for its own slice of the subject space. Prepare is
// declared, and declaring it is not optional for a backend: a module without one
// first reads its configuration in New, so a missing section is still a startup
// failure but one construction later, after the modules ordered before this one
// have already been built.
func Module() core.Module {
	return core.Module{
		Name:      ModuleName,
		Provides:  []core.Provision{{Token: (*authn.Verifier)(nil)}},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			return prepare(reader)
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return nil, err
			}
			set, err := readSettings(reader)
			if err != nil {
				return nil, err
			}
			return newBackend(set), nil
		},
	}
}

// readerOf takes up the configuration reader.
//
// Its absence means the host never handed one to the registry, which is core's
// own failure and is carried out as it stands. It is told apart from a missing
// section on purpose: reading the two alike would let a host that forgot the
// reader lose a whole backend in silence.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the subject table is read from configuration, and no reader "+
			"was handed to the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}

// prepare reads the configuration and states that this backend runs.
//
// There is no stance in which it runs with nobody to vouch for. A command that
// names this module has promised it starts, so a table that names no subject is
// a startup failure naming the key to write: a backend claiming no subject space
// authenticates nobody, and the gateway refuses it rather than carrying it along.
func prepare(reader config.Reader) (core.Enablement, error) {
	set, err := readSettings(reader)
	if err != nil {
		if errors.Is(err, config.ErrMissingRequired) {
			return core.Enablement{}, fmt.Errorf("%w: %w", err, nobodyToVouchFor())
		}
		return core.Enablement{}, err
	}
	if len(set.subjects) == 0 {
		return core.Enablement{}, nobodyToVouchFor()
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// nobodyToVouchFor is the failure this backend reports when its table holds no
// subject.
//
// An absent table and an empty one are the same fact — this backend would be the
// authority for nobody — and are worth the same sentence. It is a startup
// failure rather than a stance: a module taken into a command has to start, and
// the way out is to write the subjects, or to leave this backend out of the
// command whose traffic it was meant to authenticate.
//
// It wraps the sentinel the gateway refuses a claim of no subject space with,
// because that is what this table would hand it.
func nobodyToVouchFor() error {
	return fmt.Errorf("%w: the %s backend has no subject to be the authority for: %s.%s names "+
		"none, and a backend claiming no subject space authenticates nobody. Write the "+
		"subjects and their password hashes under that key to switch this backend on, or "+
		"leave this backend out of the command",
		authn.ErrInvalidClaim, ModuleName, ConfigNamespace, subjectsItem)
}

// newBackend builds the product, fixing the claim at construction time.
func newBackend(set settings) *backend {
	claimed := make([]string, 0, len(set.subjects))
	for subject := range set.subjects {
		claimed = append(claimed, subject)
	}
	// Sorted so that a diagnostic naming the claim reads the same on every
	// build; the gateway takes no order from it.
	sort.Strings(claimed)
	return &backend{settings: set, claimed: claimed}
}

// Name reports the name this backend answers under. It is what qualifies every
// identity it authenticates, so it comes from configuration rather than from
// the module name: two assemblies may want the same code answering under
// different names.
func (b *backend) Name() string { return b.settings.name }

// Claim reports the subject space this backend is the authority for: the
// subjects in its table, written out one by one.
//
// The claim is closed. There is no open claim and no whole-scheme claim, and
// neither would be honest here: this backend knows exactly which subjects it
// has, and a table read from a file has nothing to say about a subject that is
// not in it.
func (b *backend) Claim() authn.Claim {
	return authn.Claim{Subjects: b.claimed}
}

// Verify checks one credential against the table.
//
// It answers Unavailable in one case alone: the context it was handed has ended
// before the password is derived. The table was read at startup and lives in
// memory, so while the context holds there is no moment at which this backend
// is present but unable to say — which is the one thing that separates a
// refusal from an outage, and the distinction every lockout and alert rule is
// keyed off.
//
// The context is asked before the derivation and not during it. The standard
// library's derivation takes no context and runs to completion once begun, and
// it is the implementation the FIPS 140 mode validates, so an interruptible one
// written here would take this backend out of that mode's reach. What one
// attempt costs once it has begun is therefore bounded by the iteration count
// written into the table, not by the call budget.
func (b *backend) Verify(ctx context.Context, subject string, c authn.Credential) authn.Verdict {
	if !strings.EqualFold(c.Scheme, basicScheme) {
		return authn.Denied(fmt.Errorf("%s: the credential uses the %q scheme, and this backend "+
			"reads %q alone", ModuleName, c.Scheme, basicScheme))
	}
	offered, password, ok := basicCredential(c.Value, b.settings.name)
	if !ok {
		return authn.Denied(fmt.Errorf("%s: the %s credential is not base64 of user:password, or "+
			"it is qualified with a backend that is not this one", ModuleName, basicScheme))
	}
	// The credential must name the subject it is being checked against.
	// Nothing on the gateway's path produces a mismatch today; the check is
	// here so that no future caller can have this backend vouch for one
	// subject using another's credential.
	if offered != subject {
		return authn.Denied(fmt.Errorf("%s: the credential names the subject %q and is being "+
			"checked against %q", ModuleName, offered, subject))
	}

	stored, known := b.settings.subjects[subject]
	if !known {
		// The gateway asks this backend about the subjects it claimed, so
		// reaching here means the claim and the table have parted company.
		return authn.Denied(fmt.Errorf("%s: %q is not in this backend's table", ModuleName, subject))
	}
	if ended := ctx.Err(); ended != nil {
		return authn.Unavailable(fmt.Errorf("%s: the call ended before the password was derived: %w",
			ModuleName, ended))
	}
	if !stored.matches(password) {
		return authn.Denied(fmt.Errorf("%s: the password does not match", ModuleName))
	}
	return authn.Authenticated(subject)
}

// basicCredential splits a Basic credential into the subject it names and the
// password it offers.
//
// An owner qualification on the user is stripped, and the name it carries has
// to be this backend's. Dropping it unchecked would make local/alice and
// anyone/alice the same credential here, which is the same mistake the subject
// comparison in Verify exists to prevent: a credential must name what it is
// being checked against, in both halves. The gateway settles ownership before
// the call and would turn a wrong name away first, so this is the second half
// of that defence rather than the only one.
func basicCredential(value, backend string) (subject, password string, ok bool) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", "", false
	}
	user, password, found := strings.Cut(string(raw), ":")
	if !found || user == "" {
		return "", "", false
	}
	if owner, bare, qualified := strings.Cut(user, authn.Separator); qualified {
		if owner != backend {
			return "", "", false
		}
		user = bare
	}
	if user == "" {
		return "", "", false
	}
	return user, password, true
}
