package main

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"slices"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// A deal's stakeholders are the contacts of its tenant it names: a
// many-to-many association between two scoped models, written through GORM's
// association API and read by a preload (withDealRelations). They are written
// apart from the deal's own row, so a change to them moves no version, asks for
// no If-Match and publishes no deal event.
//
// The join table has no model of the host's. GORM builds one from the
// association's tag, and the tag writes the tenant half of both keys into one
// column, which the tenant filter takes for the join table's tenant column
// whoever built the model: it conditions, fills and checks every statement on
// the join table by that column, as it does those on the deals and the
// contacts.

// maxStakeholders bounds the set one request names.
const maxStakeholders = 50

// The failures the stakeholder routes answer with.
var (
	errNoStakeholderList   = errors.New("the request names no contact_ids list")
	errTooManyStakeholders = fmt.Errorf("a deal names at most %d stakeholders", maxStakeholders)
	errNoStakeholder       = errors.New("a contact identifier is empty")
	errUnknownStakeholder  = errors.New("the contact is not one of this tenant's live contacts")
)

// stakeholderSet is the body of a replacement of a deal's stakeholders: the
// whole set, which an empty list clears.
type stakeholderSet struct {
	ContactIDs *[]string `json:"contact_ids"`
}

// Validate refuses a set that is missing, too long or names an empty
// identifier.
func (s stakeholderSet) Validate() error {
	switch {
	case s.ContactIDs == nil:
		return errNoStakeholderList
	case len(*s.ContactIDs) > maxStakeholders:
		return errTooManyStakeholders
	case slices.ContainsFunc(*s.ContactIDs, func(id string) bool { return strings.TrimSpace(id) == "" }):
		return errNoStakeholder
	}
	return nil
}

// stakeholderAddition is the body of an addition of one stakeholder.
type stakeholderAddition struct {
	ContactID string `json:"contact_id"`
}

// Validate refuses an addition that names no contact.
func (a stakeholderAddition) Validate() error {
	if strings.TrimSpace(a.ContactID) == "" {
		return errNoStakeholder
	}
	return nil
}

// unknownStakeholder is a refusal naming the contact that is not a live contact
// of the tenant.
type unknownStakeholder struct{ id string }

func (u unknownStakeholder) Error() string { return errUnknownStakeholder.Error() + ": " + u.id }

func (unknownStakeholder) Unwrap() error { return errUnknownStakeholder }

// shareLock holds the rows a read inside a transaction returns until the
// transaction ends, against a delete that marks one of them deleted, as
// accountHere holds an account. SQLite's dialect writes no locking clause, and
// there the transaction alone keeps the two apart.
var shareLock = clause.Locking{Strength: clause.LockingStrengthShare}

// liveDealRow reads one live deal of the request's tenant, its own columns alone.
func liveDealRow(q *gorm.DB, id string) (Deal, bool, error) {
	var found []Deal
	if err := q.Where("id = ?", id).Limit(1).Find(&found).Error; err != nil || len(found) == 0 {
		return Deal{}, false, err
	}
	return found[0], true, nil
}

// liveContacts reads the request's tenant's live contacts under the identifiers
// given, and refuses the first identifier the tenant holds no live contact
// under. Each is read whole: the association writes what it is given.
func liveContacts(q *gorm.DB, ids []string) ([]Contact, error) {
	var found []Contact
	if len(ids) == 0 {
		return found, nil
	}
	if err := q.Where("id IN ?", ids).Find(&found).Error; err != nil {
		return nil, fmt.Errorf("reading the stakeholders: %w", err)
	}
	for _, id := range ids {
		if !slices.ContainsFunc(found, func(c Contact) bool { return c.ID == id }) {
			return nil, unknownStakeholder{id}
		}
	}
	return found, nil
}

// distinctIDs is a list of identifiers, trimmed, each once, in the order given.
func distinctIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// setStakeholders replaces the set of a deal's stakeholders with the contacts
// the request names.
//
// One transaction reads the deal and the contacts, each under a share lock,
// and replaces the association on the deal it read. The deal and every contact
// have to be live and the request's tenant's: the reads name no tenant, and the
// filter confines them, so another tenant's contact under a named identifier is
// no contact here. The replacement is GORM's: it links the contacts not yet
// linked and removes the links to the rest, on the join table alone.
func (p *dealsProduct) setStakeholders(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx := r.Context()
	self, ok := memberOf(w, r, setStakeholdersRoute)
	if !ok {
		return
	}
	if !canWriteDeals(self.Role) {
		writeError(w, nethttp.StatusForbidden, errReadOnlyRole.Error())
		return
	}
	var body stakeholderSet
	if err := speedhttp.Decode(r, &body); err != nil {
		writeError(w, speedhttp.StatusFor(err), err.Error())
		return
	}
	id := r.PathValue("id")
	ids := distinctIDs(*body.ContactIDs)
	err := p.db.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		deal, found, err := liveDealRow(tx.Clauses(shareLock), id)
		switch {
		case err != nil:
			return fmt.Errorf("reading the deal: %w", err)
		case !found:
			return errDealNotFound
		}
		contacts, err := liveContacts(tx.Clauses(shareLock), ids)
		if err != nil {
			return err
		}
		return tx.Model(&deal).Association("Stakeholders").Replace(contacts)
	})
	p.answerStakeholders(w, r, id, err, "replacing a deal's stakeholders")
}

