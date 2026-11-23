package log_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/configtest"
	speedlog "github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/sms"
	smslog "github.com/vislake/speed/pkg/sms/log"
)

// capture is a logging capability that writes every record to a buffer.
type capture struct{ buf *bytes.Buffer }

func (c *capture) Named(name string) *slog.Logger {
	return slog.New(slog.NewTextHandler(c.buf, &slog.HandlerOptions{Level: slog.LevelDebug})).
		With("module", name)
}

func (c *capture) Redaction() speedlog.Redaction { return noRedaction{} }

// noRedaction accepts registrations and masks nothing; these cases hold the
// warning, not the redaction layer.
type noRedaction struct{}

func (noRedaction) AddKeys(...string)                   {}
func (noRedaction) AddPattern(string, speedlog.Matcher) {}

// loggerModule provides the logging capability over buf.
func loggerModule(buf *bytes.Buffer) core.Module {
	return core.Module{
		Name:     "test.logger",
		Provides: []core.Provision{{Token: (*speedlog.Logger)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return &capture{buf: buf}, nil
		},
	}
}

// absentCarrier is a non-fallback carrier whose section is wholly unset, so it
// stands itself down and leaves the log carrier as the only candidate.
func absentCarrier() core.Module {
	prototype := struct{}{}
	return sms.NewModule(sms.Spec[struct{}]{
		ModuleName:      "test.absent",
		CarrierName:     "absent",
		ConfigNamespace: "test.absent",
		Config:          &prototype,
		Stance: func(*struct{}) (core.Enablement, error) {
			return core.Enablement{State: core.StateDisabled, Reason: "the section is unset"}, nil
		},
		Open: func(context.Context, *struct{}) (sms.Product, error) {
			return nil, errors.New("an unconfigured carrier must not be opened")
		},
	})
}

// driveErr runs an assembly over body and answers the registry it built and the
// error the drive returned, so a case can hold a configuration the carrier
// refuses at startup.
func driveErr(t *testing.T, body string, buf *bytes.Buffer, participants ...string) (*core.Registry, error) {
	t.Helper()
	modules := []core.Module{smslog.Module(), loggerModule(buf), absentCarrier()}
	reader := configtest.Reader(t, body, modules...)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	for _, module := range modules {
		reg.Register(module)
	}
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: participants,
	})
	return reg, err
}

// drive runs an assembly over body and answers the registry it built.
func drive(t *testing.T, body string, buf *bytes.Buffer, participants ...string) *core.Registry {
	t.Helper()
	reg, err := driveErr(t, body, buf, participants...)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	return reg
}

// provider resolves the carrier this assembly mounted.
func provider(t *testing.T, reg *core.Registry) sms.Provider {
	t.Helper()
	mounted, err := core.Resolve[sms.Provider](reg)
	if err != nil {
		t.Fatalf("resolving the mounted carrier: %v", err)
	}
	return mounted
}

// TestTheLogCarrierIsTheFallbackAndWarns holds the fallback and the warning: with
// no other carrier enabled the log carrier runs, and it says the channel puts
// messages into the process log.
func TestTheLogCarrierIsTheFallbackAndWarns(t *testing.T) {
	var buf bytes.Buffer
	reg := drive(t, `{}`, &buf,
		smslog.ModuleName, "test.absent", "test.logger", config.ModuleName)

	stance, ok := reg.Enablement(smslog.ModuleName)
	if !ok || stance.State != core.StateEnabled {
		t.Fatalf("the log carrier = %+v (reported %t), want enabled as the fallback", stance, ok)
	}
	out := buf.String()
	for _, want := range []string{"WARN", "process log"} {
		if !strings.Contains(out, want) {
			t.Errorf("the startup log %q does not carry %q", out, want)
		}
	}
}

