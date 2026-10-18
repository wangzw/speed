//go:build !review

package main

// reviewModules is empty in a default build. The review assembly's identity
// providers and its SMS sender are compiled only under the review build tag,
// so a production binary names none of them and carries no code path of
// theirs. See review.go.
var reviewModules []string
