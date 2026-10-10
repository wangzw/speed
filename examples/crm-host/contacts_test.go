package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"maps"
	nethttp "net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/log"
)

// storedContact is a contact row as the table holds it: the sealed columns as
// the text the database module wrote into them.
type storedContact struct {
	email, bidx string
	phone       sql.NullString
	deleted     bool
}

// storedContacts reads every row of the contacts table under one identifier,
// soft deleted or not, keyed by tenant.
func (c *crmCase) storedContacts(id string) map[string]storedContact {
	c.t.Helper()
	rows, err := c.sql().Query(c.rebind(
		"SELECT tenant_id, email, email_bidx, phone, (deleted_at IS NOT NULL) FROM contacts WHERE id = ?"), id)
	if err != nil {
		c.t.Fatalf("reading the contacts under %s: %v", id, err)
	}
	defer rows.Close()
	out := map[string]storedContact{}
	for rows.Next() {
		var tenantID string
		var s storedContact
		if err := rows.Scan(&tenantID, &s.email, &s.bidx, &s.phone, &s.deleted); err != nil {
			c.t.Fatalf("scanning a contact row: %v", err)
		}
		out[tenantID] = s
	}
	if err := rows.Err(); err != nil {
		c.t.Fatalf("reading the contacts under %s: %v", id, err)
	}
	return out
}

// contactsWithIndex counts the live contacts of a tenant stored under one blind
// index.
func (c *crmCase) contactsWithIndex(tenantID, bidx string) int {
	c.t.Helper()
	var n int
	err := c.sql().QueryRow(c.rebind(
		"SELECT COUNT(*) FROM contacts WHERE tenant_id = ? AND email_bidx = ? AND deleted_at IS NULL"),
		tenantID, bidx).Scan(&n)
	if err != nil {
		c.t.Fatalf("counting the contacts under an index: %v", err)
	}
	return n
}

// requireSealed fails a case unless a stored column holds a seal of the
// plaintext rather than the plaintext: base64 text, with no character of the
// plaintext's own shape, decoding to more bytes than the plaintext — a nonce and
// an authentication tag besides the ciphertext.
func requireSealed(t *testing.T, what, stored, plaintext string) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(stored)
	if err != nil || stored == plaintext || strings.Contains(stored, "@") || len(raw) <= len(plaintext) {
		t.Errorf("%s is stored as %q, which is not a seal of %q", what, stored, plaintext)
	}
}

// contactIDs lists the identifiers of a listing, in order.
func contactIDs(list contactList) []string {
	var ids []string
	for _, c := range list.Contacts {
		ids = append(ids, c.ID+"@"+c.TenantID)
	}
	return ids
}

