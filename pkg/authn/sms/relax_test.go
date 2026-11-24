package sms

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

func (r *rig) deliverChange(change settings.Change) {
	_ = r.bus.deliver(context.Background(), event.Event{Type: settings.ChangedEventType, Payload: change})
}

// TestARelaxingChangeWritesAWarnAndAnAuditRow checks that a relaxing change writes one Warn and one audit row.
func TestARelaxingChangeWritesAWarnAndAnAuditRow(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: codeTTLPath, Old: "5m0s", New: "8m0s", Actor: "op"})

	if got := r.logs.lines("WARN", "code-ttl", dirIncrease); len(got) == 0 {
		t.Errorf("no Warn for a relaxed code-ttl; the log was:\n%s", r.logs.String())
	}
	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("the audit holds %d rows, want 1", len(rows))
	}
	if rows[0].Action != relaxedAction || rows[0].Object != codeTTLPath || rows[0].Outcome != relaxedOutcome {
		t.Errorf("audit row = %+v, want the relaxed action on code-ttl", rows[0])
	}
	if rows[0].Detail["change"] != dirIncrease {
		t.Errorf("the audit detail = %v, want the direction", rows[0].Detail)
	}
}

// TestATighteningChangeWritesNothing checks that a tightening change writes nothing.
func TestATighteningChangeWritesNothing(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: codeTTLPath, Old: "8m0s", New: "5m0s"})
	r.deliverChange(settings.Change{Key: codeLengthPath, Old: "6", New: "8"})
	if len(r.audit.entries()) != 0 {
		t.Errorf("a tightening change wrote audit rows: %+v", r.audit.entries())
	}
}

// TestADecreaseThatLoosensIsReported checks that a decrease that loosens is reported.
func TestADecreaseThatLoosensIsReported(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: codeLengthPath, Old: "8", New: "6"})
	r.deliverChange(settings.Change{Key: resendIntervalPath, Old: "5m0s", New: "30s"})
	if len(r.audit.entries()) != 2 {
		t.Errorf("the audit holds %d rows, want 2 (the code length and the resend interval)", len(r.audit.entries()))
	}
}

// TestAWidenedAllowedTableIsReported checks that a widened allowed table is reported.
func TestAWidenedAllowedTableIsReported(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: allowedPrefixesPath, Old: `["+86"]`, New: `["+86","+1"]`})
	if len(r.audit.entries()) != 1 {
		t.Fatalf("the audit holds %d rows, want 1", len(r.audit.entries()))
	}
	r.deliverChange(settings.Change{Key: allowedPrefixesPath, Old: `["+86","+1"]`, New: `["+86"]`})
	if len(r.audit.entries()) != 1 {
		t.Errorf("removing a prefix was reported as a relaxation")
	}
}

// TestAWidenedSourceBudgetIsReported checks that a widened source budget is reported.
func TestAWidenedSourceBudgetIsReported(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: perSourceHourlyPath, Old: "30", New: "60"})
	r.deliverChange(settings.Change{Key: perSourceDailyPath, Old: "", New: "2000"})
	if got := len(r.audit.entries()); got != 2 {
		t.Fatalf("the audit holds %d rows, want 2: raising either source budget is a relaxation", got)
	}
	r.deliverChange(settings.Change{Key: perSourceHourlyPath, Old: "60", New: "30"})
	if got := len(r.audit.entries()); got != 2 {
		t.Errorf("lowering a source budget wrote an audit row")
	}
}

// TestARelaxedCeilingIsReported checks that a relaxed ceiling is reported.
func TestARelaxedCeilingIsReported(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: ceilingHourlyPath, Old: "", New: "10000"})
	if got := len(r.audit.entries()); got != 1 {
		t.Fatalf("the audit holds %d rows, want 1: raising the shared ceiling is a relaxation", got)
	}
	r.deliverChange(settings.Change{Key: ceilingHourlyPath, Old: "", New: "1000"})
	if got := len(r.audit.entries()); got != 1 {
		t.Errorf("a first write below the declared ceiling default was reported as a relaxation")
	}
}

// TestAnUnwatchedOrSensitiveChangeIsIgnored checks that an unwatched or withheld change is ignored.
func TestAnUnwatchedOrSensitiveChangeIsIgnored(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: "authn.mfa.max-per-kind", Old: "1", New: "20"})
	r.deliverChange(settings.Change{Key: codeTTLPath, Sensitive: true})
	if len(r.audit.entries()) != 0 {
		t.Errorf("an unwatched or sensitive change wrote audit rows: %+v", r.audit.entries())
	}
}

// TestASensitiveChangeThatWouldRelaxIsStillIgnored checks that a withheld change that would relax is still ignored.
func TestASensitiveChangeThatWouldRelaxIsStillIgnored(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: codeTTLPath, Old: "5m0s", New: "8m0s", Sensitive: true})
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a sensitive change wrote audit rows: %+v", rows)
	}
	if got := r.logs.lines("WARN", "code-ttl"); len(got) != 0 {
		t.Errorf("a sensitive change wrote a Warn:\n%s", r.logs.String())
	}
}

// TestAnEqualValuedChangeIsNotARelaxation checks that an equal-valued change is not a relaxation.
func TestAnEqualValuedChangeIsNotARelaxation(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: codeTTLPath, Old: "5m0s", New: "5m0s"})
	r.deliverChange(settings.Change{Key: codeLengthPath, Old: "6", New: "6"})
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a no-op change wrote audit rows: %+v", rows)
	}
}

// TestAnAuditWriteFailureIsCounted checks that an audit write failure is counted.
func TestAnAuditWriteFailureIsCounted(t *testing.T) {
	r := newRigSQLite(t)
	r.audit.err = errors.New("the audit is down")
	r.deliverChange(settings.Change{Key: codeTTLPath, Old: "5m0s", New: "8m0s"})
	if got := r.counter(auditFailuresName); got != 1 {
		t.Errorf("the audit-failure counter = %d, want 1", got)
	}
}
