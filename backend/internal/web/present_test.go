package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/domain"
)

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:              "45 с",
		3 * time.Minute:               "3 мин",
		2*time.Hour + 46*time.Minute:  "2 ч 46 мин",
		5 * time.Hour:                 "5 ч",
		26*time.Hour + 30*time.Minute: "1 дн 2 ч",
		26 * 24 * time.Hour:           "26 дн",
		-(90 * time.Minute):           "1 ч 30 мин",
	}
	for d, want := range cases {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRelative(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	if got := relative(now.Add(42*time.Minute), now); got != "через 42 мин" {
		t.Fatal(got)
	}
	if got := relative(now.Add(-3*time.Hour), now); got != "3 ч назад" {
		t.Fatal(got)
	}
}

func TestSplitDuration(t *testing.T) {
	cases := []struct {
		d           time.Duration
		value, unit int64
	}{
		{time.Hour, 1, 3600},
		{90 * time.Minute, 90, 60},
		{48 * time.Hour, 2, 86400},
		{45 * time.Second, 45, 1},
		{0, 0, 60},
	}
	for _, c := range cases {
		v, u := splitDuration(c.d)
		if v != c.value || u != c.unit {
			t.Errorf("splitDuration(%v) = %d×%d, want %d×%d", c.d, v, u, c.value, c.unit)
		}
	}
}

func TestBuildRowsOrdersLateFirstHeldLast(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	anchor := now.Add(-30 * time.Minute)
	mk := func(name string, status domain.Status, deadline time.Time) domain.Expectation {
		return domain.Expectation{ID: uuid.New(), Name: name, Status: status, WindowAnchor: anchor, Deadline: deadline,
			Schedule: domain.Schedule{Kind: domain.ScheduleInterval, Period: time.Hour}}
	}
	rows := buildRows([]domain.Expectation{
		mk("held", domain.StatusHeld, now.Add(time.Minute)),
		mk("later", domain.StatusOnTime, now.Add(2*time.Hour)),
		mk("late", domain.StatusLate, now.Add(-10*time.Minute)),
		mk("sooner", domain.StatusQuiet, now.Add(30*time.Minute)),
	}, nil, now)
	var names []string
	for _, r := range rows {
		names = append(names, r.Name)
	}
	if got := strings.Join(names, ","); got != "late,sooner,later,held" {
		t.Fatalf("order %s", got)
	}
	if rows[0].Due != "просрочено на 10 мин" || rows[1].Progress != 50 || rows[3].ShowDeadline {
		t.Fatalf("rows %+v", rows)
	}
}

func TestBuildTimelinePutsTransitionAboveItsSignal(t *testing.T) {
	at := time.Date(2026, 11, 5, 3, 41, 0, 0, time.UTC)
	outlet := domain.Outlet{ID: uuid.New(), Name: "mail"}
	tr := domain.Transition{ID: uuid.New(), From: domain.StatusLate, To: domain.StatusOnTime, Reason: domain.ReasonSignalOK, At: at}
	events := buildTimeline(domain.SourceIntake,
		[]domain.Signal{{Outcome: domain.OutcomeOK, ReceivedAt: at}, {Outcome: domain.OutcomeBegin, ReceivedAt: at.Add(-time.Minute)}},
		[]domain.Transition{tr},
		[]domain.Delivery{{TransitionID: tr.ID, OutletID: &outlet.ID, Status: domain.DeliveryFailed, AttemptCount: 3, LastError: "HTTP 502"}},
		[]domain.Outlet{outlet},
	)
	if len(events) != 3 || events[0].Kind != "transition" || events[1].Title != "Сигнал ok" || events[2].Title != "Сигнал begin" {
		t.Fatalf("events %+v", events)
	}
	d := events[0].Deliveries
	if len(d) != 1 || d[0].Outlet != "mail" || d[0].Tone != "late" || d[0].Error != "HTTP 502" {
		t.Fatalf("deliveries %+v", d)
	}
}

func TestBuildWindowOverdue(t *testing.T) {
	anchor := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	exp := domain.Expectation{
		Status: domain.StatusLate, WindowAnchor: anchor, Deadline: anchor.Add(70 * time.Minute),
		Schedule: domain.Schedule{Kind: domain.ScheduleInterval, Period: time.Hour, Slack: 10 * time.Minute},
	}
	w := buildWindow(exp, anchor.Add(140*time.Minute))
	if !w.Show || w.Base != 428 || w.Slack != 71 || w.Over != 500 {
		t.Fatalf("window %+v", w)
	}
	if buildWindow(domain.Expectation{Status: domain.StatusHeld}, anchor).Show {
		t.Fatal("held window shown")
	}
}

func TestBuildTimelineNamesProbeResults(t *testing.T) {
	at := time.Date(2026, 11, 5, 3, 41, 0, 0, time.UTC)
	events := buildTimeline(domain.SourceProbe, []domain.Signal{
		{Outcome: domain.OutcomeFail, Note: "GET https://x → 503", ReceivedAt: at},
		{Outcome: domain.OutcomeOK, ReceivedAt: at.Add(-time.Minute)},
	}, nil, nil, nil)
	if events[0].Title != "Проверка не прошла" || events[0].Note == "" || events[1].Title != "Проверка прошла" {
		t.Fatalf("%+v", events)
	}
}

// Every template must execute without error: html/template stops mid-page otherwise.
func TestTemplatesRender(t *testing.T) {
	h, err := New(nil, "http://localhost:18080", time.Hour, fakeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	smtp := domain.Outlet{ID: uuid.New(), Name: "Почта", Kind: domain.OutletSMTP, Settings: domain.OutletSettings{
		SMTP: &domain.SMTPSettings{Host: "smtp", Port: 465, Security: domain.SMTPTLS, To: "ops@example.com", Password: "hunter2"},
	}}
	tg := domain.Outlet{ID: uuid.New(), Name: "TG", Kind: domain.OutletTelegram, Settings: domain.OutletSettings{
		Telegram: &domain.TelegramSettings{BotToken: "123:secret", ChatID: "-100"},
	}}
	run := now.Add(5 * time.Minute)
	lastOK := now.Add(-time.Hour)
	exp := domain.Expectation{
		ID: uuid.New(), Name: "Бэкап", Source: domain.SourceIntake, Status: domain.StatusLate, IntakeToken: "tok",
		WindowAnchor: now.Add(-2 * time.Hour), Deadline: now.Add(-time.Hour), LastOK: &lastOK, RunDeadline: &run,
		Schedule:  domain.Schedule{Kind: domain.ScheduleCron, Cron: "0 3 * * *", Timezone: "Europe/Moscow", Slack: 30 * time.Minute},
		OutletIDs: []uuid.UUID{smtp.ID},
	}
	probeAt := now.Add(time.Minute)
	probe := exp
	probe.ID, probe.Source, probe.IntakeToken, probe.Status, probe.Deadline = uuid.New(), domain.SourceProbe, "", domain.StatusOnTime, now.Add(time.Hour)
	probe.Probe, probe.ProbeAt = &domain.Probe{Method: "HEAD", URL: "https://example.com/health", Timeout: 10 * time.Second}, &probeAt
	tr := domain.Transition{ID: uuid.New(), From: domain.StatusOnTime, To: domain.StatusLate, Reason: domain.ReasonWindowClosed, At: exp.Deadline}
	items := []domain.Expectation{exp, probe}
	proj := domain.Project{ID: uuid.New(), Name: "Магазин"}
	base := view{Title: "t", Me: domain.Account{Email: "ops@example.com", Admin: true}, Project: proj, Projects: []domain.Project{proj}, Nav: "expectations"}
	card := func(e domain.Expectation) view {
		v := base
		v.Expectation, v.Schedule = e, scheduleText(e.Schedule)
		v.Hero, v.Window = buildHero(e, []domain.Transition{tr}, now), buildWindow(e, now)
		v.Events = buildTimeline(e.Source, []domain.Signal{{Outcome: domain.OutcomeFail, Note: "disk", ReceivedAt: now}}, []domain.Transition{tr},
			[]domain.Delivery{{TransitionID: tr.ID, OutletID: &smtp.ID, Status: domain.DeliverySent}}, []domain.Outlet{smtp})
		v.Assigned = []domain.Outlet{smtp, tg}
		if e.Probe != nil {
			v.ProbeText = probeText(*e.Probe)
		} else {
			v.IntakeURL = "http://localhost/intake/tok"
		}
		return v
	}
	pages := map[string]view{
		"login":       {Title: "Вход", Error: "x", Providers: h.providerViews()},
		"invite":      {Title: "Приглашение", InviteOK: true, InviteToken: "t", Providers: h.providerViews()},
		"project-new": base,
		"settings":    base,
		"list": func() view {
			v := base
			v.Rows, v.Counts, v.Total = buildRows(items, []domain.Outlet{smtp}, now), countStatuses(items), 2
			return v
		}(),
		"card":       card(exp),
		"probe-card": card(probe),
		"form": func() view {
			v := base
			v.Form, v.Outlets, v.Methods, v.Error = formFromExpectation(probe), plainItems([]domain.Outlet{smtp}), domain.ProbeMethods, "x"
			return v
		}(),
		"outlets": func() view {
			v := base
			v.Nav, v.Outlets, v.Outlet, v.Usage = "outlets", []outletItem{{Outlet: smtp, Usage: 1}, {Outlet: tg}}, formFromOutlet(smtp), 1
			return v
		}(),
		"profile": func() view {
			v := base
			v.Nav = "profile"
			v.Profile = app.Profile{Account: base.Me, HasPassword: true, Identities: []domain.Identity{{Provider: "vk", Subject: "1"}}}
			v.SignIn = methods(v.Profile, []app.AuthProvider{fakeProvider{}})
			v.Notice, v.Error = "ok", "x"
			return v
		}(),
		"invites": func() view {
			v := base
			v.Nav, v.InviteURL = "invites", "http://localhost/invite/abc"
			v.Invites = []inviteRow{{Invite: domain.Invite{ID: uuid.New(), Note: "Аня", CreatedAt: now}, State: domain.InviteActive}}
			return v
		}(),
	}
	for name, data := range pages {
		tmpl := strings.TrimPrefix(name, "probe-")
		var buf bytes.Buffer
		if err := h.tmpl.ExecuteTemplate(&buf, tmpl, data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body := buf.String()
		if !strings.Contains(body, "</html>") {
			t.Fatalf("%s: truncated", name)
		}
		if strings.Contains(body, "hunter2") || strings.Contains(body, "123:secret") {
			t.Fatalf("%s: secret rendered", name)
		}
	}
}

func TestFormCheckboxesFollowSelection(t *testing.T) {
	h, err := New(nil, "http://localhost:18080", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	on, off := domain.Outlet{ID: uuid.New(), Name: "on"}, domain.Outlet{ID: uuid.New(), Name: "off"}
	form := newExpectationForm()
	form.Selected = map[uuid.UUID]bool{on.ID: true}
	form.ProbeMethod = "PUT"
	var buf bytes.Buffer
	v := view{Project: domain.Project{ID: uuid.New(), Name: "p"}, Form: form, Outlets: plainItems([]domain.Outlet{on, off}), Methods: domain.ProbeMethods}
	if err := h.tmpl.ExecuteTemplate(&buf, "form", v); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if !strings.Contains(body, `value="`+on.ID.String()+`" checked`) || strings.Contains(body, `value="`+off.ID.String()+`" checked`) {
		t.Fatal("checkbox state wrong")
	}
	if !strings.Contains(body, `<option value="3600" selected>`) || !strings.Contains(body, `<option value="PUT" selected>`) {
		t.Fatal("selects not prefilled")
	}
}

func TestInputFromForm(t *testing.T) {
	post := func(body string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		return req
	}
	in, err := inputFromForm(post("name=a&source=probe&kind=interval&period_value=15&period_unit=60&slack_value=2&slack_unit=3600" +
		"&probe_method=HEAD&probe_url=https://x.test&probe_status=204&probe_timeout=5"))
	if err != nil || in.Schedule.Period != 15*time.Minute || in.Schedule.Slack != 2*time.Hour {
		t.Fatalf("%+v %v", in, err)
	}
	if p := in.Probe; p == nil || p.Method != "HEAD" || p.ExpectStatus != 204 || p.Timeout != 5*time.Second {
		t.Fatalf("probe %+v", in.Probe)
	}
	if _, err := inputFromForm(post("kind=interval&period_value=1&period_unit=7&slack_value=0&slack_unit=60")); err == nil {
		t.Fatal("unknown unit accepted")
	}
	in, err = inputFromForm(post("source=intake&kind=cron&cron=0+3+*+*+*&slack_value=0&slack_unit=60"))
	if err != nil || in.Probe != nil || in.Schedule.Cron != "0 3 * * *" {
		t.Fatalf("%+v %v", in, err)
	}
}
