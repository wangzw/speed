//go:build review

package main

// The delivery ledger's record of a locally refused SMS.
//
// The rollout journey's end-to-end spec asserts what the browser renders: the
// SMS page keeps showing the template disabled with its review states, and the
// process log carries no carrier line and no external-send record. A refusal
// that never reaches a carrier writes no log line, so the ledger's failed row —
// its status, its reason naming the template, and the absent carrier receipt —
// is observed here instead. This is a review-tagged case because each of these
// two control routes is a test control compiled only into the review assembly;
// see review_sms_control.go and review_notification.go.
//
// The case drives the refusal through the product's own routes: the seeded
// platform operator registers a runtime template with the codes the review
// catalog names, disables it, and asks the review notification control to
// dispatch one notice to themselves. The scheduler's periodic trigger is off in
// this case's configuration, so the only SMS attempt the ledger can hold is the
// one the control made.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/vislake/speed/pkg/db/sqlite" // registers the sqlite driver this case reads the ledger through
)

// The template this case registers. Its name is what the review notification
// catalog names for the type's sms template, so the fixture's channel choice
// resolves it and the delivery pipeline attempts the sms channel.
const (
	reviewLedgerTemplate = reviewNotificationSMSTemplate
	reviewLedgerENCode   = "review-notice-en"
	reviewLedgerZHCode   = "review-notice-zh"
)

// reviewLedgerRow is one row of notification_sends as this case reads it.
type reviewLedgerRow struct {
	RowID             int64
	Status            string
	Reason            sql.NullString
	ProviderReceiptID sql.NullString
}

// TestReviewRefusedSMSTemplateIsRecordedFailedInTheLedger signs in as the
// seeded platform operator, registers the rollout template, disables it, and
// dispatches a notice through the review-only control. It then reads
// notification_sends directly: the sms row the dispatch produced is failed, its
// reason names the template, and it carries no carrier receipt, because the
// local gate stopped the message before any carrier was called.
func TestReviewRefusedSMSTemplateIsRecordedFailedInTheLedger(t *testing.T) {
	t.Parallel()
	binary := reviewBinary(t)
	var secret, configPath string
	_, ports := startServing(t, func(ports []int) *process {
		configPath = reviewConfigWithoutScheduler(t,
			fmt.Sprintf("127.0.0.1:%d", ports[backendListener]))
		config := "file://" + configPath
		seedReview(t, binary, config)
		secret = reviewSeededTOTPSecret(t, configPath)
		return startReview(t, binary, config)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	client := &http.Client{Timeout: probeTimeout}
	token := signInWithTOTP(t, base, reviewOperatorUsername, reviewOperatorPassword, secret)
	auth := map[string]string{"Authorization": "Bearer " + token}

	// The template the review catalog names, registered with a vendor code per
	// language. It starts enabled, so the readiness check below is the case's
	// own: the disable route is what creates the refusal state.
	registered := send(t, client, http.MethodPost, base+"/api/v1/system/sms/templates", map[string]any{
		"name":   reviewLedgerTemplate,
		"kind":   "notification",
		"params": []string{"to"},
		"codes": map[string]string{
			reviewDefaultLocale: reviewLedgerENCode,
			reviewChineseLocale: reviewLedgerZHCode,
		},
	}, auth)
	if registered.status != http.StatusCreated {
		t.Fatalf("registering the template answered %d, want %d:\n%s",
			registered.status, http.StatusCreated, registered.body)
	}
	disabled := send(t, client, http.MethodPost,
		base+"/api/v1/system/sms/templates/"+reviewLedgerTemplate+"/enabled",
		map[string]any{"enabled": false}, auth)
	if disabled.status != http.StatusNoContent {
		t.Fatalf("disabling the template answered %d, want %d:\n%s",
			disabled.status, http.StatusNoContent, disabled.body)
	}

	// The operator is a member of the personal tenant the review build creates
	// them, and the control reads the tenant from the request context.
	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      personalTenantName(t, client, base, token),
	}

	dsn := filepath.Join(filepath.Dir(configPath), "review.db")
	after := reviewLedgerMark(t, dsn)

	dispatched := send(t, client, http.MethodPost, base+reviewNotificationControlPath, nil, headers)
	if dispatched.status != http.StatusNoContent {
		t.Fatalf("POST %s answered %d, want %d:\n%s",
			reviewNotificationControlPath, dispatched.status, http.StatusNoContent, dispatched.body)
	}

	rows := waitForReviewLedgerRows(t, dsn, after)
	var failed *reviewLedgerRow
	for i := range rows {
		if rows[i].Status == "failed" {
			failed = &rows[i]
		}
	}
	if failed == nil {
		t.Fatalf("the ledger holds no failed sms row after the refused dispatch; rows: %+v", rows)
	}
	if !failed.Reason.Valid || !strings.Contains(failed.Reason.String, reviewLedgerTemplate) {
		t.Errorf("the failed row's reason is %+v, want it to name the template %q; the local gate's "+
			"refusal is the record this case reads", failed.Reason, reviewLedgerTemplate)
	}
	if failed.ProviderReceiptID.Valid {
		t.Errorf("the failed row carries provider receipt %q, want none: no carrier was called",
			failed.ProviderReceiptID.String)
	}
}

// reviewConfigWithoutScheduler writes the review configuration and turns the
// scheduler's periodic trigger off, so the only dispatch this case observes is
// the review control's own.
func reviewConfigWithoutScheduler(t *testing.T, backendAddress string) string {
	t.Helper()
	path := reviewConfig(t, backendAddress)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the written review configuration: %v", err)
	}
	body := strings.Replace(string(raw), "tick: 2s", "tick: 0s", 1)
	if body == string(raw) {
		t.Fatalf("the written review configuration carries no scheduler tick to turn off")
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("turning the scheduler off: %v", err)
	}
	return path
}

