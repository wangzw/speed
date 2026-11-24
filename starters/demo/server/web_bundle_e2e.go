//go:build e2e

package main

import "embed"

//go:embed dist-e2e
var webBundle embed.FS

const webBundleRoot = "dist-e2e"
