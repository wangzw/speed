package main

// What this assembly treats as a loopback listener address. The seed guard asks
// the question, so the answer lives here.

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
