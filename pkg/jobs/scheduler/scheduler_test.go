package scheduler

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/jobs"
)

var epochAligned = time.Unix(1_700_000_040, 0).UTC()

// TestWindowStartRoundsDownToTheIntervalFromTheEpoch checks that a window start
// is the interval's largest multiple from the epoch not after now.
func TestWindowStartRoundsDownToTheIntervalFromTheEpoch(t *testing.T) {
	cases := []struct {
		name     string
		now      time.Time
		interval time.Duration
		want     time.Time
	}{
		{"on a boundary", epochAligned, time.Minute, epochAligned},
		{"within the minute", epochAligned.Add(30 * time.Second), time.Minute, epochAligned},
		{"across a minute", epochAligned.Add(90 * time.Second), time.Minute, epochAligned.Add(time.Minute)},
		{"a thirty-second window", epochAligned.Add(29 * time.Second), 30 * time.Second, epochAligned},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := windowStart(tc.now, tc.interval); !got.Equal(tc.want) {
				t.Errorf("windowStart(%s, %s) = %s, want %s", tc.now, tc.interval, got, tc.want)
			}
		})
	}
}

// TestTheWindowKeyVariesWithTheWindowAndTheKey checks that the key is stable per
// declaration and window and differs across either.
func TestTheWindowKeyVariesWithTheWindowAndTheKey(t *testing.T) {
	at := windowKey("orders.reconcile", epochAligned)
	if got := windowKey("orders.reconcile", epochAligned); got != at {
		t.Errorf("the same declaration and window gave %q and %q, want one key", at, got)
	}
	if len(at) != 64 {
		t.Errorf("the key is %q, whose length is %d, want a 64-character hex digest", at, len(at))
	}
	if other := windowKey("orders.reconcile", epochAligned.Add(time.Minute)); other == at {
		t.Errorf("two windows of one declaration share the key %q", at)
	}
	if other := windowKey("orders.sweep", epochAligned); other == at {
		t.Errorf("two declarations share the key %q", at)
	}
}

// TestRepeatedTicksOfOneWindowConvergeToOneJob checks that repeated ticks over one
// window leave one job.
func TestRepeatedTicksOfOneWindowConvergeToOneJob(t *testing.T) {
	q := newQueue()
	s := newTestScheduler(q, handlersFor("orders.reconcile"), newCatalog(active("acme")),
		time.Minute, epochAligned.Add(30*time.Second),
		jobs.Schedule{Key: "orders.reconcile", Type: "orders.reconcile", Interval: time.Minute})

	s.tickOnce(context.Background(), false)
	s.tickOnce(context.Background(), false)

	if got := q.tried(); got != 2 {
		t.Errorf("two ticks made %d enqueue attempts, want 2", got)
	}
	if got := q.count(); got != 1 {
		t.Errorf("two ticks of one window left %d jobs, want 1", got)
	}
}

// TestEachTenantGetsItsOwnJobForOneWindow checks that one window gives each tenant
// its own job under one key.
func TestEachTenantGetsItsOwnJobForOneWindow(t *testing.T) {
	q := newQueue()
	s := newTestScheduler(q, handlersFor("orders.reconcile"), newCatalog(active("acme"), active("globex")),
		time.Minute, epochAligned,
		jobs.Schedule{Key: "orders.reconcile", Type: "orders.reconcile", Interval: time.Minute})

	s.tickOnce(context.Background(), false)

	accepted := q.acceptedJobs()
	if len(accepted) != 2 {
		t.Fatalf("one window over two tenants left %d jobs, want 2", len(accepted))
	}
	if accepted[0].task.IdempotencyKey != accepted[1].task.IdempotencyKey {
		t.Errorf("the two tenants' jobs carry keys %q and %q, want one key without the tenant",
			accepted[0].task.IdempotencyKey, accepted[1].task.IdempotencyKey)
	}
	tenants := []tenantID{accepted[0].tenant, accepted[1].tenant}
	slices.Sort(tenants)
	if !slices.Equal(tenants, []tenantID{"acme", "globex"}) {
		t.Errorf("the jobs are scoped to %v, want acme and globex", tenants)
	}
}