// TestTheFallbackCarrierWritesTheMessageToTheProcessLog holds the channel
// itself: a resolved message handed to the sender the fallback resolves to
// lands in the process log, number, template and verification code included.
// Without it the fallback would be a warning about a channel nothing proves
// carries anything.
func TestTheFallbackCarrierWritesTheMessageToTheProcessLog(t *testing.T) {
	var buf bytes.Buffer
	reg := drive(t, `{}`, &buf,
		smslog.ModuleName, "test.absent", "test.logger", config.ModuleName)

	sender, err := core.Resolve[sms.Sender](reg)
	if err != nil {
		t.Fatalf("resolving the fallback sender: %v", err)
	}
	receipt, err := sender.Send(t.Context(), sms.Resolved{
		To:       "+15551234567",
		Template: "signin",
		Locale:   "en-US",
		Kind:     sms.KindVerification,
		Params:   map[string]string{"code": "654321"},
		DedupeID: "d1",
	})
	if err != nil {
		t.Fatalf("Send through the fallback: %v", err)
	}
	if got, want := receipt.ProviderID, "log:signin:d1"; got != want {
		t.Errorf("the receipt names %q, want %q", got, want)
	}
	out := buf.String()
	for _, want := range []string{"+15551234567", "signin", "654321", "verification_code"} {
		if !strings.Contains(out, want) {
			t.Errorf("the process log %q does not carry %q", out, want)
		}
	}
}

// TestSelectionNoneLeavesNoCarrierAndNoWarning holds the explicit off value:
// none switches the capability off without falling back, and no carrier is
// constructed, so no warning claims a log channel is running.
func TestSelectionNoneLeavesNoCarrierAndNoWarning(t *testing.T) {
	var buf bytes.Buffer
	reg := drive(t, `{"sms":{"carrier":"none"}}`, &buf,
		smslog.ModuleName, "test.absent", "test.logger", config.ModuleName)

	stance, _ := reg.Enablement(smslog.ModuleName)
	if stance.State != core.StateDisabled {
		t.Errorf("the log carrier = %+v, want disabled under sms.carrier=none", stance)
	}
	if _, err := core.Resolve[sms.Sender](reg); !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("resolving a sender = %v, want the missing-provider sentinel", err)
	}
	if out := buf.String(); strings.Contains(out, "process log") {
		t.Errorf("the startup log %q warns about a log channel that is off", out)
	}
}

// TestAConfiguredVerdictAnswersForItsCode holds the item itself: each state the
// per-template review defines is accepted for its code, and the reason is
// reported with it.
func TestAConfiguredVerdictAnswersForItsCode(t *testing.T) {
	const body = `{"sms":{"log":{"approvals":{
		"log-signin":{"state":"approved","reason":"the vendor approved it"},
		"log-notice":{"state":"rejected","reason":"the vendor turned it down"},
		"log-reset":{"state":"withdrawn"},
		"log-enroll":{"state":"pending","reason":"the vendor has not answered"}
	}}}}`
	reg := drive(t, body, &bytes.Buffer{},
		smslog.ModuleName, "test.absent", "test.logger", config.ModuleName)
	mounted := provider(t, reg)

	cases := []struct {
		code string
		want sms.Approval
	}{
		{"log-signin", sms.Approval{State: sms.ApprovalApproved, Reason: "the vendor approved it"}},
		{"log-notice", sms.Approval{State: sms.ApprovalRejected, Reason: "the vendor turned it down"}},
		{"log-reset", sms.Approval{State: sms.ApprovalWithdrawn}},
		{"log-enroll", sms.Approval{State: sms.ApprovalPending, Reason: "the vendor has not answered"}},
	}
	for _, tc := range cases {
		got, err := mounted.Approval(t.Context(), tc.code)
		if err != nil {
			t.Fatalf("Approval(%q): %v", tc.code, err)
		}
		if got != tc.want {
			t.Errorf("Approval(%q) = %+v, want %+v", tc.code, got, tc.want)
		}
	}
}