// TestContactsStayInTheirTenant is the case for every statement form the
// contacts module issues, each in two tenants holding the same identifiers; for
// the sealed columns, the blind index and the unique email; for the redaction
// of a contact's email and phone; and for the role each route takes.
func TestContactsStayInTheirTenant(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.sealWithCaseKey()
		c.migrate()
		h := c.serve()
		cr := setUpCrew(t, c, h)
		aliceAcme, aliceGlobex := cr.as(h, cr.alice, cr.acme), cr.as(h, cr.alice, cr.globex)
		ivan, rita, vera := cr.as(h, cr.ivan, cr.acme), cr.as(h, cr.rita, cr.acme), cr.as(h, cr.vera, cr.acme)
		gus := cr.as(h, cr.gus, cr.globex)
		acme, globex := cr.acme.ID, cr.globex.ID

		// One account identifier in both tenants, under different names, and
		// a second account in acme alone.
		aliceAcme.must(t, nethttp.MethodPost, "/accounts", map[string]any{"id": "acc-1", "name": "Rocket Works"},
			nethttp.StatusCreated)
		aliceAcme.must(t, nethttp.MethodPost, "/accounts", map[string]any{"id": "acc-2", "name": "Anvil Corp"},
			nethttp.StatusCreated)
		aliceGlobex.must(t, nethttp.MethodPost, "/accounts", map[string]any{"id": "acc-1", "name": "Rocket Supply"},
			nethttp.StatusCreated)

		get := func(t *testing.T, who actor, path string) contactList {
			t.Helper()
			var list contactList
			who.must(t, nethttp.MethodGet, path, nil, nethttp.StatusOK).decode(t, &list)
			return list
		}

		t.Run("a contact is sealed at rest and reads back in the clear", func(t *testing.T) {
			sub := c.in(t)
			var made contactView
			rita.must(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-1", "account_id": "acc-1", "name": "Ada Lovelace",
				"email": "Ada@Example.test", "phone": "+1 555 0100", "title": "CTO",
			}, nethttp.StatusCreated).decode(t, &made)
			if made.TenantID != acme || made.Email != "Ada@Example.test" || made.Phone == nil || *made.Phone != "+1 555 0100" {
				t.Errorf("the creation in acme answered %+v", made)
			}
			// The same person at the other tenant, under the same identifier
			// and the same address, written the same way: the email is
			// unique within a tenant.
			gus.must(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-1", "account_id": "acc-1", "name": "Grace Hopper", "email": "Ada@Example.test",
			}, nethttp.StatusCreated)

			stored := sub.storedContacts("con-1")
			mine, other := stored[acme], stored[globex]
			requireSealed(t, "acme's email", mine.email, "Ada@Example.test")
			requireSealed(t, "acme's phone", mine.phone.String, "+1 555 0100")
			requireSealed(t, "globex's email", other.email, "Ada@Example.test")
			if other.phone.Valid {
				t.Errorf("globex's con-1 was given no phone and stores %q", other.phone.String)
			}
			// The two rows seal one plaintext under the one root key the
			// database is configured with, so only a nonce drawn afresh for
			// each seal tells them apart.
			if mine.email == other.email {
				t.Errorf("one address is stored as the same text in two rows, %q: the seal takes no fresh nonce", mine.email)
			}
			// The blind index is the one column the two rows agree on: the
			// address normalises to the same text. That is what leaves the
			// tenant condition as the only thing keeping a lookup apart.
			if mine.bidx == "" || mine.bidx != other.bidx {
				t.Errorf("the two rows index one address as %q and %q, want one digest", mine.bidx, other.bidx)
			}

			var got contactView
			rita.must(t, nethttp.MethodGet, "/contacts/con-1", nil, nethttp.StatusOK).decode(t, &got)
			if got.TenantID != acme || got.Name != "Ada Lovelace" || got.Email != "Ada@Example.test" ||
				got.Phone == nil || *got.Phone != "+1 555 0100" {
				t.Errorf("acme's con-1 reads back as %+v", got)
			}
			gus.must(t, nethttp.MethodGet, "/contacts/con-1", nil, nethttp.StatusOK).decode(t, &got)
			if got.TenantID != globex || got.Name != "Grace Hopper" || got.Phone != nil {
				t.Errorf("globex's con-1 reads back as %+v", got)
			}
		})

		t.Run("a lookup by email goes through the blind index and stays in the tenant", func(t *testing.T) {
			// Case and surrounding blanks are flattened on both sides.
			requireNames(t, "acme's contacts with the address",
				contactIDs(get(t, rita, "/contacts?email=%20ADA@example.TEST%20")), []string{"con-1@" + acme})
			requireNames(t, "globex's contacts with the address",
				contactIDs(get(t, gus, "/contacts?email=ada@example.test")), []string{"con-1@" + globex})
			requireNames(t, "acme's contacts with an address nobody has",
				contactIDs(get(t, rita, "/contacts?email=nobody@example.test")), nil)
			expect(t, "a listing naming nothing", rita.send(t, nethttp.MethodGet, "/contacts", nil),
				nethttp.StatusUnprocessableEntity)
		})

		t.Run("a contact's email and phone reach no log record", func(t *testing.T) {
			rita.must(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-2", "account_id": "acc-2", "name": "Bea Brown",
				"email": "bea@example.test", "phone": "+44 20 7946 0000",
			}, nethttp.StatusCreated)
			var rec record
			if !eventually(settleLimit, func() bool {
				var ok bool
				rec, ok = findRecord(h.output(), "contact created", "contact_id", "con-2", "tenant_id", acme)
				return ok
			}) {
				t.Fatalf("the run wrote no record of con-2's creation:\n%s", tail(h.output()))
			}
			if rec[contactEmailKey] != log.MaskedText || rec[contactPhoneKey] != log.MaskedText {
				t.Errorf("the record of con-2's creation carries %s=%q and %s=%q, want both masked",
					contactEmailKey, rec[contactEmailKey], contactPhoneKey, rec[contactPhoneKey])
			}
			for _, r := range records(h.output()) {
				for key, value := range r {
					if strings.Contains(value, "bea@example.test") || strings.Contains(value, "7946 0000") {
						t.Errorf("a record carries con-2's email or phone under %q: %v", key, r)
					}
				}
			}
		})

		t.Run("a listing by account stays in the tenant", func(t *testing.T) {
			requireNames(t, "acme's contacts at acc-1",
				contactIDs(get(t, rita, "/contacts?account_id=acc-1")), []string{"con-1@" + acme})
			requireNames(t, "globex's contacts at acc-1",
				contactIDs(get(t, gus, "/contacts?account_id=acc-1")), []string{"con-1@" + globex})
			requireNames(t, "acme's contacts at acc-2",
				contactIDs(get(t, rita, "/contacts?account_id=acc-2")), []string{"con-2@" + acme})
		})

		t.Run("a contact names an account of its own tenant", func(t *testing.T) {
			sub := c.in(t)
			expect(t, "a contact in globex at acme's acc-2", gus.send(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-3", "account_id": "acc-2", "name": "Nobody", "email": "nobody@example.test",
			}), nethttp.StatusUnprocessableEntity)
			if rows := sub.storedContacts("con-3"); len(rows) != 0 {
				t.Errorf("a refused contact left %+v", rows)
			}
		})

		t.Run("a find-or-create by email creates once per tenant", func(t *testing.T) {
			sub := c.in(t)
			var got contactView
			rita.must(t, nethttp.MethodPost, "/contacts/upsert", map[string]any{
				"id": "con-7", "account_id": "acc-1", "name": "Cy Twombly", "email": "cy@example.test",
			}, nethttp.StatusCreated).decode(t, &got)
			if got.ID != "con-7" || got.TenantID != acme || got.Email != "cy@example.test" {
				t.Errorf("the first find-or-create answered %+v", got)
			}
			rita.must(t, nethttp.MethodPost, "/contacts/upsert", map[string]any{
				"id": "con-8", "account_id": "acc-2", "name": "Someone Else", "email": " CY@example.test ",
			}, nethttp.StatusOK).decode(t, &got)
			if got.ID != "con-7" || got.Name != "Cy Twombly" || got.AccountID != "acc-1" {
				t.Errorf("the second find-or-create answered %+v, want con-7 as it was created", got)
			}
			// The other tenant has no contact with the address, and gets its
			// own under the same identifier.
			gus.must(t, nethttp.MethodPost, "/contacts/upsert", map[string]any{
				"id": "con-7", "account_id": "acc-1", "name": "Cy Globex", "email": "cy@example.test",
			}, nethttp.StatusCreated).decode(t, &got)
			if got.ID != "con-7" || got.TenantID != globex || got.Name != "Cy Globex" {
				t.Errorf("the find-or-create in globex answered %+v", got)
			}
			stored := sub.storedContacts("con-7")
			if len(stored) != 2 || sub.contactsWithIndex(acme, stored[acme].bidx) != 1 {
				t.Errorf("con-7 is stored as %+v, want one row in each tenant", stored)
			}
			if rows := sub.storedContacts("con-8"); len(rows) != 0 {
				t.Errorf("a find-or-create that found a contact wrote %+v", rows)
			}
			requireSealed(t, "the email a find-or-create wrote", stored[acme].email, "cy@example.test")
			rita.must(t, nethttp.MethodGet, "/contacts/con-7", nil, nethttp.StatusOK).decode(t, &got)
			if got.Name != "Cy Twombly" {
				t.Errorf("acme's con-7 reads back as %+v", got)
			}
		})

		t.Run("the export streams the tenant's contacts through Rows", func(t *testing.T) {
			for _, tc := range []struct {
				who  actor
				want [][]string
			}{
				{vera, [][]string{
					{"con-1", "acc-1", "Ada Lovelace", "CTO"},
					{"con-7", "acc-1", "Cy Twombly", ""},
					{"con-2", "acc-2", "Bea Brown", ""},
				}},
				{gus, [][]string{
					{"con-7", "acc-1", "Cy Globex", ""},
					{"con-1", "acc-1", "Grace Hopper", ""},
				}},
			} {
				resp := tc.who.must(t, nethttp.MethodGet, "/contacts/export", nil, nethttp.StatusOK)
				if ct := resp.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
					t.Errorf("the export in %s is served as %q", tc.who.tenant.ID, ct)
				}
				// The selection leaves the sealed columns out, and the header
				// above is the whole of what each line carries: no address
				// reaches the export.
				if strings.Contains(string(resp.body), "@") {
					t.Errorf("the export in %s carries an email:\n%s", tc.who.tenant.ID, resp.body)
				}
				lines, err := csv.NewReader(strings.NewReader(string(resp.body))).ReadAll()
				if err != nil {
					t.Fatalf("the export in %s is not CSV: %v\n%s", tc.who.tenant.ID, err, resp.body)
				}
				if len(lines) == 0 || !slices.Equal(lines[0], exportColumns) {
					t.Fatalf("the export in %s starts with %v, want the header %v", tc.who.tenant.ID, lines, exportColumns)
				}
				var got [][]string
				for _, line := range lines[1:] {
					got = append(got, line[:4])
					if line[4] == "" {
						t.Errorf("the export in %s carries no creation time for %s", tc.who.tenant.ID, line[0])
					}
				}
				if !slices.EqualFunc(got, tc.want, slices.Equal) {
					t.Errorf("the export in %s holds %v, want %v", tc.who.tenant.ID, got, tc.want)
				}
			}
		})

		t.Run("the autocomplete plucks distinct names from the tenant", func(t *testing.T) {
			// A second Cy Twombly in acme, at another account: the name is
			// answered once.
			rita.must(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-12", "account_id": "acc-2", "name": "Cy Twombly", "email": "cy.twombly@example.test",
			}, nethttp.StatusCreated)
			for _, tc := range []struct {
				who    actor
				prefix string
				want   []string
			}{
				{vera, "c", []string{"Cy Twombly"}},
				{gus, "C", []string{"Cy Globex"}},
				{vera, "", []string{"Ada Lovelace", "Bea Brown", "Cy Twombly"}},
				{gus, "", []string{"Cy Globex", "Grace Hopper"}},
				{vera, "%25", nil},
			} {
				var got autocompleteView
				tc.who.must(t, nethttp.MethodGet, "/contacts/autocomplete?prefix="+tc.prefix, nil, nethttp.StatusOK).
					decode(t, &got)
				requireNames(t, "the names in "+tc.who.tenant.ID+" starting with "+tc.prefix, got.Names, tc.want)
			}
		})

		t.Run("the directory joins each contact's own account and its owner", func(t *testing.T) {
			// The directory's join names the nested path to the account's
			// owner alone, so the account is its middle level; both tenants
			// hold acc-1, and a middle level left without the tenant
			// condition would list each contact at acc-1 once per tenant.
			for _, tc := range []struct {
				who  actor
				want []string
			}{
				{vera, []string{"con-1 Rocket Works", "con-2 Anvil Corp", "con-12 Anvil Corp", "con-7 Rocket Works"}},
				{gus, []string{"con-7 Rocket Supply", "con-1 Rocket Supply"}},
			} {
				var got directoryView
				tc.who.must(t, nethttp.MethodGet, "/contacts/directory", nil, nethttp.StatusOK).decode(t, &got)
				var entries []string
				for _, e := range got.Entries {
					entries = append(entries, e.ID+" "+e.AccountName)
					if e.AccountOwner != "alice" {
						t.Errorf("the directory of %s shows %s's account owned by %q, want alice, who opened it",
							tc.who.tenant.ID, e.ID, e.AccountOwner)
					}
				}
				requireNames(t, "the directory of "+tc.who.tenant.ID, entries, tc.want)
			}
		})

		t.Run("an update through a map reseals the email and moves its index, in the tenant", func(t *testing.T) {
			sub := c.in(t)
			before := sub.storedContacts("con-1")
			var got contactView
			rita.must(t, nethttp.MethodPatch, "/contacts/con-1", map[string]any{
				"email": "ada@analytical.test", "title": "Chief Engineer",
			}, nethttp.StatusOK).decode(t, &got)
			if got.Email != "ada@analytical.test" || got.Title != "Chief Engineer" || got.Name != "Ada Lovelace" {
				t.Errorf("the update answered %+v", got)
			}
			after := sub.storedContacts("con-1")
			requireSealed(t, "acme's email after the update", after[acme].email, "ada@analytical.test")
			if after[acme].email == before[acme].email || after[acme].bidx == before[acme].bidx {
				t.Errorf("acme's con-1 kept its stored email or index across the update: %+v", after[acme])
			}
			if after[globex] != before[globex] {
				t.Errorf("globex's con-1 changed with acme's update: %+v, was %+v", after[globex], before[globex])
			}
			// Writing the address the row already holds seals it again: the
			// stored text changes and the index, a digest of the address,
			// does not.
			rita.must(t, nethttp.MethodPatch, "/contacts/con-1", map[string]any{"email": "ada@analytical.test"},
				nethttp.StatusOK)
			again := sub.storedContacts("con-1")[acme]
			requireSealed(t, "acme's email after it is written again", again.email, "ada@analytical.test")
			if again.email == after[acme].email || again.bidx != after[acme].bidx {
				t.Errorf("rewriting acme's con-1 with its own address stored %+v, was %+v: want a new seal under the same index",
					again, after[acme])
			}
			after[acme] = again
			requireNames(t, "acme's contacts at the new address",
				contactIDs(get(t, rita, "/contacts?email=ada@analytical.test")), []string{"con-1@" + acme})
			requireNames(t, "acme's contacts at the old address",
				contactIDs(get(t, rita, "/contacts?email=ada@example.test")), nil)
			requireNames(t, "globex's contacts at the old address",
				contactIDs(get(t, gus, "/contacts?email=ada@example.test")), []string{"con-1@" + globex})

			// A member spelled in another case is read as the field it
			// resembles, as the HTTP module's binder reads a body, so the
			// update writes that field. The address is put back for the steps
			// that follow.
			expect(t, "an update spelling the email member in another case",
				rita.send(t, nethttp.MethodPatch, "/contacts/con-1", map[string]any{"Email": "mallory@example.test"}),
				nethttp.StatusOK)
			requireNames(t, "acme's contacts at the address the other-case member named",
				contactIDs(get(t, rita, "/contacts?email=mallory@example.test")), []string{"con-1@" + acme})
			rita.must(t, nethttp.MethodPatch, "/contacts/con-1", map[string]any{"email": "ada@analytical.test"},
				nethttp.StatusOK)

			// An empty phone clears it.
			rita.must(t, nethttp.MethodPatch, "/contacts/con-1", map[string]any{"phone": ""}, nethttp.StatusOK).
				decode(t, &got)
			if got.Phone != nil || sub.storedContacts("con-1")[acme].phone.Valid {
				t.Errorf("clearing the phone left %+v", got)
			}
			expect(t, "an update in globex of acme's con-2",
				gus.send(t, nethttp.MethodPatch, "/contacts/con-2", map[string]any{"name": "Taken Over"}),
				nethttp.StatusNotFound)
			expect(t, "an update naming nothing",
				rita.send(t, nethttp.MethodPatch, "/contacts/con-1", map[string]any{}), nethttp.StatusUnprocessableEntity)
		})

		t.Run("a second contact with one email is refused within the tenant and not across", func(t *testing.T) {
			sub := c.in(t)
			bidx := sub.storedContacts("con-2")[acme].bidx
			resp := rita.send(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-9", "account_id": "acc-2", "name": "Bea Again", "email": "BEA@example.test",
			})
			// The unique index refuses the row whatever the answer says.
			if n := sub.contactsWithIndex(acme, bidx); n != 1 {
				t.Errorf("acme holds %d live contacts at con-2's address after a second creation, want 1", n)
			}
			if rows := sub.storedContacts("con-9"); len(rows) != 0 {
				t.Errorf("the refused contact was stored: %+v", rows)
			}
			// The refusal is a conflict: the host tells it from any other
			// failure by the database module's mark for a unique violation,
			// gorm.ErrDuplicatedKey, the same on both dialects.
			if resp.status != nethttp.StatusConflict {
				t.Errorf("a second contact with con-2's email answered %d %s, want %d", resp.status, resp.body,
					nethttp.StatusConflict)
			}
			// An update giving another contact that address meets the same
			// index, and is the same conflict.
			held := sub.storedContacts("con-1")[acme]
			resp = rita.send(t, nethttp.MethodPatch, "/contacts/con-1", map[string]any{"email": "Bea@Example.test"})
			if now := sub.storedContacts("con-1")[acme]; now != held {
				t.Errorf("an update of con-1 to con-2's email changed it to %+v, was %+v", now, held)
			}
			if resp.status != nethttp.StatusConflict {
				t.Errorf("an update of con-1 to con-2's email answered %d %s, want %d", resp.status, resp.body,
					nethttp.StatusConflict)
			}
			gus.must(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-9", "account_id": "acc-1", "name": "Bea Globex", "email": "bea@example.test",
			}, nethttp.StatusCreated)
			expect(t, "acme reading the con-9 globex holds",
				rita.send(t, nethttp.MethodGet, "/contacts/con-9", nil), nethttp.StatusNotFound)
		})

		t.Run("a delete is soft, stays in the tenant and frees the email", func(t *testing.T) {
			sub := c.in(t)
			refused(t, "a viewer deleting a contact", vera.send(t, nethttp.MethodDelete, "/contacts/con-1", nil))
			refused(t, "a rep deleting a contact", rita.send(t, nethttp.MethodDelete, "/contacts/con-1", nil))
			ivan.must(t, nethttp.MethodDelete, "/contacts/con-1", nil, nethttp.StatusNoContent)
			expect(t, "acme's con-1 after its delete",
				rita.send(t, nethttp.MethodGet, "/contacts/con-1", nil), nethttp.StatusNotFound)
			gus.must(t, nethttp.MethodGet, "/contacts/con-1", nil, nethttp.StatusOK)
			stored := sub.storedContacts("con-1")
			if !stored[acme].deleted || stored[globex].deleted {
				t.Errorf("con-1 is stored as %+v after acme's delete, want acme's kept and marked deleted, globex's live",
					stored)
			}
			expect(t, "a second delete of acme's con-1",
				ivan.send(t, nethttp.MethodDelete, "/contacts/con-1", nil), nethttp.StatusNotFound)
			// The unique index covers the live rows, so the address is free.
			rita.must(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-10", "account_id": "acc-1", "name": "Ada Again", "email": "ada@analytical.test",
			}, nethttp.StatusCreated)
		})

		t.Run("each write takes its role and is journalled with its statement", func(t *testing.T) {
			sub := c.in(t)
			vera.must(t, nethttp.MethodGet, "/contacts/con-2", nil, nethttp.StatusOK)
			refused(t, "a viewer creating a contact", vera.send(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-11", "account_id": "acc-1", "name": "Viewer", "email": "viewer@example.test",
			}))
			refused(t, "a viewer updating a contact",
				vera.send(t, nethttp.MethodPatch, "/contacts/con-2", map[string]any{"name": "Viewer"}))
			refused(t, "a viewer finding or creating a contact",
				vera.send(t, nethttp.MethodPost, "/contacts/upsert", map[string]any{
					"account_id": "acc-1", "name": "Viewer", "email": "viewer@example.test",
				}))
			if rows := sub.storedContacts("con-11"); len(rows) != 0 {
				t.Errorf("a refused creation left %+v", rows)
			}
			for _, want := range []struct {
				what                            string
				source, action, object, outcome string
				who                             actor
			}{
				{"the viewer's refused creation", "request", "POST", routePath(createContactRoute), "403", vera},
				{"the viewer's refused update", "request", "PATCH", routePath(patchContactRoute), "403", vera},
				{"the viewer's refused find-or-create", "request", "POST", routePath(upsertContactRoute), "403", vera},
				{"the viewer's refused delete", "request", "DELETE", routePath(deleteContactRoute), "403", vera},
				{"the rep's refused delete", "request", "DELETE", routePath(deleteContactRoute), "403", rita},
				{"the rep's creation", "request", "POST", routePath(createContactRoute), "201", rita},
				{"the rep's update", "request", "PATCH", routePath(patchContactRoute), "200", rita},
				{"the rep's find-or-create", "request", "POST", routePath(upsertContactRoute), "201", rita},
				{"the admin's delete", "request", "DELETE", routePath(deleteContactRoute), "204", ivan},
				{"the other tenant's creation", "request", "POST", routePath(createContactRoute), "201", gus},
				{"the statement of the rep's creation", "statement", "create", contactTable, "", rita},
				{"the statement of the rep's update", "statement", "update", contactTable, "", rita},
				{"the statement of the admin's delete", "statement", "delete", contactTable, "", ivan},
				{"the statement of the other tenant's creation", "statement", "create", contactTable, "", gus},
			} {
				sub.awaitJournal(want.what, func(r auditRow) bool {
					return r.source == want.source && r.action == want.action && r.object == want.object &&
						(want.outcome == "" || r.outcome == want.outcome) &&
						r.tenant == want.who.tenant.ID && r.actor == want.who.sessionActor()
				})
			}
		})

		// The cases below stand for a second writer with a transaction the
		// case holds open on its own connection, so the interleaving each
		// needs happens on every run rather than by chance. They run where
		// the engine locks rows: SQLite's transactions exclude each other
		// over the whole database, and with the locator this harness writes,
		// which sets no busy timeout, one held open there makes the host's
		// write fail as busy rather than wait.
		if c.dialect != "postgres" {
			return
		}

		t.Run("a contact written while its account's delete is uncommitted waits for it and is refused", func(t *testing.T) {
			sub := c.in(t)
			aliceAcme.must(t, nethttp.MethodPost, "/accounts", map[string]any{"id": "acc-8", "name": "Closing Down"},
				nethttp.StatusCreated)
			held := sub.hold()
			held.exec("UPDATE accounts SET deleted_at = ? WHERE tenant_id = ? AND id = ?", time.Now().UTC(), acme, "acc-8")
			write := rita.start(nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-20", "account_id": "acc-8", "name": "Late Arrival", "email": "late@closing.test",
			})
			if !sub.awaitLockWaits(write, 1) {
				t.Errorf("the contact at acc-8 was answered before the account's delete committed, " +
					"so its check took no lock the delete held up")
			}
			held.commit()
			expect(t, "a contact at acc-8 whose delete committed while it waited", write.wait(t), nethttp.StatusUnprocessableEntity)
			if rows := sub.storedContacts("con-20"); len(rows) != 0 {
				t.Errorf("a contact was stored under acc-8 after the account's delete: %+v", rows)
			}
		})

		t.Run("an account's delete waits for a contact being written under it and takes it along", func(t *testing.T) {
			sub := c.in(t)
			aliceAcme.must(t, nethttp.MethodPost, "/accounts", map[string]any{"id": "acc-6", "name": "Busy Line"},
				nethttp.StatusCreated)
			// The held writer is another contact write: it holds the account
			// under a share lock, as the contacts module does, and inserts a
			// row whose email is a seal the host wrote, so the row reads back.
			seal := sub.storedContacts("con-2")[acme].email
			held := sub.hold()
			held.lockShared("SELECT id FROM accounts WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL FOR SHARE",
				acme, "acc-6")
			held.exec("INSERT INTO contacts (tenant_id, id, account_id, name, email, email_bidx, title, created_at) "+
				"VALUES (?, ?, ?, ?, ?, ?, ?, ?)", acme, "con-21", "acc-6", "Early Bird", seal, "held-writer-index", "",
				time.Now().UTC())
			remove := ivan.start(nethttp.MethodDelete, "/accounts/acc-6", nil)
			if !sub.awaitLockWaits(remove, 1) {
				t.Errorf("the delete of acc-6 was answered before the contact under it committed")
			}
			held.commit()
			expect(t, "the delete of acc-6", remove.wait(t), nethttp.StatusNoContent)
			if stored := sub.storedContacts("con-21")[acme]; !stored.deleted {
				t.Errorf("con-21, committed while acc-6's delete waited, is live under the deleted account: %+v", stored)
			}
		})

		t.Run("a contact's check holds its account until the contact is written", func(t *testing.T) {
			sub := c.in(t)
			aliceAcme.must(t, nethttp.MethodPost, "/accounts", map[string]any{"id": "acc-5", "name": "Slow Close"},
				nethttp.StatusCreated)
			// The index the host writes for an address, taken from a contact
			// written with it and then deleted, which frees it.
			rita.must(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-23", "account_id": "acc-5", "name": "First Try", "email": "slow@close.test",
			}, nethttp.StatusCreated)
			index := sub.storedContacts("con-23")[acme].bidx
			ivan.must(t, nethttp.MethodDelete, "/contacts/con-23", nil, nethttp.StatusNoContent)

			// A held writer has inserted a live contact under that index, so
			// the host's insert of the address waits for it, after the
			// host's check of the account has run. The held row is at
			// another account: the reference it is checked against locks
			// the account it names, and the only lock on acc-5 is then the
			// host's own.
			seal := sub.storedContacts("con-2")[acme].email
			held := sub.hold()
			held.exec("INSERT INTO contacts (tenant_id, id, account_id, name, email, email_bidx, title, created_at) "+
				"VALUES (?, ?, ?, ?, ?, ?, ?, ?)", acme, "con-22", "acc-2", "Placeholder", seal, index, "",
				time.Now().UTC())
			write := rita.start(nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-24", "account_id": "acc-5", "name": "Second Try", "email": "slow@close.test",
			})
			if !sub.awaitLockWaits(write, 1) {
				t.Fatalf("the contact at acc-5 did not wait for the held writer's row under its index")
			}
			// The account's delete now meets a contact whose check has run and
			// whose insert has not. The check's lock lasts until that insert
			// commits, so the delete waits for it.
			remove := ivan.start(nethttp.MethodDelete, "/accounts/acc-5", nil)
			if !sub.awaitLockWaits(remove, 2) {
				t.Errorf("the delete of acc-5 was answered while a contact checked against it was still being written")
			}
			held.rollback()
			expect(t, "the contact at acc-5", write.wait(t), nethttp.StatusCreated)
			expect(t, "the delete of acc-5", remove.wait(t), nethttp.StatusNoContent)
			if stored := sub.storedContacts("con-24")[acme]; !stored.deleted {
				t.Errorf("con-24, checked against acc-5 before its delete and written after it, is live under the "+
					"deleted account: %+v", stored)
			}
		})

		t.Run("an account's delete locks its own tenant's row and no other", func(t *testing.T) {
			sub := c.in(t)
			// A writer in globex holds globex's acc-1. Acme's delete of the
			// acc-1 acme holds locks acme's row alone, so it has nothing to
			// wait for; a lock taken without the tenant condition would take
			// globex's row too and wait for that writer.
			held := sub.hold()
			held.lockShared("SELECT id FROM accounts WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL FOR SHARE",
				globex, "acc-1")
			remove := ivan.start(nethttp.MethodDelete, "/accounts/acc-1", nil)
			if sub.awaitLockWaits(remove, 1) {
				t.Errorf("acme's delete of acc-1 waited for the lock a writer in globex holds on globex's acc-1")
			}
			held.commit()
			expect(t, "acme's delete of acc-1", remove.wait(t), nethttp.StatusNoContent)
			gus.must(t, nethttp.MethodGet, "/accounts/acc-1", nil, nethttp.StatusOK)
		})
	})
}

