package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/sms"
)

// logLines is the records the module wrote, one per line, with the blank tail a
// trailing newline leaves dropped.
func logLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// templateOperation is one call on the registry a case drives through the real
// route surface, with the operation name and template name its record must
// carry.
type templateOperation struct {
	name      string
	operation string
	template  string
	drive     func(*rig) *httptest.ResponseRecorder
}

// templateOperations is the three calls this module records. The submission
// body carries a value shaped like a message body: a record may name the
// template, and must not repeat what the template is written with.
func templateOperations() []templateOperation {
	return []templateOperation{
		{
			name: "list", operation: operationList,
			drive: func(r *rig) *httptest.ResponseRecorder {
				return r.do(http.MethodGet, pathTemplates, nil)
			},
		},
		{
			name: "offer", operation: operationOffer, template: "welcome",
			drive: func(r *rig) *httptest.ResponseRecorder {
				r.registry.offer = sms.Record{Name: "welcome", Carrier: "stub-carrier"}
				return r.do(http.MethodPost, pathSubmissions, offerBody{
					Name: "welcome", Content: "MESSAGE-BODY-SECRET", Kind: "notification",
					Locale: "en-US", Remark: "REVIEW-NOTE-SECRET",
				})
			},
		},
		{
			name: "remove", operation: operationRemove, template: "welcome",
			drive: func(r *rig) *httptest.ResponseRecorder {
				return r.do(http.MethodDelete, "/api/v1/system/sms/templates/welcome", nil)
			},
		},
	}
}

// TestEachTemplateOperationWritesOneSuccessRecord holds that every call this
// module makes on the registry leaves exactly one record when it succeeds: the
// operation, the template where the call names one, the outcome and the time.
// The count is held too, so a second line cannot hide behind the first.
func TestEachTemplateOperationWritesOneSuccessRecord(t *testing.T) {
	for _, tc := range templateOperations() {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			w := tc.drive(r)
			if w.Code >= http.StatusMultipleChoices {
				t.Fatalf("status = %d, want a success (body %s)", w.Code, w.Body.String())
			}
			lines := logLines(r.logs.String())
			if len(lines) != 1 {
				t.Fatalf("the module wrote %d records, want one: %s", len(lines), r.logs.String())
			}
			line := lines[0]
			for _, want := range []string{
				"level=INFO",
				"operation=" + tc.operation,
				"outcome=" + outcomeOK,
				"duration_ms=",
			} {
				if !strings.Contains(line, want) {
					t.Errorf("the record carries no %q: %s", want, line)
				}
			}
			if tc.template != "" && !strings.Contains(line, "template="+tc.template) {
				t.Errorf("the record does not name the template %q: %s", tc.template, line)
			}
			for _, unwanted := range []string{"MESSAGE-BODY-SECRET", "REVIEW-NOTE-SECRET"} {
				if strings.Contains(line, unwanted) {
					t.Errorf("the record carries %q, which is not the operation's to record: %s", unwanted, line)
				}
			}
		})
	}
}

// TestEachTemplateOperationWritesOneFailureRecord holds that a call the
// registry failed leaves one record naming the operation, the template where
// there is one, the failure outcome, the time and the cause — and that the
// cause is the only thing the failure adds. A mapped refusal is an answer, not
// a failure of the call, and still falls to the same one-record rule when the
// registry could not answer at all.
func TestEachTemplateOperationWritesOneFailureRecord(t *testing.T) {
	for _, tc := range templateOperations() {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.registry.fail(errors.New("store down"))
			w := tc.drive(r)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 (body %s)", w.Code, w.Body.String())
			}
			lines := logLines(r.logs.String())
			if len(lines) != 1 {
				t.Fatalf("the module wrote %d records, want one: %s", len(lines), r.logs.String())
			}
			line := lines[0]
			for _, want := range []string{
				"level=ERROR",
				"operation=" + tc.operation,
				"outcome=" + outcomeFailed,
				"duration_ms=",
				"cause=",
				"store down",
			} {
				if !strings.Contains(line, want) {
					t.Errorf("the record carries no %q: %s", want, line)
				}
			}
			if tc.template != "" && !strings.Contains(line, "template="+tc.template) {
				t.Errorf("the record does not name the template %q: %s", tc.template, line)
			}
		})
	}
}

// TestAMappedRefusalIsNotRecordedAsAFailure holds that a refusal the registry
// stated — a template from the configuration, a missing template — is mapped to
// its answer without the failure record the unmapped case writes: the registry
// answered, so there is no call failure to record.
func TestAMappedRefusalIsNotRecordedAsAFailure(t *testing.T) {
	r := newRig(t)
	r.registry.fail(sms.ErrImmutable)
	w := r.do(http.MethodDelete, "/api/v1/system/sms/templates/from-config", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if out := r.logs.String(); strings.Contains(out, "outcome="+outcomeFailed) {
		t.Errorf("a mapped refusal was recorded as a failed call: %s", out)
	}
}

// auditDelay is what one audit row costs in the duration case: long enough that
// a duration which counted it cannot be mistaken for one that did not.
const auditDelay = 100 * time.Millisecond

// TestAFailureDurationExcludesTheFailedWriteAudit holds that a failure record's
// duration_ms is the registry call's own time. The failed-write audit row is
// written after the call returned; the duration is read before that row, so a
// slow audit store cannot inflate it. The case drives an audit that takes
// auditDelay per row and requires the recorded duration to stay below it — the
// registry stub answers in well under that.
func TestAFailureDurationExcludesTheFailedWriteAudit(t *testing.T) {
	for _, tc := range templateOperations() {
		if tc.operation == operationList {
			continue // a list writes no failed-write audit row
		}
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.audit.slow(auditDelay)
			r.registry.fail(errors.New("store down"))
			w := tc.drive(r)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 (body %s)", w.Code, w.Body.String())
			}
			lines := logLines(r.logs.String())
			if len(lines) != 1 {
				t.Fatalf("the module wrote %d records, want one: %s", len(lines), r.logs.String())
			}
			got := durationMillis(t, lines[0])
			if got >= auditDelay.Milliseconds() {
				t.Errorf("duration_ms = %d counts the failed-write audit's %v: %s", got, auditDelay, lines[0])
			}
		})
	}
}

// durationMillis reads the duration_ms field out of one record.
func durationMillis(t *testing.T, line string) int64 {
	t.Helper()
	const field = "duration_ms="
	i := strings.Index(line, field)
	if i < 0 {
		t.Fatalf("the record carries no duration_ms: %s", line)
	}
	value := line[i+len(field):]
	if end := strings.IndexByte(value, ' '); end >= 0 {
		value = value[:end]
	}
	ms, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("duration_ms is not a number: %s", line)
	}
	return ms
}
