package mail

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestTheExportedSurfaceIsTheDocumentedOne holds the surface this package's doc
// promises: the seam, the message and its parts, the receipt, the carrier
// factory, the classification helpers and the four sentinels — and nothing of a
// template mechanism or a carrier SDK, which this capability does not have.
func TestTheExportedSurfaceIsTheDocumentedOne(t *testing.T) {
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
	doc := string(out)

	for _, name := range []string{
		"type Message ", "type Attachment ", "type Receipt ", "type Sender ",
		"func NewModule[", "type Spec[", "func Classify(", "func Unsettled(", "func NoStatus(",
		"func (m Message) Validate()",
		"ErrThrottled", "ErrRejected", "ErrUncertain", "ErrInvalidConfig",
	} {
		if !strings.Contains(doc, name) {
			t.Errorf("the exported surface does not hold %q:\n%s", name, doc)
		}
	}
	for _, absent := range []string{"Declared", "Template", "netmail"} {
		if strings.Contains(doc, absent) {
			t.Errorf("the exported surface names %q, which the package doc does not describe:\n%s",
				absent, doc)
		}
	}
}
