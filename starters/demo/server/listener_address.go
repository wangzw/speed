package main

// What this assembly treats as a loopback listener address. Two startup guards
// ask the question -- the review build's own from review.go, and the seed
// guard every build carries -- so the answer lives here, outside either build
// tag, rather than in one of them.

import (
	"net"
	"strings"
)

// loopbackAddress reports whether address, in net.Listen's form, names a
// loopback host. An empty host is every interface and is refused; "localhost"
// is accepted as the conventional loopback name.
func loopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
