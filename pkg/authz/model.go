package authz

// ModelText is the Casbin model the policy engine is loaded with. It is fixed
// here and read from no other source: a host writes policy data, never the
// model, so the judgment language cannot be changed out from under the engine.
//
// The model is classic RBAC over (domain, object, action). r is a request and p
// a policy, each over subject, domain, object and action; g resolves a subject
// to a role within a domain, its third argument the domain, so a binding in one
// domain grants nothing in another; the effect allows when some matching policy
// allows, so a denial is the absence of a match; and the matcher requires the
// domain, object and action to be equal and the subject to reach the policy's
// subject through g. There is no wildcard, no role-to-role inheritance and no
// deny row.
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
