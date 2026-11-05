//go:build e2e

package main

import "embed"

//go:embed dist-e2e
var webBundle embed.FS

// webBundleRoot is the directory inside webBundle the frontend host serves. An
// e2e-tagged build embeds dist-e2e/, which the end-to-end harness produces
// before building, so that tier never writes the working tree's dist/. See
// web_bundle_default.go for the default variant.
const webBundleRoot = "dist-e2e"
