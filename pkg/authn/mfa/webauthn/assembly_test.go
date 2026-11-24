package webauthn

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/authn/mfa"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/log"
)

const (
	dbModule      = "stand-in.db"
	accountModule = "stand-in.account"
	mounterModule = "stand-in.mounter"
)

func standIn(name string, instance any, exclusive bool, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, tk := range tokens {
		provides[i] = core.Provision{Token: tk, Exclusive: exclusive}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

type accountStandIn struct {
	authn.Accounts
	authn.AccountAdmin
	authn.Throttle
	usernames map[authn.AccountID]string
}

func (a *accountStandIn) Identifiers(_ context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	if u, ok := a.usernames[id]; ok {
		return []authn.Identifier{{Kind: authn.IdentifierUsername, Value: u}}, nil
	}
	return nil, nil
}

func (a *accountStandIn) Check(context.Context, authn.ThrottleScope, string) (time.Duration, error) {
	return 0, nil
}

func (a *accountStandIn) Fail(context.Context, authn.ThrottleScope, string) (bool, error) {
	return false, nil
}

func (a *accountStandIn) Succeed(context.Context, authn.ThrottleScope, string) error { return nil }

type assembly struct {
	migrate bool
}

func migrationSets(module core.Module) []db.Migrations {
	var out []db.Migrations
	for _, res := range module.Resources {
		if m, ok := res.(db.Migrations); ok {
			out = append(out, m)
		}
	}
	return out
}

func applyModuleMigrations(t *testing.T, handle *gorm.DB, module core.Module, dialect string) {
	t.Helper()
	for _, m := range migrationSets(module) {
		entries, err := fs.ReadDir(m.FS, dialect)
		if err != nil {
			t.Fatalf("listing the %s migrations of %s: %v", dialect, module.Name, err)
		}
		for _, entry := range entries {
			sql, err := fs.ReadFile(m.FS, dialect+"/"+entry.Name())
			if err != nil {
				t.Fatalf("reading %s: %v", entry.Name(), err)
			}
			for _, statement := range strings.Split(string(sql), ";") {
				if strings.TrimSpace(statement) == "" {
					continue
				}
				if err := handle.Exec(statement).Error; err != nil {
					t.Fatalf("applying %s: %v", entry.Name(), err)
				}
			}
		}
	}
}

func (a assembly) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()

	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "webauthn.db"), nil)
	handle := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	if a.migrate {
		applyModuleMigrations(t, handle, Module(), sqliteDialect)
		applyModuleMigrations(t, handle, mfa.Module(), sqliteDialect)
	}

	accounts := &accountStandIn{usernames: map[authn.AccountID]string{acct: "alice"}}
	reader := mapReader{values: map[string]any{
		gateNamespace + "." + gateMountPath: gateMount{PublicURL: testOrigin},
	}}

	reg := core.New()
	reg.Register(Module())
	reg.Register(mfa.Module())
	names := []string{ModuleName, mfa.ModuleName}

	reg.Register(standIn(dbModule, database, true, (*db.Database)(nil)))
	names = append(names, dbModule)
	reg.Register(standIn(accountModule, accounts, true,
		(*authn.Accounts)(nil), (*authn.AccountAdmin)(nil), (*authn.Throttle)(nil)))
	names = append(names, accountModule)
	reg.Register(standIn(mounterModule, mounttest.New(t), true, (*mount.Mounter)(nil)))
	names = append(names, mounterModule)
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	names = append(names, config.ModuleName)

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	})
}

// TestTheDescriptorNamesWhatItDeliversAndRequires checks that the descriptor delivers the factor and login method unclaimed and requires the database, MFA and account reads.
func TestTheDescriptorNamesWhatItDeliversAndRequires(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.mfa.webauthn" {
		t.Errorf("module name = %q (constant %q), want authn.mfa.webauthn", m.Name, ModuleName)
	}
	if len(m.Provides) != 2 {
		t.Fatalf("Provides = %+v, want the factor and the login method", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.Factor)(nil)) || p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.Factor and not exclusive", p)
	}
	if p := m.Provides[1]; p.Token != core.Token((*authn.LoginMethod)(nil)) || p.Exclusive {
		t.Errorf("Provides[1] = %+v, want authn.LoginMethod and not exclusive", p)
	}

	wantRequired := []core.Token{(*db.Database)(nil), (*authn.MFA)(nil), (*authn.Accounts)(nil)}
	if len(m.Requires) != len(wantRequired)+1 {
		t.Fatalf("Requires = %+v, want the three required capabilities and the logging capability optional", m.Requires)
	}
	for i, tk := range wantRequired {
		if m.Requires[i].Token != tk || m.Requires[i].Optional {
			t.Errorf("Requires[%d] = %+v, want %v required", i, m.Requires[i], tk)
		}
	}
	if last := m.Requires[len(m.Requires)-1]; last.Token != core.Token((*log.Logger)(nil)) || !last.Optional {
		t.Errorf("Requires[%d] = %+v, want the logging capability optional", len(m.Requires)-1, last)
	}
}

