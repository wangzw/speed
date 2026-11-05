package policy

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
)

// The cases here hold the transaction-aware policy writes: they write in the
// transaction they are handed, they return the snapshot reload for after its
// commit, and they leave the ctx-taking writes' behaviour in place.

// txAfter is one after closure a tx write handed back.
type txAfter = func(context.Context) error

var errRollback = errors.New("caller rolled back")

func TestPutRoleTxWritesInTheCallersTransaction(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	record := authz.RoleRecord{Tenant: "acme", Role: "roleA", DescriptionKey: "k"}

	var wrote bool
	err := f.handle.Transaction(func(tx *gorm.DB) error {
		after, err := f.PutRoleTx(ctx, tx, record)
		if err != nil {
			return err
		}
		wrote = after != nil
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("the caller's transaction returned %v, want the rollback", err)
	}
	if !wrote {
		t.Fatal("PutRoleTx wrote a row and returned no after closure")
	}
	if roles, err := f.Roles(ctx, "acme"); err != nil || len(roles) != 0 {
		t.Fatalf("the rolled-back role write left %v, %v, want no role", roles, err)
	}

	var after txAfter
	if err := f.handle.Transaction(func(tx *gorm.DB) error {
		var err error
		after, err = f.PutRoleTx(ctx, tx, record)
		return err
	}); err != nil {
		t.Fatalf("PutRoleTx: %v", err)
	}
	// Roles reads the table, so the row is visible as soon as it commits; the
	// snapshot is what the after closure reloads, and it is exercised by the
	// grant and binding cases below.
	if roles, _ := f.Roles(ctx, "acme"); len(roles) != 1 {
		t.Fatalf("the committed role write reads back as %v, want roleA", roles)
	}
	if err := after(ctx); err != nil {
		t.Fatalf("running the after closure: %v", err)
	}
	if roles, err := f.Roles(ctx, "acme"); err != nil || len(roles) != 1 || roles[0].Role != "roleA" {
		t.Fatalf("the committed role write reads back as %v, %v, want roleA", roles, err)
	}
}

func TestDeleteRoleTxWritesInTheCallersTransaction(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")

	err := f.handle.Transaction(func(tx *gorm.DB) error {
		if _, err := f.DeleteRoleTx(ctx, tx, "acme", "roleA"); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("the caller's transaction returned %v, want the rollback", err)
	}
	if roles, _ := f.Roles(ctx, "acme"); len(roles) != 1 {
		t.Fatalf("the rolled-back role deletion left %v, want the role kept", roles)
	}

	var after txAfter
	if err := f.handle.Transaction(func(tx *gorm.DB) error {
		var err error
		after, err = f.DeleteRoleTx(ctx, tx, "acme", "roleA")
		return err
	}); err != nil {
		t.Fatalf("DeleteRoleTx: %v", err)
	}
	if err := after(ctx); err != nil {
		t.Fatalf("running the after closure: %v", err)
	}
	if roles, _ := f.Roles(ctx, "acme"); len(roles) != 0 {
		t.Fatalf("the committed role deletion left %v, want none", roles)
	}
}

func TestSeedGrantsTxAndPutBindingTxWriteInTheCallersTransaction(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")
	grant := authz.Grant{Resource: "account", Action: "read"}
	binding := authz.Binding{Tenant: "acme", Account: "acc1", Role: "roleA"}

	var added int
	var seedAfter, bindingAfter txAfter
	err := f.handle.Transaction(func(tx *gorm.DB) error {
		var err error
		added, seedAfter, err = f.SeedGrantsTx(ctx, tx, "acme", "roleA", []authz.Grant{grant})
		if err != nil {
			return err
		}
		bindingAfter, err = f.PutBindingTx(ctx, tx, binding)
		if err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("the caller's transaction returned %v, want the rollback", err)
	}
	if added != 1 || seedAfter == nil || bindingAfter == nil {
		t.Fatalf("the rolled-back writes reported added=%d, seed after=%v, binding after=%v, want both written",
			added, seedAfter != nil, bindingAfter != nil)
	}
	if grants, _ := f.Grants(ctx, "acme", "roleA"); len(grants) != 0 {
		t.Fatalf("the rolled-back seed left %v, want no grant", grants)
	}
	if bindings, _ := f.Bindings(ctx, "acme"); len(bindings) != 0 {
		t.Fatalf("the rolled-back binding left %v, want none", bindings)
	}

	added = 0
	if err := f.handle.Transaction(func(tx *gorm.DB) error {
		var err error
		added, seedAfter, err = f.SeedGrantsTx(ctx, tx, "acme", "roleA", []authz.Grant{grant})
		if err != nil {
			return err
		}
		bindingAfter, err = f.PutBindingTx(ctx, tx, binding)
		return err
	}); err != nil {
		t.Fatalf("the committing writes: %v", err)
	}
	if added != 1 {
		t.Errorf("SeedGrantsTx added %d grant(s), want one", added)
	}
	if f.allow(t, "acc1", "acme") {
		t.Error("the snapshot already allows the account before the after work reloaded it")
	}
	for _, after := range []txAfter{seedAfter, bindingAfter} {
		if err := after(ctx); err != nil {
			t.Fatalf("running an after closure: %v", err)
		}
	}
	if grants, _ := f.Grants(ctx, "acme", "roleA"); len(grants) != 1 || grants[0].Resource != "account" {
		t.Fatalf("the committed seed reads back as %v, want the account grant", grants)
	}
	if bindings, _ := f.Bindings(ctx, "acme"); len(bindings) != 1 || bindings[0].Account != "acc1" {
		t.Fatalf("the committed binding reads back as %v, want acc1", bindings)
	}
	if !f.allow(t, "acc1", "acme") {
		t.Error("the committed grant and binding do not allow the account to read")
	}
}

func TestClearAccountBindingsTxWritesInTheCallersTransaction(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")
	mustPutBinding(t, ctx, f, "acme", "acc1", "roleA")

	err := f.handle.Transaction(func(tx *gorm.DB) error {
		if _, err := f.ClearAccountBindingsTx(ctx, tx, "acme", "acc1"); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("the caller's transaction returned %v, want the rollback", err)
	}
	if bindings, _ := f.Bindings(ctx, "acme"); len(bindings) != 1 {
		t.Fatalf("the rolled-back clear left %v, want the binding kept", bindings)
	}

	var after txAfter
	if err := f.handle.Transaction(func(tx *gorm.DB) error {
		var err error
		after, err = f.ClearAccountBindingsTx(ctx, tx, "acme", "acc1")
		return err
	}); err != nil {
		t.Fatalf("ClearAccountBindingsTx: %v", err)
	}
	if after == nil {
		t.Fatal("ClearAccountBindingsTx removed a row and returned no after closure")
	}
	if err := after(ctx); err != nil {
		t.Fatalf("running the after closure: %v", err)
	}
	if bindings, _ := f.Bindings(ctx, "acme"); len(bindings) != 0 {
		t.Fatalf("the committed clear left %v, want none", bindings)
	}

	// A clear that removes nothing returns no after work.
	if err := f.handle.Transaction(func(tx *gorm.DB) error {
		empty, err := f.ClearAccountBindingsTx(ctx, tx, "acme", "acc1")
		if err != nil {
			return err
		}
		if empty != nil {
			t.Error("a clear that removed nothing returned after work")
		}
		return nil
	}); err != nil {
		t.Fatalf("the empty clear: %v", err)
	}
}
