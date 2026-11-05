//go:build !e2e

package main

import "embed"

//go:embed dist
var webBundle embed.FS

// webBundleRoot is the directory inside webBundle the frontend host serves. The
// default build embeds the working tree's dist/, the bundle `pnpm build`
// produces. See web_bundle_e2e.go for the tagged variant.
const webBundleRoot = "dist"
