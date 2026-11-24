package authz

// ModelText is the Casbin model the policy engine is loaded with: classic RBAC
// over (domain, object, action). g resolves a subject to a role within a domain,
// the effect allows when a matching policy allows, and the matcher requires the
// domain, object and action to be equal. There is no wildcard, no role-to-role
// inheritance and no deny row.
const ModelText = `[request_definition]
r = sub, dom, obj, act
[policy_definition]
p = sub, dom, obj, act
[role_definition]
g = _, _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub, r.dom) && r.dom == p.dom && r.obj == p.obj && r.act == p.act
`
