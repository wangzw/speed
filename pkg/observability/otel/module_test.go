package otel

import (
	"strings"
	"testing"
)

// TestDistinctDropsARepeatedNameAndSaysSo pins what happens to a configured
// list that names the same endpoint twice.
//
// The list is registered by looping, and the entry module's Use does not
// dedupe: a repeated endpoint carries two identical layers, which doubles
// every request in the histogram and opens two server spans per request. The
// intent of a repeated name is the single one, so the duplicate is dropped —
// and the line is there because a configuration that is not what it says is
// something a host has no other way to find out.
func TestDistinctDropsARepeatedNameAndSaysSo(t *testing.T) {
	lg, buf := logBuffer()
	p := &providers{lg: lg}

	got := p.distinct("endpoints", []string{"api", "admin", "api"})
	if len(got) != 2 || got[0] != "api" || got[1] != "admin" {
		t.Errorf("distinct returned %v, want the repeated name dropped and the order kept", got)
	}
	if !strings.Contains(buf.String(), "twice") {
		t.Errorf("the log says %q, want a line reporting the repeat", buf.String())
	}

	// A list with no repeat is returned as it came, and said nothing about.
	buf.Reset()
	clean := p.distinct("endpoints", []string{"api", "admin"})
	if len(clean) != 2 {
		t.Errorf("distinct returned %v for a list with no repeat", clean)
	}
	if buf.Len() != 0 {
		t.Errorf("a list with no repeat wrote %q", buf.String())
	}
}
