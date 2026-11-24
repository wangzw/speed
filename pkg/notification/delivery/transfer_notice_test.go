package delivery

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant/ownership"
)

const transferRequestType = "tenant.owner.transfer_requested"

func ownershipNoticeResources() []any {
	var resources []any
	for _, resource := range ownership.Module().Resources {
		switch resource.(type) {
		case i18n.Catalog, notification.TypeSpec:
			resources = append(resources, resource)
		}
	}
	return resources
}

type stubZoneProfile struct {
	zones map[string]string
}

func (stubZoneProfile) LocaleFor(context.Context, string) (i18n.Locale, bool, error) {
	return "", false, nil
}

func (p stubZoneProfile) TimeZoneFor(_ context.Context, account string) (string, bool, error) {
	zone, ok := p.zones[account]
	return zone, ok, nil
}

// TestTheTransferRequestNoticeRendersItsExpiryAfterTheQueue drives the notice
// through Dispatch, the queue and Handle with the real i18n and Asia/Shanghai.
func TestTheTransferRequestNoticeRendersItsExpiryAfterTheQueue(t *testing.T) {
	rig := newRig(t, func(h *host) {
		h.specs = map[string]notification.TypeSpec{}
		h.realLocalizer = true
		h.profile = stubZoneProfile{zones: map[string]string{"u1": "Asia/Shanghai"}}
		h.resources = ownershipNoticeResources()
	})
	rig.prefs.mu.Lock()
	rig.prefs.channels[transferRequestType] = []notification.Channel{notification.ChannelInApp}
	rig.prefs.mu.Unlock()

	ctx := rig.tenantCtx("t1")
	expires := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			before := len(rig.queue.enqueued())
			if _, err := rig.p.Dispatch(ctx, notification.Dispatch{
				TypeKey:      transferRequestType,
				Recipient:    notification.Recipient{Class: notification.RecipientUser, UserID: "u1"},
				Locale:       locale,
				Params:       map[string]any{"tenant": "acme", "request": "req-1", "expires_at": expires},
				OccurrenceID: "transfer-" + locale,
			}); err != nil {
				t.Fatalf("dispatching the transfer notice: %v", err)
			}
			tasks := rig.queue.enqueued()
			if len(tasks) != before+1 {
				t.Fatalf("the queue holds %d tasks, want %d", len(tasks), before+1)
			}
			job := &jobs.Job{ID: jobs.JobID("job-" + locale), Type: JobType, Payload: tasks[len(tasks)-1].Payload}
			if _, err := rig.p.Handle(ctx, job, nil); err != nil {
				t.Fatalf("handling the transfer notice: %v", err)
			}

			body := rig.inbox.last().Body
			if strings.Contains(body, "{{") {
				t.Errorf("%s body = %q, want no placeholder left as written", locale, body)
			}
			if want := "\uE000time:expires_at\uE001"; !strings.Contains(body, want) {
				t.Errorf("%s body = %q, want the expiry as the time marker %q", locale, body, want)
			}
			if frozen := "2026-11-20 17:30 +0800"; strings.Contains(body, frozen) {
				t.Errorf("%s body = %q, want no server-formatted instant %q", locale, body, frozen)
			}
			if _, ok := rig.inbox.last().Params["expires_at"].(time.Time); !ok {
				t.Errorf("%s params = %#v, want expires_at restored to a time.Time before the inbox sees it",
					locale, rig.inbox.last().Params)
			}
		})
	}
}
