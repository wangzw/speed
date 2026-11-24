package admin

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	typePrefix     = "urn:speed:audit:"
	badRequestCode = "bad-request"
)

func badRequestFault() error {
	return speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
}

func (p *product) caller(c *gin.Context) (authn.Identity, bool) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		writeNoIdentity401(c, realmOf(c))
		return authn.Identity{}, false
	}
	return id, true
}

func realmOf(c *gin.Context) string {
	if v, ok := c.Get(realmKey); ok {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}

func writeNoIdentity401(c *gin.Context, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
}

// quoteRealm escapes what goes between the quotes of a realm: a quote or
// a backslash gets a backslash, and a line ending is dropped because it
// would start another header.
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

func (p *product) require(c *gin.Context) bool {
	return p.guard.RequirePermissionIn(c, authz.PermissionAuditRead, authz.PlatformDomain)
}

// bad answers a request this module refuses on shape alone: a path parameter or
// a query parameter it cannot read. It is written before any read of the table.
func (p *product) bad(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode})
}
