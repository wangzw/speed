# pkg/tenant/self

The tenant capability's caller-facing module. It mounts
`GET /api/v1/auth/me/tenants`, the read that answers which tenants the calling
account holds an active membership in, and delivers no capability of its own: it
composes the account module's `authn.Memberships` with the roster's
`tenant.Catalog`.

The identity is the one the request's credential settled; no path parameter or
body names a subject. The route resolves no tenant: it is mounted on the account
endpoint, whose requests carry no tenant name, because the answer is about the
caller and not about one tenant.

## The answer

```json
{ "tenants": [ { "alias": "Acme", "displayName": "", "kind": "business" }, { "alias": "alice", "displayName": "", "kind": "personal" } ] }
```

A membership enters the answer when it is itself active (`Membership.Active`) and
its roster row's status is `tenant.StatusActive`. A suspended membership, a
disabled tenant and an identifier the roster snapshot does not carry are all left
out. The list is ordered by the membership read's own order — `Memberships.Tenants`
already lists by identifier, and the filter preserves that order rather than
sorting again — and an empty result is `[]`, never `null`. The answer carries no
identifier and no status: a client addresses the tenant by the alias, and a
tenant's kind tells it whether the alias names an ordinary organization or the
account's own space.

## The kind

`kind` is the closed set `business | personal`, and it is the host's answer, not
the roster's: the roster records no kind. This module takes an optional
`tenant.KindResolver` and passes each answer through unchanged, asking it once
per listed tenant.

- **No resolver means every row is `business`.** That is the assembly with no
  personal-tenant policy, where no personal tenant exists. The requirement is
  optional at this module's descriptor.
- **An assembly that carries a personal-tenant policy provides a resolver, and
  its module declares the capability required**, so a command that enables the
  policy without one fails at dependency resolution rather than serving every
  row as business.
- **A resolver error is a 503**, never a silent `business`: the kind is a fact,
  and a failure to read it is not one.

The closed set is written into this module's fragment as an OpenAPI `enum`, so
the generated clients type `kind` as a union of the two values.

## Access control

The route carries no permission: every authenticated caller may read its own
memberships, and any permission it could name would have to be held already —
which is the question the read answers. It is declared open on the route
declaration surface so the enforcement middleware lets the request through.

Authenticated admission is the gateway's: the route is mounted on the endpoints
`tenant.self.endpoints` lists, and a route on a guarded endpoint that carries no
exemption requires a credential, so a request with none is refused before a
handler runs. The endpoint must be the one claiming `/api/v1/auth`; it must not
be an endpoint the tenant resolution layer guards, because that layer requires a
tenant name the account endpoint does not carry. A handler reached without an
identity — the endpoint is not guarded, or the handler was reached another way —
answers the same 401 the other authenticated surfaces answer: status alone, no
problem type, and the realm challenge
`Bearer realm="<endpoint>", Cookie realm="<endpoint>"`.

The route is registered impersonatable. An impersonating caller is confined to
its grant's tenant, and the handler narrows the answer to that one membership:
the host needs the tenant's alias after a full page load, when the roster cache
it read before is gone, while the impersonated account's memberships in other
tenants stay outside the grant. An impersonating identity with no scope (the
gateway's exit check never lets one through; only a direct handler call can
produce one) gets an empty list rather than the full one.

## Boundaries

- **The module holds no memberships and no roster.** It reads through
  `authn.Memberships.Tenants` and `tenant.Catalog.Get` and maps nothing: the
  identifiers and aliases are the roster's, and the order is the membership
  read's.
- **A read that cannot answer is a 503, never an empty list.** The cause goes to
  the log and never to the body. An empty list says the caller holds no active
  membership, which is a different answer from an outage. A membership read or a
  roster read that fails, and `tenant.ErrRosterMissing` for a snapshot that has
  not been published, are all this one 503.
- **An impersonating caller sees only the grant's tenant.** The route is
  reachable so a host can name that tenant after a full reload; the answer is
  filtered by the identity's `Scope()` before the roster lookup, so the
  impersonated account's memberships elsewhere never leave the handler. The
  register step that makes the route impersonatable is the gateway's admission
  surface, taken up optionally in `Init`.
- **The route set holds no 403 and no 404.** It decides no permission, and it
  addresses no object that could be absent.
- **The endpoint list is required and takes concrete names only.** An empty
  list, an empty name, `*` and a repeated name are refused with
  `ErrInvalidConfig`; a name the entry point does not declare fails the startup
  with `tenant.ErrUnknownEndpoint`, and a command with no entry point fails with
  `tenant.ErrNoRouter`. The list is read from the primary configuration source
  alone.
- **The route is mounted on every endpoint the list names.** It is one route
  under the ordinary API root `/api/v1`, and its OpenAPI fragment is generated
  from the operation declaration, not written by hand. The fragment is declared
  against the `account` endpoint because the route only makes sense where the
  `/api/v1/auth` prefix is claimed.

## Running it

```
go -C pkg test ./tenant/self/...
go -C pkg test -race ./tenant/self/...
env -C pkg golangci-lint run ./tenant/self/...
```

`TestTheFragmentIsGeneratedFromTheDeclarations` compares the committed fragment
with the declarations; regenerate it with
`UPDATE_FRAGMENTS=1 go -C pkg test ./tenant/self -run TestTheFragmentIsGeneratedFromTheDeclarations`.
