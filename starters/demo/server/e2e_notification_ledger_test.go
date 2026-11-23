package main

// The delivery ledger's record of a locally refused SMS.
//
// The rollout journey's end-to-end spec asserts what the browser renders: the
// SMS page keeps showing the template disabled with its review states, and the
// process log carries no carrier line and no external-send record. A refusal
// that never reaches a carrier writes no log line, so the ledger's failed row —
// its status, its reason naming the template, and the absent carrier receipt —
// is observed here instead.
//
// The case drives the refusal through the product's own routes and the
// fixture's periodic dispatch: the platform operator registers a runtime
// template with the codes the catalog names and disables it, a member proves a
// phone number through the ordinary verification-code flow, and the next
// dispatch window attempts the sms channel and records the refusal.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core/coretest"

	_ "github.com/vislake/speed/pkg/db/sqlite" // registers the sqlite driver this case reads the ledger through
)

// The template this case registers. Its name is what the notification catalog
// names for the type's sms template, so the fixture's channel choice resolves it
// and the delivery pipeline attempts the sms channel.
const (
	e2eLedgerTemplate = demoNotificationSMSTemplate
	e2eLedgerENCode   = "demo-notice-en"
	e2eLedgerZHCode   = "demo-notice-zh"
)

// The phone number this case proves through the SMS login method. A first
// sign-in on an unheld number asks for the account's username and password
// while registration is open; the personal tenant its creation writes is the
// organization the worker dispatches to, and the proved phone is the sms
// destination.
const (
	e2eLedgerMemberPhone    = "+15550100001"
	e2eLedgerMemberUsername = "ledger-member"
	e2eLedgerMemberPassword = "ledger-member-secret"
)

// e2eLedgerRow is one row of notification_sends as this case reads it.
type e2eLedgerRow struct {
	RowID             int64
	Status            string
	Reason            sql.NullString
	ProviderReceiptID sql.NullString
}

// TestRefusedSMSTemplateIsRecordedFailedInTheLedger signs in as the seeded
// platform operator, registers the rollout template, disables it, and waits for
// the periodic dispatch to address a member whose phone number is proved. It
// then reads notification_sends directly: the sms row the dispatch produced is
// failed, its reason names the template, and it carries no carrier receipt,
// because the local gate stopped the message before any carrier was called.
func TestRefusedSMSTemplateIsRecordedFailedInTheLedger(t *testing.T) {
	t.Parallel()
	binary := e2eBinary(t)
	var configPath string
	serve, ports := startServing(t, func(ports []int) *process {
		configPath = e2eConfig(t,
			fmt.Sprintf("127.0.0.1:%d", ports[backendListener]))
		config := "file://" + configPath
		seedE2E(t, binary, config)
		return startE2E(t, binary, config)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	client := &http.Client{Timeout: probeTimeout}
	operator := signIn(t, base, e2eOperatorUsername, e2eOperatorPassword)
	auth := map[string]string{"Authorization": "Bearer " + operator}

	// The template the catalog names, registered with a vendor code per
	// language. It starts enabled, so the readiness check below is the case's
	// own: the disable route is what creates the refusal state.
	registered := send(t, client, http.MethodPost, base+"/api/v1/system/sms/templates", map[string]any{
		"name":   e2eLedgerTemplate,
		"kind":   "notification",
		"params": []string{"to"},
		"codes": map[string]string{
			e2eDefaultLocale: e2eLedgerENCode,
			e2eChineseLocale: e2eLedgerZHCode,
		},
	}, auth)
	if registered.status != http.StatusCreated {
		t.Fatalf("registering the template answered %d, want %d:\n%s",
			registered.status, http.StatusCreated, registered.body)
	}
	disabled := send(t, client, http.MethodPost,
		base+"/api/v1/system/sms/templates/"+e2eLedgerTemplate+"/enabled",
		map[string]any{"enabled": false}, auth)
	if disabled.status != http.StatusNoContent {
		t.Fatalf("disabling the template answered %d, want %d:\n%s",
			disabled.status, http.StatusNoContent, disabled.body)
	}

	// A member proves a phone number through the product's own code flow. The
	// fixture addresses a recipient by their proved phone, and the personal
	// tenant the registration wrote is the organization the worker dispatches
	// to.
	proofE2EPhone(t, base, serve)

	dsn := filepath.Join(filepath.Dir(configPath), "e2e.db")
	after := e2eLedgerMark(t, dsn)

	rows := waitForE2ELedgerRows(t, dsn, after)
	var failed *e2eLedgerRow
	for i := range rows {
		if rows[i].Status == "failed" {
			failed = &rows[i]
		}
	}
	if failed == nil {
		t.Fatalf("the ledger holds no failed sms row after the refused dispatch; rows: %+v", rows)
	}
	if !failed.Reason.Valid || !strings.Contains(failed.Reason.String, e2eLedgerTemplate) {
		t.Errorf("the failed row's reason is %+v, want it to name the template %q; the local gate's "+
			"refusal is the record this case reads", failed.Reason, e2eLedgerTemplate)
	}
	if failed.ProviderReceiptID.Valid {
		t.Errorf("the failed row carries provider receipt %q, want none: no carrier was called",
			failed.ProviderReceiptID.String)
	}
}

// proofE2EPhone signs a member in with the SMS method on a number no account
// holds, reading the code the log SMS carrier writes. With registration open the
// completion opens the account and writes its personal tenant, so the fixture
// has an addressable recipient whose proved phone is the sms destination.
func proofE2EPhone(t *testing.T, base string, serve *process) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building a cookie jar: %v", err)
	}
	flowClient := &http.Client{Timeout: probeTimeout, Jar: jar}
	start := send(t, flowClient, http.MethodPost, base+"/api/v1/auth/flows", map[string]any{
		"method":   "sms",
		"delivery": "token",
		"binding":  "cookie",
		"input":    map[string]string{"phone": e2eLedgerMemberPhone},
	}, nil)
	if start.status != http.StatusOK {
		t.Fatalf("starting the SMS login answered %d:\n%s", start.status, start.body)
	}
	flow := decodeE2EFlow(t, start.body)
	if flow.Next.Kind != "challenge" {
		t.Fatalf("the SMS method offered kind %q, want a code prompt:\n%s", flow.Next.Kind, start.body)
	}
	code := e2eSMSCode(t, serve)
	answered := send(t, flowClient, http.MethodPost, base+"/api/v1/auth/flows/answer", map[string]any{
		"seq":   flow.Seq,
		"step":  "answer",
		"input": map[string]string{"code": code},
	}, nil)
	if answered.status != http.StatusOK {
		t.Fatalf("answering the SMS code answered %d:\n%s", answered.status, answered.body)
	}
	flow = decodeE2EFlow(t, answered.body)
	if flow.State == "signup" {
		// A first sign-in on an unheld number asks for the account it opens.
		if flow.Next.Kind != "challenge" {
			t.Fatalf("the SMS signup offered kind %q, want the account fields:\n%s",
				flow.Next.Kind, answered.body)
		}
		signup := send(t, flowClient, http.MethodPost, base+"/api/v1/auth/flows/answer", map[string]any{
			"seq":  flow.Seq,
			"step": "answer",
			"input": map[string]string{
				"username":     e2eLedgerMemberUsername,
				"display_name": "Ledger Member",
				"password":     e2eLedgerMemberPassword,
			},
		}, nil)
		if signup.status != http.StatusOK {
			t.Fatalf("answering the SMS signup answered %d:\n%s", signup.status, signup.body)
		}
		flow = decodeE2EFlow(t, signup.body)
	}
	if flow.Next.Kind != "complete" {
		t.Fatalf("the SMS verification answered kind %q, want a complete step:\n%s",
			flow.Next.Kind, answered.body)
	}
	done := send(t, flowClient, http.MethodPost, base+"/api/v1/auth/flows/answer", map[string]any{
		"seq":  flow.Seq,
		"step": "complete",
	}, nil)
	if done.status != http.StatusOK {
		t.Fatalf("completing the SMS login answered %d:\n%s", done.status, done.body)
	}
}