// TestADisabledTenantIsSkipped checks that a disabled tenant is not entered and
// gets no job.
func TestADisabledTenantIsSkipped(t *testing.T) {
	q := newQueue()
	s := newTestScheduler(q, handlersFor("orders.reconcile"), newCatalog(active("acme"), disabled("globex")),
		time.Minute, epochAligned,
		jobs.Schedule{Key: "orders.reconcile", Type: "orders.reconcile", Interval: time.Minute})

	s.tickOnce(context.Background(), false)

	accepted := q.acceptedJobs()
	if len(accepted) != 1 || accepted[0].tenant != "acme" {
		t.Fatalf("the round accepted %+v, want one job for acme", accepted)
	}
	if got := q.tried(); got != 1 {
		t.Errorf("the round made %d enqueue attempts, want 1: a disabled tenant is not entered", got)
	}
}

// TestTheWindowsATickCovers checks which windows the startup tick covers.
func TestTheWindowsATickCovers(t *testing.T) {
	cases := []struct {
		name     string
		tick     time.Duration
		interval time.Duration
		now      time.Time
		want     []time.Time
	}{
		{
			name:     "the window the process starts in",
			tick:     5 * time.Second,
			interval: time.Minute,
			now:      epochAligned.Add(30 * time.Second),
			want:     []time.Time{epochAligned},
		},
		{
			name:     "a window start on the backscan floor",
			tick:     30 * time.Second,
			interval: time.Minute,
			now:      epochAligned.Add(30 * time.Second),
			want:     []time.Time{epochAligned},
		},
		{
			name:     "a tick longer than the interval",
			tick:     3 * time.Minute,
			interval: time.Minute,
			now:      epochAligned.Add(30 * time.Second),
			want:     []time.Time{epochAligned, epochAligned.Add(-time.Minute), epochAligned.Add(-2 * time.Minute)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := newQueue()
			s := newTestScheduler(q, handlersFor("orders.reconcile"), newCatalog(active("acme")),
				tc.tick, tc.now,
				jobs.Schedule{Key: "orders.reconcile", Type: "orders.reconcile", Interval: tc.interval})

			s.tickOnce(context.Background(), true)

			accepted := q.acceptedJobs()
			if len(accepted) != len(tc.want) {
				t.Fatalf("the tick left %d jobs, want %d, one per covered window", len(accepted), len(tc.want))
			}
			covered := make(map[string]bool, len(accepted))
			for _, j := range accepted {
				covered[j.task.IdempotencyKey] = true
			}
			for _, w := range tc.want {
				if !covered[windowKey("orders.reconcile", w)] {
					t.Errorf("the tick covered no window starting at %s, want one", w)
				}
			}
		})
	}
}

// TestTheStartupCoverageRepeatsNoEnqueuedWindow checks that covering the startup
// window converges on a job already enqueued.
func TestTheStartupCoverageRepeatsNoEnqueuedWindow(t *testing.T) {
	q := newQueue()
	cat := newCatalog(active("acme"))
	s := newTestScheduler(q, handlersFor("orders.reconcile"), cat,
		5*time.Second, epochAligned.Add(30*time.Second),
		jobs.Schedule{Key: "orders.reconcile", Type: "orders.reconcile", Interval: time.Minute})

	tctx, err := cat.Enter(context.Background(), "acme")
	if err != nil {
		t.Fatalf("entering the tenant: %v", err)
	}
	if _, err := q.Enqueue(tctx, jobs.Task{
		Type:           "orders.reconcile",
		IdempotencyKey: windowKey("orders.reconcile", epochAligned),
	}); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}

	s.tickOnce(context.Background(), true)

	if got := q.tried(); got != 2 {
		t.Errorf("with the window already enqueued the tick made %d enqueue attempts in total, want 2: "+
			"the startup coverage attempts the window", got)
	}
	if got := q.count(); got != 1 {
		t.Errorf("the startup coverage left %d jobs, want 1: the queue converges the repeat", got)
	}
}

