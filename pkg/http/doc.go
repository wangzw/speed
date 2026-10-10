// Package http is the entry point module: the registration surface modules bind
// routes and middleware through, the resource type they declare their OpenAPI
// fragment as, and the error response every layer and handler writes through.
//
// The module is a wrapper around gin. The surface is expressed in gin's own
// types — a route's handlers and a middleware's Handler are gin.HandlerFunc —
// and in this package's types. Routing, the middleware chain, the binding and
// validation of a request body and the rendering of a response are gin's, and
// a handler reaches them through gin.Context. What this package adds is the
// assembly around them: named listening endpoints, the registration gate,
// ordering layers by the capabilities they declare, the request context, the
// drain, the fallback for a panic, and the error response.
//
// A host that takes this package up carries gin and its dependencies in its
// dependency list.
//
// # Naming this package http
//
// This package is named http, which collides with the standard library's. The
// direction of the alias is fixed per kind of file, so that a reader never has
// to work out which http a file means:
//
//   - A production file or an internal test file, both in package http, import
//     the standard library as nethttp "net/http".
//   - An external test file in package http_test, and example_test.go with it,
//     go the other way: net/http keeps its own name and this package is
//     imported as speedhttp "github.com/vislake/speed/pkg/http".
//   - A Go block in AGENTS.md carries a package clause and is compiled by
//     tools/check_markdown_examples.py, so it follows whichever of the rules
//     above its package clause selects.
package http
