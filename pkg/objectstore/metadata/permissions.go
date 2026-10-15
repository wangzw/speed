package metadata

import "github.com/vislake/speed/pkg/authz"

// The permissions this module owns. They reach the process's permission catalog
// through authz.PermissionSpec and the (object, action) pair each one decides on
// reaches the process's translation through authz.TranslationSpec.
//
// The translation is what makes the decision answerable at all. The policy
// engine matches a permission on its (object, action) pair and never on the
// permission key, and a permission the process's translation has no row for is a
// decision the guard cannot express — it answers a denial, so a route held to
// either of these would refuse every caller. A route this module mounts names
// one of these, so the two rows are what keep its routes reachable.
const (
	// PermissionObjectRead is what a caller needs to read an object, list
	// objects, or stream an object's bytes.
	PermissionObjectRead authz.Permission = "objectstore.object.read"
	// PermissionObjectWrite is what a caller needs to declare, upload, complete
	// or delete an object.
	PermissionObjectWrite authz.Permission = "objectstore.object.write"
)

// objectResource is the resource noun both permissions act on. The engine
// matches the pair and never the permission key, so the pair is unique across
// the whole process set: this object carries no other capability's action.
const objectResource = "object"

// permissionSpec is this module's declaration of the two permissions, with the
// i18n key of each one's description.
func permissionSpec() authz.PermissionSpec {
	return authz.PermissionSpec{
		Permissions: []authz.Permission{PermissionObjectRead, PermissionObjectWrite},
		DescriptionKeys: map[authz.Permission]string{
			PermissionObjectRead:  "objectstore.permission.object-read",
			PermissionObjectWrite: "objectstore.permission.object-write",
		},
	}
}

// translationSpec is this module's declaration of how each permission
// translates: the row the engine matches on for it.
func translationSpec() authz.TranslationSpec {
	return authz.TranslationSpec{Rows: map[authz.Permission]authz.Translation{
		PermissionObjectRead:  {Object: objectResource, Action: "read"},
		PermissionObjectWrite: {Object: objectResource, Action: "write"},
	}}
}