// TestPrepareStatesEnabledAndNothingElse checks that Prepare states the enabled stance and nothing else.
func TestPrepareStatesEnabledAndNothingElse(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(configReader(testOrigin), config.ModuleName)
	got, err := Module().Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestPrepareRefusesARelyingPartyIDOutsideTheHost checks that Prepare refuses a relying party id that does not cover the public host.
func TestPrepareRefusesARelyingPartyIDOutsideTheHost(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(configReaderFor(testOrigin, moduleConfig{RPID: "evil.com"}), config.ModuleName)
	if _, err := Module().Prepare(t.Context(), reg); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("Prepare returned %v, want ErrInvalidConfig", err)
	}
}

// TestImportingThePackageRegistersTheModuleWithTheProcess checks that importing the package registers the module with the process registry.
func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s", ModuleName)
	}
	if len(got.Provides) != 2 || got.Provides[0].Token != core.Token((*authn.Factor)(nil)) {
		t.Errorf("the registered module delivers %+v, want the factor", got.Provides)
	}
}

// TestARealRegistryDeliversTheFactorAndTheLoginMethod checks that a real registry delivers the factor and the login method from one instance.
func TestARealRegistryDeliversTheFactorAndTheLoginMethod(t *testing.T) {
	var got authn.Factor
	var method authn.LoginMethod
	err := assembly{migrate: true}.drive(t, func(_ context.Context, reg *core.Registry) error {
		all := core.ResolveAll[authn.Factor](reg)
		if len(all) != 1 {
			return fmt.Errorf("%d factors were delivered, want one", len(all))
		}
		got = all[0]
		var err error
		method, err = core.Resolve[authn.LoginMethod](reg)
		return err
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if got == nil || method == nil {
		t.Fatal("the registry delivered nothing for the capabilities the descriptor claims")
	}
	if any(got) != any(method) {
		t.Errorf("the two capabilities are delivered by %T and %T, want one instance", got, method)
	}
	if got.Kind() != authn.FactorWebAuthn || method.Name() != methodName {
		t.Errorf("the instance reports kind %q and name %q", got.Kind(), method.Name())
	}
}

// TestStartRefusesWhenATableIsMissing checks that Start refuses with ErrNotMigrated when a table is missing.
func TestStartRefusesWhenATableIsMissing(t *testing.T) {
	err := assembly{migrate: false}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with the tables missing")
		return nil
	})
	if !errors.Is(err, authn.ErrNotMigrated) {
		t.Errorf("the drive returned %v, want ErrNotMigrated", err)
	}
}

