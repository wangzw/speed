package totp

import (
	"context"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

func nothing(context.Context, *core.Registry, *gorm.DB) error { return nil }

// TestTheDescriptorNamesWhatItDeliversAndRequires checks that the descriptor delivers the factor unclaimed and requires the database, the multi-factor module and the account reads.
func TestTheDescriptorNamesWhatItDeliversAndRequires(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.mfa.totp" {
		t.Errorf("module name = %q (constant %q), want authn.mfa.totp", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the factor capability alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.Factor)(nil)) || p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.Factor and not exclusive: the factor kinds stand side by side", p)
	}

	wantRequired := []core.Token{(*db.Database)(nil), (*authn.MFA)(nil), (*authn.Accounts)(nil)}
	if len(m.Requires) != len(wantRequired) {
		t.Fatalf("Requires = %+v, want %v", m.Requires, wantRequired)
	}
	for i, tk := range wantRequired {
		if m.Requires[i].Token != tk || m.Requires[i].Optional {
			t.Errorf("Requires[%d] = %+v, want a required %v", i, m.Requires[i], tk)
		}
	}
}

// TestImportingThePackageRegistersTheModuleWithTheProcess checks that importing the package registers the module with the process registry.
func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s", ModuleName)
	}
	if len(got.Provides) != 1 || got.Provides[0].Token != core.Token((*authn.Factor)(nil)) {
		t.Errorf("the registered module delivers %+v, want the factor capability", got.Provides)
	}
}

// TestStartRefusesWithoutAUsableEncryptionKey checks that Start refuses with ErrNoEncryptionKey when the encrypted column cannot round-trip.
func TestStartRefusesWithoutAUsableEncryptionKey(t *testing.T) {
	err := assembly{seed: true, register: true}.drive(t, nothing)
	if !errors.Is(err, authn.ErrNoEncryptionKey) {
		t.Errorf("the drive returned %v, want ErrNoEncryptionKey", err)
	}
}

// TestStartRefusesWhenTheSeedTableIsMissing checks that Start refuses with ErrNotMigrated when the seed table is missing.
func TestStartRefusesWhenTheSeedTableIsMissing(t *testing.T) {
	err := assembly{key: rootKey, register: true}.drive(t, nothing)
	if !errors.Is(err, authn.ErrNotMigrated) {
		t.Errorf("the drive returned %v, want ErrNotMigrated", err)
	}
}

// TestARealRegistryRegistersAndVerifiesATOTPFactor checks that a real registry registers a TOTP factor and verifies a later code.
func TestARealRegistryRegistersAndVerifiesATOTPFactor(t *testing.T) {
	var uri string
	err := assembly{key: rootKey, seed: true, register: true}.drive(t,
		func(ctx context.Context, reg *core.Registry, h *gorm.DB) error {
			m, err := core.Resolve[authn.MFA](reg)
			if err != nil {
				return err
			}

			first, err := m.Enroll(ctx, flow(), authn.FactorTOTP,
				authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{"kind": "totp"}})
			if err != nil {
				return fmt.Errorf("the first registration step: %w", err)
			}
			if k := step.KindOf(first); k != step.KindData {
				return fmt.Errorf("the first registration step answered %v, want data with the address", k)
			}
			options, ok := step.DataOf(first).(enrollmentOptions)
			if !ok {
				return fmt.Errorf("the registration payload is %T, want enrollmentOptions", step.DataOf(first))
			}
			uri = options.OTPAuthURI
			if !strings.HasPrefix(uri, "otpauth://totp/speed:") {
				return fmt.Errorf("the registration address %q is not an otpauth address of the issuer", uri)
			}

			id := pendingID(t, h)
			secret := secretOf(t, h, id)
			code := codeAt(t, secret, time.Now())

			second, err := m.Enroll(ctx,
				authn.FlowState{ID: "flow-1", Purpose: authn.PurposeEnroll, Account: acct, Private: step.PrivateOf(first)},
				"", input(code))
			if err != nil {
				return fmt.Errorf("the confirmation step: %w", err)
			}
			if k := step.KindOf(second); k != step.KindEnrolled {
				return fmt.Errorf("the confirmation step answered %v, want enrolled", k)
			}
			if step.DataOf(second) != nil {
				return errors.New("the confirmation step answered with a payload, so the seed was handed over twice")
			}

			verifyCode := codeAt(t, secret, time.Now().Add(periodSeconds*time.Second))
			got, err := m.Verify(ctx, flow(), id, input(verifyCode))
			if err != nil {
				return fmt.Errorf("verifying the factor: %w", err)
			}
			if k := step.KindOf(got); k != step.KindPassed {
				return fmt.Errorf("verifying answered %v, want a pass", k)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if uri == "" {
		t.Fatal("the registration answered with no otpauth address")
	}
}

// TestEnrollmentGeneratesAConventionalSeed checks that an enrollment generates and records the conventional TOTP parameters.
func TestEnrollmentGeneratesAConventionalSeed(t *testing.T) {
	var (
		bytes     int
		algorithm string
		digits    int
		period    int
	)
	err := assembly{key: rootKey, seed: true, register: true}.drive(t,
		func(ctx context.Context, reg *core.Registry, h *gorm.DB) error {
			m, err := core.Resolve[authn.MFA](reg)
			if err != nil {
				return err
			}
			if _, err = m.Enroll(ctx, flow(), authn.FactorTOTP,
				authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{"kind": "totp"}}); err != nil {
				return fmt.Errorf("the first registration step: %w", err)
			}
			id := pendingID(t, h)

			raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secretOf(t, h, id))
			if err != nil {
				return fmt.Errorf("the stored secret is not base32 without padding: %w", err)
			}
			bytes = len(raw)

			var row struct {
				Algorithm string `gorm:"column:algorithm"`
				Digits    int    `gorm:"column:digits"`
				Period    int    `gorm:"column:period"`
			}
			if err := h.Raw("SELECT algorithm, digits, period FROM "+seedTable+" WHERE factor_id = ?", id).
				Scan(&row).Error; err != nil {
				return fmt.Errorf("reading the recorded parameters: %w", err)
			}
			algorithm, digits, period = row.Algorithm, row.Digits, row.Period
			return nil
		})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if bytes != 20 {
		t.Errorf("the seed is %d bytes, want the conventional 20", bytes)
	}
	if algorithm != "SHA1" || digits != 6 || period != 30 {
		t.Errorf("the recorded parameters are %s/%d/%d, want SHA1/6/30", algorithm, digits, period)
	}
}
