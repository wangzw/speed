//go:build !e2e

package main

import "embed"

//go:embed dist
var webBundle embed.FS

const webBundleRoot = "dist"