// TestAConfiguredVerdictOutranksWhatTheProcessRecords holds the precedence:
// Offer records pending and Review records its own verdict, and neither moves a
// code the configuration pinned. Retire forgets only the in-memory record and
// leaves the pinned verdict answering.
func TestAConfiguredVerdictOutranksWhatTheProcessRecords(t *testing.T) {
	const body = `{"sms":{"log":{"approvals":{"log-notice":{"state":"approved","reason":"pinned"}}}}}`
	reg := drive(t, body, &bytes.Buffer{},
		smslog.ModuleName, "test.absent", "test.logger", config.ModuleName)
	mounted := provider(t, reg)

	code, err := mounted.Offer(t.Context(), sms.Submission{Name: "notice", Locale: "en-US"})
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if want := "log-notice"; code != want {
		t.Fatalf("Offer answered %q, want %q", code, want)
	}
	pinned := sms.Approval{State: sms.ApprovalApproved, Reason: "pinned"}
	got, err := mounted.Approval(t.Context(), code)
	if err != nil {
		t.Fatalf("Approval after Offer: %v", err)
	}
	if got != pinned {
		t.Errorf("Approval after Offer = %+v, want the pinned verdict %+v", got, pinned)
	}

	reviewer, ok := mounted.(smslog.Reviewer)
	if !ok {
		t.Fatalf("the mounted carrier is %T, not the log carrier's Reviewer", mounted)
	}
	if err = reviewer.Review(code, sms.ApprovalRejected, "the process said no"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	got, err = mounted.Approval(t.Context(), code)
	if err != nil {
		t.Fatalf("Approval after Review: %v", err)
	}
	if got != pinned {
		t.Errorf("Approval after Review = %+v, want the pinned verdict %+v", got, pinned)
	}

	if err = mounted.Retire(t.Context(), code); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	got, err = mounted.Approval(t.Context(), code)
	if err != nil {
		t.Fatalf("Approval after Retire: %v", err)
	}
	if got != pinned {
		t.Errorf("Approval after Retire = %+v, want the pinned verdict %+v", got, pinned)
	}
}

// TestAnUnconfiguredCodeAnswersPending holds the fallback answer: a code the
// configuration does not name is pending, whether it was never offered or the
// process recorded it a moment ago.
func TestAnUnconfiguredCodeAnswersPending(t *testing.T) {
	const body = `{"sms":{"log":{"approvals":{"log-signin":{"state":"approved"}}}}}`
	reg := drive(t, body, &bytes.Buffer{},
		smslog.ModuleName, "test.absent", "test.logger", config.ModuleName)
	mounted := provider(t, reg)

	want := sms.Approval{State: sms.ApprovalPending}
	got, err := mounted.Approval(t.Context(), "log-unknown")
	if err != nil {
		t.Fatalf("Approval of an unconfigured code: %v", err)
	}
	if got != want {
		t.Errorf("an unconfigured code answers %+v, want %+v", got, want)
	}

	code, err := mounted.Offer(t.Context(), sms.Submission{Name: "unknown", Locale: "en-US"})
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	got, err = mounted.Approval(t.Context(), code)
	if err != nil {
		t.Fatalf("Approval of an offered code: %v", err)
	}
	if got != want {
		t.Errorf("an offered, unconfigured code answers %+v, want %+v", got, want)
	}
}

// TestAnIllegalConfiguredStateFailsStartup holds the startup check: a state the
// per-template review does not define fails the assembly, and the error names
// the item and the code so the operator can find the entry. An unlisted state,
// the review-only "not-required", and an empty or missing state all fail: an
// entry that names no reviewable state must not start the carrier.
func TestAnIllegalConfiguredStateFailsStartup(t *testing.T) {
	cases := []struct {
		name string
		// entry is the configured verdict under the code log-signin.
		entry string
		// state is the value the failure must name, or "" when the entry names
		// no state at all.
		state string
	}{
		{"an unlisted state", `{"state":"approved-ish"}`, "approved-ish"},
		{"the review-only state", `{"state":"not-required"}`, "not-required"},
		{"an explicit empty state", `{"state":""}`, ""},
		{"no state at all", `{}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"sms":{"log":{"approvals":{"log-signin":` + tc.entry + `}}}}`
			_, err := driveErr(t, body, &bytes.Buffer{},
				smslog.ModuleName, "test.absent", "test.logger", config.ModuleName)
			if err == nil {
				t.Fatal("the assembly started with an illegal configured state, want a failure")
			}
			if !errors.Is(err, sms.ErrInvalidConfig) {
				t.Errorf("the failure = %v, want one wrapping sms.ErrInvalidConfig", err)
			}
			for _, want := range []string{"sms.log.approvals", "log-signin"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the failure %q does not name %q", err, want)
				}
			}
			want := `state is "` + tc.state + `"`
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the failure %q does not name the state as %q", err, want)
			}
		})
	}
}