// addStakeholder links one contact to a deal, which is a no-op when the two are
// linked already. It reads the deal and the contact as setStakeholders does,
// and appends the contact it read to the association on the deal it read.
func (p *dealsProduct) addStakeholder(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx := r.Context()
	self, ok := memberOf(w, r, addStakeholderRoute)
	if !ok {
		return
	}
	if !canWriteDeals(self.Role) {
		writeError(w, nethttp.StatusForbidden, errReadOnlyRole.Error())
		return
	}
	var body stakeholderAddition
	if err := speedhttp.Decode(r, &body); err != nil {
		writeError(w, speedhttp.StatusFor(err), err.Error())
		return
	}
	id := r.PathValue("id")
	err := p.db.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		deal, found, err := liveDealRow(tx.Clauses(shareLock), id)
		switch {
		case err != nil:
			return fmt.Errorf("reading the deal: %w", err)
		case !found:
			return errDealNotFound
		}
		contacts, err := liveContacts(tx.Clauses(shareLock), []string{strings.TrimSpace(body.ContactID)})
		if err != nil {
			return err
		}
		return tx.Model(&deal).Association("Stakeholders").Append(&contacts[0])
	})
	p.answerStakeholders(w, r, id, err, "adding a deal's stakeholder")
}

// answerStakeholders answers a write of a deal's stakeholders with the deal as
// it now reads.
func (p *dealsProduct) answerStakeholders(w nethttp.ResponseWriter, r *nethttp.Request, id string, err error, doing string) {
	ctx := r.Context()
	switch {
	case errors.Is(err, errDealNotFound):
		writeError(w, nethttp.StatusNotFound, err.Error())
		return
	case errors.Is(err, errUnknownStakeholder):
		writeError(w, nethttp.StatusUnprocessableEntity, err.Error())
		return
	case err != nil:
		log.FromContext(ctx).Error(doing, "error", err)
		writeError(w, nethttp.StatusInternalServerError, "the stakeholders could not be written")
		return
	}
	deal, found, err := dealByID(ctx, p.db, id)
	if err != nil || !found {
		log.FromContext(ctx).Error("reading a deal back", "error", err, "found", found)
		writeError(w, nethttp.StatusInternalServerError, errDealUnresolvable.Error())
		return
	}
	w.Header().Set("ETag", etagOf(deal.Version))
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, viewOfDeal(deal))
}

// removeStakeholder unlinks one contact from a deal, and answers 204 whether or
// not the two were linked.
//
// The deal is read first, so a deal the tenant does not hold is answered 404.
// The contact is addressed by the identifier half of its key, as deleteDeal
// addresses a deal, and the tenant half is the filter's to fill from the
// request: the association is given the contact the path names, and the
// deletion is GORM's, on the join table alone.
func (p *dealsProduct) removeStakeholder(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx := r.Context()
	self, ok := memberOf(w, r, removeStakeholderRoute)
	if !ok {
		return
	}
	if !canWriteDeals(self.Role) {
		writeError(w, nethttp.StatusForbidden, errReadOnlyRole.Error())
		return
	}
	database := p.db.DB().WithContext(ctx)
	deal, found, err := liveDealRow(database, r.PathValue("id"))
	switch {
	case err != nil:
		log.FromContext(ctx).Error("reading a deal", "error", err)
		writeError(w, nethttp.StatusServiceUnavailable, "the deal table cannot be read")
		return
	case !found:
		writeError(w, nethttp.StatusNotFound, errDealNotFound.Error())
		return
	}
	if err := database.Model(&deal).Association("Stakeholders").Delete(&Contact{ID: r.PathValue("contact")}); err != nil {
		log.FromContext(ctx).Error("removing a deal's stakeholder", "error", err)
		writeError(w, nethttp.StatusInternalServerError, "the stakeholder could not be removed")
		return
	}
	w.WriteHeader(nethttp.StatusNoContent)
}
