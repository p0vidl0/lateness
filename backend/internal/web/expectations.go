package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/domain"
)

// Projects

func (h *Handler) projectNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, http.StatusOK, "project-new", h.page(r, "Новый проект", "projects"))
}

func (h *Handler) projectCreate(w http.ResponseWriter, r *http.Request) {
	p, err := h.Service.CreateProject(r.Context(), me(r), r.FormValue("name"))
	if errors.Is(err, app.ErrInvalid) {
		v := h.page(r, "Новый проект", "projects")
		v.Error, v.Project.Name = "Название — от 1 до 200 символов.", r.FormValue("name")
		h.render(w, http.StatusBadRequest, "project-new", v)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/p/"+p.ID.String()+"/")
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) {
	h.render(w, http.StatusOK, "settings", h.page(r, "Настройки проекта", "settings"))
}

func (h *Handler) settingsSave(w http.ResponseWriter, r *http.Request) {
	p := project(r)
	err := h.Service.RenameProject(r.Context(), me(r), p.ID, r.FormValue("name"))
	if errors.Is(err, app.ErrInvalid) {
		v := h.page(r, "Настройки проекта", "settings")
		v.Error = "Название — от 1 до 200 символов."
		h.render(w, http.StatusBadRequest, "settings", v)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/p/"+p.ID.String()+"/settings")
}

func (h *Handler) projectDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.Service.DeleteProject(r.Context(), me(r), project(r).ID); err != nil {
		h.fail(w, r, err)
		return
	}
	clearCookie(w, projectCookie, "/")
	redirect(w, r, "/")
}

// Expectations

func base(r *http.Request) string { return "/p/" + project(r).ID.String() }

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p := project(r)
	items, err := h.Service.ListExpectations(r.Context(), p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	outlets, err := h.Service.ListOutlets(r.Context(), p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := h.page(r, "Ожидания", "expectations")
	v.Rows, v.Counts, v.Total = buildRows(items, outlets, h.Service.Now()), countStatuses(items), len(items)
	h.render(w, http.StatusOK, "list", v)
}

func (h *Handler) card(w http.ResponseWriter, r *http.Request) {
	p := project(r)
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	exp, err := h.Service.GetExpectation(r.Context(), p, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	journal, err := h.Service.Journal(r.Context(), exp)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	outlets, err := h.Service.ListOutlets(r.Context(), p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	now := h.Service.Now()
	v := h.page(r, exp.Name, "expectations")
	v.Expectation, v.Schedule = exp, scheduleText(exp.Schedule)
	v.Hero, v.Window = buildHero(exp, journal.Transitions, now), buildWindow(exp, now)
	v.Events = buildTimeline(exp.Source, journal.Signals, journal.Transitions, journal.Deliveries, outlets)
	v.Assigned = assigned(exp.OutletIDs, outlets)
	if exp.Source == domain.SourceProbe {
		v.ProbeText = probeText(*exp.Probe)
	} else {
		v.IntakeURL = h.PublicURL + "/intake/" + exp.IntakeToken
	}
	h.render(w, http.StatusOK, "card", v)
}

func assigned(ids []uuid.UUID, outlets []domain.Outlet) []domain.Outlet {
	byID := make(map[uuid.UUID]domain.Outlet, len(outlets))
	for _, o := range outlets {
		byID[o.ID] = o
	}
	out := make([]domain.Outlet, 0, len(ids))
	for _, id := range ids {
		if o, ok := byID[id]; ok {
			out = append(out, o)
		}
	}
	return out
}

func (h *Handler) newForm(w http.ResponseWriter, r *http.Request) {
	outlets, err := h.Service.ListOutlets(r.Context(), project(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	form := newExpectationForm()
	for _, o := range outlets {
		form.Selected[o.ID] = true
	}
	h.renderForm(w, r, http.StatusOK, form, outlets, "")
}

func (h *Handler) editForm(w http.ResponseWriter, r *http.Request) {
	p := project(r)
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	exp, err := h.Service.GetExpectation(r.Context(), p, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	outlets, err := h.Service.ListOutlets(r.Context(), p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderForm(w, r, http.StatusOK, formFromExpectation(exp), outlets, "")
}

func (h *Handler) renderForm(w http.ResponseWriter, r *http.Request, status int, form expectationForm, outlets []domain.Outlet, errText string) {
	title := "Новое ожидание"
	if !form.IsNew {
		title = form.Name
	}
	v := h.page(r, title, "expectations")
	v.Form, v.Outlets, v.Methods, v.Error = form, plainItems(outlets), domain.ProbeMethods, errText
	h.render(w, status, "form", v)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) { h.saveExpectation(w, r, uuid.Nil) }

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.saveExpectation(w, r, id)
}

func (h *Handler) saveExpectation(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	p := project(r)
	form := formFromRequest(r, id)
	in, err := inputFromForm(r)
	if err == nil {
		var exp domain.Expectation
		if id == uuid.Nil {
			exp, err = h.Service.CreateExpectation(r.Context(), p, in)
		} else {
			exp, err = h.Service.UpdateExpectation(r.Context(), p, id, in)
		}
		if err == nil {
			redirect(w, r, base(r)+"/expectations/"+exp.ID.String())
			return
		}
	}
	if !errors.Is(err, app.ErrInvalid) && !errors.Is(err, app.ErrConflict) {
		h.fail(w, r, err)
		return
	}
	msg := "Проверьте название, расписание, допуск и часовой пояс. Для проверки — URL с http(s), код ответа и таймаут от 1 до 60 с."
	if errors.Is(err, app.ErrConflict) {
		msg = "Ожидание изменилось, пока вы редактировали. Проверьте значения и сохраните ещё раз."
	}
	outlets, lerr := h.Service.ListOutlets(r.Context(), p)
	if lerr != nil {
		h.fail(w, r, lerr)
		return
	}
	h.renderForm(w, r, http.StatusBadRequest, form, outlets, msg)
}

func (h *Handler) hold(w http.ResponseWriter, r *http.Request)   { h.setHeld(w, r, true) }
func (h *Handler) resume(w http.ResponseWriter, r *http.Request) { h.setHeld(w, r, false) }

func (h *Handler) setHeld(w http.ResponseWriter, r *http.Request, held bool) {
	id, err := pathID(r)
	if err == nil {
		if held {
			err = h.Service.Hold(r.Context(), project(r), id)
		} else {
			err = h.Service.Resume(r.Context(), project(r), id)
		}
	}
	// A repeated click (already held or resumed) just shows the current state.
	if err != nil && !errors.Is(err, app.ErrConflict) {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, base(r)+"/expectations/"+id.String())
}

func (h *Handler) expectationDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = h.Service.DeleteExpectation(r.Context(), project(r), id)
	}
	if err != nil && !errors.Is(err, app.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, base(r)+"/")
}

// Expectation form

type expectationForm struct {
	IsNew        bool
	ID           uuid.UUID
	Name         string
	Source       string
	Kind         string
	PeriodValue  int64
	PeriodUnit   int64
	SlackValue   int64
	SlackUnit    int64
	Cron         string
	Timezone     string
	ProbeMethod  string
	ProbeURL     string
	ProbeStatus  string // empty accepts any 2xx
	ProbeTimeout int64  // seconds
	Selected     map[uuid.UUID]bool
}

func newExpectationForm() expectationForm {
	return expectationForm{
		IsNew: true, Source: string(domain.SourceIntake), Kind: string(domain.ScheduleInterval),
		PeriodValue: 1, PeriodUnit: 3600, SlackValue: 5, SlackUnit: 60, Cron: "0 3 * * *",
		ProbeMethod: "GET", ProbeTimeout: int64(domain.DefaultProbeTimeout / time.Second), Selected: map[uuid.UUID]bool{},
	}
}

func formFromExpectation(exp domain.Expectation) expectationForm {
	f := newExpectationForm()
	f.IsNew, f.ID, f.Name, f.Source, f.Kind = false, exp.ID, exp.Name, string(exp.Source), string(exp.Schedule.Kind)
	f.Timezone = exp.Schedule.Timezone
	if exp.Schedule.Kind == domain.ScheduleInterval {
		f.PeriodValue, f.PeriodUnit = splitDuration(exp.Schedule.Period)
	} else {
		f.Cron = exp.Schedule.Cron
	}
	f.SlackValue, f.SlackUnit = splitDuration(exp.Schedule.Slack)
	if p := exp.Probe; p != nil {
		f.ProbeMethod, f.ProbeURL, f.ProbeTimeout = p.Method, p.URL, int64(p.Timeout/time.Second)
		if p.ExpectStatus != 0 {
			f.ProbeStatus = strconv.Itoa(p.ExpectStatus)
		}
	}
	for _, id := range exp.OutletIDs {
		f.Selected[id] = true
	}
	return f
}

// formFromRequest echoes the submitted values back after a failed save.
func formFromRequest(r *http.Request, id uuid.UUID) expectationForm {
	num := func(key string, def int64) int64 {
		n, err := strconv.ParseInt(r.FormValue(key), 10, 64)
		if err != nil {
			return def
		}
		return n
	}
	f := expectationForm{
		IsNew: id == uuid.Nil, ID: id, Name: r.FormValue("name"), Source: r.FormValue("source"), Kind: r.FormValue("kind"),
		PeriodValue: num("period_value", 1), PeriodUnit: num("period_unit", 3600),
		SlackValue: num("slack_value", 5), SlackUnit: num("slack_unit", 60),
		Cron: r.FormValue("cron"), Timezone: r.FormValue("timezone"),
		ProbeMethod: r.FormValue("probe_method"), ProbeURL: r.FormValue("probe_url"),
		ProbeStatus: r.FormValue("probe_status"), ProbeTimeout: num("probe_timeout", 10), Selected: map[uuid.UUID]bool{},
	}
	for _, oid := range parseOutletIDs(r) {
		f.Selected[oid] = true
	}
	return f
}

func inputFromForm(r *http.Request) (app.ExpectationInput, error) {
	spec, err := scheduleFromForm(r)
	if err != nil {
		return app.ExpectationInput{}, err
	}
	in := app.ExpectationInput{
		Name: r.FormValue("name"), Schedule: spec, Source: domain.Source(r.FormValue("source")), OutletIDs: parseOutletIDs(r),
	}
	if in.Source == domain.SourceProbe {
		timeout, err := strconv.ParseInt(r.FormValue("probe_timeout"), 10, 64)
		if err != nil {
			return in, app.ErrInvalid
		}
		status := 0
		if raw := strings.TrimSpace(r.FormValue("probe_status")); raw != "" {
			if status, err = strconv.Atoi(raw); err != nil {
				return in, app.ErrInvalid
			}
		}
		in.Probe = &domain.Probe{
			Method: r.FormValue("probe_method"), URL: r.FormValue("probe_url"),
			ExpectStatus: status, Timeout: time.Duration(timeout) * time.Second,
		}
	}
	return in, nil
}

func parseOutletIDs(r *http.Request) []uuid.UUID {
	values := r.Form["outlet_ids"]
	out := make([]uuid.UUID, 0, len(values))
	for _, raw := range values {
		if id, err := uuid.Parse(raw); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// durationField reads value × unit, where unit is one of durationUnits.
func durationField(r *http.Request, valueKey, unitKey string) (int64, bool) {
	value, err := strconv.ParseInt(r.FormValue(valueKey), 10, 64)
	if err != nil {
		return 0, false
	}
	unit, err := strconv.ParseInt(r.FormValue(unitKey), 10, 64)
	if err != nil || !validUnit(unit) {
		return 0, false
	}
	return value * unit, true
}

func scheduleFromForm(r *http.Request) (domain.Schedule, error) {
	slack, ok := durationField(r, "slack_value", "slack_unit")
	if !ok || slack < 0 {
		return domain.Schedule{}, app.ErrInvalid
	}
	spec := domain.Schedule{Kind: domain.ScheduleKind(r.FormValue("kind")), Slack: time.Duration(slack) * time.Second}
	switch spec.Kind {
	case domain.ScheduleInterval:
		period, ok := durationField(r, "period_value", "period_unit")
		if !ok || period < 1 {
			return domain.Schedule{}, app.ErrInvalid
		}
		spec.Period = time.Duration(period) * time.Second
	case domain.ScheduleCron:
		spec.Cron = strings.TrimSpace(r.FormValue("cron"))
		spec.Timezone = strings.TrimSpace(r.FormValue("timezone"))
	default:
		return domain.Schedule{}, app.ErrInvalid
	}
	return spec, nil
}