// TestAConfiguredVerdictNeedsACode holds the other check the item makes: a
// verdict keyed by no code names nothing the carrier could ever answer.
func TestAConfiguredVerdictNeedsACode(t *testing.T) {
	_, err := driveErr(t, `{"sms":{"log":{"approvals":{"":{"state":"approved"}}}}}`,
		&bytes.Buffer{},
		smslog.ModuleName, "test.absent", "test.logger", config.ModuleName)
	if err == nil {
		t.Fatal("the assembly started with a verdict under an empty code, want a failure")
	}
	for _, want := range []string{"sms.log.approvals", "empty template code"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %q does not name %q", err, want)
		}
	}
}

// TestAnEmptyApprovalsMapKeepsTheOldAnswers holds the default: with no verdict
// configured, a code answers what the in-memory store holds and pending
// otherwise, exactly as before the item existed.
func TestAnEmptyApprovalsMapKeepsTheOldAnswers(t *testing.T) {
	for _, body := range []string{`{}`, `{"sms":{"log":{"approvals":{}}}}`} {
		reg := drive(t, body, &bytes.Buffer{},
			smslog.ModuleName, "test.absent", "test.logger", config.ModuleName)
		mounted := provider(t, reg)

		want := sms.Approval{State: sms.ApprovalPending}
		got, err := mounted.Approval(t.Context(), "log-anything")
		if err != nil {
			t.Fatalf("body %s: Approval: %v", body, err)
		}
		if got != want {
			t.Errorf("body %s: an unknown code answers %+v, want %+v", body, got, want)
		}

		code, err := mounted.Offer(t.Context(), sms.Submission{Name: "anything", Locale: "en-US"})
		if err != nil {
			t.Fatalf("body %s: Offer: %v", body, err)
		}
		got, err = mounted.Approval(t.Context(), code)
		if err != nil {
			t.Fatalf("body %s: Approval of an offered code: %v", body, err)
		}
		if got != want {
			t.Errorf("body %s: an offered code answers %+v, want %+v", body, got, want)
		}

		reviewer, ok := mounted.(smslog.Reviewer)
		if !ok {
			t.Fatalf("body %s: the mounted carrier is %T, not the log carrier's Reviewer", body, mounted)
		}
		if err = reviewer.Review(code, sms.ApprovalApproved, "reviewed by the control"); err != nil {
			t.Fatalf("body %s: Review: %v", body, err)
		}
		got, err = mounted.Approval(t.Context(), code)
		if err != nil {
			t.Fatalf("body %s: Approval after Review: %v", body, err)
		}
		if want := (sms.Approval{State: sms.ApprovalApproved, Reason: "reviewed by the control"}); got != want {
			t.Errorf("body %s: Review answers %+v, want %+v", body, got, want)
		}

		if err = mounted.Retire(t.Context(), code); err != nil {
			t.Fatalf("body %s: Retire: %v", body, err)
		}
		got, err = mounted.Approval(t.Context(), code)
		if err != nil {
			t.Fatalf("body %s: Approval after Retire: %v", body, err)
		}
		if got != (sms.Approval{State: sms.ApprovalPending}) {
			t.Errorf("body %s: a retired code answers %+v, want pending", body, got)
		}
	}
}
