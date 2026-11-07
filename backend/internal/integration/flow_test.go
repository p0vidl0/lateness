//go:build integration

// Package integration runs the scenarios of internal/app against a real Postgres.
package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/adapters/postgres"
	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/domain"
	"github.com/p0vidl0/lateness/internal/secret"
)

var store *postgres.Store

func TestMain(m *testing.M) {
	if os.Getenv("LATENESS_INTEGRATION") != "1" {
		os.Exit(m.Run())
	}
	ctx := context.Background()
	cipher, err := secret.NewCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	if err != nil {
		panic(err)
	}
	store, err = postgres.Open(ctx, os.Getenv("LATENESS_DATABASE_URL"), cipher)
	if err != nil {
		panic(err)
	}
	if err := store.Migrate(ctx); err != nil {
		panic(err)
	}
	code := m.Run()
	store.Close()
	os.Exit(code)
}

var ctx = context.Background()

// Fakes

type sent struct {
	outlet domain.Outlet
	notice domain.Notice
}

type notifier struct {
	mu   sync.Mutex
	sent []sent
	err  error
}

func (n *notifier) Send(_ context.Context, o domain.Outlet, notice domain.Notice) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.err != nil {
		return n.err
	}
	n.sent = append(n.sent, sent{o, notice})
	return nil
}

func (n *notifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.sent)
}

type prober struct {
	mu    sync.Mutex
	ok    bool
	calls int
}

func (p *prober) Probe(context.Context, domain.Probe) domain.ProbeResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.ok {
		return domain.ProbeResult{OK: true, Note: "GET → 200"}
	}
	return domain.ProbeResult{Note: "GET → 503"}
}

func (p *prober) set(ok bool) {
	p.mu.Lock()
	p.ok = ok
	p.mu.Unlock()
}

// Harness

type harness struct {
	svc    *app.Service
	clock  *app.ManualClock
	notify *notifier
	probe  *prober
}