// heldTransaction is a transaction a case holds open on a connection of its
// own, standing for another writer whose statements have run and whose commit
// has not.
type heldTransaction struct {
	c  *crmCase
	tx *sql.Tx
}

// hold begins a transaction on the case's own connection. It is rolled back at
// the end of the test unless it was committed.
func (c *crmCase) hold() *heldTransaction {
	c.t.Helper()
	tx, err := c.sql().BeginTx(c.t.Context(), nil)
	if err != nil {
		c.t.Fatalf("beginning a held transaction: %v", err)
	}
	c.t.Cleanup(func() { _ = tx.Rollback() })
	return &heldTransaction{c: c, tx: tx}
}

// exec runs one statement in the held transaction.
func (h *heldTransaction) exec(statement string, args ...any) {
	h.c.t.Helper()
	if _, err := h.tx.ExecContext(h.c.t.Context(), h.c.rebind(statement), args...); err != nil {
		h.c.t.Fatalf("running %q in the held transaction: %v", statement, err)
	}
}

// lockShared runs a locking read of one row in the held transaction, failing
// the case unless it found the row.
func (h *heldTransaction) lockShared(statement string, args ...any) {
	h.c.t.Helper()
	var id string
	if err := h.tx.QueryRowContext(h.c.t.Context(), h.c.rebind(statement), args...).Scan(&id); err != nil {
		h.c.t.Fatalf("running %q in the held transaction: %v", statement, err)
	}
}

