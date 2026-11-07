package web

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/domain"
)

// Presentation helpers: everything here is pure and takes "now" explicitly.

func statusLabel(status domain.Status) string {
	switch status {
	case domain.StatusQuiet:
		return "Ждёт сигнала"
	case domain.StatusOnTime:
		return "Вовремя"
	case domain.StatusLate:
		return "Просрочено"
	case domain.StatusHeld:
		return "На паузе"
	default:
		return string(status)
	}
}

func reasonLabel(reason domain.Reason) string {
	switch reason {
	case domain.ReasonSignalOK:
		return "пришёл ok"
	case domain.ReasonSignalFail:
		return "сигнал fail"
	case domain.ReasonWindowClosed:
		return "окно закрылось"
	case domain.ReasonBeginExpired:
		return "прогон не завершился"
	case domain.ReasonHold:
		return "пауза"
	case domain.ReasonResume:
		return "возобновлено"
	default:
		return string(reason)
	}
}

// humanDuration renders a duration with short Russian units, two components at most.
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	secs := int64(d / time.Second)
	if secs < 60 {
		return fmt.Sprintf("%d с", secs)
	}
	mins := secs / 60
	if mins < 60 {
		return fmt.Sprintf("%d мин", mins)
	}
	hours, restMin := mins/60, mins%60
	if hours < 24 {
		if restMin == 0 {
			return fmt.Sprintf("%d ч", hours)
		}
		return fmt.Sprintf("%d ч %d мин", hours, restMin)
	}
	days, restHours := hours/24, hours%24
	if days < 7 && restHours != 0 {
		return fmt.Sprintf("%d дн %d ч", days, restHours)
	}
	return fmt.Sprintf("%d дн", days)
}

func relative(t, now time.Time) string {
	d := t.Sub(now)
	if d >= 0 {
		return "через " + humanDuration(d)
	}
	return humanDuration(d) + " назад"
}

func slackText(slack time.Duration) string {
	if slack <= 0 {
		return "без допуска"
	}
	return "допуск " + humanDuration(slack)
}

func scheduleText(spec domain.Schedule) string {
	if spec.Kind == domain.ScheduleCron {
		tz := spec.Timezone
		if tz == "" {
			tz = "UTC"
		}
		return "cron " + spec.Cron + " · " + tz + " · " + slackText(spec.Slack)
	}
	return "раз в " + humanDuration(spec.Period) + " · " + slackText(spec.Slack)
}

// probeText is a one-line summary of a probe for the card.
func probeText(p domain.Probe) string {
	want := "любой 2xx"
	if p.ExpectStatus != 0 {
		want = fmt.Sprintf("код %d", p.ExpectStatus)
	}
	return p.Method + " " + p.URL + " · ждём " + want + " · таймаут " + humanDuration(p.Timeout)
}

// Duration units offered in forms, in seconds.
var durationUnits = []int64{86400, 3600, 60, 1}

// splitDuration picks the largest unit that divides d exactly, for prefilling forms.
func splitDuration(d time.Duration) (value, unit int64) {
	secs := int64(d / time.Second)
	if secs == 0 {
		return 0, 60
	}
	for _, u := range durationUnits {
		if secs%u == 0 {
			return secs / u, u
		}
	}
	return secs, 1
}

func validUnit(u int64) bool {
	for _, known := range durationUnits {
		if u == known {
			return true
		}
	}
	return false
}

type statusCount struct {
	Status domain.Status
	Count  int
}

type expRow struct {
	ID           uuid.UUID
	Name         string
	Status       domain.Status
	Schedule     string
	Due          string
	DueLate      bool
	Deadline     time.Time
	ShowDeadline bool
	Progress     int
	LastOK       string
	Running      bool
	RunDeadline  time.Time
	Outlets      string
	Probe        bool
}

func progress(anchor, deadline, now time.Time) int {
	total := deadline.Sub(anchor)
	if total <= 0 {
		return 100
	}
	pct := int(now.Sub(anchor) * 100 / total)
	return max(0, min(100, pct))
}