// TestATickAfterTheFirstCoversOnlyTheBackscan checks that a later tick covers only
// the backscan and enqueues nothing already past.
func TestATickAfterTheFirstCoversOnlyTheBackscan(t *testing.T) {
	q := newQueue()
	s := newTestScheduler(q, handlersFor("orders.reconcile"), newCatalog(active("acme")),
		5*time.Second, epochAligned.Add(30*time.Second),
		jobs.Schedule{Key: "orders.reconcile", Type: "orders.reconcile", Interval: time.Minute})

	s.tickOnce(context.Background(), false)

	if got := q.tried(); got != 0 {
		t.Errorf("a tick after the first made %d enqueue attempts, want 0: the window now falls in "+
			"started more than one tick ago, and no window crossed while the process was stopped is "+
			"enqueued", got)
	}
}

// TestStartRunsTheCoveringTick checks that Start runs the tick covering the
// process's first window.
func TestStartRunsTheCoveringTick(t *testing.T) {
	q := newQueue()
	s := newTestScheduler(q, handlersFor("orders.reconcile"), newCatalog(active("acme")),
		5*time.Second, epochAligned.Add(30*time.Second),
		jobs.Schedule{Key: "orders.reconcile", Type: "orders.reconcile", Interval: time.Minute})

	s.start(context.Background())
	defer s.stop()

	waitFor(t, "the ticker's first tick", func() bool { return q.count() > 0 })
	if got := q.count(); got != 1 {
		t.Errorf("the running ticker left %d jobs, want 1 for the window the process started in", got)
	}
}

// TestATickCrossingSeveralWindowsEnqueuesEach checks that a tick longer than the
// interval enqueues one job per crossed window.
func TestATickCrossingSeveralWindowsEnqueuesEach(t *testing.T) {
	q := newQueue()
	s := newTestScheduler(q, handlersFor("orders.reconcile"), newCatalog(active("acme")),
		3*time.Minute, epochAligned,
		jobs.Schedule{Key: "orders.reconcile", Type: "orders.reconcile", Interval: time.Minute})

	s.tickOnce(context.Background(), false)

	if got := q.count(); got != 3 {
		t.Errorf("a three-minute tick over a one-minute interval left %d jobs, want 3", got)
	}
	seen := map[string]bool{}
	for _, j := range q.acceptedJobs() {
		if seen[j.task.IdempotencyKey] {
			t.Errorf("two windows derived the same key %q", j.task.IdempotencyKey)
		}
		seen[j.task.IdempotencyKey] = true
	}
}

// TestARosterReadFailureExpandsNothing checks that a failed roster read expands no
// window.
func TestARosterReadFailureExpandsNothing(t *testing.T) {
	q := newQueue()
	cat := newCatalog(active("acme"))
	cat.listErr = errors.New("the roster is unreachable")
	s := newTestScheduler(q, handlersFor("orders.reconcile"), cat, time.Minute, epochAligned,
		jobs.Schedule{Key: "orders.reconcile", Type: "orders.reconcile", Interval: time.Minute})

	s.tickOnce(context.Background(), false)

	if got := q.tried(); got != 0 {
		t.Errorf("a failed roster read made %d enqueue attempts, want 0", got)
	}
}

// TestAnEnqueueFailureDoesNotStopTheRound checks that a failing enqueue is
// skipped and the round carries on.
func TestAnEnqueueFailureDoesNotStopTheRound(t *testing.T) {
	q := newQueue()
	q.fail = errors.New("the queue is not running")
	s := newTestScheduler(q, handlersFor("orders.reconcile"), newCatalog(active("acme"), active("globex")),
		time.Minute, epochAligned,
		jobs.Schedule{Key: "orders.reconcile", Type: "orders.reconcile", Interval: time.Minute})

	s.tickOnce(context.Background(), false)

	if got := q.tried(); got != 2 {
		t.Errorf("with every enqueue failing the round made %d attempts, want 2, one per tenant", got)
	}
	if got := q.count(); got != 0 {
		t.Errorf("a failing queue recorded %d jobs, want 0", got)
	}
}

