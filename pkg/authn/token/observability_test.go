package token

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// tokenMeter registers the operations counter on a manual reader and hands it to
// the product, so a case reads back what recordOperation reported.
func (r *rig) tokenMeter() *sdkmetric.ManualReader {
	r.t.Helper()
	collected := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(collected))
	r.t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			r.t.Errorf("shutting the meter provider down: %v", err)
		}
	})
	counter, err := provider.Meter(meterName).Int64Counter(operationsInstrument, metric.WithUnit("{operation}"))
	if err != nil {
		r.t.Fatalf("registering the operations counter: %v", err)
	}
	failures, err := provider.Meter(meterName).Int64Counter(auditFailuresInstrument, metric.WithUnit("{failure}"))
	if err != nil {
		r.t.Fatalf("registering the audit-failures counter: %v", err)
	}
	r.p.meters = meters{operations: counter, auditFailures: failures}
	return collected
}

// operationPoints reads the operations counter back as the counts keyed by the
// operation and outcome attributes, with the instrument's name and unit.
func operationPoints(t *testing.T, rm metricdata.ResourceMetrics) (map[[2]string]int64, string, string, string) {
	t.Helper()
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != operationsInstrument {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				return nil, "", "", ""
			}
			out := make(map[[2]string]int64, len(sum.DataPoints))
			for _, point := range sum.DataPoints {
				op, _ := point.Attributes.Value(attribute.Key(operationAttribute))
				outcome, _ := point.Attributes.Value(attribute.Key(outcomeAttribute))
				out[[2]string{op.AsString(), outcome.AsString()}] = point.Value
			}
			return out, scope.Scope.Name, m.Name, m.Unit
		}
	}
	return nil, "", "", ""
}

// TestTheOperationsCounterCarriesTheClosedAttributeSet holds the metric the
// design fixes: every operation and outcome pair is reportable, on the named
// instrument and unit, and the values are the closed sets a dashboard reads.
func TestTheOperationsCounterCarriesTheClosedAttributeSet(t *testing.T) {
	r := newRig(t)
	collected := r.tokenMeter()

	operations := []string{operationIssued, operationRefreshed, operationRevoked, operationClientCredentials}
	outcomes := []string{outcomeOK, outcomeInvalid, outcomeReuseDetected, outcomeUnavailable}
	ctx := context.Background()
	for _, op := range operations {
		for _, outcome := range outcomes {
			r.p.recordOperation(ctx, op, outcome)
		}
	}

	var rm metricdata.ResourceMetrics
	if err := collected.Collect(ctx, &rm); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	points, meter, name, unit := operationPoints(t, rm)
	if points == nil {
		t.Fatalf("no %q instrument was collected", operationsInstrument)
	}
	if name != operationsInstrument || unit != "{operation}" || meter != meterName {
		t.Errorf("instrument %q on meter %q with unit %q, want %q on %q with unit {operation}",
			name, meter, unit, operationsInstrument, meterName)
	}
	if len(points) != len(operations)*len(outcomes) {
		t.Errorf("the counter reported %d points, want %d", len(points), len(operations)*len(outcomes))
	}
	for _, op := range operations {
		for _, outcome := range outcomes {
			if got := points[[2]string{op, outcome}]; got != 1 {
				t.Errorf("operation %q outcome %q counted %d, want 1", op, outcome, got)
			}
		}
	}
}

