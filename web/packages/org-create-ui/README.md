# @speed/org-create-ui

The shared organization-creation surface: one controlled form (alias,
display name, description, plus the platform-only identifier, contact
address and website), the client-side validation for those fields, the
self-service creation page, and the bilingual `org-create-ui` namespace.

The platform console renders `OrganizationCreateForm` in `platform` mode
inside its own registration page; the demo host renders
`OrganizationCreatePage` over its `org.create` operation. The server
remains authoritative on every rule: the client repeats the alias shape,
the built-in reserved names and the profile bounds so a typo is caught in
place.

The package owns no request, no router and no tenant state. A host that
renders the self-service page supplies the creation operation, the
hand-over that switches to the new organization, and the destination.

```tsx
import {
  ORG_CREATE_UI_NAMESPACE,
  OrganizationCreatePage,
  orgCreateUiResources,
} from '@speed/org-create-ui'

registerNamespace(i18n, ORG_CREATE_UI_NAMESPACE, orgCreateUiResources)
```

The design rationale lives in `docs/design/modules/web/design-web.md` and
`docs/design/modules/tenant/design-tenant-selfcreate.md`; this package's
boundaries live in `AGENTS.md`.
