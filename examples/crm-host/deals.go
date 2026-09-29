package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/outbox"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// dealsModuleName is the name the deals module is registered under, and the
// namespace of its settings.
const dealsModuleName = "deals"

// dealTable is the table the deal model maps to.
const dealTable = "deals"

// The deal routes, on the API endpoint and so inside the tenant the request
// names. Any member reads; an owner, an admin or a rep writes.
const (
	createDealRoute = "POST /deals"
	listDealsRoute  = "GET /deals"
	getDealRoute    = "GET /deals/{id}"
	patchDealRoute  = "PATCH /deals/{id}"
	moveDealRoute   = "POST /deals/{id}/stage"
	deleteDealRoute = "DELETE /deals/{id}"

	setStakeholdersRoute   = "PUT /deals/{id}/stakeholders"
	addStakeholderRoute    = "POST /deals/{id}/stakeholders"
	removeStakeholderRoute = "DELETE /deals/{id}/stakeholders/{contact}"
)

// The settings this module declares, under their full paths: the namespace, a
// dot, then the item key.
const (
	defaultCurrencySetting = dealsModuleName + ".default-currency"
	idempotencyTTLSetting  = dealsModuleName + ".idempotency-ttl"
)

// The declared defaults of those settings.
const (
	defaultDealCurrency    = "USD"
	defaultIdempotencyTTL  = 24 * time.Hour
	idempotencyKeyHeader   = "Idempotency-Key"
	idempotentReplayHeader = "Idempotent-Replayed"
)

// maxIdempotencyKey bounds the header's length. The key is stored as a digest,
// so the bound is about what a client may send rather than what the store
// holds.
const maxIdempotencyKey = 255

// The page a listing answers with when the request names none, and the largest
// one it answers with at all.
const (
	defaultDealPage = 50
	maxDealPage     = 200
)

// currencyPattern is an ISO 4217 code's shape.
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// dealIDPattern is the shape of a deal identifier a client may choose.
var dealIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// dateLayout is how a calendar date travels in a request and a response.
const dateLayout = time.DateOnly

var (
	errNoTitle          = errors.New("the deal title is empty")
	errNoAccount        = errors.New("the deal names no account")
	errNegativeAmount   = errors.New("the deal amount is negative")
	errBadCurrency      = errors.New("the currency is not a three-letter ISO 4217 code")
	errNoStage          = errors.New("the deal names no stage")
	errBadDate          = errors.New("the expected close is not a date written YYYY-MM-DD")
	errBadDealID        = errors.New("the deal identifier is not letters, digits, dashes and underscores, at most 64 long")
	errEmptyPatch       = errors.New("the change names nothing to change")
	errUnknownStage     = errors.New("the stage is not one of this tenant's stages")
	errDealExists       = errors.New("a deal with this identifier exists")
	errDealIDKept       = errors.New("a deleted deal keeps this identifier")
	errDealNotFound     = errors.New("no deal has this identifier")
	errVersionMismatch  = errors.New("the deal is not at the version the request names")
	errAlreadyInStage   = errors.New("the deal is already in this stage")
	errNoPrecondition   = errors.New("the request names no version: send If-Match with the deal's ETag")
	errBadIfMatch       = errors.New("If-Match is not one strong ETag this API issued")
	errKeyReused        = errors.New("the idempotency key was used with another request body")
	errKeyInFlight      = errors.New("a request with this idempotency key is still being answered")
	errBadIdempotency   = errors.New("the idempotency key is empty or longer than 255 characters")
	errReadOnlyRole     = errors.New("a viewer reads this tenant's deals and does not write them")
	errIdempotencyTTL   = errors.New("the idempotency lifetime is not positive")
	errDefaultCurrency  = errors.New("the default currency is misconfigured")
	errDealUnresolvable = errors.New("the deal could not be read back after it was written")
)

//go:embed openapi/deals.json
var dealsOpenAPI []byte

//go:embed migrations/deals
var dealsMigrationRoot embed.FS

var dealsMigrations = subtree(dealsMigrationRoot, "migrations/deals")

// Deal is a scoped model: the same identifier names a deal in each of two
// tenants, and the statements this module issues with a request's context name
// no tenant.
type Deal struct {
	tenant.Model
	ID string `gorm:"column:id;primaryKey"`
	// AccountID names the account the deal is with: a live account of the
	// deal's tenant when the deal is written, which the write checks and the
	// table's reference over the tenant and the account enforces.
	AccountID     string         `gorm:"column:account_id;not null"`
	Title         string         `gorm:"column:title;not null"`
	Amount        int64          `gorm:"column:amount;not null"`
	Currency      string         `gorm:"column:currency;not null"`
	StageKey      string         `gorm:"column:stage_key;not null"`
	OwnerID       string         `gorm:"column:owner_id;not null"`
	Version       int64          `gorm:"column:version;not null"`
	ExpectedClose *time.Time     `gorm:"column:expected_close"`
	ClosedAt      *time.Time     `gorm:"column:closed_at"`
	CreatedAt     time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time      `gorm:"column:updated_at;not null"`
	DeletedAt     gorm.DeletedAt `gorm:"column:deleted_at"`

	// Stage, Account and Owner are the rows the deal points at, read by a join
	// or a preload. The stage and the account are scoped as the deal is; the
	// owner is a CRM user, whose table is global.
	Stage   PipelineStage `gorm:"foreignKey:StageKey;references:Key"`
	Account Account       `gorm:"foreignKey:AccountID;references:ID"`
	Owner   User          `gorm:"foreignKey:OwnerID;references:ID"`

	// Stakeholders are the contacts of the deal's tenant the deal names, read
	// by a preload and written through the association (stakeholders.go).
	// The tag writes the tenant half of the deal's key and of the contact's
	// into one column of the join table, which is what the tenant filter
	// takes for the join table's tenant column: it scopes every statement on
	// the join table by it, whose model GORM builds from this tag.
	Stakeholders []Contact `gorm:"many2many:deal_stakeholders;joinForeignKey:TenantID,DealID;joinReferences:TenantID,ContactID"`
}