// TestATickAtOrBelowZeroStartsNoTicker checks that a non-positive tick leaves the
// ticker off.
func TestATickAtOrBelowZeroStartsNoTicker(t *testing.T) {
	for _, tick := range []time.Duration{0, -time.Second} {
		s := newTestScheduler(newQueue(), handlersFor("orders.reconcile"), newCatalog(active("acme")),
			tick, epochAligned,
			jobs.Schedule{Key: "orders.reconcile", Type: "orders.reconcile", Interval: time.Minute})
		s.start(context.Background())
		if s.running() {
			t.Errorf("start with tick %s left the ticker running, want it off", tick)
		}
	}
}

// TestStartRunsTheTickerAndStopWaitsForIt checks that Start runs the ticker and
// Stop leaves it stopped.
func TestStartRunsTheTickerAndStopWaitsForIt(t *testing.T) {
	s := newTestScheduler(newQueue(), handlersFor("orders.reconcile"), newCatalog(active("acme")),
		time.Hour, epochAligned,
		jobs.Schedule{Key: "orders.reconcile", Type: "orders.reconcile", Interval: time.Minute})
	s.start(context.Background())
	if !s.running() {
		t.Fatal("start with a positive tick left the ticker off")
	}
	s.stop()
	if s.running() {
		t.Error("stop left the ticker running")
	}
}

// TestReadSettingsTakesTheTick checks that the configured tick is read.
func TestReadSettingsTakesTheTick(t *testing.T) {
	settings, err := readSettings(configWith{tick: 30 * time.Second})
	if err != nil {
		t.Fatalf("reading the settings: %v", err)
	}
	if settings.tick != 30*time.Second {
		t.Errorf("the tick is %s, want 30s", settings.tick)
	}
}

// TestCheckHandlersReportsAMissingType checks that a declared type with no
// registered handler is reported.
func TestCheckHandlersReportsAMissingType(t *testing.T) {
	present := &scheduler{
		handlers:  handlersFor("orders.reconcile"),
		schedules: []jobs.Schedule{{Key: "k", Type: "orders.reconcile", Interval: time.Minute}},
	}
	if err := present.checkHandlers(); err != nil {
		t.Errorf("a registered type was reported missing: %v", err)
	}
	missing := &scheduler{
		handlers:  handlersFor("orders.sweep"),
		schedules: []jobs.Schedule{{Key: "k", Type: "orders.reconcile", Interval: time.Minute}},
	}
	if err := missing.checkHandlers(); !errors.Is(err, ErrMissingHandler) {
		t.Errorf("an unregistered type gave %v, want ErrMissingHandler", err)
	}
}