// TestADetectedReuseWritesOneEventAndOneAudit holds the reuse signal: the whole
// family is revoked, one authn.token.reuse-detected event goes out with the
// session and account, and one authn.token.refresh-reuse audit row is written.
func TestADetectedReuseWritesOneEventAndOneAudit(t *testing.T) {
	r := newRig(t)
	id, first := r.issueRefresh()
	ctx := context.Background()

	if _, _, err := r.p.Refresh(ctx, first.Value); err != nil {
		t.Fatalf("the first rotation failed: %v", err)
	}
	row := r.rowByDigest(first.Value)
	if err := r.p.handle.Exec("UPDATE "+tableName+" SET used_at = ? WHERE id = ?",
		epoch.Add(-time.Hour), row.ID).Error; err != nil {
		t.Fatalf("rewriting used_at: %v", err)
	}
	if _, _, err := r.p.Refresh(ctx, first.Value); err == nil {
		t.Fatal("a presentation past the grace did not fail")
	}

	if len(r.bus.events) != 1 {
		t.Fatalf("published %d events, want one", len(r.bus.events))
	}
	e := r.bus.events[0]
	if e.Type != authn.EventTokenReuseDetected {
		t.Errorf("event type %q, want %q", e.Type, authn.EventTokenReuseDetected)
	}
	var payload authn.TokenReuseDetected
	if err := event.DecodePayload(e.Payload, &payload); err != nil {
		t.Fatalf("decoding the payload: %v", err)
	}
	if payload.SessionID != string(id) || payload.AccountID != string(account) {
		t.Errorf("the payload is %+v, want session %s and account %s", payload, id, account)
	}

	if len(r.audit.entries) != 1 {
		t.Fatalf("wrote %d audit rows, want one", len(r.audit.entries))
	}
	entry := r.audit.entries[0]
	if entry.Action != actionRefreshReuse || entry.Outcome != outcomeDetected || entry.Object != string(id) {
		t.Errorf("the audit row is %+v, want %s on %s with outcome %s", entry, actionRefreshReuse, id, outcomeDetected)
	}
}

// TestTheStartupDiagnosticNamesTheRelaxationSwitch holds the startup line the
// design fixes: when check-session is off the module writes one Warn naming the
// setting, prefixed so the line is recognisable among the diagnostics' other
// writers.
func TestTheStartupDiagnosticNamesTheRelaxationSwitch(t *testing.T) {
	r := newRig(t)
	r.p.cfg.checkSession = false
	r.p.diagnose()

	logs := r.logs.String()
	if !strings.Contains(logs, "level=WARN") {
		t.Errorf("no Warn line was written:\n%s", logs)
	}
	if !strings.Contains(logs, diagnosticPrefix) {
		t.Errorf("the diagnostic line does not start with %q:\n%s", diagnosticPrefix, logs)
	}
	if !strings.Contains(logs, ConfigNamespace+"."+checkSessionItem) {
		t.Errorf("the Warn line does not name the setting %s.%s:\n%s",
			ConfigNamespace, checkSessionItem, logs)
	}
}

// TestNoTokenReachesTheLogsMetricsOrErrors holds the discipline the design puts
// on the whole surface: an access token, a refresh token and a service secret
// never appear in a log line, a metric attribute or an error body.
func TestNoTokenReachesTheLogsMetricsOrErrors(t *testing.T) {
	r := newRig(t)
	collected := r.tokenMeter()
	m := r.mountToken()
	r.serviceSession()
	r.passwords.verify = func(string, string) (authn.AccountID, string, time.Time, error) {
		return account, "secret-1", time.Time{}, nil
	}

	const secret = "spd_cs_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopq"
	_, refresh := r.issueRefresh()

	// A successful client credential: its access token must not leak.
	rec := form(m, m.BaseIn("")+tokenPath, url.Values{
		"grant_type":    {grantClientCredentials},
		"client_id":     {string(account)},
		"client_secret": {secret},
	})
	access := decodeToken(t, rec).AccessToken

	// A rotation and a revocation each write a line.
	form(m, m.BaseIn("")+tokenPath, url.Values{"grant_type": {grantRefreshToken}, "refresh_token": {refresh.Value}})
	form(m, m.BaseIn("")+revokePath, url.Values{"token": {refresh.Value}})

	// A rejected credential: the secret no longer verifies, so this request is
	// the 401 whose body and log line must carry none of the three secrets.
	r.passwords.verify = func(string, string) (authn.AccountID, string, time.Time, error) {
		return "", "", time.Time{}, authn.ErrInvalidCredentials
	}
	bad := form(m, m.BaseIn("")+tokenPath, url.Values{
		"grant_type":    {grantClientCredentials},
		"client_id":     {string(account)},
		"client_secret": {secret},
	})
	if bad.Code != 401 {
		t.Fatalf("the rejected credential was answered %d, want 401", bad.Code)
	}

	secrets := []string{access, refresh.Value, secret}
	logs := r.logs.String()
	for _, s := range secrets {
		if s != "" && strings.Contains(logs, s) {
			t.Errorf("a credential %q reached the log:\n%s", s, logs)
		}
	}

	var rm metricdata.ResourceMetrics
	if err := collected.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			data, err := json.Marshal(metric)
			if err != nil {
				t.Fatalf("marshalling a metric: %v", err)
			}
			for _, s := range secrets {
				if s != "" && strings.Contains(string(data), s) {
					t.Errorf("a credential %q reached the metric %s", s, metric.Name)
				}
			}
		}
	}

	for _, s := range secrets {
		if s != "" && strings.Contains(bad.Body.String(), s) {
			t.Errorf("a credential %q reached the error body: %s", s, bad.Body.String())
		}
	}
}

