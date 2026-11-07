package schedule

import (
	"testing"
	"time"

	"github.com/p0vidl0/lateness/internal/domain"
)

func TestIntervalDeadline(t *testing.T) {
	anchor := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	spec := domain.Schedule{Kind: domain.ScheduleInterval, Period: time.Hour, Slack: 5 * time.Minute}
	got, err := Deadline(spec, anchor)
	if err != nil {
		t.Fatal(err)
	}
	want := anchor.Add(time.Hour + 5*time.Minute)
	if !got.Equal(want) {
		t.Fatalf("got %s want %s", got, want)
	}
	open, err := Closed(spec, anchor, want.Add(-time.Nanosecond))
	if err != nil || open {
		t.Fatalf("before deadline: closed=%v err=%v", open, err)
	}
	closed, err := Closed(spec, anchor, want)
	if err != nil || !closed {
		t.Fatalf("at deadline: closed=%v err=%v", closed, err)
	}
}

func TestCronDeadline(t *testing.T) {
	spec := domain.Schedule{Kind: domain.ScheduleCron, Cron: "0 * * * *", Slack: 5 * time.Minute}
	anchor := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	got, err := Deadline(spec, anchor)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 22, 11, 5, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("on the hour: got %s want %s", got, want)
	}
	mid := time.Date(2026, 10, 22, 10, 30, 0, 0, time.UTC)
	got, err = Deadline(spec, mid)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatalf("half past: got %s want %s", got, want)
	}
	zeroSlack := spec
	zeroSlack.Slack = 0
	got, err = Deadline(zeroSlack, anchor)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("zero slack: %s", got)
	}
}

func TestCronTimezone(t *testing.T) {
	spec := domain.Schedule{
		Kind: domain.ScheduleCron, Cron: "0 9 * * *", Slack: time.Minute,
		Timezone: "Europe/Berlin",
	}
	anchor := time.Date(2026, 3, 29, 7, 0, 0, 0, time.UTC)
	got, err := Deadline(spec, anchor)
	if err != nil {
		t.Fatal(err)
	}
	if got.Location() != time.UTC {
		t.Fatalf("location %s", got.Location())
	}
	if err := Validate(domain.Schedule{Kind: domain.ScheduleCron, Cron: "0 * * * *", Timezone: "Not/AZone"}); err == nil {
		t.Fatal("bad timezone accepted")
	}
}

func TestValidate(t *testing.T) {
	ok := []domain.Schedule{
		{Kind: domain.ScheduleInterval, Period: time.Second, Slack: 0},
		{Kind: domain.ScheduleCron, Cron: "*/15 * * * *", Slack: time.Minute},
	}
	for _, spec := range ok {
		if err := Validate(spec); err != nil {
			t.Fatalf("%+v: %v", spec, err)
		}
	}
	bad := []domain.Schedule{
		{Kind: domain.ScheduleInterval, Period: 0, Slack: 0},
		{Kind: domain.ScheduleInterval, Period: time.Second, Slack: -time.Second},
		{Kind: "weekly"},
		{Kind: domain.ScheduleCron, Cron: "@hourly"},
		{Kind: domain.ScheduleCron, Cron: "0 * * *"},
		{Kind: domain.ScheduleCron, Cron: "not a cron expr"},
	}
	for _, spec := range bad {
		if err := Validate(spec); err == nil {
			t.Fatalf("accepted %+v", spec)
		}
	}
}

func TestNextExcludesSlack(t *testing.T) {
	anchor := time.Date(2026, 10, 22, 10, 30, 0, 0, time.UTC)
	interval := domain.Schedule{Kind: domain.ScheduleInterval, Period: 10 * time.Minute, Slack: time.Minute}
	got, err := Next(interval, anchor)
	if err != nil || !got.Equal(anchor.Add(10*time.Minute)) {
		t.Fatalf("interval next %s %v", got, err)
	}
	cron := domain.Schedule{Kind: domain.ScheduleCron, Cron: "0 * * * *", Slack: time.Minute}
	got, err = Next(cron, anchor)
	if err != nil || !got.Equal(time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("cron next %s %v", got, err)
	}
}