// newHarness hides everything earlier tests left behind, so a sweep sees only this test's rows.
func newHarness(t *testing.T) harness {
	t.Helper()
	if os.Getenv("LATENESS_INTEGRATION") != "1" {
		t.Skip("set LATENESS_INTEGRATION=1")
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE expectations SET deleted_at = now() WHERE deleted_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE deliveries SET status = 'failed' WHERE status IN ('pending', 'sending')`); err != nil {
		t.Fatal(err)
	}
	clock := app.NewManualClock(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	h := harness{clock: clock, notify: &notifier{}, probe: &prober{ok: true}}
	h.svc = &app.Service{Store: store, Clock: clock, SessionTTL: time.Hour, Notifier: h.notify, Prober: h.probe}
	return h
}

// signUp registers a fresh account through an invite and returns it with its first project.
func (h harness) signUp(t *testing.T) (domain.Account, domain.Project) {
	t.Helper()
	_, token, err := h.svc.IssueInvite(ctx, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	_, account, err := h.svc.RegisterWithInvite(ctx, token, uuid.NewString()+"@example.com", "password1")
	if err != nil {
		t.Fatal(err)
	}
	projects, err := h.svc.Projects(ctx, account)
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects %v %v", projects, err)
	}
	return account, projects[0]
}

func (h harness) outlet(t *testing.T, proj domain.Project) domain.Outlet {
	t.Helper()
	o, err := h.svc.SaveOutlet(ctx, proj, domain.Outlet{Name: "hook-" + uuid.NewString()[:8], Kind: domain.OutletHTTP,
		Settings: domain.OutletSettings{HTTP: &domain.HTTPSettings{URL: "https://hooks.example/x"}}})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (h harness) expectation(t *testing.T, proj domain.Project, in app.ExpectationInput) domain.Expectation {
	t.Helper()
	if in.Name == "" {
		in.Name = "job"
	}
	if in.Source == "" {
		in.Source = domain.SourceIntake
	}
	if in.Schedule.Kind == "" {
		in.Schedule = domain.Schedule{Kind: domain.ScheduleInterval, Period: time.Hour, Slack: 5 * time.Minute}
	}
	exp, err := h.svc.CreateExpectation(ctx, proj, in)
	if err != nil {
		t.Fatal(err)
	}
	return exp
}

func (h harness) signal(t *testing.T, exp domain.Expectation, body string) {
	t.Helper()
	if err := h.svc.AcceptSignal(ctx, exp.IntakeToken, []byte(body)); err != nil {
		t.Fatal(err)
	}
}

func (h harness) sweep(t *testing.T) app.SweepResult {
	t.Helper()
	r, err := h.svc.SweepOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (h harness) reload(t *testing.T, proj domain.Project, exp domain.Expectation) domain.Expectation {
	t.Helper()
	got, err := h.svc.GetExpectation(ctx, proj, exp.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (h harness) journal(t *testing.T, exp domain.Expectation) app.Journal {
	t.Helper()
	j, err := h.svc.Journal(ctx, exp)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func expectStatus(t *testing.T, exp domain.Expectation, want domain.Status) {
	t.Helper()
	if exp.Status != want {
		t.Fatalf("status %s, want %s", exp.Status, want)
	}
}

// Expectation lifecycle

func TestSignalThenClosedWindowWritesTransitionAndDelivery(t *testing.T) {
	h := newHarness(t)
	_, proj := h.signUp(t)
	o := h.outlet(t, proj)
	exp := h.expectation(t, proj, app.ExpectationInput{OutletIDs: []uuid.UUID{o.ID}})

	h.signal(t, exp, `{"outcome":"ok"}`)
	expectStatus(t, h.reload(t, proj, exp), domain.StatusOnTime)
	if r := h.sweep(t); r.Closed != 0 {
		t.Fatalf("closed early: %+v", r)
	}

	h.clock.Advance(time.Hour + 5*time.Minute)
	if r := h.sweep(t); r.Closed != 1 || r.Sent != 1 {
		t.Fatalf("sweep %+v", r)
	}
	expectStatus(t, h.reload(t, proj, exp), domain.StatusLate)
	j := h.journal(t, exp)
	if len(j.Transitions) != 2 || j.Transitions[0].Reason != domain.ReasonWindowClosed {
		t.Fatalf("transitions %+v", j.Transitions)
	}
	if len(j.Deliveries) != 1 || j.Deliveries[0].Status != domain.DeliverySent || *j.Deliveries[0].OutletID != o.ID {
		t.Fatalf("deliveries %+v", j.Deliveries)
	}
	if h.notify.sent[0].notice.Reason != domain.ReasonWindowClosed || h.notify.sent[0].outlet.Settings.HTTP == nil {
		t.Fatalf("sent %+v", h.notify.sent)
	}

	// The first ok after late notifies recovery.
	h.signal(t, exp, `{"outcome":"ok"}`)
	h.sweep(t)
	if h.notify.count() != 2 || h.notify.sent[1].notice.To != domain.StatusOnTime {
		t.Fatalf("recovery %+v", h.notify.sent)
	}
}

func TestHeldIsNotClosedAndResumeOpensWindow(t *testing.T) {
	h := newHarness(t)
	_, proj := h.signUp(t)
	exp := h.expectation(t, proj, app.ExpectationInput{})
	if err := h.svc.Hold(ctx, proj, exp.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Hold(ctx, proj, exp.ID); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("second hold: %v", err)
	}
	h.clock.Advance(48 * time.Hour)
	if r := h.sweep(t); r.Closed != 0 {
		t.Fatalf("held closed: %+v", r)
	}
	if err := h.svc.Resume(ctx, proj, exp.ID); err != nil {
		t.Fatal(err)
	}
	got := h.reload(t, proj, exp)
	expectStatus(t, got, domain.StatusQuiet)
	if !got.Deadline.Equal(h.clock.Now().Add(time.Hour + 5*time.Minute)) {
		t.Fatalf("deadline %s", got.Deadline)
	}
}

func TestFailWithoutOutletsLeavesVisibleDelivery(t *testing.T) {
	h := newHarness(t)
	_, proj := h.signUp(t)
	exp := h.expectation(t, proj, app.ExpectationInput{})
	h.signal(t, exp, `{"outcome":"fail","note":"disk full"}`)
	expectStatus(t, h.reload(t, proj, exp), domain.StatusLate)
	if r := h.sweep(t); r.Failed != 1 {
		t.Fatalf("sweep %+v", r)
	}
	j := h.journal(t, exp)
	if len(j.Deliveries) != 1 || j.Deliveries[0].OutletID != nil || j.Deliveries[0].LastError != "outlet is not configured" {
		t.Fatalf("deliveries %+v", j.Deliveries)
	}
	if len(j.Signals) != 1 || j.Signals[0].Note != "disk full" {
		t.Fatalf("signals %+v", j.Signals)
	}
}

func TestBeginExpiredClosesWindow(t *testing.T) {
	h := newHarness(t)
	_, proj := h.signUp(t)
	exp := h.expectation(t, proj, app.ExpectationInput{})
	h.signal(t, exp, `{"outcome":"begin"}`)
	got := h.reload(t, proj, exp)
	if got.RunDeadline == nil || !got.RunDeadline.Equal(h.clock.Now().Add(5*time.Minute)) {
		t.Fatalf("run deadline %v", got.RunDeadline)
	}
	h.clock.Advance(6 * time.Minute)
	h.sweep(t)
	j := h.journal(t, exp)
	if len(j.Transitions) != 1 || j.Transitions[0].Reason != domain.ReasonBeginExpired {
		t.Fatalf("transitions %+v", j.Transitions)
	}
}

func TestUpdateAndDeleteExpectation(t *testing.T) {
	h := newHarness(t)
	_, proj := h.signUp(t)
	o := h.outlet(t, proj)
	exp := h.expectation(t, proj, app.ExpectationInput{})
	updated, err := h.svc.UpdateExpectation(ctx, proj, exp.ID, app.ExpectationInput{
		Name: "renamed", Schedule: domain.Schedule{Kind: domain.ScheduleCron, Cron: "0 3 * * *", Timezone: "Europe/Moscow"},
		OutletIDs: []uuid.UUID{o.ID, o.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := h.reload(t, proj, exp)
	if got.Name != "renamed" || got.Schedule.Cron != "0 3 * * *" || len(got.OutletIDs) != 1 || got.Version != updated.Version {
		t.Fatalf("%+v", got)
	}
	// A write based on the old version loses.
	stale := exp
	stale.Name = "stale"
	if err := store.Commit(ctx, app.Change{Expectation: &stale}); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("stale write: %v", err)
	}
	if err := h.svc.DeleteExpectation(ctx, proj, exp.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.AcceptSignal(ctx, exp.IntakeToken, []byte(`{"outcome":"ok"}`)); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("intake after delete: %v", err)
	}
	if _, err := h.svc.GetExpectation(ctx, proj, exp.ID); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	var deletedAt *time.Time
	if err := store.Pool().QueryRow(ctx, `SELECT deleted_at FROM expectations WHERE id = $1`, exp.ID).Scan(&deletedAt); err != nil || deletedAt == nil {
		t.Fatalf("row must stay with deleted_at: %v %v", deletedAt, err)
	}
}

func TestOutletFromAnotherProjectIsRefused(t *testing.T) {
	h := newHarness(t)
	_, proj := h.signUp(t)
	_, other := h.signUp(t)
	foreign := h.outlet(t, other)
	_, err := h.svc.CreateExpectation(ctx, proj, app.ExpectationInput{
		Name: "x", Source: domain.SourceIntake, Schedule: domain.Schedule{Kind: domain.ScheduleInterval, Period: time.Hour},
		OutletIDs: []uuid.UUID{foreign.ID},
	})
	if !errors.Is(err, app.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}

// Probes

func TestProbeSuccessFailureAndLateness(t *testing.T) {
	h := newHarness(t)
	_, proj := h.signUp(t)
	o := h.outlet(t, proj)
	exp := h.expectation(t, proj, app.ExpectationInput{
		Source: domain.SourceProbe, Probe: &domain.Probe{Method: "HEAD", URL: "https://example.com/health"},
		Schedule:  domain.Schedule{Kind: domain.ScheduleInterval, Period: 5 * time.Minute, Slack: 2 * time.Minute},
		OutletIDs: []uuid.UUID{o.ID},
	})
	if exp.IntakeToken != "" || exp.Probe.Timeout != domain.DefaultProbeTimeout {
		t.Fatalf("probe defaults %+v", exp)
	}

	// Runs right after creation; success opens the next window.
	if r := h.sweep(t); r.Probed != 1 {
		t.Fatalf("sweep %+v", r)
	}
	got := h.reload(t, proj, exp)
	expectStatus(t, got, domain.StatusOnTime)
	if !got.ProbeAt.Equal(h.clock.Now().Add(5 * time.Minute)) {
		t.Fatalf("probe_at %s", got.ProbeAt)
	}

	// A failure is journaled and retried soon but changes no status.
	h.probe.set(false)
	h.clock.Advance(5 * time.Minute)
	h.sweep(t)
	got = h.reload(t, proj, exp)
	expectStatus(t, got, domain.StatusOnTime)
	if !got.ProbeAt.Equal(h.clock.Now().Add(domain.ProbeRetry)) {
		t.Fatalf("retry at %s", got.ProbeAt)
	}

	// No success within slack: the window closes as for any expectation.
	h.clock.Advance(2 * time.Minute)
	if r := h.sweep(t); r.Closed != 1 || r.Sent != 1 {
		t.Fatalf("sweep %+v", r)
	}
	expectStatus(t, h.reload(t, proj, exp), domain.StatusLate)

	// Recovery on the next successful probe.
	h.probe.set(true)
	h.clock.Advance(domain.ProbeRetry)
	h.sweep(t)
	expectStatus(t, h.reload(t, proj, exp), domain.StatusOnTime)
	j := h.journal(t, exp)
	if len(j.Signals) != 4 || j.Signals[1].Outcome != domain.OutcomeFail || j.Signals[1].Note != "GET → 503" {
		t.Fatalf("signals %+v", j.Signals)
	}
	if h.notify.count() != 2 {
		t.Fatalf("notices %+v", h.notify.sent)
	}
}

func TestClaimProbesLeases(t *testing.T) {
	h := newHarness(t)
	_, proj := h.signUp(t)
	h.expectation(t, proj, app.ExpectationInput{Source: domain.SourceProbe, Probe: &domain.Probe{URL: "https://example.com"}})
	first, err := store.ClaimProbes(ctx, h.clock.Now(), time.Minute, 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim %v %v", first, err)
	}
	second, err := store.ClaimProbes(ctx, h.clock.Now(), time.Minute, 10)
	if err != nil || len(second) != 0 {
		t.Fatalf("second claim must be empty: %v %v", second, err)
	}
	again, err := store.ClaimProbes(ctx, h.clock.Now().Add(time.Minute), time.Minute, 10)
	if err != nil || len(again) != 1 {
		t.Fatalf("lease expiry %v %v", again, err)
	}
}

func TestHeldProbeDoesNotRun(t *testing.T) {
	h := newHarness(t)
	_, proj := h.signUp(t)
	exp := h.expectation(t, proj, app.ExpectationInput{Source: domain.SourceProbe, Probe: &domain.Probe{URL: "https://example.com"}})
	if err := h.svc.Hold(ctx, proj, exp.ID); err != nil {
		t.Fatal(err)
	}
	if r := h.sweep(t); r.Probed != 0 || h.probe.calls != 0 {
		t.Fatalf("held probe ran: %+v", r)
	}
}

// Outlets

func TestOutletSettingsAreSealedAndSecretsKept(t *testing.T) {
	h := newHarness(t)
	_, proj := h.signUp(t)
	o, err := h.svc.SaveOutlet(ctx, proj, domain.Outlet{Name: "mail", Kind: domain.OutletSMTP, Settings: domain.OutletSettings{
		SMTP: &domain.SMTPSettings{Host: "smtp.example.com", Port: 465, Security: domain.SMTPTLS, Username: "u", Password: "hunter2",
			From: "a@example.com", To: "b@example.com"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := store.Pool().QueryRow(ctx, `SELECT settings FROM outlets WHERE id = $1`, o.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("hunter2")) || bytes.Contains(raw, []byte("smtp.example.com")) {
		t.Fatal("settings stored in clear")
	}
	// An edit without the password keeps it.
	edit := o
	smtp := *o.Settings.SMTP
	smtp.Password, smtp.To = "", "c@example.com"
	edit.Settings = domain.OutletSettings{SMTP: &smtp}
	if _, err := h.svc.SaveOutlet(ctx, proj, edit); err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.GetOutlet(ctx, proj, o.ID)
	if err != nil || got.Settings.SMTP.Password != "hunter2" || got.Settings.SMTP.To != "c@example.com" {
		t.Fatalf("%+v %v", got.Settings.SMTP, err)
	}
}

func TestAssignedOutletCannotBeDeleted(t *testing.T) {
	h := newHarness(t)
	_, proj := h.signUp(t)
	o := h.outlet(t, proj)
	exp := h.expectation(t, proj, app.ExpectationInput{OutletIDs: []uuid.UUID{o.ID}})
	if err := h.svc.DeleteOutlet(ctx, proj, o.ID); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("assigned delete: %v", err)
	}
	if err := h.svc.DeleteExpectation(ctx, proj, exp.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DeleteOutlet(ctx, proj, o.ID); err != nil {
		t.Fatalf("delete after expectation is gone: %v", err)
	}
	if outlets, _ := h.svc.ListOutlets(ctx, proj); len(outlets) != 0 {
		t.Fatalf("outlets %+v", outlets)
	}
}

// Accounts, invites, projects

func TestInviteIsSingleUse(t *testing.T) {
	h := newHarness(t)
	_, token, err := h.svc.IssueInvite(ctx, nil, "twice", false)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, results[i] = h.svc.RegisterWithInvite(ctx, token, uuid.NewString()+"@example.com", "password1")
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, app.ErrInviteUnusable):
			t.Fatalf("unexpected %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d sign-ups used one invite", ok)
	}
}

func TestExpiredAndRevokedInvites(t *testing.T) {
	h := newHarness(t)
	admin := domain.Account{ID: uuid.New(), Admin: true}
	if _, _, err := h.svc.IssueInvite(ctx, &domain.Account{ID: uuid.New()}, "", false); !errors.Is(err, app.ErrForbidden) {
		t.Fatalf("non-admin issued an invite: %v", err)
	}
	_, expired, _ := h.svc.IssueInvite(ctx, nil, "", false)
	inv, revoked, _ := h.svc.IssueInvite(ctx, nil, "", false)
	if err := h.svc.RevokeInvite(ctx, admin, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.RegisterWithInvite(ctx, revoked, uuid.NewString()+"@example.com", "password1"); !errors.Is(err, app.ErrInviteUnusable) {
		t.Fatalf("revoked: %v", err)
	}
	h.clock.Advance(8 * 24 * time.Hour)
	if _, _, err := h.svc.RegisterWithInvite(ctx, expired, uuid.NewString()+"@example.com", "password1"); !errors.Is(err, app.ErrInviteUnusable) {
		t.Fatalf("expired: %v", err)
	}
}

func TestAdminInviteMakesAdmin(t *testing.T) {
	h := newHarness(t)
	_, token, _ := h.svc.IssueInvite(ctx, nil, "", true)
	email := uuid.NewString() + "@example.com"
	session, account, err := h.svc.RegisterWithInvite(ctx, token, email, "password1")
	if err != nil {
		t.Fatal(err)
	}
	me, err := h.svc.Session(ctx, session)
	if err != nil || !me.Admin || me.ID != account.ID {
		t.Fatalf("%+v %v", me, err)
	}
	if _, _, err := h.svc.Login(ctx, email, "password1"); err != nil {
		t.Fatal(err)
	}
}

func TestLoginExternal(t *testing.T) {
	h := newHarness(t)
	id := domain.Identity{Provider: "vk", Subject: uuid.NewString(), Name: "Ann"}

	if _, _, err := h.svc.LoginExternal(ctx, id, ""); !errors.Is(err, app.ErrInviteRequired) {
		t.Fatalf("unknown identity without invite: %v", err)
	}
	_, token, _ := h.svc.IssueInvite(ctx, nil, "", false)
	_, created, err := h.svc.LoginExternal(ctx, id, token)
	if err != nil || created.Email != "" || created.Name != "Ann" {
		t.Fatalf("sign-up: %+v %v", created, err)
	}
	_, again, err := h.svc.LoginExternal(ctx, id, "")
	if err != nil || again.ID != created.ID {
		t.Fatalf("repeat login: %+v %v", again, err)
	}

	// A provider email that matches a password account links to it.
	owner, _ := h.signUp(t)
	linked := domain.Identity{Provider: "vk", Subject: uuid.NewString(), Email: owner.Email}
	_, got, err := h.svc.LoginExternal(ctx, linked, "")
	if err != nil || got.ID != owner.ID {
		t.Fatalf("link by email: %+v %v", got, err)
	}
}

func TestProjectsAreIsolatedAndSoftDeleted(t *testing.T) {
	h := newHarness(t)
	alice, first := h.signUp(t)
	bob, _ := h.signUp(t)
	if _, err := h.svc.Project(ctx, bob, first.ID); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("foreign project: %v", err)
	}
	second, err := h.svc.CreateProject(ctx, alice, "Второй")
	if err != nil {
		t.Fatal(err)
	}
	exp := h.expectation(t, second, app.ExpectationInput{})
	if err := h.svc.DeleteProject(ctx, alice, second.ID); err != nil {
		t.Fatal(err)
	}
	projects, _ := h.svc.Projects(ctx, alice)
	if len(projects) != 1 || projects[0].ID != first.ID {
		t.Fatalf("projects %+v", projects)
	}
	if err := h.svc.AcceptSignal(ctx, exp.IntakeToken, []byte(`{"outcome":"ok"}`)); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("intake in deleted project: %v", err)
	}
	h.clock.Advance(48 * time.Hour)
	if r := h.sweep(t); r.Closed != 0 {
		t.Fatalf("deleted project swept: %+v", r)
	}
}

// Profile

func TestLinkAndUnlinkIdentities(t *testing.T) {
	h := newHarness(t)
	me, _ := h.signUp(t)
	other, _ := h.signUp(t)
	vk := domain.Identity{Provider: "vk", Subject: uuid.NewString()}
	tg := domain.Identity{Provider: "telegram", Subject: uuid.NewString(), Name: "Ann"}

	if err := h.svc.LinkIdentity(ctx, me, vk); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.LinkIdentity(ctx, me, vk); err != nil {
		t.Fatalf("relink is a no-op: %v", err)
	}
	if err := h.svc.LinkIdentity(ctx, other, vk); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("foreign identity: %v", err)
	}
	if err := h.svc.LinkIdentity(ctx, me, domain.Identity{Provider: "vk", Subject: uuid.NewString()}); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("second vk: %v", err)
	}
	if err := h.svc.LinkIdentity(ctx, me, tg); err != nil {
		t.Fatal(err)
	}
	// The linked identity signs in to this account without an invite.
	if _, got, err := h.svc.LoginExternal(ctx, tg, ""); err != nil || got.ID != me.ID {
		t.Fatalf("login through linked telegram: %+v %v", got, err)
	}
	p, err := h.svc.Profile(ctx, me)
	if err != nil || !p.HasPassword || len(p.Identities) != 2 || p.Methods() != 3 {
		t.Fatalf("%+v %v", p, err)
	}
	if err := h.svc.UnlinkIdentity(ctx, me, "vk"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.LinkIdentity(ctx, other, vk); err != nil {
		t.Fatalf("unlinked identity can move: %v", err)
	}
}

func TestLastSignInMethodStays(t *testing.T) {
	h := newHarness(t)
	_, token, _ := h.svc.IssueInvite(ctx, nil, "", false)
	tg := domain.Identity{Provider: "telegram", Subject: uuid.NewString()}
	session, me, err := h.svc.LoginExternal(ctx, tg, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.UnlinkIdentity(ctx, me, "telegram"); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("last method unlinked: %v", err)
	}
	// A provider-only account sets email and password, then may unlink.
	if err := h.svc.ChangePassword(ctx, me, session, "", "bad", "password1"); !errors.Is(err, app.ErrInvalid) {
		t.Fatalf("email required: %v", err)
	}
	email := uuid.NewString() + "@example.com"
	if err := h.svc.ChangePassword(ctx, me, session, "", email, "password1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.Login(ctx, email, "password1"); err != nil {
		t.Fatalf("password login: %v", err)
	}
	if err := h.svc.UnlinkIdentity(ctx, me, "telegram"); err != nil {
		t.Fatal(err)
	}
}

func TestChangePasswordSignsOutOtherSessions(t *testing.T) {
	h := newHarness(t)
	_, token, _ := h.svc.IssueInvite(ctx, nil, "", false)
	email := uuid.NewString() + "@example.com"
	current, me, err := h.svc.RegisterWithInvite(ctx, token, email, "password1")
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := h.svc.Login(ctx, email, "password1")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ChangePassword(ctx, me, current, "wrong", "", "password2"); !errors.Is(err, app.ErrUnauthorized) {
		t.Fatalf("wrong current: %v", err)
	}
	if err := h.svc.ChangePassword(ctx, me, current, "password1", "ignored@example.com", "password2"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Session(ctx, current); err != nil {
		t.Fatalf("current session must stay: %v", err)
	}
	if _, err := h.svc.Session(ctx, other); !errors.Is(err, app.ErrUnauthorized) {
		t.Fatalf("other session must end: %v", err)
	}
	if _, _, err := h.svc.Login(ctx, email, "password2"); err != nil {
		t.Fatalf("email must not change: %v", err)
	}
	if err := h.svc.RenameAccount(ctx, me, "  Ann  "); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.svc.Session(ctx, current); got.Name != "Ann" {
		t.Fatalf("name %q", got.Name)
	}
}