// collectedPoints reads the operations counter back as the (operation, outcome)
// counts.
func collectedPoints(t *testing.T, collected *sdkmetric.ManualReader) map[[2]string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := collected.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	points, _, _, _ := operationPoints(t, rm)
	return points
}

// TestTheOperationsCounterRecordsEachRealPath drives one real path per case and
// holds the exact (operation, outcome) pair it recorded, so the counter a
// dashboard reads is the one the code actually emits, not only what
// recordOperation would write if called directly.
func TestTheOperationsCounterRecordsEachRealPath(t *testing.T) {
	pair := func(op, outcome string) [2]string { return [2]string{op, outcome} }
	cases := []struct {
		name  string
		drive func(t *testing.T, r *rig)
		want  [2]string
	}{
		{"issue success", func(t *testing.T, r *rig) {
			if _, err := r.p.Issue(context.Background(), normalIssue("sess-1")); err != nil {
				t.Fatalf("Issue: %v", err)
			}
		}, pair(operationIssued, outcomeOK)},
		{"issue with no signing key", func(t *testing.T, r *rig) {
			r.p.keys.signing = nil
			if _, err := r.p.Issue(context.Background(), normalIssue("sess-1")); !errors.Is(err, authn.ErrNoSigningKey) {
				t.Fatalf("Issue = %v, want ErrNoSigningKey", err)
			}
		}, pair(operationIssued, outcomeUnavailable)},
		{"issue with an invalid lifetime", func(t *testing.T, r *rig) {
			in := normalIssue("sess-1")
			in.TTL = 2 * time.Hour
			if _, err := r.p.Issue(context.Background(), in); err == nil {
				t.Fatal("a two-hour token was issued")
			}
		}, pair(operationIssued, outcomeInvalid)},
		{"refresh of an unknown token", func(t *testing.T, r *rig) {
			if _, _, err := r.p.Refresh(context.Background(), "spd_rt_"+strings.Repeat("A", 43)); !errors.Is(err, authn.ErrInvalidGrant) {
				t.Fatalf("Refresh = %v, want ErrInvalidGrant", err)
			}
		}, pair(operationRefreshed, outcomeInvalid)},
		{"a detected reuse", func(t *testing.T, r *rig) {
			_, first := r.issueRefresh()
			if err := r.p.handle.Exec("UPDATE "+tableName+" SET used_at = ? WHERE token_hash = ?",
				epoch.Add(-time.Hour), digestRefreshToken(first.Value)).Error; err != nil {
				t.Fatalf("marking the row used: %v", err)
			}
			if _, _, err := r.p.Refresh(context.Background(), first.Value); !errors.Is(err, authn.ErrRefreshReuse) {
				t.Fatalf("Refresh = %v, want ErrRefreshReuse", err)
			}
		}, pair(operationRefreshed, outcomeReuseDetected)},
		{"revoking a refresh token", func(t *testing.T, r *rig) {
			_, refresh := r.issueRefresh()
			if err := r.p.Revoke(context.Background(), refresh.Value); err != nil {
				t.Fatalf("Revoke: %v", err)
			}
		}, pair(operationRevoked, outcomeOK)},
		{"an issued client credential", func(t *testing.T, r *rig) {
			r.serviceSession()
			r.passwords.verify = func(string, string) (authn.AccountID, string, time.Time, error) {
				return account, "secret-1", time.Time{}, nil
			}
			if _, err := r.p.clientCredentials(context.Background(), string(account), "spd_cs_x"); err != nil {
				t.Fatalf("clientCredentials: %v", err)
			}
		}, pair(operationClientCredentials, outcomeOK)},
		{"a rejected client credential", func(t *testing.T, r *rig) {
			r.passwords.verify = func(string, string) (authn.AccountID, string, time.Time, error) {
				return "", "", time.Time{}, authn.ErrInvalidCredentials
			}
			if _, err := r.p.clientCredentials(context.Background(), "client-x", "spd_cs_x"); !errors.Is(err, authn.ErrInvalidCredentials) {
				t.Fatalf("clientCredentials = %v, want ErrInvalidCredentials", err)
			}
		}, pair(operationClientCredentials, outcomeInvalid)},
		{"a client in back-off", func(t *testing.T, r *rig) {
			r.throttle.retry = 30 * time.Second
			if _, err := r.p.clientCredentials(context.Background(), "client-x", "spd_cs_x"); !errors.Is(err, authn.ErrThrottled) {
				t.Fatalf("clientCredentials = %v, want ErrThrottled", err)
			}
		}, pair(operationClientCredentials, outcomeInvalid)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			collected := r.tokenMeter()
			c.drive(t, r)
			points := collectedPoints(t, collected)
			if len(points) != 1 {
				t.Fatalf("the counter reported %v, want exactly one (operation, outcome) pair", points)
			}
			if points[c.want] != 1 {
				t.Errorf("the counter reported %v, want %v once", points, c.want)
			}
		})
	}
}