func buildRows(items []domain.Expectation, outlets []domain.Outlet, now time.Time) []expRow {
	names := outletNames(outlets)
	rows := make([]expRow, 0, len(items))
	for _, exp := range items {
		row := expRow{
			ID: exp.ID, Name: exp.Name, Status: exp.Status, Schedule: scheduleText(exp.Schedule),
			Deadline: exp.Deadline, ShowDeadline: exp.Status != domain.StatusHeld,
			LastOK: "ещё не было", Outlets: joinOutlets(exp.OutletIDs, names), Probe: exp.Source == domain.SourceProbe,
		}
		if exp.LastOK != nil {
			row.LastOK = relative(*exp.LastOK, now)
		}
		switch exp.Status {
		case domain.StatusHeld:
			row.Due = "на паузе"
		case domain.StatusLate:
			row.DueLate = true
			row.Progress = 100
			if now.After(exp.Deadline) {
				row.Due = "просрочено на " + humanDuration(now.Sub(exp.Deadline))
			} else {
				row.Due = "просрочено"
			}
		default:
			row.Due = relative(exp.Deadline, now)
			row.Progress = progress(exp.WindowAnchor, exp.Deadline, now)
			if exp.RunDeadline != nil {
				row.Running = true
				row.RunDeadline = *exp.RunDeadline
			}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := statusRank(rows[i].Status), statusRank(rows[j].Status)
		if ri != rj {
			return ri < rj
		}
		return rows[i].Deadline.Before(rows[j].Deadline)
	})
	return rows
}

func statusRank(s domain.Status) int {
	switch s {
	case domain.StatusLate:
		return 0
	case domain.StatusHeld:
		return 2
	default:
		return 1
	}
}

func countStatuses(items []domain.Expectation) []statusCount {
	order := []domain.Status{domain.StatusLate, domain.StatusOnTime, domain.StatusQuiet, domain.StatusHeld}
	counts := map[domain.Status]int{}
	for _, exp := range items {
		counts[exp.Status]++
	}
	out := make([]statusCount, 0, len(order))
	for _, s := range order {
		out = append(out, statusCount{Status: s, Count: counts[s]})
	}
	return out
}

func outletNames(outlets []domain.Outlet) map[uuid.UUID]string {
	names := make(map[uuid.UUID]string, len(outlets))
	for _, o := range outlets {
		names[o.ID] = o.Name
	}
	return names
}

func joinOutlets(ids []uuid.UUID, names map[uuid.UUID]string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, ok := names[id]; ok {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return "не выбраны"
	}
	return strings.Join(parts, ", ")
}

type deliveryView struct {
	Outlet string
	State  string
	Tone   string
	Error  string
}

type event struct {
	At         time.Time
	Kind       string // "signal" or "transition"
	Glyph      string
	Tone       string // ok, late, info, held, muted
	Title      string
	Note       string
	Deliveries []deliveryView
}

func deliveryState(d domain.Delivery) (string, string) {
	switch d.Status {
	case domain.DeliverySent:
		return "доставлено", "ok"
	case domain.DeliveryFailed:
		return fmt.Sprintf("ошибка, попыток: %d", d.AttemptCount), "late"
	case domain.DeliverySending:
		return "отправляется", "muted"
	default:
		if d.AttemptCount > 0 {
			return fmt.Sprintf("повтор, попыток: %d", d.AttemptCount), "muted"
		}
		return "в очереди", "muted"
	}
}

func buildTimeline(source domain.Source, signals []domain.Signal, transitions []domain.Transition, deliveries []domain.Delivery, outlets []domain.Outlet) []event {
	names := outletNames(outlets)
	byTransition := map[uuid.UUID][]deliveryView{}
	for _, d := range deliveries {
		name := "без канала"
		if d.OutletID != nil {
			if n, ok := names[*d.OutletID]; ok {
				name = n
			} else {
				name = "удалённый канал"
			}
		}
		state, tone := deliveryState(d)
		byTransition[d.TransitionID] = append(byTransition[d.TransitionID], deliveryView{Outlet: name, State: state, Tone: tone, Error: d.LastError})
	}
	events := make([]event, 0, len(signals)+len(transitions))
	for _, tr := range transitions {
		glyph, tone := "→", "muted"
		switch tr.To {
		case domain.StatusLate:
			glyph, tone = "!", "late"
		case domain.StatusOnTime:
			glyph, tone = "↺", "ok"
		case domain.StatusHeld:
			glyph, tone = "‖", "held"
		}
		if tr.Reason == domain.ReasonResume {
			glyph, tone = "▶", "info"
		}
		events = append(events, event{
			At: tr.At, Kind: "transition", Glyph: glyph, Tone: tone,
			Title:      statusLabel(tr.From) + " → " + statusLabel(tr.To) + " · " + reasonLabel(tr.Reason),
			Deliveries: byTransition[tr.ID],
		})
	}
	for _, sig := range signals {
		glyph, tone := "✓", "ok"
		switch sig.Outcome {
		case domain.OutcomeBegin:
			glyph, tone = "▸", "info"
		case domain.OutcomeFail:
			glyph, tone = "✕", "late"
		}
		title := "Сигнал " + string(sig.Outcome)
		if source == domain.SourceProbe {
			title = "Проверка прошла"
			if sig.Outcome == domain.OutcomeFail {
				title = "Проверка не прошла"
			}
		}
		events = append(events, event{At: sig.ReceivedAt, Kind: "signal", Glyph: glyph, Tone: tone, Title: title, Note: sig.Note})
	}
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].At.Equal(events[j].At) {
			return events[i].At.After(events[j].At)
		}
		// A transition is the result of a signal at the same instant: show it above.
		return events[i].Kind == "transition" && events[j].Kind != "transition"
	})
	return events
}

