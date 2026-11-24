package sms

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
)

func (r *rig) metricLabels(t *testing.T) []string {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting the metrics: %v", err)
	}
	var out []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				for _, kv := range dp.Attributes.ToSlice() {
					out = append(out, string(kv.Key)+"="+kv.Value.AsString())
				}
			}
		}
	}
	return out
}

// TestTheCodeAndTheRootKeyNeverReachTheLog checks that the code and the root key never reach the log.
func TestTheCodeAndTheRootKeyNeverReachTheLog(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msg := <-r.p.pool.queue
	code := msg.Params["code"]
	if code == "" {
		t.Fatal("the queued message carries no code")
	}

	r.sender.setResult(sms.ErrRejected)
	r.p.deliverOne(context.Background(), msg)

	logs := r.logs.String()
	if logs == "" {
		t.Fatal("no line was written; the case cannot show anything is kept out")
	}
	if strings.Contains(logs, code) {
		t.Errorf("the code %q appears in the log:\n%s", code, logs)
	}
	if strings.Contains(logs, rootKey) {
		t.Errorf("the root key appears in the log:\n%s", logs)
	}
	if strings.Contains(logs, phone) {
		t.Errorf("the phone number appears in the log:\n%s", logs)
	}
}

// TestNoMetricAttributeCarriesASecret checks that no metric attribute carries a secret.
func TestNoMetricAttributeCarriesASecret(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msg := <-r.p.pool.queue
	code := msg.Params["code"]
	r.sender.setResult(sms.ErrRejected)
	r.p.deliverOne(context.Background(), msg)
	r.bus.deliver(context.Background(), event.Event{
		Type:    settings.ChangedEventType,
		Payload: settings.Change{Key: allowedPrefixesPath, New: `["+86"]`},
	})

	labels := r.metricLabels(t)
	if len(labels) == 0 {
		t.Fatal("no metric data point was recorded; the case cannot show anything is kept out")
	}
	for _, label := range labels {
		key, value, _ := strings.Cut(label, "=")
		switch key {
		case outcomeAttribute, mandatoryAttribute:
		default:
			t.Errorf("a metric carries an attribute %q outside the closed set", key)
		}
		if strings.Contains(value, code) || strings.Contains(value, rootKey) || strings.Contains(value, phone) {
			t.Errorf("a metric attribute carries a secret: %s", label)
		}
	}
}

// TestTheMetricNamesAndAttributesAreTheDesignsLiterals checks that the metric names and attributes are the design's literals.
func TestTheMetricNamesAndAttributesAreTheDesignsLiterals(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.sender.setResult(nil)
	r.p.deliverOne(context.Background(), sms.Resolved{To: phone})
	r.audit.err = errors.New("the audit is down")
	r.deliverChange(settings.Change{Key: codeTTLPath, Old: "5m0s", New: "8m0s"})

	var rm metricdata.ResourceMetrics
	if err := r.metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting the metrics: %v", err)
	}
	names := map[string]bool{}
	sent := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || m.Name != "authn.sms.sends" {
				continue
			}
			for _, dp := range sum.DataPoints {
				if v, ok := dp.Attributes.Value("outcome"); ok && v.AsString() == "sent" {
					sent = true
				}
			}
		}
	}
	if !names["authn.sms.sends"] {
		t.Error("no instrument is named the literal authn.sms.sends")
	}
	if !names["authn.audit.write-failures"] {
		t.Error("no instrument is named the literal authn.audit.write-failures")
	}
	if !sent {
		t.Error("no data point of authn.sms.sends carried the attribute outcome=sent")
	}
}

// TestTheRefusalsEchoNoSecret checks that refusals echo no secret.
func TestTheRefusalsEchoNoSecret(t *testing.T) {
	r := newRigSQLite(t)
	r.store.set(allowedPrefixesPath, `["+1"]`)
	_, err := r.p.Send(context.Background(), authn.SendCode{Purpose: authn.SMSPurposeLogin, Phone: phone, Deliver: true})
	if !errors.Is(err, authn.ErrUnsupportedRegion) {
		t.Fatalf("Send returned %v, want ErrUnsupportedRegion", err)
	}
	text := err.Error()
	for _, secret := range []string{phone, rootKey} {
		if strings.Contains(text, secret) {
			t.Errorf("the refusal echoes %q: %s", secret, text)
		}
	}
}
