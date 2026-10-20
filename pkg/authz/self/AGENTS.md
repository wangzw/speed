# pkg/authz/self

The authorization capability's caller-facing module. It mounts
`GET /api/v1/authz/me/permissions`, the read that answers which permissions the
calling principal holds, and delivers no capability of its own: the read is the
policy module's `SubjectPermissions`.

The identity is the one the request's credential settled; no path parameter or
body names a subject. The tenant is the one the request resolved to, read
through `tenant.FromContext` — the same seam the enforcement middleware uses. An
absent tenant leaves the tenant list empty; the module never falls back to the
platform domain, because the two lists are the two domains' answers.

## The answer

```json
{ "platform": ["authn.account.read"], "tenant": ["authz.role.read"] }
```

`platform` is the union of permissions the principal holds in the platform
domain; `tenant` is the union in the tenant the request resolved to. A
permission reached through several roles appears once, and the lists are sorted
by permission name. A held `(object, action)` pair that no declared permission
translates to is left out. Both members are always present; an empty result is
`[]`, never `null`.

## Access control

The route carries no permission: every authenticated caller may read its own
permissions, and any permission it could name would have to be held already —
which is the question the read answers. It is declared open on the route
declaration surface so the enforcement middleware lets the request through.

Authenticated admission is the gateway's: the route is mounted on the endpoints
`authz.self.endpoints` lists, and a route on a guarded endpoint that carries no
exemption requires a credential, so a request with none is refused before a
handler runs. A handler reached without an identity — the endpoint is not
guarded, or the handler was reached another way — answers the same 401 the other
authenticated surfaces answer: status alone, no problem type, and the realm
challenge `Bearer realm="<endpoint>", Cookie realm="<endpoint>"`.

## Boundaries

- **The module holds no policy.** It reads through `SubjectPermissions` and maps
  nothing: the domain names come from the request and the platform constant, and
  the permission names are the read's.
- **An unreadable policy is a 503, never an empty set.** The cause goes to the
  log and never to the body. An empty list says the principal holds nothing,
  which is a different answer from a policy that could not be read.
- **The endpoint list is required and takes concrete names only.** An empty
  list, an empty name, `*` and a repeated name are refused with
  `authn.ErrInvalidConfig`; a name the entry point does not declare fails the
  startup with `authz.ErrUnknownEndpoint`. The list is read from the primary
  configuration source alone.
- **The route is mounted on every endpoint the list names.** It is one route
  under the ordinary API root `/api/v1`, and its OpenAPI fragment is generated
  from the operation declaration, not written by hand.

## Running it

```
go -C pkg test ./authz/self/...
go -C pkg test -race ./authz/self/...
env -C pkg golangci-lint run ./authz/self/...
```

`TestTheFragmentIsGeneratedFromTheDeclarations` compares the committed fragment
with the declarations; regenerate it with
`UPDATE_FRAGMENTS=1 go -C pkg test ./authz/self -run TestTheFragmentIsGeneratedFromTheDeclarations`.
