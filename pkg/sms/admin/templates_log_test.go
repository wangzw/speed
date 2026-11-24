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

func logLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

type templateOperation struct {
	name      string
	operation string
	template  string
	drive     func(*rig) *httptest.ResponseRecorder
}

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

// TestEachTemplateOperationWritesOneSuccessRecord checks that every successful registry
// call leaves exactly one record with the operation, template, outcome and time.
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

// TestEachTemplateOperationWritesOneFailureRecord checks that a failed call leaves one record adding only the cause.
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

// TestAMappedRefusalIsNotRecordedAsAFailure checks that a refusal the registry stated is mapped without a failure record.
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

const auditDelay = 100 * time.Millisecond

// TestAFailureDurationExcludesTheFailedWriteAudit checks that a failure record
// duration is the registry call's own time, below the audit delay.
func TestAFailureDurationExcludesTheFailedWriteAudit(t *testing.T) {
	for _, tc := range templateOperations() {
		if tc.operation == operationList {
			continue
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