// commit ends the held transaction by committing it.
func (h *heldTransaction) commit() {
	h.c.t.Helper()
	if err := h.tx.Commit(); err != nil {
		h.c.t.Fatalf("committing the held transaction: %v", err)
	}
}

// rollback ends the held transaction by rolling it back.
func (h *heldTransaction) rollback() {
	h.c.t.Helper()
	if err := h.tx.Rollback(); err != nil {
		h.c.t.Fatalf("rolling the held transaction back: %v", err)
	}
}

// inFlight is a request sent off the case's goroutine, so the case can act
// while the host is still working on it.
type inFlight struct {
	what string
	done chan struct{}
	resp response
	err  error
}

// start sends one request as the actor without waiting for the answer.
func (a actor) start(method, path string, body any) *inFlight {
	f := &inFlight{what: method + " " + path, done: make(chan struct{})}
	go func() {
		defer close(f.done)
		f.resp, f.err = a.h.do(request{
			method: method, endpoint: apiEndpoint, path: path, tenant: a.tenant.Alias, cred: bearer(a.token), body: body,
		})
	}()
	return f
}

// answered reports whether the request has been answered.
func (f *inFlight) answered() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

// wait answers the response, failing the case when the request could not be
// made. The shared client's timeout bounds the wait.
func (f *inFlight) wait(t *testing.T) response {
	t.Helper()
	<-f.done
	if f.err != nil {
		t.Fatalf("%s: %v", f.what, f.err)
	}
	return f.resp
}

