//go:build !review

package main

// reviewEnabled says this test binary is built with the review tag; the
// default build is not, so TestMain compiles one program and no case looks for
// a second.
const reviewEnabled = false
