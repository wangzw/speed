package main

import (
	nethttp "net/http"
	"slices"
	"strings"
	"testing"
)

// rotatedRootKey is the root key the rotation case moves to.
const rotatedRootKey = "crm-host case rotated root key, 32"

// contactOf reads one contact through the API and returns the answer.
func contactOf(t *testing.T, h *crmHost, ref tenantRef, cred credential, id string) (contactView, response) {
	t.Helper()
	resp := h.get(t, apiEndpoint, "/contacts/"+id, ref.Alias, cred)
	var got contactView
	if resp.status == nethttp.StatusOK {
		resp.decode(t, &got)
	}
	return got, resp
}

// contactsByEmail looks a tenant's contacts up by email through the API.
func contactsByEmail(t *testing.T, h *crmHost, ref tenantRef, cred credential, email string) []string {
	t.Helper()
	resp := h.get(t, apiEndpoint, "/contacts?email="+email, ref.Alias, cred)
	expect(t, "looking "+email+" up in "+ref.ID, resp, nethttp.StatusOK)
	var list contactList
	resp.decode(t, &list)
	ids := []string{}
	for _, c := range list.Contacts {
		ids = append(ids, c.ID)
	}
	return ids
}

// TestAContactRekeyRollsARotatedRootKeyOut is the case for the database
// module's key rotation as the contacts live through it, on the local stack.
//
// Three tenants each hold a contact written under the first root key, and one
// of them is disabled. The key is rotated the way the database module has a
// deployment rotate it: the new key written with, the old one listed as retired.
// A contact still reads in the clear then, since the retired key decrypts it,
// and a lookup by email finds nothing, since the digest is taken under the
// current key alone and the stored one was taken under the retired key: that
// is the window the design names, and the rekey is what closes it. After
// `contact rekey` a lookup finds each tenant's own contact, a second contact
// with the same email in the tenant is refused by the unique index again, the
// command states its work through the seam inside each tenant, and with the
// retired key dropped every contact still reads in the clear.
//
// The disabled tenant's contacts are under the retired key as much as the
// others', and dropping that key leaves them unreadable. Enter refuses a
// disabled tenant; the rekey enters each tenant through EnterRegistered, the
// roster's entry for work that still belongs to a tenant it does not serve,
// so it writes the disabled tenant's contacts again inside that tenant and
// states it there. The last subtest holds the command to that: once the
// tenant is served again, its contact reads in the clear with the retired key
// dropped and is found by its email.
func TestAContactRekeyRollsARotatedRootKeyOut(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	engine := c.stack.engine
	c.sealWithCaseKey()
	c.migrate()
	h := c.serve()
	ann := h.adminCreateUser(t, c.userNamed("ann"))
	tenants := []tenantRef{c.tenantNamed("acme"), c.tenantNamed("globex"), c.tenantNamed("dormant")}
	email := "ann@rocket.test"
	for _, ref := range tenants {
		h.adminCreateTenant(t, ref)
		h.adminGrant(t, ref, ann, roleOwner)
		expect(t, "opening acc-1 in "+ref.ID, h.send(t, api(nethttp.MethodPost, "/accounts", ref, signedIn(ann),
			map[string]any{"id": "acc-1", "name": "Rocket"})), nethttp.StatusCreated)
		expect(t, "writing con-1 in "+ref.ID, h.send(t, api(nethttp.MethodPost, "/contacts", ref, signedIn(ann),
			map[string]any{"id": "con-1", "account_id": "acc-1", "name": "Ann at " + ref.ID, "email": email})),
			nethttp.StatusCreated)
	}
	acme, globex, dormant := tenants[0], tenants[1], tenants[2]
	h.stop(t)
	c.disableTenant(dormant)

	c.config.set(engine+".encryption-key", rotatedRootKey)
	c.config.set(engine+".encryption-retired-keys", []string{caseRootKey})
	h = c.serve()
	for _, ref := range []tenantRef{acme, globex} {
		got, resp := contactOf(t, h, ref, signedIn(ann), "con-1")
		expect(t, "reading "+ref.ID+"'s con-1 under the rotated key", resp, nethttp.StatusOK)
		if got.Email != email {
			t.Errorf("%s's con-1 reads the email %q under the rotated key, want it in the clear", ref.ID, got.Email)
		}
		if ids := contactsByEmail(t, h, ref, signedIn(ann), email); len(ids) != 0 {
			t.Fatalf("%s's lookup by email finds %v before the rekey, so the rotation took no effect on the digest "+
				"and the case says nothing", ref.ID, ids)
		}
	}

	rekey := c.run(nil, "", contactGroupWord, "rekey")
	var done []rekeyedTenant
	c.resultOf(rekey, &done)
	for _, ref := range []tenantRef{acme, globex} {
		if !slices.Contains(done, rekeyedTenant{TenantID: ref.ID, Contacts: 1}) {
			t.Errorf("the rekey answered %+v, want %s's one contact written again", done, ref.ID)
		}
		if ids := contactsByEmail(t, h, ref, signedIn(ann), email); !slices.Equal(ids, []string{"con-1"}) {
			t.Errorf("%s's lookup by email finds %v after the rekey, want its con-1", ref.ID, ids)
		}
		// The unique index takes the digest under the current key again, so a
		// second contact with the email is refused, as a conflict.
		resp := h.send(t, api(nethttp.MethodPost, "/contacts", ref, signedIn(ann),
			map[string]any{"id": "con-2", "account_id": "acc-1", "name": "Ann again", "email": strings.ToUpper(email)}))
		expect(t, ref.ID+" taking a second contact with con-1's email after the rekey", resp, nethttp.StatusConflict)
		if n := c.count("SELECT count(*) FROM contacts WHERE tenant_id = ? AND id = ?", ref.ID, "con-2"); n != 0 {
			t.Errorf("%s holds a second contact with con-1's email after the rekey", ref.ID)
		}
	}
	seam := map[string]int{}
	for _, row := range c.journal() {
		if row.source == "seam" && row.action == contactRekeyAction && row.object == contactTable {
			seam[row.tenant]++
		}
	}
	for _, ref := range tenants {
		if seam[ref.ID] != 1 {
			t.Errorf("the journal holds %d rekey seam rows in %s, want 1: %v", seam[ref.ID], ref.ID, seam)
		}
	}
	h.stop(t)

	c.config.set(engine+".encryption-retired-keys", []string{})
	c.mustRun("", "tenant", "enable", "--tenant-id", dormant.ID)
	h = c.serve()
	for _, ref := range []tenantRef{acme, globex} {
		got, resp := contactOf(t, h, ref, signedIn(ann), "con-1")
		expect(t, "reading "+ref.ID+"'s con-1 with the retired key dropped", resp, nethttp.StatusOK)
		if got.Email != email {
			t.Errorf("%s's con-1 reads the email %q with the retired key dropped, want it in the clear", ref.ID, got.Email)
		}
	}

	t.Run("a disabled tenant's contacts are rekeyed as well", func(t *testing.T) {
		if rekey.err != nil {
			t.Errorf("the rekey failed: %v\n%s", rekey.err, rekey.combined())
		}
		if !slices.Contains(done, rekeyedTenant{TenantID: dormant.ID, Contacts: 1}) {
			t.Errorf("the rekey answered %+v, want the disabled %s's one contact written again", done, dormant.ID)
		}
		got, resp := contactOf(t, h, dormant, signedIn(ann), "con-1")
		if resp.status != nethttp.StatusOK || got.Email != email {
			t.Errorf("dormant's con-1, disabled through the rekey, answered %d %s with the retired key dropped, "+
				"want it in the clear", resp.status, resp.body)
		}
		if ids := contactsByEmail(t, h, dormant, signedIn(ann), email); !slices.Equal(ids, []string{"con-1"}) {
			t.Errorf("dormant's lookup by email finds %v once it is served again, want its con-1", ids)
		}
	})
}