// e2eSMSCode polls the child's output for the log SMS record and answers the
// verification code it carries. The value is read off the logfmt record's last
// verification_code attribute, and a notice line carries none.
func e2eSMSCode(t *testing.T, p *process) string {
	t.Helper()
	const (
		marker = "log sms: a verification code was sent"
		field  = "verification_code="
	)
	deadline := time.Now().Add(startLimit)
	for {
		for _, line := range strings.Split(p.output(), "\n") {
			if !strings.Contains(line, marker) {
				continue
			}
			at := strings.LastIndex(line, field)
			if at < 0 {
				continue
			}
			value, _, _ := strings.Cut(line[at+len(field):], " ")
			value = strings.Trim(value, `"`)
			if value != "" {
				return value
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no log SMS record carrying a verification code within %s:\n%s",
				startLimit, p.output())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// e2eLedgerMark is the newest rowid the sms channel holds before an action,
// or zero when it holds none.
func e2eLedgerMark(t *testing.T, dsn string) int64 {
	t.Helper()
	rows, err := readE2ELedger(dsn, 0)
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

// waitForE2ELedgerRows polls the ledger until the dispatch's sms row lands.
// The delivery pipeline writes the channel's row after the job queue hands the
// dispatch over, and the fixture dispatches on its own window, so the wait
// covers several windows rather than a fixed pause.
func waitForE2ELedgerRows(t *testing.T, dsn string, after int64) []e2eLedgerRow {
	t.Helper()
	deadline := time.Now().Add(coretest.Scale(45 * time.Second))
	var lastErr error
	for {
		rows, err := readE2ELedger(dsn, after)
		if err == nil {
			if len(rows) > 0 {
				return rows
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ledger held no sms row for %q after rowid %d within the bound"+
				"(last error: %v)", demoNotificationTypeKey, after, lastErr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// readE2ELedger reads the sms rows of the demo type after a cursor. The
// connection is read-only and the statement names the table and its columns
// directly: notification_sends is notification.delivery's own table, and this
// case is the one place a test asserts on it.
func readE2ELedger(dsn string, after int64) ([]e2eLedgerRow, error) {
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
		demoNotificationTypeKey, "sms", after)
	if err != nil {
		return nil, fmt.Errorf("querying the ledger: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []e2eLedgerRow
	for rows.Next() {
		var row e2eLedgerRow
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
