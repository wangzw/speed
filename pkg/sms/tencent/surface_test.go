package tencent

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestTheExportedSurfaceNamesNoCarrierType checks that no carrier type reaches
// the exported surface, so a host sees only the sms.Sender seam.
func TestTheExportedSurfaceNamesNoCarrierType(t *testing.T) {
	cmd := exec.Command("go", "doc", "-all", ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go doc: %v\n%s", err, stderr.String())
	}
	if len(out) == 0 {
		t.Fatal("go doc printed nothing")
	}
	if strings.Contains(string(out), "tencentcloud") {
		t.Errorf("the exported surface names a carrier SDK type:\n%s", out)
	}
}
