package tencent

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// The carrier SDK is bound by this package alone, so no carrier type reaches the
// exported surface: a host that imports this package to name the carrier sees the
// sms.Sender seam and nothing of the SDK behind it.
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