// awaitLockWaits waits until at least n sessions on the case's database wait
// for a row lock, and reports false when the request was answered first or
// neither happened within the settling bound. A row lock, and an insert
// meeting a unique entry another transaction has not committed, are waited
// for on the transaction holding them, or on the tuple; an advisory lock is
// another wait event and is not counted.
func (c *crmCase) awaitLockWaits(f *inFlight, n int) bool {
	c.t.Helper()
	waiting := false
	eventually(settleLimit, func() bool {
		if f.answered() {
			return true
		}
		var sessions int
		err := c.sql().QueryRowContext(c.t.Context(), "SELECT count(*) FROM pg_stat_activity "+
			"WHERE datname = current_database() AND wait_event_type = 'Lock' "+
			"AND wait_event IN ('transactionid', 'tuple')").Scan(&sessions)
		if err != nil {
			c.t.Fatalf("reading the sessions waiting for a lock: %v", err)
		}
		waiting = sessions >= n
		return waiting
	})
	return waiting
}

// contactBurst is how many contact writes a concurrency case lets go at once.
const contactBurst = 24

// burstOf sends requests at once from goroutines of their own, as a client
// fleet does, and returns each one's answer in the order given. A request that
// could not be made fails the case once every one has returned.
func burstOf(t *testing.T, h *crmHost, reqs []request) []response {
	t.Helper()
	answers := make([]outcome, len(reqs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, req := range reqs {
		wg.Go(func() {
			<-start
			answers[i].resp, answers[i].err = h.do(req)
		})
	}
	close(start)
	wg.Wait()
	out := make([]response, len(reqs))
	for i, a := range answers {
		if a.err != nil {
			t.Fatalf("%s %s could not be sent: %v", reqs[i].method, reqs[i].path, a.err)
		}
		out[i] = a.resp
	}
	return out
}

// statusCounts counts a burst's answers by status.
func statusCounts(answers []response) map[int]int {
	counts := map[int]int{}
	for _, a := range answers {
		counts[a.status]++
	}
	return counts
}

// TestConcurrentContactWritesAtOneAccountAllCommit is the case for the contacts
// module's transactions under concurrent writers, on every stack. Each creation
// reads its account and then inserts, in one transaction: a burst of them in one
// tenant at one live account all commit, and so does a burst of finds-or-creates
// at another, each checked the same way and each finding the contact or
// writing it. Then an
// account's delete races a burst of creations at it: each creation is written
// before the delete and taken along by it, or checked after it and refused, and
// none fails.
//
// The writes are the ordinary shape of a transaction that checks and then
// writes. On SQLite such a transaction waits out a concurrent writer only when
// it takes the database's write lock as it begins, which the locator asks for
// (see sqliteLocator); a transaction begun without it is refused the lock
// outright at its first write while another writer holds it.
func TestConcurrentContactWritesAtOneAccountAllCommit(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.sealWithCaseKey()
		c.migrate()
		c.setGlobal("ratelimit.limit", liftedRateLimit)
		h := c.serve()
		cr := setUpCrew(t, c, h)
		rita, ivan := cr.as(h, cr.rita, cr.acme), cr.as(h, cr.ivan, cr.acme)
		for _, id := range []string{"acc-1", "acc-2", "acc-3"} {
			rita.must(t, nethttp.MethodPost, "/accounts", map[string]any{"id": id, "name": "Busy " + id},
				nethttp.StatusCreated)
		}
		asRita := func(method, path string, body any) request {
			return request{
				method: method, endpoint: apiEndpoint, path: path, tenant: cr.acme.Alias,
				cred: bearer(rita.token), body: body,
			}
		}
		live := func(t *testing.T, account string) int {
			return c.in(t).count("SELECT count(*) FROM contacts WHERE tenant_id = ? AND account_id = ? AND deleted_at IS NULL",
				cr.acme.ID, account)
		}

		t.Run("a burst of creations at one account all commit", func(t *testing.T) {
			reqs := make([]request, contactBurst)
			for i := range reqs {
				reqs[i] = asRita(nethttp.MethodPost, "/contacts", map[string]any{
					"id": fmt.Sprintf("con-b%d", i), "account_id": "acc-1", "name": fmt.Sprintf("Burst %d", i),
					"email": fmt.Sprintf("burst-%d@busy.test", i),
				})
			}
			answers := burstOf(t, h, reqs)
			if got := statusCounts(answers); got[nethttp.StatusCreated] != contactBurst {
				t.Errorf("%d concurrent creations at acc-1 were answered %v, want every one 201", contactBurst, got)
			}
			if n := live(t, "acc-1"); n != contactBurst {
				t.Errorf("acc-1 holds %d live contacts after %d concurrent creations, want every one", n, contactBurst)
			}
		})

		t.Run("a burst of finds-or-creates at one account all commit", func(t *testing.T) {
			// Half of them name an address a contact of the first burst holds
			// and find it; the other half name a new one and create it.
			reqs := make([]request, contactBurst)
			for i := range reqs {
				email := fmt.Sprintf("burst-%d@busy.test", i)
				if i%2 == 1 {
					email = fmt.Sprintf("upsert-%d@busy.test", i)
				}
				reqs[i] = asRita(nethttp.MethodPost, "/contacts/upsert", map[string]any{
					"account_id": "acc-2", "name": fmt.Sprintf("Upsert %d", i), "email": email,
				})
			}
			answers := burstOf(t, h, reqs)
			want := map[int]int{nethttp.StatusOK: contactBurst / 2, nethttp.StatusCreated: contactBurst / 2}
			if got := statusCounts(answers); !maps.Equal(got, want) {
				t.Errorf("%d concurrent finds-or-creates at acc-2 were answered %v, want %v", contactBurst, got, want)
			}
			if n := live(t, "acc-2"); n != contactBurst/2 {
				t.Errorf("acc-2 holds %d live contacts after %d concurrent creations there, want every one",
					n, contactBurst/2)
			}
		})

		t.Run("a delete racing creations at its account takes each along or refuses it", func(t *testing.T) {
			reqs := make([]request, contactBurst+1)
			for i := range contactBurst {
				reqs[i] = asRita(nethttp.MethodPost, "/contacts", map[string]any{
					"id": fmt.Sprintf("con-r%d", i), "account_id": "acc-3", "name": fmt.Sprintf("Racer %d", i),
					"email": fmt.Sprintf("racer-%d@busy.test", i),
				})
			}
			reqs[contactBurst] = request{
				method: nethttp.MethodDelete, endpoint: apiEndpoint, path: "/accounts/acc-3",
				tenant: cr.acme.Alias, cred: bearer(ivan.token),
			}
			answers := burstOf(t, h, reqs)
			if got := answers[contactBurst]; got.status != nethttp.StatusNoContent {
				t.Errorf("the delete of acc-3 racing its contacts' creations answered %d %s, want 204", got.status, got.body)
			}
			creations := statusCounts(answers[:contactBurst])
			if creations[nethttp.StatusCreated]+creations[nethttp.StatusUnprocessableEntity] != contactBurst {
				t.Errorf("the creations racing the delete of acc-3 were answered %v, want each 201 or 422", creations)
			}
			if n := live(t, "acc-3"); n != 0 {
				t.Errorf("acc-3 is deleted and holds %d live contacts, want none", n)
			}
		})
	})
}
