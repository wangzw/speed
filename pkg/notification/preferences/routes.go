package preferences

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/notification"
)

// handleTypes answers GET /types: the taxonomy itself, one list every recipient
// shares. It is a read with no tenant of its own and it refuses nothing of this
// module's own; the body does not vary by language, but the endpoint keys its
// representation by the negotiated language — this module's error responses are
// written in it — so the Vary header keeps a shared cache from serving one
// language's representation for another.
func (p *product) handleTypes(c *gin.Context) {
	c.Header("Vary", "Accept-Language")
	specs := p.taxonomy.Types()
	views := make([]typeView, len(specs))
	for i, spec := range specs {
		views[i] = typeViewOf(spec)
	}
	c.JSON(http.StatusOK, gin.H{"types": views})
}

// handlePreferences answers GET /preferences: the caller's preference matrix,
// one row per type in the taxonomy. Each row's channels are the effective set —
// the stored channels when the recipient has a row for the type, the type's
// default channels otherwise. Like GET /types it refuses nothing of this
// module's own and keys its representation by language.
func (p *product) handlePreferences(c *gin.Context) {
	c.Header("Vary", "Accept-Language")
	ctx := c.Request.Context()
	recipient, _ := recipientFrom(ctx)
	stored, err := p.loadStored(ctx, recipient)
	if err != nil {
		p.answer(c, "reading the preferences", err)
		return
	}
	specs := p.taxonomy.Types()
	views := make([]preferenceView, len(specs))
	for i, spec := range specs {
		channels, explicit := effectiveChannels(spec, stored)
		views[i] = preferenceView{
			TypeKey:  spec.Key,
			Group:    spec.Group,
			Channels: channelStrings(channels),
			Explicit: explicit,
		}
	}
	c.JSON(http.StatusOK, gin.H{"preferences": views})
}

// effectiveChannels computes the effective set of one type for the recipient
// whose stored rows are given: a type that cannot be unsubscribed takes all of
// its default channels and is never explicit, a type that can takes its stored
// row when there is one and its default channels otherwise. It is the same
// computation Effective makes for one type, driven over the whole taxonomy.
func effectiveChannels(spec notification.TypeSpec, stored map[string][]notification.Channel) ([]notification.Channel, bool) {
	if !spec.Unsubscribable {
		return canonicalChannels(spec.DefaultChannels), false
	}
	if row, ok := stored[spec.Key]; ok {
		return canonicalChannels(row), true
	}
	return canonicalChannels(spec.DefaultChannels), false
}

// setPreferenceBody is the request of a PUT: the target state of one channel for
// one type. The state is a pointer so that a body naming none is told apart from
// one naming false.
type setPreferenceBody struct {
	Enabled *bool `json:"enabled"`
}

// handleSetPreference answers PUT /preferences/:typeKey/:channel: it turns one
// channel on or off for one type and returns that type's effective set.
//
// The decision order is fixed. The body is read first; the channel is checked
// next, before any no-op branch, so an unknown channel is never taken for a
// no-op; the type is checked after the channel; and the type's unsubscribability
// is checked after that. Only then is the effective set computed and the target
// set derived from it, and a target that equals the current effective set is a
// 200 that writes nothing. A write that does proceed goes through Set.
func (p *product) handleSetPreference(c *gin.Context) {
	ctx := c.Request.Context()

	var body setPreferenceBody
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		p.refuse(c, http.StatusBadRequest, badRequestCode)
		return
	}

	channel := notification.Channel(c.Param("channel"))
	if !notification.KnownChannel(channel) {
		p.refuse(c, http.StatusBadRequest, badRequestCode)
		return
	}

	typeKey := c.Param("typeKey")
	spec, ok := p.taxonomy.Type(typeKey)
	if !ok {
		p.refuse(c, http.StatusBadRequest, badRequestCode)
		return
	}
	if !spec.Unsubscribable {
		p.refuse(c, http.StatusConflict, notUnsubscribableCode)
		return
	}

	recipient, ok := recipientFrom(ctx)
	if !ok || recipient == "" {
		// A write names a recipient; the host's identity and authorization side
		// supplies it on the request context, and a request that reaches this
		// handler without one cannot be acted on.
		p.refuse(c, http.StatusBadRequest, badRequestCode)
		return
	}

	current, err := p.Effective(ctx, recipient, typeKey)
	if err != nil {
		p.answer(c, "reading the preference", err)
		return
	}
	target := applyToggle(current, channel, *body.Enabled)
	if sameSet(target, current) {
		c.JSON(http.StatusOK, gin.H{"type_key": typeKey, "channels": channelStrings(current)})
		return
	}

	written, err := p.Set(ctx, recipient, typeKey, target)
	if err != nil {
		p.answer(c, "writing the preference", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"type_key": typeKey, "channels": channelStrings(written)})
}

// answer maps a failure a seam method returned to the response it belongs to.
// The mapping is closed and fail-closed: a request whose shape cannot be read, an
// unknown type or channel, and a bad request are a 400; a type that cannot be
// unsubscribed is a 409; every other failure — a missing tenant, a table that
// cannot be read — is a 503 whose cause stays in the log and never reaches the
// caller.
func (p *product) answer(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, notification.ErrInvalidRequest),
		errors.Is(err, notification.ErrUnknownType),
		errors.Is(err, notification.ErrUnknownChannel):
		p.refuse(c, http.StatusBadRequest, badRequestCode)
	case errors.Is(err, notification.ErrNotUnsubscribable):
		p.refuse(c, http.StatusConflict, notUnsubscribableCode)
	default:
		p.logger.Error(ModuleName+": "+op+" failed", "cause", err.Error())
		p.refuse(c, http.StatusServiceUnavailable, "")
	}
}
