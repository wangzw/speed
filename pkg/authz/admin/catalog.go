package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
)

// handlePermissionList answers GET /authz/admin/permissions: every permission
// the catalog declares, each as its own view. It is a read, so the action is
// role.read.
func (p *product) handlePermissionList(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, authz.PermissionRoleRead) {
		return
	}
	declared := p.catalog.Declared()
	views := make([]permissionView, len(declared))
	for i, perm := range declared {
		views[i] = permissionViewOf(perm)
	}
	c.JSON(http.StatusOK, gin.H{"permissions": views})
}
