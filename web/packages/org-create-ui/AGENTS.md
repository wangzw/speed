# org-create-ui contract

`@speed/org-create-ui` is the shared organization-creation UI. It owns:

- the controlled creation form (`OrganizationCreateForm`) and its values;
- the client-side validation the server remains authoritative over
  (`validateOrganizationCreateValues`, `nameViolation`,
  `isRejectedIdentifier`, the reserved name set and the display-name,
  description and website bounds);
- the self-service creation page (`OrganizationCreatePage`) and its
  create-then-enter sequence;
- the error-code-to-text resolver for creation refusals;
- the bilingual `org-create-ui` namespace.

It does not own requests, the router, the current tenant, the caller's
tenant list, or the platform console's page chrome. A host supplies the
creation operation, the hand-over that switches to the new organization,
and the destination path.

## Boundaries

- The public interface is `src/index.ts` only. `src/internal/` does not
  exist; everything in `src/` that is not exported is package-internal.
- No imports from `@speed/route-kit`, `@speed/tenancy-ui`,
  `@speed/product-shell` or `@speed/api-sdk`. The package is transport-
  and host-agnostic.
- User-facing copy lives only in `src/locales/{zh-CN,en-US}.json`, with
  identical leaf sets. A host registers the namespace once at bootstrap.
- The server is authoritative on every rule. The client repeats the name
  shape, the built-in reserved names, the identifier domain and the
  profile bounds; a deployment may append reserved names the client cannot
  know.

## Commands

```sh
pnpm -C web/packages/org-create-ui test
pnpm -C web/packages/org-create-ui lint
pnpm -C web/packages/org-create-ui typecheck
pnpm -C web/packages/org-create-ui build
```

The workspace's shared test preset excludes `*.browser.test.tsx`; the
browser tier (`make web-browser`) runs them in Chromium.
