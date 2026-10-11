package mfa

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// The routes of the factor self-service, relative to the base path the mounter
// carries. Registering a factor is not among them: it goes through the login
// engine's enroll purpose, which is the one entry for it.
const (
	factorsRoute = "/factors"
	factorRoute  = "/factors/:id"
	factorParam  = "id"
)

// maxFactorIDLen is the longest a factor identifier in a path may be before the
// request is refused without a read. The register's identifiers are UUID texts,
// far shorter, so a longer one names nothing.
const maxFactorIDLen = 64

// mountRoutes registers the routes. Both are guarded by the gateway, which is
// what makes them Required, and neither is registered Impersonable: the gateway
// refuses an impersonating identity on them, and the handlers refuse one too, so
// the refusal does not depend on a single layer.
func (m *product) mountRoutes() {
	m.mounter.Guarded(http.MethodGet, factorsRoute, m.listFactors)
	m.mounter.Guarded(http.MethodDelete, factorRoute, m.deleteFactor)
}

// factorView is one factor as the list shows it. It is built field by field from
// the record, which holds nothing secret to begin with, so that a field added to
// the record later does not reach a client by being there.
type factorView struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Label       string     `json:"label"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

func statusName(s authn.FactorStatus) string {
	switch s {
	case authn.FactorPending:
		return "pending"
	case authn.FactorActive:
		return "active"
	default:
		return "disabled"
	}
}

func viewOf(rec authn.FactorRecord) factorView {
	v := factorView{
		ID:        rec.ID,
		Kind:      string(rec.Kind),
		Label:     rec.Label,
		Status:    statusName(rec.Status),
		CreatedAt: rec.CreatedAt.UTC(),
	}
	if !rec.ActivatedAt.IsZero() {
		at := rec.ActivatedAt.UTC()
		v.ActivatedAt = &at
	}
	if !rec.LastUsedAt.IsZero() {
		at := rec.LastUsedAt.UTC()
		v.LastUsedAt = &at
	}
	return v
}

// listFactors answers GET /factors: the caller's own factors, in a list that
// carries no seed, key or other data of a kind's own.
func (m *product) listFactors(c *gin.Context) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		mount.Unauthorized(c, m.mounter.Endpoint(), "")
		return
	}
	recs, err := m.Factors(c.Request.Context(), id.Account())
	if err != nil {
		m.answerError(c, id.Account(), err)
		return
	}
	views := make([]factorView, len(recs))
	for i, rec := range recs {
		views[i] = viewOf(rec)
	}
	c.JSON(http.StatusOK, gin.H{"factors": views})
}

// deleteFactor answers DELETE /factors/:id. It asks for a fresh multi-factor
// authentication that was verified here: an IdP's declaration of one is not
// enough to remove an account's own factor. A factor that is not the caller's
// reads as one already gone, because the route has no 404 among its answers and
// a different answer for another account's identifier would say it exists.
func (m *product) deleteFactor(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := authn.FromContext(ctx)
	if !ok {
		mount.Unauthorized(c, m.mounter.Endpoint(), "")
		return
	}
	if id.Impersonating() {
		mount.Forbidden(c, mount.CodeImpersonationRestricted)
		return
	}
	requirement := authn.FreshLocalMulti(m.settings.recentWindow())
	if !id.Satisfies(requirement, m.clock.Now()) {
		mount.StepUpRequired(c, id, requirement)
		return
	}
	factorID := c.Param(factorParam)
	if factorID == "" || len(factorID) > maxFactorIDLen {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	err := m.Remove(ctx, id.Account(), factorID)
	switch {
	case err == nil, errors.Is(err, authn.ErrFactorNotEnrolled):
		c.Status(http.StatusNoContent)
		c.Writer.WriteHeaderNow()
	default:
		m.answerError(c, id.Account(), err)
	}
}

// answerError writes the answer for an error a route's call returned: a conflict
// for the last factor, and for everything else an outage that names no cause,
// which the log carries.
func (m *product) answerError(c *gin.Context, account authn.AccountID, err error) {
	if errors.Is(err, authn.ErrLastFactor) {
		mount.Fail(c, mount.CodeConflict)
		return
	}
	m.logger.Warn(ModuleName+": a factor route could not be served",
		slog.String("account", string(account)), slog.String("cause", err.Error()))
	mount.Unavailable(c)
}
