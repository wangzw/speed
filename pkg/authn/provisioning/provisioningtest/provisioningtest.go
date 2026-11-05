// Package provisioningtest supplies a stand-in for the account-provisioning
// seam to an assembly's tests.
//
// The account module requires an AccountProvisioning provider, so every test
// that drives it through a registry has to supply one. Module registers a Seam
// that does nothing and records what it was handed; a case that needs a refusal
// or a write of its own sets the Seam's hooks or errors. The stand-in lives
// here, outside the test files, because the assemblies that need it sit in
// several packages.
package provisioningtest

import (
	"context"
	"sync"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/core"
)

// ModuleName is the stand-in module's name in the registry.
const ModuleName = "provisioning.test"

// Seam is a stand-in AccountProvisioning. Every call is recorded, and a case
// may set an error to return or a hook to run inside the account's transaction.
type Seam struct {
	mu        sync.Mutex
	creates   []provisioning.AccountFacts
	deletions []provisioning.AccountFacts

	// CreateErr and DeleteErr are returned by the matching method after the
	// hook has run.
	CreateErr error
	DeleteErr error
	// OnCreate and OnDelete run inside the account's transaction, before the
	// matching error is returned.
	OnCreate func(ctx context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error
	OnDelete func(ctx context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error
}

// New returns an empty stand-in.
func New() *Seam { return &Seam{} }

// ProvisionAccount records the call and runs OnCreate.
func (s *Seam) ProvisionAccount(ctx context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
	s.mu.Lock()
	s.creates = append(s.creates, facts)
	hook, err := s.OnCreate, s.CreateErr
	s.mu.Unlock()
	if hook != nil {
		if hookErr := hook(ctx, tx, facts); hookErr != nil {
			return hookErr
		}
	}
	return err
}

// ProvisionAccountDeletion records the call and runs OnDelete.
func (s *Seam) ProvisionAccountDeletion(ctx context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
	s.mu.Lock()
	s.deletions = append(s.deletions, facts)
	hook, err := s.OnDelete, s.DeleteErr
	s.mu.Unlock()
	if hook != nil {
		if hookErr := hook(ctx, tx, facts); hookErr != nil {
			return hookErr
		}
	}
	return err
}

// Creates returns the facts of every creation call so far.
func (s *Seam) Creates() []provisioning.AccountFacts {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]provisioning.AccountFacts(nil), s.creates...)
}

// Deletions returns the facts of every deletion call so far.
func (s *Seam) Deletions() []provisioning.AccountFacts {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]provisioning.AccountFacts(nil), s.deletions...)
}

// Module registers a no-op stand-in under ModuleName.
func Module() core.Module { return ModuleWith(New()) }

// ModuleWith registers the given stand-in under ModuleName.
func ModuleWith(s *Seam) core.Module {
	return core.Module{
		Name:     ModuleName,
		Provides: []core.Provision{{Token: (*provisioning.AccountProvisioning)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return s, nil },
	}
}
