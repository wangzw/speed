package admin

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
)

// records is the part of the impersonation capability the record routes read:
// the published Impersonation interface together with reading one record by its
// identifier. The capability the impersonation module delivers answers this, and
// the record group is mounted only when it does, because the routes resolve a
// record's tenant and target before asking the Decider and cannot do so from the
// published List alone.
type records interface {
	authn.Impersonation
	Get(ctx context.Context, id string) (authn.ImpersonationGrant, error)
}

// grantView is one impersonation record as the management routes show it. The
// reason never appears; it is in the record and the audit row alone.
type grantView struct {
	ID        string    `json:"id"`
	Actor     string    `json:"actor"`
	Target    string    `json:"target"`
	Tenant    string    `json:"tenant"`
	SessionID string    `json:"session_id"`
	StartedAt time.Time `json:"started_at"`
	EndsAt    time.Time `json:"ends_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	EndReason string    `json:"end_reason,omitempty"`
}

func grantViewOf(g authn.ImpersonationGrant) grantView {
	v := grantView{
		ID:        g.ID,
		Actor:     string(g.Actor),
		Target:    string(g.Target),
		Tenant:    g.Tenant,
		SessionID: string(g.SessionID),
		StartedAt: g.StartedAt.UTC(),
		EndsAt:    g.EndsAt.UTC(),
		EndReason: g.EndReason,
	}
	if !g.EndedAt.IsZero() {
		v.EndedAt = g.EndedAt.UTC()
	}
	return v
}

// handleImpersonationList answers GET /admin/impersonations: it lists the records
// of one tenant, named by the required query parameter. The action is
// impersonation.read.
func (m *product) handleImpersonationList(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	tenant := c.Query("tenant")
	if tenant == "" {
		m.bad(c)
		return
	}
	var limit int
	if s := c.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			m.bad(c)
			return
		}
		limit = n
	}
	if !m.asks(c, authn.Decision{Action: authn.ActionImpersonationRead, Actor: id, Tenant: tenant}) {
		return
	}
	grants, next, err := m.records.List(c.Request.Context(), authn.GrantQuery{Tenant: tenant, After: c.Query("after"), Limit: limit})
	if err != nil {
		m.cause(c, "listing impersonation records", err)
		return
	}
	// The tenant is the required query parameter and is passed to the store, so
	// the page holds only that tenant's records and the cursor counts them.
	views := make([]grantView, 0, len(grants))
	for _, g := range grants {
		views = append(views, grantViewOf(g))
	}
	c.JSON(200, gin.H{"impersonations": views, "next_cursor": next})
}

// handleImpersonationRevoke answers DELETE /admin/impersonations/:id: it reads
// the record to learn its tenant and target, asks the Decider with those, and
// revokes it. The action is impersonation.revoke. It writes no mandatory audit:
// the revocation is a move in the safe direction and the impersonation module
// writes its own best-effort row, so a failed audit does not stop the
// revocation. Revoking a record that has already ended is a no-op and gets the
// same answer as the first revocation.
func (m *product) handleImpersonationRevoke(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	grantID := c.Param("id")
	grant, err := m.records.Get(c.Request.Context(), grantID)
	if err != nil {
		if errors.Is(err, authn.ErrNotImpersonating) {
			m.notFound(c)
			return
		}
		m.answer(c, "reading an impersonation record", err)
		return
	}
	if !m.asks(c, authn.Decision{
		Action: authn.ActionImpersonationRevoke, Actor: id,
		Tenant: grant.Tenant, Target: string(grant.Target), TargetAccount: string(grant.Target),
	}) {
		return
	}
	if err := m.records.Revoke(c.Request.Context(), grantID, id, ""); err != nil {
		m.answer(c, "revoking an impersonation", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}