type hero struct {
	Caption string
	Big     string
	Tone    string
	Hint    string
}

func buildHero(exp domain.Expectation, transitions []domain.Transition, now time.Time) hero {
	switch exp.Status {
	case domain.StatusHeld:
		return hero{Caption: "Ожидание на паузе", Big: "Дедлайн не считается", Tone: "held",
			Hint: "Возобновление откроет новое окно от текущего момента."}
	case domain.StatusLate:
		var last *domain.Transition
		for i := range transitions {
			tr := transitions[i]
			if tr.To == domain.StatusLate && (last == nil || tr.At.After(last.At)) {
				last = &transitions[i]
			}
		}
		h := hero{Caption: "Окно закрылось без сигнала ok", Tone: "late",
			Hint: "Следующий сигнал ok вернёт ожидание во «Вовремя» и отправит уведомление о восстановлении."}
		if exp.Source == domain.SourceProbe {
			h.Caption = "Проверки не проходят дольше допуска"
			h.Hint = "Сервис продолжает проверять. Первая успешная проверка вернёт ожидание во «Вовремя» и отправит уведомление о восстановлении."
		}
		at := exp.Deadline
		if last != nil {
			at = last.At
			switch last.Reason {
			case domain.ReasonSignalFail:
				h.Caption = "Задача сообщила о сбое"
			case domain.ReasonBeginExpired:
				h.Caption = "Прогон не завершился в пределах допуска"
			}
		}
		h.Big = humanDuration(now.Sub(at)) + " назад"
		return h
	default:
		h := hero{Caption: "До дедлайна", Big: humanDuration(exp.Deadline.Sub(now)), Tone: "ok"}
		if exp.Status == domain.StatusQuiet {
			h.Tone = "muted"
			h.Hint = "Сигнала ok ещё не было. Добавьте вызов приёма в конец задачи — пример ниже."
			if exp.Source == domain.SourceProbe {
				h.Hint = "Успешной проверки ещё не было. Первая выполнится в ближайший проход sweep."
			}
		}
		if exp.RunDeadline != nil {
			h.Hint = "Идёт прогон: ok нужен до " + exp.RunDeadline.UTC().Format("15:04 UTC") + ", иначе ожидание станет просроченным."
		}
		return h
	}
}

type windowBar struct {
	Show     bool
	Base     int
	Slack    int
	Over     int
	NowPct   int
	Anchor   time.Time
	Deadline time.Time
	Late     bool
}

func buildWindow(exp domain.Expectation, now time.Time) windowBar {
	if exp.Status == domain.StatusHeld {
		return windowBar{}
	}
	end := exp.Deadline
	if now.After(end) {
		end = now
	}
	total := end.Sub(exp.WindowAnchor)
	if total <= 0 {
		return windowBar{}
	}
	slack := exp.Schedule.Slack
	base := exp.Deadline.Sub(exp.WindowAnchor) - slack
	if base < 0 {
		slack += base
		base = 0
	}
	over := now.Sub(exp.Deadline)
	if over < 0 {
		over = 0
	}
	weight := func(d time.Duration) int { return int(d * 1000 / total) }
	return windowBar{
		Show: true, Base: weight(base), Slack: weight(slack), Over: weight(over),
		NowPct: max(0, min(100, int(now.Sub(exp.WindowAnchor)*100/total))),
		Anchor: exp.WindowAnchor, Deadline: exp.Deadline, Late: exp.Status == domain.StatusLate,
	}
}