// TableName fixes the table this model maps to.
func (Deal) TableName() string { return dealTable }

// canWriteDeals reports whether a role writes deals and their activities.
func canWriteDeals(role string) bool { return roleRank(role) >= roleRank(roleRep) }

// dealSettings is the carrier of this module's setting items. A field's value is
// the declared default.
type dealSettings struct {
	DefaultCurrency string        `config:"default-currency"`
	IdempotencyTTL  time.Duration `config:"idempotency-ttl"`
}

// dealsSettingsSchema declares the two settings on the settings surface, where
// each tenant can hold its own value and the global row is the fallback.
func dealsSettingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: dealsModuleName,
		Mounts: []settings.Mount{{Value: &dealSettings{
			DefaultCurrency: defaultDealCurrency,
			IdempotencyTTL:  defaultIdempotencyTTL,
		}}},
		Items: map[string]settings.Item{
			"default-currency": {Description: "the currency a deal created without one is written in"},
			"idempotency-ttl":  {Description: "how long an Idempotency-Key of a deal creation is remembered"},
		},
	}
}

// dealsTable is the capability the deals module stands for, so that a module
// whose tables reference the deals table can declare a requirement on it. It is
// a token only, claimed exclusively.
type dealsTable interface{ dealsTable() }

// dealsProduct is the deals module's product.
type dealsProduct struct {
	released
	db       db.Database
	store    kv.KV
	settings settings.Store
	// outbox stages each deal event in the transaction of the write it
	// announces; its relay publishes the event once that transaction has
	// committed.
	outbox outbox.Outbox
}

// dealsTable marks the product as the deals module's.
func (*dealsProduct) dealsTable() {}

// dealView is one deal as the API shows it. The amount is an integer in the
// currency's minor units, and it travels as a JSON integer whatever its size.
type dealView struct {
	ID            string     `json:"id"`
	AccountID     string     `json:"account_id"`
	AccountName   string     `json:"account_name"`
	Title         string     `json:"title"`
	Amount        int64      `json:"amount"`
	Currency      string     `json:"currency"`
	Stage         string     `json:"stage"`
	StageName     string     `json:"stage_name"`
	StageKind     string     `json:"stage_kind"`
	OwnerID       string     `json:"owner_id"`
	OwnerName     string     `json:"owner_name"`
	Version       int64      `json:"version"`
	ExpectedClose *string    `json:"expected_close"`
	ClosedAt      *time.Time `json:"closed_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	// Stakeholders are the live contacts the deal names, by name.
	Stakeholders []stakeholderView `json:"stakeholders"`
}

// stakeholderView is one of a deal's stakeholders as the API shows it.
type stakeholderView struct {
	ContactID string `json:"contact_id"`
	Name      string `json:"name"`
}

// viewOfDeal renders a deal together with whatever of its stage, its account,
// its owner and its stakeholders the statement that read it loaded. A deleted
// account or contact is not loaded, so the account's name reads empty and the
// contact is not among the stakeholders.
func viewOfDeal(d Deal) dealView {
	v := dealView{
		ID:          d.ID,
		AccountID:   d.AccountID,
		AccountName: d.Account.Name,
		Title:       d.Title,
		Amount:      d.Amount,
		Currency:    d.Currency,
		Stage:       d.StageKey,
		StageName:   d.Stage.Name,
		StageKind:   d.Stage.Kind,
		OwnerID:     d.OwnerID,
		OwnerName:   d.Owner.DisplayName,
		Version:     d.Version,
		ClosedAt:    d.ClosedAt,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
		// Stakeholders are always a list, so a deal naming none reads [] rather
		// than null.
		Stakeholders: make([]stakeholderView, 0, len(d.Stakeholders)),
	}
	for _, c := range d.Stakeholders {
		v.Stakeholders = append(v.Stakeholders, stakeholderView{ContactID: c.ID, Name: c.Name})
	}
	if d.ExpectedClose != nil {
		text := d.ExpectedClose.UTC().Format(dateLayout)
		v.ExpectedClose = &text
	}
	return v
}

// etagOf is the entity tag a deal's version is sent as.
func etagOf(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }

// ifMatchVersion reads the version a write is conditioned on out of If-Match.
// A request without one is answered 428, since a write that names no version
// is one a concurrent writer can silently undo; one naming anything other than
// a single strong tag this API issued matches no version and is answered 412.
func ifMatchVersion(w nethttp.ResponseWriter, r *nethttp.Request) (int64, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		writeError(w, nethttp.StatusPreconditionRequired, errNoPrecondition.Error())
		return 0, false
	}
	unquoted, quoted := strings.CutPrefix(raw, `"`)
	unquoted, closed := strings.CutSuffix(unquoted, `"`)
	version, err := strconv.ParseInt(unquoted, 10, 64)
	if !quoted || !closed || err != nil || version < 1 {
		writeError(w, nethttp.StatusPreconditionFailed, errBadIfMatch.Error())
		return 0, false
	}
	return version, true
}

// parseDate reads a calendar date, or nothing when the text is empty.
func parseDate(text string) (*time.Time, error) {
	if text == "" {
		return nil, nil
	}
	day, err := time.Parse(dateLayout, text)
	if err != nil {
		return nil, errBadDate
	}
	return &day, nil
}