// TestRegistrationAndVerificationThroughTheMultiFactorModule checks the whole registration and assertion through the multi-factor module.
func TestRegistrationAndVerificationThroughTheMultiFactorModule(t *testing.T) {
	err := assembly{migrate: true}.drive(t, func(ctx context.Context, reg *core.Registry) error {
		m, err := core.Resolve[authn.MFA](reg)
		if err != nil {
			return err
		}
		a := newAuthenticator(t)

		s1, err := m.Enroll(ctx, authn.FlowState{Account: acct}, authn.FactorWebAuthn,
			authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldProfile: profilePasskey}})
		if err != nil {
			return fmt.Errorf("Enroll step 1: %w", err)
		}
		if step.KindOf(s1) != step.KindData {
			return fmt.Errorf("Enroll step 1 gave %v, want data", step.KindOf(s1))
		}
		opts := decodeCreation(t, step.DataOf(s1))
		body := a.registration(t, opts.Challenge, true)

		s2, err := m.Enroll(ctx, authn.FlowState{Account: acct, Private: step.PrivateOf(s1)}, authn.FactorWebAuthn,
			authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldCredential: string(body)}})
		if err != nil {
			return fmt.Errorf("Enroll step 2: %w", err)
		}
		if step.KindOf(s2) != step.KindEnrolled {
			return fmt.Errorf("Enroll step 2 gave %v, want enrolled", step.KindOf(s2))
		}
		rec := step.RecordOf(s2)
		if rec.Status != authn.FactorActive {
			return fmt.Errorf("the factor is %d after the last step, want active", rec.Status)
		}

		c1, err := m.Challenge(ctx, authn.FlowState{Account: acct}, rec.ID)
		if err != nil {
			return fmt.Errorf("Challenge: %w", err)
		}
		if step.KindOf(c1) != step.KindData {
			return fmt.Errorf("Challenge gave %v, want data", step.KindOf(c1))
		}
		aopts := decodeAssertion(t, step.DataOf(c1))
		abody := a.assertion(t, aopts.Challenge, 1, true, nil)

		v, err := m.Verify(ctx, authn.FlowState{Account: acct, Private: step.PrivateOf(c1)}, rec.ID,
			authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldCredential: string(abody)}})
		if err != nil {
			return fmt.Errorf("Verify: %w", err)
		}
		if step.KindOf(v) != step.KindPassed {
			return fmt.Errorf("Verify gave %v, want passed", step.KindOf(v))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// TestAssuranceComposesFromAPasskeyProof checks that the assurance composes from a passkey proof by whether the user was verified.
func TestAssuranceComposesFromAPasskeyProof(t *testing.T) {
	err := assembly{migrate: true}.drive(t, func(_ context.Context, reg *core.Registry) error {
		m, err := core.Resolve[authn.MFA](reg)
		if err != nil {
			return err
		}
		verified := m.Assurance(authn.Proof{Account: acct, Methods: []authn.Method{authn.MethodWebAuthn}, UserVerified: true}, nil)
		if verified.Assurance != authn.AssuranceMulti {
			return fmt.Errorf("a passkey that verified the user composed to %v, want multi-factor", verified.Assurance)
		}
		plain := m.Assurance(authn.Proof{Account: acct, Methods: []authn.Method{authn.MethodWebAuthn}}, nil)
		if plain.Assurance != authn.AssuranceSingle {
			return fmt.Errorf("a passkey that did not verify the user composed to %v, want single", plain.Assurance)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// TestAPasskeyLoginComposesToMultiFactorWithoutASecondFactor checks that a passwordless passkey login composes to multi-factor without a second factor.
func TestAPasskeyLoginComposesToMultiFactorWithoutASecondFactor(t *testing.T) {
	err := assembly{migrate: true}.drive(t, func(ctx context.Context, reg *core.Registry) error {
		m, err := core.Resolve[authn.MFA](reg)
		if err != nil {
			return err
		}
		method, err := core.Resolve[authn.LoginMethod](reg)
		if err != nil {
			return err
		}
		a := newAuthenticator(t)

		s1, err := m.Enroll(ctx, authn.FlowState{Account: acct}, authn.FactorWebAuthn,
			authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldProfile: profilePasskey}})
		if err != nil {
			return fmt.Errorf("Enroll step 1: %w", err)
		}
		creation := decodeCreation(t, step.DataOf(s1))
		handle, err := base64.RawURLEncoding.DecodeString(creation.User.ID)
		if err != nil {
			return fmt.Errorf("decoding the user handle: %w", err)
		}
		s2, err := m.Enroll(ctx, authn.FlowState{Account: acct, Private: step.PrivateOf(s1)}, authn.FactorWebAuthn,
			authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldCredential: string(a.registration(t, creation.Challenge, true))}})
		if err != nil {
			return fmt.Errorf("Enroll step 2: %w", err)
		}
		if step.KindOf(s2) != step.KindEnrolled {
			return fmt.Errorf("Enroll step 2 gave %v, want enrolled", step.KindOf(s2))
		}

		start, err := method.Begin(ctx, authn.Start{Purpose: authn.PurposeLogin})
		if err != nil {
			return fmt.Errorf("Begin: %w", err)
		}
		if step.KindOf(start) != step.KindData {
			return fmt.Errorf("Begin gave %v, want data", step.KindOf(start))
		}
		assertion := decodeAssertion(t, step.DataOf(start))
		answered, err := method.Answer(ctx,
			authn.FlowState{Purpose: authn.PurposeLogin, Step: stepPasskey, Private: step.PrivateOf(start)},
			authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{
				fieldCredential: string(a.assertion(t, assertion.Challenge, 1, true, handle)),
			}})
		if err != nil {
			return fmt.Errorf("Answer: %w", err)
		}
		if step.KindOf(answered) != step.KindProven {
			return fmt.Errorf("Answer gave %v, want proven", step.KindOf(answered))
		}
		proof := step.ProofOf(answered)
		if proof.Account != acct || !proof.UserVerified {
			return fmt.Errorf("the proof is %+v, want the account and user verification", proof)
		}

		outcome, offered, err := m.Require(ctx, acct, acct, proof)
		if err != nil {
			return fmt.Errorf("Require: %w", err)
		}
		if outcome != authn.MFANone || len(offered) != 0 {
			return fmt.Errorf("Require = %v, %+v, want no second factor", outcome, offered)
		}
		if got := m.Assurance(proof, nil); got.Assurance != authn.AssuranceMulti {
			return fmt.Errorf("Assurance = %v, want multi-factor", got.Assurance)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}
