//go:build review

package main

// reviewEnabled says this test binary is built with the review tag, so its
// cases drive a review assembly and TestMain compiles a second, review-tagged
// program for the commands that have to read that assembly's configuration.
const reviewEnabled = true
