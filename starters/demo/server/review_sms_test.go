//go:build review

package main

// The review assembly's SMS carrier, tested where it is compiled. A normal
// deployment's carriers answer a per-template review they do not have; this one
// answers a real per-language review, so the template registry starts a runtime
// template pending in every language and a language's verdict can change on its
// own.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/sms"
	smsregistry "github.com/vislake/speed/pkg/sms/registry"
)

// TestReviewSMSDeclaresPerTemplateReviewAndStartsPending holds the carrier's
// declaration and its initial verdict: the vendor reviews each template, and a
// code the carrier has not been told about answers pending — the state the
// registry records for a language a runtime template was registered in.
func TestReviewSMSDeclaresPerTemplateReviewAndStartsPending(t *testing.T) {
	carrier := &reviewSender{}
	if !carrier.Model().Review.PerTemplate {
		t.Fatalf("the review carrier declares PerTemplate %t, want true: the template registry "+
			"would otherwise register every language as not-required",
			carrier.Model().Review.PerTemplate)
	}
	for _, code := range []string{"rollout-en-US", "rollout-zh-CN"} {
		got, err := carrier.Approval(t.Context(), code)
		if err != nil {
			t.Fatalf("Approval(%q): %v", code, err)
		}
		if got.State != sms.ApprovalPending {
			t.Errorf("Approval(%q).State = %q, want %q", code, got.State, sms.ApprovalPending)
		}
	}
}

// TestReviewSMSControlMovesOneVerdict holds the review-only control: a POST
// moves the named code and leaves a sibling code where it was, and a state the
// carrier does not review is refused.
func TestReviewSMSControlMovesOneVerdict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	carrier := newReviewCarrier()
	engine := gin.New()
	engine.POST(reviewSMSControlPath, reviewVerdictHandler(carrier))

	post := func(body string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, reviewSMSControlPath, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := post(`{"code":"rollout-zh-CN","state":"rejected","reason":"content not allowed"}`); code != http.StatusNoContent {
		t.Fatalf("the control answered %d for a valid verdict, want %d", code, http.StatusNoContent)
	}
	zh, err := carrier.Approval(t.Context(), "rollout-zh-CN")
	if err != nil {
		t.Fatalf("Approval(zh): %v", err)
	}
	if zh.State != sms.ApprovalRejected || zh.Reason != "content not allowed" {
		t.Errorf("the Chinese verdict = %+v, want rejected with its reason", zh)
	}
	en, err := carrier.Approval(t.Context(), "rollout-en-US")
	if err != nil {
		t.Fatalf("Approval(en): %v", err)
	}
	if en.State != sms.ApprovalPending {
		t.Errorf("the English verdict became %q, want it left pending", en.State)
	}

	if code := post(`{"code":"rollout-zh-CN","state":"not-required"}`); code != http.StatusBadRequest {
		t.Errorf("the control answered %d for a non-review state, want %d", code, http.StatusBadRequest)
	}
	if code := post(`{"code":"","state":"approved"}`); code != http.StatusBadRequest {
		t.Errorf("the control answered %d for an empty code, want %d", code, http.StatusBadRequest)
	}
}

// TestReviewSMSTemplateStartsPendingAndSettlesOneLanguage drives the real
// template registry over the review carrier: a runtime template registered with
// a code per language starts pending in both, the control moves one language's
// verdict and the poller records it there alone, and disabling the template
// leaves both languages' review states reported.
func TestReviewSMSTemplateStartsPendingAndSettlesOneLanguage(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "review-sms.db")
	carrier := newReviewCarrier()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}},"sms":{"registry":{"poll-interval":"10ms"}}}`, dsn)
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module(), reviewSMSModule(carrier), smsregistry.Module())

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Register(reviewSMSModule(carrier))
	reg.Register(smsregistry.Module())
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth: core.StageInit,
		Participants: []string{
			sqlite.ModuleName,
			reviewCarrierModuleName,
			smsregistry.ModuleName,
		},
		Action: func(ctx context.Context, reg *core.Registry) error {
			templates, err := core.Resolve[sms.Registry](reg)
			if err != nil {
				return fmt.Errorf("taking up the template registry: %w", err)
			}
			record, err := templates.Register(ctx, "rollout", sms.TemplateSpec{
				Kind:   sms.KindNotification,
				Params: []string{"code"},
				Codes: map[string]string{
					"en-US": "rollout-en-US",
					"zh-CN": "rollout-zh-CN",
				},
			})
			if err != nil {
				return fmt.Errorf("registering the template: %w", err)
			}
			for _, locale := range []string{"en-US", "zh-CN"} {
				if got := record.Approvals[locale].State; got != sms.ApprovalPending {
					return fmt.Errorf("the template starts %q in %s, want pending", got, locale)
				}
			}

			if reviewErr := carrier.Review("rollout-zh-CN", sms.ApprovalRejected, "content not allowed"); reviewErr != nil {
				return fmt.Errorf("moving the Chinese verdict: %w", reviewErr)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				records, listErr := templates.List(ctx)
				if listErr != nil {
					return fmt.Errorf("listing the templates: %w", listErr)
				}
				listed, ok := recordNamed(records, "rollout")
				if !ok {
					return fmt.Errorf("the registered template is not in the list")
				}
				if listed.Approvals["zh-CN"].State == sms.ApprovalRejected {
					break
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("the poller had not recorded the Chinese verdict: %+v", listed.Approvals)
				}
				time.Sleep(5 * time.Millisecond)
			}

			records, err := templates.List(ctx)
			if err != nil {
				return fmt.Errorf("listing the templates: %w", err)
			}
			listed, ok := recordNamed(records, "rollout")
			if !ok {
				return fmt.Errorf("the registered template is not in the list")
			}
			if got := listed.Approvals["en-US"].State; got != sms.ApprovalPending {
				return fmt.Errorf("the English verdict became %q, want it left pending", got)
			}
			if !listed.Enabled {
				return fmt.Errorf("the template is disabled before the case disables it")
			}

			if enableErr := templates.SetEnabled(ctx, "rollout", false); enableErr != nil {
				return fmt.Errorf("disabling the template: %w", enableErr)
			}
			records, err = templates.List(ctx)
			if err != nil {
				return fmt.Errorf("listing the templates after disabling: %w", err)
			}
			listed, ok = recordNamed(records, "rollout")
			if !ok {
				return fmt.Errorf("the disabled template is not in the list")
			}
			if listed.Enabled {
				return fmt.Errorf("the template still reports enabled after disabling")
			}
			if got := listed.Approvals["en-US"].State; got != sms.ApprovalPending {
				return fmt.Errorf("disabling changed the English verdict to %q", got)
			}
			if got := listed.Approvals["zh-CN"].State; got != sms.ApprovalRejected {
				return fmt.Errorf("disabling changed the Chinese verdict to %q", got)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// recordNamed answers one record from a list by name.
func recordNamed(records []sms.Record, name string) (sms.Record, bool) {
	for _, record := range records {
		if record.Name == name {
			return record, true
		}
	}
	return sms.Record{}, false
}