// auditFailurePoints reads the audit-write-failures counter back as the counts
// keyed by the mandatory attribute.
func auditFailurePoints(t *testing.T, rm metricdata.ResourceMetrics) map[bool]int64 {
	t.Helper()
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != auditFailuresInstrument {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			out := map[bool]int64{}
			for _, p := range sum.DataPoints {
				v, _ := p.Attributes.Value(attribute.Key(mandatoryAttribute))
				out[v.AsBool()] += p.Value
			}
			return out
		}
	}
	return nil
}

// TestADetectedReuseReportsAnAuditWriteFailure holds the design's requirement
// that a module whose best-effort audit write fails reports it through
// authn.audit.write-failures: a detected reuse whose audit row cannot be written
// records one point with mandatory=false.
func TestADetectedReuseReportsAnAuditWriteFailure(t *testing.T) {
	r := newRig(t)
	collected := r.tokenMeter()
	r.audit.err = errors.New("token test: the audit store is down")

	_, first := r.issueRefresh()
	if err := r.p.handle.Exec("UPDATE "+tableName+" SET used_at = ? WHERE token_hash = ?",
		epoch.Add(-time.Hour), digestRefreshToken(first.Value)).Error; err != nil {
		t.Fatalf("marking the row used: %v", err)
	}
	if _, _, err := r.p.Refresh(context.Background(), first.Value); !errors.Is(err, authn.ErrRefreshReuse) {
		t.Fatalf("Refresh = %v, want ErrRefreshReuse", err)
	}

	var rm metricdata.ResourceMetrics
	if err := collected.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	got := auditFailurePoints(t, rm)
	if got[false] != 1 {
		t.Errorf("the audit-failure counter reported %v, want one point with mandatory=false", got)
	}
	if got[true] != 0 {
		t.Errorf("the audit-failure counter reported a mandatory point: %v", got)
	}
}
