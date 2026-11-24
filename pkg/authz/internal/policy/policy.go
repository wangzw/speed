// Package policy loads the process's policy into an immutable snapshot and holds the one the enforcement path reads.
package policy

import (
	"context"
	"fmt"
	"sync/atomic"

	casbin "github.com/casbin/casbin/v3"
	casbinmodel "github.com/casbin/casbin/v3/model"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/persist"
)

// Snapshot is one fully loaded policy: a parsed model holding every tenant's
// rows. It is immutable once Load returns, so the same snapshot serves
// concurrent reads without a lock.
type Snapshot struct {
	enforcer *casbin.Enforcer
}

// Load reads every tenant's grants and bindings through the thin adapter and
// builds a snapshot over text. ctx bounds the read.
func Load(ctx context.Context, handle *gorm.DB, text string) (*Snapshot, error) {
	model, err := casbinmodel.NewModelFromString(text)
	if err != nil {
		return nil, fmt.Errorf("authz/policy: parsing the model text: %w", err)
	}
	enforcer, err := casbin.NewEnforcer(model, persist.New(ctx, handle, ""))
	if err != nil {
		return nil, fmt.Errorf("authz/policy: loading the policy: %w", err)
	}
	return &Snapshot{enforcer: enforcer}, nil
}

// Allowed answers one query by running the snapshot's matcher. The result is a
// policy decision; a non-nil error is the engine failing and not a denial.
func (s *Snapshot) Allowed(q authz.Query) (bool, error) {
	allowed, err := s.enforcer.Enforce(q.Subject, string(q.Domain), q.Object, q.Action)
	if err != nil {
		return false, fmt.Errorf("authz/policy: evaluating the query: %w", err)
	}
	return allowed, nil
}

// Held returns every (object, action) pair the subject holds in domain, read
// through the engine's implicit permissions, with a pair reached through several
// roles kept once and the result a copy. A subject with no binding in the domain
// gets an empty result. A non-nil error is the engine failing, never an empty
// set.
func (s *Snapshot) Held(domain authz.Domain, subject string) ([][2]string, error) {
	rules, err := s.enforcer.GetImplicitPermissionsForUser(subject, string(domain))
	if err != nil {
		return nil, fmt.Errorf("authz/policy: reading the subject's permissions: %w", err)
	}
	pairs := make([][2]string, 0, len(rules))
	seen := make(map[[2]string]struct{}, len(rules))
	for _, rule := range rules {
		if len(rule) < 4 {
			continue
		}
		pair := [2]string{rule[2], rule[3]}
		if _, duplicate := seen[pair]; duplicate {
			continue
		}
		seen[pair] = struct{}{}
		pairs = append(pairs, pair)
	}
	return pairs, nil
}

// Holder holds the current snapshot behind an atomic pointer. A reader takes
// either the old snapshot or the new one, never a mixture; before the first load
// it holds nil, which the enforcement path reads as unavailable rather than as a
// denial.
type Holder struct {
	current atomic.Pointer[Snapshot]
}

// Get returns the current snapshot, or nil before the first load.
func (h *Holder) Get() *Snapshot { return h.current.Load() }

// Swap replaces the current snapshot and returns the one it replaced, nil
// before the first load.
func (h *Holder) Swap(s *Snapshot) *Snapshot { return h.current.Swap(s) }