// TestStartRefusesADeclarationWhoseTypeHasNoHandler checks that Start fails when a
// declaration's type has no handler.
func TestStartRefusesADeclarationWhoseTypeHasNoHandler(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(noConfig{}, config.ModuleName)
	reg.Register(providingQueue(newQueue()))
	reg.Register(providingHandlers(handlersFor()))
	reg.Register(providingCatalog(newCatalog(active("acme"))))
	reg.Register(declaringSchedules("test.shop",
		jobs.Schedule{Key: "shop.reconcile", Type: "shop.reconcile", Interval: time.Minute}))
	reg.Register(Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageStart,
		Participants: []string{"test.queue", "test.handlers", "test.catalog", "test.shop", ModuleName},
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
	if !errors.Is(err, ErrMissingHandler) {
		t.Fatalf("driving with no handler for the declared type gave %v, want ErrMissingHandler", err)
	}
}

// TestStartFailsWhenTheRosterIsAbsent checks that an assembly without a tenant
// roster fails startup.
func TestStartFailsWhenTheRosterIsAbsent(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(noConfig{}, config.ModuleName)
	reg.Register(providingQueue(newQueue()))
	reg.Register(providingHandlers(handlersFor("shop.reconcile")))
	reg.Register(declaringSchedules("test.shop",
		jobs.Schedule{Key: "shop.reconcile", Type: "shop.reconcile", Interval: time.Minute}))
	reg.Register(Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{"test.queue", "test.handlers", "test.shop", ModuleName},
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("driving with no tenant roster gave %v, want core.ErrMissingProvider", err)
	}
}

// TestCollectSchedulesRefusesAMalformedDeclaration checks that an empty key, an
// empty type and a non-positive interval are refused.
func TestCollectSchedulesRefusesAMalformedDeclaration(t *testing.T) {
	cases := []struct {
		name string
		s    jobs.Schedule
	}{
		{"an empty key", jobs.Schedule{Type: "t", Interval: time.Minute}},
		{"an empty type", jobs.Schedule{Key: "k", Interval: time.Minute}},
		{"a non-positive interval", jobs.Schedule{Key: "k", Type: "t"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := core.New()
			reg.Register(declaringSchedules("test.shop", tc.s))
			err := reg.Drive(context.Background(), core.Drive{
				Depth:        core.StageNew,
				Participants: []string{"test.shop"},
				Action: func(_ context.Context, reg *core.Registry) error {
					_, e := collectSchedules(reg)
					return e
				},
			})
			if !errors.Is(err, ErrInvalidSchedule) {
				t.Fatalf("collectSchedules gave %v, want ErrInvalidSchedule", err)
			}
		})
	}
}

// TestCollectSchedulesRefusesADuplicateKey checks that two declarations sharing a
// key are refused.
func TestCollectSchedulesRefusesADuplicateKey(t *testing.T) {
	reg := core.New()
	reg.Register(declaringSchedules("test.a", jobs.Schedule{Key: "dup", Type: "t", Interval: time.Minute}))
	reg.Register(declaringSchedules("test.b", jobs.Schedule{Key: "dup", Type: "t", Interval: time.Minute}))
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{"test.a", "test.b"},
		Action: func(_ context.Context, reg *core.Registry) error {
			_, e := collectSchedules(reg)
			return e
		},
	})
	if !errors.Is(err, ErrDuplicateSchedule) {
		t.Fatalf("collectSchedules gave %v, want ErrDuplicateSchedule", err)
	}
}

// TestCollectSchedulesTakesTheParticipatingSet checks that only declarations in
// force are collected.
func TestCollectSchedulesTakesTheParticipatingSet(t *testing.T) {
	reg := core.New()
	reg.Register(providingQueueExplicitly(newQueue()))
	reg.Register(alsoProvidesQueue("test.standdown",
		jobs.Schedule{Key: "standdown.s", Type: "t", Interval: time.Minute}))
	reg.Register(declaringSchedules("test.shop",
		jobs.Schedule{Key: "shop.s", Type: "t", Interval: time.Minute}))
	reg.Register(declaringSchedules("test.other",
		jobs.Schedule{Key: "other.s", Type: "t", Interval: time.Minute}))
	reg.Register(declaringSchedules("test.extra",
		jobs.Schedule{Key: "extra.s", Type: "t", Interval: time.Minute}))
	reg.Register(declaringSchedules("test.forced",
		jobs.Schedule{Key: "forced.s", Type: "t", Interval: time.Minute}))

	var got []string
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{"test.queue", "test.standdown", "test.shop"},
		DeclaringOnly: []core.Declaration{
			{Name: "test.extra"},
			{Name: "test.forced", AllResources: true},
		},
		Action: func(_ context.Context, reg *core.Registry) error {
			schedules, err := collectSchedules(reg)
			if err != nil {
				return err
			}
			for _, s := range schedules {
				got = append(got, s.Key)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving: %v", err)
	}
	if want := []string{"forced.s", "shop.s"}; !slices.Equal(got, want) {
		t.Errorf("collectSchedules kept %v, want %v", got, want)
	}
}