// reviewLedgerMark is the newest rowid the sms channel holds before an action,
// or zero when it holds none.
func reviewLedgerMark(t *testing.T, dsn string) int64 {
	t.Helper()
	rows, err := readReviewLedger(dsn, 0)
	if err != nil {
		t.Fatalf("reading the ledger for its mark: %v", err)
	}
	var newest int64
	for _, row := range rows {
		if row.RowID > newest {
			newest = row.RowID
		}
	}
	return newest
}

// waitForReviewLedgerRows polls the ledger until the dispatch's sms row lands.
// The delivery pipeline writes the channel's row after the job queue hands the
// dispatch over, so the wait is the queue's window rather than a fixed pause.
func waitForReviewLedgerRows(t *testing.T, dsn string, after int64) []reviewLedgerRow {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for {
		rows, err := readReviewLedger(dsn, after)
		if err == nil {
			if len(rows) > 0 {
				return rows
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ledger held no sms row for %q after rowid %d within the bound"+
				"(last error: %v)", reviewNotificationTypeKey, after, lastErr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// readReviewLedger reads the sms rows of the review type after a cursor. The
// connection is read-only and the statement names the table and its columns
// directly: notification_sends is notification.delivery's own table, and this
// review-tagged case is the one place a test asserts on it.
func readReviewLedger(dsn string, after int64) ([]reviewLedgerRow, error) {
	database, err := sql.Open("sqlite", "file:"+dsn+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("opening the ledger: %w", err)
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := database.QueryContext(ctx,
		`SELECT rowid, status, error, provider_receipt_id FROM notification_sends `+
			`WHERE type_key = ? AND channel = ? AND rowid > ? ORDER BY rowid ASC`,
		reviewNotificationTypeKey, "sms", after)
	if err != nil {
		return nil, fmt.Errorf("querying the ledger: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []reviewLedgerRow
	for rows.Next() {
		var row reviewLedgerRow
		if err := rows.Scan(&row.RowID, &row.Status, &row.Reason, &row.ProviderReceiptID); err != nil {
			return nil, fmt.Errorf("reading a ledger row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the ledger: %w", err)
	}
	return out, nil
}