// dealRequest is the body of a deal creation.
type dealRequest struct {
	// ID is the identifier the client chooses, or empty for one generated
	// here.
	ID            string `json:"id"`
	AccountID     string `json:"account_id"`
	Title         string `json:"title"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	Stage         string `json:"stage"`
	OwnerID       string `json:"owner_id"`
	ExpectedClose string `json:"expected_close"`
}

// Validate refuses a creation whose values this host does not accept. Whether
// the stage is one of the tenant's and the owner one of its members is read
// from the tenant's tables afterwards.
func (d dealRequest) Validate() error {
	switch {
	case d.ID != "" && !dealIDPattern.MatchString(d.ID):
		return errBadDealID
	case strings.TrimSpace(d.Title) == "":
		return errNoTitle
	case strings.TrimSpace(d.AccountID) == "":
		return errNoAccount
	case d.Amount < 0:
		return errNegativeAmount
	case d.Currency != "" && !currencyPattern.MatchString(d.Currency):
		return errBadCurrency
	case strings.TrimSpace(d.Stage) == "":
		return errNoStage
	}
	if _, err := parseDate(d.ExpectedClose); err != nil {
		return err
	}
	return nil
}

// withDealRelations has a read of deals load what a deal's view shows beside
// its own columns: its stage, its account, its owner and its stakeholders.
// The stakeholders are read through the join table, whose model is scoped as
// the deal and the contacts are, in name order.
func withDealRelations(q *gorm.DB) *gorm.DB {
	return q.
		Preload("Stage").
		Preload("Account").
		Preload("Owner", func(q *gorm.DB) *gorm.DB { return q.Select("id", "display_name") }).
		Preload("Stakeholders", func(q *gorm.DB) *gorm.DB { return q.Order(contactTable + ".name, " + contactTable + ".id") })
}

// dealByID reads one of the request's tenant's deals with its stage, its
// account, its owner and its stakeholders. It reads with Find: an identifier
// the tenant has no deal under is an ordinary answer here.
func dealByID(ctx context.Context, database db.Database, id string) (Deal, bool, error) {
	var found []Deal
	err := withDealRelations(database.DB().WithContext(ctx)).
		Where("id = ?", id).Limit(1).Find(&found).Error
	if err != nil || len(found) == 0 {
		return Deal{}, false, err
	}
	return found[0], true, nil
}

// holderOf reads the deal holding an identifier in the request's tenant,
// deleted or not: a deleted deal keeps its row, and with it the identifier
// half of its primary key. Unscoped lifts the soft-delete condition and
// nothing else; the filter still scopes the read to the tenant.
func holderOf(ctx context.Context, database db.Database, id string) (Deal, bool, error) {
	var found []Deal
	err := database.DB().WithContext(ctx).Unscoped().Where("id = ?", id).Limit(1).Find(&found).Error
	if err != nil || len(found) == 0 {
		return Deal{}, false, err
	}
	return found[0], true, nil
}

// answer is a response computed before it is written, so that the creation path
// can store it for a replay and write the replay the same way.
type answer struct {
	status int
	etag   string
	body   []byte
	// replayed says the answer is one stored for an earlier request with the
	// same idempotency key.
	replayed bool
}

// refusal is a refusal or a failure as an answer.
func refusal(status int, message string) answer {
	body, err := json.Marshal(apiError{Error: message})
	if err != nil {
		// A struct of one string field always encodes; this is here so that
		// the impossible case still answers.
		return answer{status: nethttp.StatusInternalServerError, body: []byte(`{"error":"the answer could not be encoded"}`)}
	}
	return answer{status: status, body: body}
}

// write sends an answer.
func (a answer) write(w nethttp.ResponseWriter) {
	if a.etag != "" {
		w.Header().Set("ETag", a.etag)
	}
	if a.replayed {
		w.Header().Set(idempotentReplayHeader, "true")
	}
	_ = speedhttp.WriteJSON(w, a.status, json.RawMessage(a.body))
}

// idempotencyRecord is what a key's claim holds: the digest of the request
// that claimed it and, once that request is answered, the answer.
type idempotencyRecord struct {
	Fingerprint string          `json:"fingerprint"`
	Done        bool            `json:"done"`
	Status      int             `json:"status,omitempty"`
	ETag        string          `json:"etag,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"`
}

// fingerprintOf digests a creation's request as it was decoded, so two bodies
// that differ only in spacing or member order are the same request.
func fingerprintOf(body dealRequest) (string, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// idempotencyStoreKey is where a key's claim is stored: under the tenant and the
// caller, so a key one client chose never meets another tenant's or another
// user's, and the digest of the key the client sent.
func idempotencyStoreKey(self member, key string) string {
	return storeKey("idempotency", dealsModuleName, string(self.TenantID), self.UserID, keyDigest(key))
}

// createDeal creates a deal, honouring an Idempotency-Key when the request
// carries one.
//
// The first request with a key claims it: the claim is one atomic
// create-if-absent in the key-value store, with the lifetime the
// deals.idempotency-ttl setting gives, and it holds the digest of the request's
// body. That request then creates the deal and stores its answer in the claim.
// A request finding the claim answers from it: the stored answer when the
// first request has been answered and the bodies agree, 409 while the first is
// still being answered, and 422 when the bodies differ. A request that claimed
// the key and was then refused gives the claim up, so the client can send a
// corrected request under the same key.
func (p *dealsProduct) createDeal(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx := r.Context()
	self, ok := memberOf(w, r, createDealRoute)
	if !ok {
		return
	}
	if !canWriteDeals(self.Role) {
		writeError(w, nethttp.StatusForbidden, errReadOnlyRole.Error())
		return
	}
	var body dealRequest
	if err := speedhttp.Decode(r, &body); err != nil {
		writeError(w, speedhttp.StatusFor(err), err.Error())
		return
	}
	key, keyed := r.Header[nethttp.CanonicalHeaderKey(idempotencyKeyHeader)]
	if !keyed {
		p.insertDeal(ctx, self, body).write(w)
		return
	}
	if len(key) != 1 || strings.TrimSpace(key[0]) == "" || len(key[0]) > maxIdempotencyKey {
		writeError(w, nethttp.StatusBadRequest, errBadIdempotency.Error())
		return
	}
	p.createOnce(ctx, self, key[0], body).write(w)
}

// createOnce is the keyed half of createDeal.
func (p *dealsProduct) createOnce(ctx context.Context, self member, key string, body dealRequest) answer {
	logger := log.FromContext(ctx)
	ttl, err := settings.Get[time.Duration](p.settings, settings.ForTenant(string(self.TenantID)), idempotencyTTLSetting)
	if err != nil {
		logger.Error("reading the idempotency lifetime", "error", err)
		return refusal(nethttp.StatusInternalServerError, "the idempotency lifetime cannot be read")
	}
	// The store keeps a value whose lifetime is not positive for ever, so a
	// claim written with one would never lapse. Validating a setting is the
	// consumer's part.
	if ttl <= 0 {
		logger.Error("the idempotency lifetime is not positive, so no key is claimed",
			"setting", idempotencyTTLSetting, "value", ttl.String())
		return refusal(nethttp.StatusInternalServerError, errIdempotencyTTL.Error())
	}
	fingerprint, err := fingerprintOf(body)
	if err != nil {
		logger.Error("digesting a creation", "error", err)
		return refusal(nethttp.StatusInternalServerError, "the request could not be digested")
	}
	pending, err := json.Marshal(idempotencyRecord{Fingerprint: fingerprint})
	if err != nil {
		logger.Error("encoding an idempotency claim", "error", err)
		return refusal(nethttp.StatusInternalServerError, "the idempotency claim could not be encoded")
	}
	claimKey := idempotencyStoreKey(self, key)
	claimed, err := p.store.SetIfAbsent(ctx, claimKey, pending, ttl)
	if err != nil {
		logger.Error("claiming an idempotency key", "error", err)
		return refusal(nethttp.StatusServiceUnavailable, "the idempotency store cannot be written")
	}
	if !claimed {
		return p.answerFromClaim(ctx, claimKey, fingerprint)
	}

	resp := p.insertDeal(ctx, self, body)

	// What follows is bookkeeping for a claim this request holds, and it runs
	// whatever becomes of the request's context: a client that gives up once
	// the deal is written still leaves the answer for its retry to find.
	settle := context.WithoutCancel(ctx)
	if resp.status != nethttp.StatusCreated {
		if err = p.store.Delete(settle, claimKey); err != nil {
			logger.Error("giving up an idempotency claim", "error", err)
		}
		return resp
	}
	done, err := json.Marshal(idempotencyRecord{
		Fingerprint: fingerprint, Done: true, Status: resp.status, ETag: resp.etag, Body: resp.body,
	})
	if err != nil {
		logger.Error("encoding an idempotency answer", "error", err)
		return resp
	}
	// The swap is from the pending record this request wrote, so an answer
	// never lands on a finished claim. The record names the body and nothing
	// else: a request with the same body that claimed the key after this
	// one's claim lapsed wrote the same record, and whichever of the two swaps
	// first stores its answer on the claim that stands. Both created a deal
	// under the key, so a lifetime shorter than a creation takes is what the
	// setting's value has to rule out.
	stored, err := p.store.CompareAndSwap(settle, claimKey, pending, done)
	switch {
	case err != nil:
		logger.Error("storing an idempotency answer", "error", err)
	case !stored:
		logger.Warn("the idempotency claim lapsed before its answer was stored", "setting", idempotencyTTLSetting)
	}
	return resp
}

// answerFromClaim answers a request whose key another request claimed.
func (p *dealsProduct) answerFromClaim(ctx context.Context, claimKey, fingerprint string) answer {
	stored, present, err := p.store.Get(ctx, claimKey)
	if err != nil {
		log.FromContext(ctx).Error("reading an idempotency claim", "error", err)
		return refusal(nethttp.StatusServiceUnavailable, "the idempotency store cannot be read")
	}
	if !present {
		// The claim lapsed between the two calls: whatever claimed it is
		// either still running or gone, and neither is an answer to replay.
		return refusal(nethttp.StatusConflict, errKeyInFlight.Error())
	}
	var record idempotencyRecord
	if err := json.Unmarshal(stored, &record); err != nil {
		log.FromContext(ctx).Error("decoding an idempotency claim", "error", err)
		return refusal(nethttp.StatusInternalServerError, "the idempotency claim cannot be read")
	}
	switch {
	case record.Fingerprint != fingerprint:
		return refusal(nethttp.StatusUnprocessableEntity, errKeyReused.Error())
	case !record.Done:
		return refusal(nethttp.StatusConflict, errKeyInFlight.Error())
	}
	return answer{status: record.Status, etag: record.ETag, body: record.Body, replayed: true}
}

// insertDeal writes a new deal, stages its deal.created in the same
// transaction and computes the answer.
//
// The row it creates names no tenant: the filter fills the tenant half of the
// key from the request's context. The stage has to be one of the tenant's own,
// which the look-up below answers and the composite reference to the stages
// table enforces; the owner defaults to the caller and has to be a member of
// the tenant.
//
// The account has to be a live account of the tenant. A look-up before the
// insert answers an account the tenant does not hold, or holds deleted, and
// the check is made again inside the insert's transaction, before the insert,
// as the contacts module makes it: a delete of the account that committed in
// between is seen there and the insert is not made, and on PostgreSQL the
// check's share lock orders the insert against a delete still running, which
// locks the account's row before it marks it deleted (see accountHere). The
// composite reference to the accounts table refuses an account the tenant
// does not hold at all; it accepts a deleted one, whose row is still there.
func (p *dealsProduct) insertDeal(ctx context.Context, self member, body dealRequest) answer {
	logger := log.FromContext(ctx)
	stage, found, err := stageByKey(ctx, p.db, body.Stage)
	if err != nil {
		logger.Error("reading a stage", "error", err)
		return refusal(nethttp.StatusServiceUnavailable, "the pipeline cannot be read")
	}
	if !found {
		return refusal(nethttp.StatusUnprocessableEntity, errUnknownStage.Error())
	}
	account := strings.TrimSpace(body.AccountID)
	switch err = requireAccount(p.db.DB().WithContext(ctx), account); {
	case errors.Is(err, errUnknownAccount):
		return refusal(nethttp.StatusUnprocessableEntity, errUnknownAccount.Error())
	case err != nil:
		logger.Error("reading the deal's account", "error", err)
		return refusal(nethttp.StatusServiceUnavailable, "the account table cannot be read")
	}
	owner := body.OwnerID
	if owner == "" {
		owner = self.UserID
	}
	belongs, err := isMember(ctx, p.db, owner)
	if err != nil {
		logger.Error("reading the owner's membership", "error", err)
		return refusal(nethttp.StatusServiceUnavailable, "the membership table cannot be read")
	}
	if !belongs {
		return refusal(nethttp.StatusUnprocessableEntity, errOwnerNotMember.Error())
	}
	currency := body.Currency
	if currency == "" {
		currency, err = settings.Get[string](p.settings, settings.ForTenant(string(self.TenantID)), defaultCurrencySetting)
		if err != nil {
			logger.Error("reading the default currency", "error", err)
			return refusal(nethttp.StatusInternalServerError, "the default currency cannot be read")
		}
		// The settings surface holds whatever text was written under the
		// declared type, so the code's shape is this module's to check, as a
		// body's currency is: a deal written in a currency the deal event
		// contract refuses would be announced and then dropped by every
		// consumer of its deal.won.
		if !currencyPattern.MatchString(currency) {
			logger.Error("the default currency is not a three-letter ISO 4217 code, so no deal is written",
				"setting", defaultCurrencySetting, "value", currency)
			return refusal(nethttp.StatusInternalServerError, errDefaultCurrency.Error())
		}
	}
	id := body.ID
	if id == "" {
		if id, err = newID("d_"); err != nil {
			logger.Error("drawing a deal identifier", "error", err)
			return refusal(nethttp.StatusInternalServerError, "no identifier could be drawn")
		}
	} else {
		// A look-up before the insert answers the ordinary duplicate sooner,
		// and tells a deleted holder from a live one; the primary key is what
		// refuses a concurrent one, answered as a conflict below.
		var holder Deal
		var held bool
		holder, held, err = holderOf(ctx, p.db, id)
		switch {
		case err != nil:
			logger.Error("reading a deal", "error", err)
			return refusal(nethttp.StatusServiceUnavailable, "the deal table cannot be read")
		case held && holder.DeletedAt.Valid:
			return refusal(nethttp.StatusConflict, errDealIDKept.Error())
		case held:
			return refusal(nethttp.StatusConflict, errDealExists.Error())
		}
	}
	expected, _ := parseDate(body.ExpectedClose)
	now := time.Now().UTC()
	deal := Deal{
		ID:            id,
		AccountID:     account,
		Title:         strings.TrimSpace(body.Title),
		Amount:        body.Amount,
		Currency:      currency,
		StageKey:      stage.Key,
		OwnerID:       owner,
		Version:       1,
		ExpectedClose: expected,
		ClosedAt:      closedAtFor(stage, now),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	err = p.db.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if checkErr := requireAccount(tx, deal.AccountID); checkErr != nil {
			return checkErr
		}
		if createErr := tx.Create(&deal).Error; createErr != nil {
			return createErr
		}
		return p.stageWrite(tx, self, dealCreatedEvent, dealCreated, id)
	})
	switch {
	case errors.Is(err, errUnknownAccount):
		return refusal(nethttp.StatusUnprocessableEntity, errUnknownAccount.Error())
	case errors.Is(err, gorm.ErrDuplicatedKey):
		// The database module marks the key's refusal on either engine.
		return refusal(nethttp.StatusConflict, errDealExists.Error())
	case err != nil:
		logger.Error("writing a deal", "error", err)
		return refusal(nethttp.StatusInternalServerError, "the deal could not be written")
	}
	created, found, err := dealByID(ctx, p.db, id)
	if err != nil || !found {
		logger.Error("reading a deal back", "error", err, "found", found)
		return refusal(nethttp.StatusInternalServerError, errDealUnresolvable.Error())
	}
	encoded, err := json.Marshal(viewOfDeal(created))
	if err != nil {
		logger.Error("encoding a deal", "error", err)
		return refusal(nethttp.StatusInternalServerError, "the deal could not be encoded")
	}
	return answer{status: nethttp.StatusCreated, etag: etagOf(created.Version), body: encoded}
}

// requireAccount answers errUnknownAccount unless the tenant the handle's
// context names holds a live account under the identifier. Inside a
// transaction it holds the account's row as accountHere does until the
// transaction ends.
func requireAccount(tx *gorm.DB, accountID string) error {
	found, err := accountHere(tx, accountID)
	switch {
	case err != nil:
		return fmt.Errorf("reading the deal's account: %w", err)
	case !found:
		return errUnknownAccount
	}
	return nil
}

// closedAtFor is when a deal entering a stage closed: now for a won or lost
// stage, and not at all for an open one.
func closedAtFor(stage PipelineStage, now time.Time) *time.Time {
	if !stage.closes() {
		return nil
	}
	return &now
}

// dealFilters are the query parameters a listing filters by, and the column
// each compares.
var dealFilters = []struct{ param, column string }{
	{"stage", "stage_key"},
	{"owner", "owner_id"},
	{"account", "account_id"},
}

// dealList is what the listing answers with: the number of deals the filters
// match, and one page of them.
type dealList struct {
	Total int64      `json:"total"`
	Deals []dealView `json:"deals"`
}

// listDeals answers the tenant's deals, filtered by stage, owner or account,
// newest first, each with its stage's, its account's and its owner's names
// joined in and its stakeholders preloaded.
//
// The count and the page are two statements on one chain. Neither carries a
// tenant condition: the filter adds one to the deals table, and one to each of
// the two scoped tables the page joins — the stages and the accounts — in that
// join's ON clause, since each join follows the deal's association to a scoped
// model. The owner's table is global and is joined as it is. The stakeholders
// are two statements more, on the join table and on the contacts, both scoped
// models, for every deal of the page at once.
func (p *dealsProduct) listDeals(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx := r.Context()
	if _, ok := memberOf(w, r, listDealsRoute); !ok {
		return
	}
	query := r.URL.Query()
	limit := defaultDealPage
	if text := query.Get("limit"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > maxDealPage {
			writeError(w, nethttp.StatusBadRequest, fmt.Sprintf("limit is not a number from 1 to %d", maxDealPage))
			return
		}
		limit = n
	}
	chain := p.db.DB().WithContext(ctx).Model(&Deal{})
	for _, filter := range dealFilters {
		if value := query.Get(filter.param); value != "" {
			chain = chain.Where(dealTable+"."+filter.column+" = ?", value)
		}
	}
	chain = chain.Session(&gorm.Session{})

	var total int64
	if err := chain.Count(&total).Error; err != nil {
		log.FromContext(ctx).Error("counting deals", "error", err)
		writeError(w, nethttp.StatusServiceUnavailable, "the deal table cannot be read")
		return
	}
	var deals []Deal
	err := chain.
		Joins("Stage").
		Joins("Account", p.db.DB().Select("id", "name")).
		Joins("Owner", p.db.DB().Select("id", "display_name")).
		Preload("Stakeholders", func(q *gorm.DB) *gorm.DB { return q.Order(contactTable + ".name, " + contactTable + ".id") }).
		Order(dealTable + ".created_at DESC, " + dealTable + ".id").
		Limit(limit).
		Find(&deals).Error
	if err != nil {
		log.FromContext(ctx).Error("reading deals", "error", err)
		writeError(w, nethttp.StatusServiceUnavailable, "the deal table cannot be read")
		return
	}
	out := dealList{Total: total, Deals: make([]dealView, 0, len(deals))}
	for _, d := range deals {
		out.Deals = append(out.Deals, viewOfDeal(d))
	}
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, out)
}

// getDeal answers one deal, with its version as the ETag a write conditions on.
func (p *dealsProduct) getDeal(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx := r.Context()
	if _, ok := memberOf(w, r, getDealRoute); !ok {
		return
	}
	deal, found, err := dealByID(ctx, p.db, r.PathValue("id"))
	if err != nil {
		log.FromContext(ctx).Error("reading a deal", "error", err)
		writeError(w, nethttp.StatusServiceUnavailable, "the deal table cannot be read")
		return
	}
	if !found {
		writeError(w, nethttp.StatusNotFound, errDealNotFound.Error())
		return
	}
	w.Header().Set("ETag", etagOf(deal.Version))
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, viewOfDeal(deal))
}

// dealPatch is the body of a change to a deal. A member left out is left as it
// is; the stage is not among them, because a stage move is a transition with an
// activity and events of its own.
type dealPatch struct {
	Title         *string `json:"title"`
	AccountID     *string `json:"account_id"`
	Amount        *int64  `json:"amount"`
	Currency      *string `json:"currency"`
	OwnerID       *string `json:"owner_id"`
	ExpectedClose *string `json:"expected_close"`
}

// Validate refuses a change that names nothing, or a value this host does not
// accept.
func (d dealPatch) Validate() error {
	switch {
	case d == (dealPatch{}):
		return errEmptyPatch
	case d.Title != nil && strings.TrimSpace(*d.Title) == "":
		return errNoTitle
	case d.AccountID != nil && strings.TrimSpace(*d.AccountID) == "":
		return errNoAccount
	case d.Amount != nil && *d.Amount < 0:
		return errNegativeAmount
	case d.Currency != nil && !currencyPattern.MatchString(*d.Currency):
		return errBadCurrency
	case d.OwnerID != nil && strings.TrimSpace(*d.OwnerID) == "":
		return errOwnerNotMember
	}
	if d.ExpectedClose != nil {
		if _, err := parseDate(*d.ExpectedClose); err != nil {
			return err
		}
	}
	return nil
}

// changes is the patch as the columns it writes.
func (d dealPatch) changes() map[string]any {
	out := map[string]any{}
	if d.Title != nil {
		out["title"] = strings.TrimSpace(*d.Title)
	}
	if d.AccountID != nil {
		out["account_id"] = strings.TrimSpace(*d.AccountID)
	}
	if d.Amount != nil {
		out["amount"] = *d.Amount
	}
	if d.Currency != nil {
		out["currency"] = *d.Currency
	}
	if d.OwnerID != nil {
		out["owner_id"] = *d.OwnerID
	}
	if d.ExpectedClose != nil {
		day, _ := parseDate(*d.ExpectedClose)
		out["expected_close"] = day
	}
	return out
}

// patchDeal changes a deal the request names by the version it last read.
//
// The update is conditioned on that version and moves it on in the same
// statement, so of two writers that read one version the second's update
// matches no row, whichever of them the database orders first. A version that
// no longer matches is answered 412; the deal's absence, 404. A change of the
// account is checked before the transaction and again inside it, once the
// update has matched the version. The change's deal.changed is staged in the
// same transaction, so it is published if and only if the change commits.
func (p *dealsProduct) patchDeal(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx)
	self, ok := memberOf(w, r, patchDealRoute)
	if !ok {
		return
	}
	if !canWriteDeals(self.Role) {
		writeError(w, nethttp.StatusForbidden, errReadOnlyRole.Error())
		return
	}
	version, ok := ifMatchVersion(w, r)
	if !ok {
		return
	}
	var body dealPatch
	if err := speedhttp.Decode(r, &body); err != nil {
		writeError(w, speedhttp.StatusFor(err), err.Error())
		return
	}
	if body.OwnerID != nil {
		belongs, err := isMember(ctx, p.db, *body.OwnerID)
		if err != nil {
			logger.Error("reading the owner's membership", "error", err)
			writeError(w, nethttp.StatusServiceUnavailable, "the membership table cannot be read")
			return
		}
		if !belongs {
			writeError(w, nethttp.StatusUnprocessableEntity, errOwnerNotMember.Error())
			return
		}
	}
	if body.AccountID != nil {
		switch err := requireAccount(p.db.DB().WithContext(ctx), strings.TrimSpace(*body.AccountID)); {
		case errors.Is(err, errUnknownAccount):
			writeError(w, nethttp.StatusUnprocessableEntity, err.Error())
			return
		case err != nil:
			logger.Error("reading the deal's account", "error", err)
			writeError(w, nethttp.StatusServiceUnavailable, "the account table cannot be read")
			return
		}
	}
	id := r.PathValue("id")
	changes := body.changes()
	changes["version"] = version + 1
	var updated Deal
	err := p.db.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Deal{}).Where("id = ? AND version = ?", id, version).Updates(changes)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var present int64
			if err := tx.Model(&Deal{}).Where("id = ?", id).Count(&present).Error; err != nil {
				return err
			}
			if present == 0 {
				return errDealNotFound
			}
			return errVersionMismatch
		}
		if body.AccountID != nil {
			if err := requireAccount(tx, strings.TrimSpace(*body.AccountID)); err != nil {
				return err
			}
		}
		if err := withDealRelations(tx).First(&updated, "id = ?", id).Error; err != nil {
			return err
		}
		return p.stageWrite(tx, self, dealChangedEvent, dealChanged, id)
	})
	switch {
	case errors.Is(err, errDealNotFound):
		writeError(w, nethttp.StatusNotFound, err.Error())
	case errors.Is(err, errVersionMismatch):
		writeError(w, nethttp.StatusPreconditionFailed, err.Error())
	case errors.Is(err, errUnknownAccount):
		writeError(w, nethttp.StatusUnprocessableEntity, err.Error())
	case err != nil:
		logger.Error("changing a deal", "error", err)
		writeError(w, nethttp.StatusInternalServerError, "the deal could not be written")
	default:
		w.Header().Set("ETag", etagOf(updated.Version))
		_ = speedhttp.WriteJSON(w, nethttp.StatusOK, viewOfDeal(updated))
	}
}

// moveRequest is the body of a stage move.
type moveRequest struct {
	Stage string `json:"stage"`
	Note  string `json:"note"`
}

// Validate refuses a move that names no stage.
func (m moveRequest) Validate() error {
	if strings.TrimSpace(m.Stage) == "" {
		return errNoStage
	}
	return nil
}

// moveView is what a stage move answers with: the deal as the move left it,
// and the activity that records the move.
type moveView struct {
	Deal     dealView     `json:"deal"`
	Activity activityView `json:"activity"`
}

// moveDeal moves a deal to another of the tenant's stages.
//
// The move is one transaction: the deal's update, conditioned on the version
// the request names, the stage-change activity that records it and the move's
// events commit together or not at all: deal.stage-changed for every move, and
// deal.won as well when the stage is of kind won. The events are staged in the
// transaction through the outbox, whose relay publishes them once it has
// committed, so no consumer hears of a move that did not happen, and a move
// that stands is announced however often the bus refuses the event first.
// Each event's identifier is minted before the transaction and written on the
// activity row, which is what traces an event a consumer received back to the
// write; the relay publishes an event under the identifier it was staged with
// every time.
func (p *dealsProduct) moveDeal(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx)
	self, ok := memberOf(w, r, moveDealRoute)
	if !ok {
		return
	}
	if !canWriteDeals(self.Role) {
		writeError(w, nethttp.StatusForbidden, errReadOnlyRole.Error())
		return
	}
	version, ok := ifMatchVersion(w, r)
	if !ok {
		return
	}
	var body moveRequest
	if err := speedhttp.Decode(r, &body); err != nil {
		writeError(w, speedhttp.StatusFor(err), err.Error())
		return
	}
	target, found, err := stageByKey(ctx, p.db, body.Stage)
	if err != nil {
		logger.Error("reading a stage", "error", err)
		writeError(w, nethttp.StatusServiceUnavailable, "the pipeline cannot be read")
		return
	}
	if !found {
		writeError(w, nethttp.StatusUnprocessableEntity, errUnknownStage.Error())
		return
	}

	// The deal is read before the transaction, for the stage it leaves and for
	// the answers a move that cannot happen gets. What it read holds for the
	// version the request names, since every write moves the version on, and
	// the update below is conditioned on that version: a writer that got in
	// between makes it match no row. The transaction therefore starts with its
	// write.
	id := r.PathValue("id")
	current, found, err := dealByID(ctx, p.db, id)
	switch {
	case err != nil:
		logger.Error("reading a deal", "error", err)
		writeError(w, nethttp.StatusServiceUnavailable, "the deal table cannot be read")
		return
	case !found:
		writeError(w, nethttp.StatusNotFound, errDealNotFound.Error())
		return
	case current.Version != version:
		writeError(w, nethttp.StatusPreconditionFailed, errVersionMismatch.Error())
		return
	case current.StageKey == target.Key:
		writeError(w, nethttp.StatusConflict, errAlreadyInStage.Error())
		return
	}
	from := current.StageKey
	now := time.Now().UTC()
	changedID := event.NewID()
	var wonID *string
	if target.Kind == stageWon {
		minted := event.NewID()
		wonID = &minted
	}
	var moved Deal
	var activity Activity
	err = p.db.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Deal{}).
			Where("id = ? AND version = ?", id, version).
			Select("stage_key", "version", "closed_at", "updated_at").
			Updates(Deal{StageKey: target.Key, Version: version + 1, ClosedAt: closedAtFor(target, now), UpdatedAt: now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errVersionMismatch
		}
		activityID, idErr := newID("a_")
		if idErr != nil {
			return idErr
		}
		activity = Activity{
			ID:          activityID,
			SubjectKind: subjectDeal,
			SubjectID:   id,
			Kind:        activityStageChange,
			Body:        stageChangeBody(from, target, body.Note),
			ActorID:     self.UserID,
			OccurredAt:  now,
			EventID:     &changedID,
			WonEventID:  wonID,
		}
		if createErr := tx.Create(&activity).Error; createErr != nil {
			return fmt.Errorf("writing the stage change: %w", createErr)
		}
		if readErr := withDealRelations(tx).First(&moved, "id = ?", id).Error; readErr != nil {
			return readErr
		}
		object := "deal/" + id
		if stageErr := p.outbox.Stage(tx, event.Event{
			ID:       changedID,
			Type:     dealStageChangedEvent,
			TenantID: string(self.TenantID),
			Payload: stageChangedPayload{
				Object: object, Outcome: target.Kind, DealID: id,
				FromStage: from, ToStage: target.Key, Amount: moved.Amount, Currency: moved.Currency,
				Version: moved.Version, ActivityID: activity.ID, ActorID: self.UserID,
			},
		}); stageErr != nil {
			return stageErr
		}
		if wonID == nil {
			return nil
		}
		return p.outbox.Stage(tx, event.Event{
			ID:       *wonID,
			Type:     dealWonEvent,
			TenantID: string(self.TenantID),
			Payload: wonPayload{
				Object: object, Outcome: stageWon, DealID: id, AccountID: moved.AccountID,
				Stage: target.Key, Amount: moved.Amount, Currency: moved.Currency, OwnerID: moved.OwnerID,
				WonAt: now.Format(time.RFC3339Nano), ActivityID: activity.ID, ActorID: self.UserID,
			},
		})
	})
	switch {
	case errors.Is(err, errVersionMismatch):
		writeError(w, nethttp.StatusPreconditionFailed, err.Error())
		return
	case err != nil:
		logger.Error("moving a deal", "error", err)
		writeError(w, nethttp.StatusInternalServerError, "the stage move could not be written")
		return
	}

	w.Header().Set("ETag", etagOf(moved.Version))
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, moveView{Deal: viewOfDeal(moved), Activity: viewOfActivity(activity)})
}

// stageWrite stages the event of a creation, a change or a deletion in the
// transaction that makes it, under an identifier the outbox mints for it.
func (p *dealsProduct) stageWrite(tx *gorm.DB, self member, eventType, outcome, id string) error {
	return p.outbox.Stage(tx, event.Event{
		Type:     eventType,
		TenantID: string(self.TenantID),
		Payload:  dealWrittenPayload{Object: "deal/" + id, Outcome: outcome, DealID: id, ActorID: self.UserID},
	})
}

// deleteDeal removes a deal from the tenant's pipeline. The deal is soft
// deleted: its row stays, with its activities, and every read leaves it out.
//
// It is addressed by the identifier half of its primary key; the tenant half is
// the filter's to fill from the request. A deletion that removed a deal stages
// deal.deleted in its transaction, and one that found no deal stages nothing.
func (p *dealsProduct) deleteDeal(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx := r.Context()
	self, ok := memberOf(w, r, deleteDealRoute)
	if !ok {
		return
	}
	if !canWriteDeals(self.Role) {
		writeError(w, nethttp.StatusForbidden, errReadOnlyRole.Error())
		return
	}
	id := r.PathValue("id")
	err := p.db.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&Deal{ID: id})
		switch {
		case result.Error != nil:
			return result.Error
		case result.RowsAffected == 0:
			return errDealNotFound
		}
		return p.stageWrite(tx, self, dealDeletedEvent, dealDeleted, id)
	})
	switch {
	case errors.Is(err, errDealNotFound):
		writeError(w, nethttp.StatusNotFound, err.Error())
	case err != nil:
		log.FromContext(ctx).Error("deleting a deal", "error", err)
		writeError(w, nethttp.StatusInternalServerError, "the deal could not be deleted")
	default:
		w.WriteHeader(nethttp.StatusNoContent)
	}
}

// init registers the deals module. It runs on serve: its routes sit on the API
// endpoint.
func init() {
	registerApplication(application{
		module: dealsModule(),
		runsOn: []string{serveCommand},
		models: []any{Deal{}, PipelineStage{}, Activity{}, Account{}, Membership{}, User{}, Contact{}},
		journalled: []string{
			createDealRoute, patchDealRoute, moveDealRoute, deleteDealRoute,
			setStakeholdersRoute, addStakeholderRoute, removeStakeholderRoute,
		},
	})
}

// dealsModule declares the deals table and its settings, and binds the deal
// routes.
//
// Its requirements are what its product holds and what its table references.
// The roster, because the deals table is scoped. The key-value store, the
// settings surface and the outbox are exclusive provisions, so the requirement
// brings none of them into a command; it fails a command that runs this module
// and forgot one, at resolution and naming the capability, rather than at the
// first request. The bus is the outbox relay's to require: this module stages
// its events and publishes none itself. The deals table references the stages, accounts and users
// tables, and the stakeholders' join table the contacts table, so the
// requirements on those four modules' table tokens order its migrations after
// theirs; they are optional, because the order is computed over every
// registered module and this module reads those tables through the database
// handle rather than through any of those modules' products.
func dealsModule() core.Module {
	return core.Module{
		Name: dealsModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*kv.KV)(nil)},
			{Token: (*settings.Store)(nil)},
			{Token: (*outbox.Outbox)(nil)},
			{Token: (*pipelineTable)(nil), Optional: true},
			{Token: (*accountsTable)(nil), Optional: true},
			{Token: (*usersTable)(nil), Optional: true},
			{Token: (*contactsTable)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*dealsTable)(nil), Exclusive: true},
		},
		Resources: []any{
			db.Migrations{FS: dealsMigrations},
			dealsSettingsSchema(),
			speedhttp.Spec{Endpoint: apiEndpoint, Document: dealsOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, dealsModuleName)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			store, err := core.Resolve[kv.KV](reg)
			if err != nil {
				return nil, err
			}
			surface, err := core.Resolve[settings.Store](reg)
			if err != nil {
				return nil, err
			}
			staged, err := core.Resolve[outbox.Outbox](reg)
			if err != nil {
				return nil, err
			}
			return &dealsProduct{
				released: released{log: logger},
				db:       database,
				store:    store,
				settings: surface,
				outbox:   staged,
			}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*dealsProduct)
			if !ok {
				return errForeignInstance(dealsModuleName, instance)
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			api, err := router.Endpoint(apiEndpoint)
			if err != nil {
				return fmt.Errorf("binding the deal routes: %w", err)
			}
			api.Route(createDealRoute, nethttp.HandlerFunc(product.createDeal))
			api.Route(listDealsRoute, nethttp.HandlerFunc(product.listDeals))
			api.Route(getDealRoute, nethttp.HandlerFunc(product.getDeal))
			api.Route(patchDealRoute, nethttp.HandlerFunc(product.patchDeal))
			api.Route(moveDealRoute, nethttp.HandlerFunc(product.moveDeal))
			api.Route(deleteDealRoute, nethttp.HandlerFunc(product.deleteDeal))
			api.Route(setStakeholdersRoute, nethttp.HandlerFunc(product.setStakeholders))
			api.Route(addStakeholderRoute, nethttp.HandlerFunc(product.addStakeholder))
			api.Route(removeStakeholderRoute, nethttp.HandlerFunc(product.removeStakeholder))
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
