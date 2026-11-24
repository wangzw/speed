package impersonation

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheWireNamesAreTheDeclaredOnes checks the metric, scope, attribute, audit action, outcome and detail key constants against independent literals.
func TestTheWireNamesAreTheDeclaredOnes(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		if _, err := r.p.Start(context.Background(), adminIdentity(t), request()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		rows := r.audit.entries()
		if len(rows) == 0 || rows[0].Action != "authn.impersonation.start" || rows[0].Outcome != "granted" {
			t.Errorf("the granted audit row is %+v, want the action authn.impersonation.start and the outcome granted", rows)
		}
		if _, ok := rows[0].Detail["tenant"]; !ok {
			t.Errorf("the audit detail %v does not carry the key tenant", rows[0].Detail)
		}

		var rm metricdata.ResourceMetrics
		if err := r.metrics.Collect(context.Background(), &rm); err != nil {
			t.Fatalf("collecting the metrics: %v", err)
		}
		found := false
		for _, sm := range rm.ScopeMetrics {
			if sm.Scope.Name != "authn" {
				continue
			}
			for _, m := range sm.Metrics {
				if m.Name != "authn.impersonation.sessions" {
					continue
				}
				found = true
				sum, ok := m.Data.(metricdata.Sum[int64])
				if !ok {
					t.Fatalf("the sessions metric is not an int64 sum")
				}
				hit := false
				for _, dp := range sum.DataPoints {
					if v, ok := dp.Attributes.Value(attribute.Key("outcome")); ok && v.AsString() == "started" {
						hit = true
					}
				}
				if !hit {
					t.Errorf("no data point carries the attribute outcome=started: %+v", sum.DataPoints)
				}
			}
		}
		if !found {
			t.Error("no metric named authn.impersonation.sessions was collected under the scope authn")
		}
	})
}

// TestARefusalWithNoInstrumentDoesNotPanic checks that counting an outcome with a nil instrument records nothing and does not panic.
func TestARefusalWithNoInstrumentDoesNotPanic(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.p.meters = &meters{}
		r.accounts.put(actorID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
		if _, err := r.p.Start(context.Background(), adminIdentity(t), request()); !errors.Is(err, authn.ErrImpersonationRestricted) {
			t.Fatalf("Start = %v, want the refusal", err)
		}
	})
}
