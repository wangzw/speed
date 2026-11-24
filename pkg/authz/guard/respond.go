package guard

import (
	nethttp "net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

type outcome int

const (
	allowed outcome = iota
	denied
	unguarded
	outage
	noIdentity

	outcomeCount
)

var names = [outcomeCount]string{
	allowed:    "allowed",
	denied:     "denied",
	unguarded:  "unguarded",
	outage:     "unavailable",
	noIdentity: "no-identity",
}

func (o outcome) String() string { return names[o] }

func (o outcome) passes() bool { return o == allowed }

func (o outcome) status() int {
	switch o {
	case denied, unguarded:
		return nethttp.StatusForbidden
	case noIdentity:
		return nethttp.StatusUnauthorized
	case outage:
		return nethttp.StatusServiceUnavailable
	}
	return nethttp.StatusOK
}

type decision struct {
	outcome outcome
	// The decision's cause goes to the log and never to the caller.
	cause error
	// The permission is kept out of the detail, the body and the log; naming it would make the answer a probe of the policy.
	permission authz.Permission
}

const (
	typePrefix      = "urn:speed:authz:"
	forbiddenCode   = "forbidden"
	forbiddenDetail = forbiddenCode
)

func forbidden(c *gin.Context, o outcome, perm authz.Permission) {
	speedhttp.Fail(c, speedhttp.Problem{
		Status: o.status(),
		Type:   typePrefix + forbiddenCode,
		Detail: forbiddenDetail,
	})
}

func unavailable(c *gin.Context, o outcome, err error) {
	if err != nil {
		log.FromContext(c.Request.Context()).Error(ModuleName+": the request could not be decided",
			outcomeAttribute, o.String(), "error", err)
	}
	speedhttp.Fail(c, speedhttp.Problem{Status: o.status()})
}

func noIdentity401(c *gin.Context, o outcome, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: o.status()})
}

func quoteRealm(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		switch ch := name[i]; ch {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		case '\r', '\n':
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}
